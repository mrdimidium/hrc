// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gilti.dev/hrc/pkg/listener"
	"gilti.dev/hrc/pkg/worker"

	"github.com/actions/scaleset"
)

// Session retry delays grow from the minimum to the maximum and reset after a
// session has served for longer than the maximum.
var sessionRetryMin, sessionRetryMax = 5 * time.Second, 2 * time.Minute

// run returns an error only for local failures that a retry cannot fix. GitHub
// API and session failures are retried until ctx is canceled, without
// interrupting runners already started by the group.
func run(ctx context.Context, cfg *Config, group *Group) error {
	logger := slog.Default().With("group", group.Name)
	logger.Info("Starting group controller")

	runnerDir := cfg.DirRunner
	if runnerDir == "" {
		version, err := os.ReadFile(filepath.Join(cfg.DirBin, "VERSION"))
		if err != nil {
			return fmt.Errorf("read runner version: %w", err)
		}
		runnerDir = filepath.Join(cfg.DirBin, "github-bundled-"+strings.TrimSpace(string(version)))
	}

	if _, err := worker.New(worker.Config{Dir: runnerDir}); err != nil {
		return fmt.Errorf("validate runner installation: %w", err)
	}
	if err := os.MkdirAll(cfg.DirBuilds, 0o700); err != nil {
		return fmt.Errorf("create builds directory: %w", err)
	}

	// Runners outlive the session that started them: they hold their own
	// Broker connections and must not be canceled by a session failure.
	var runners sync.WaitGroup
	var running atomic.Int32
	defer runners.Wait()

	delay := sessionRetryMin
	for {
		started := time.Now()
		err := serve(ctx, cfg, group, runnerDir, &runners, &running)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > sessionRetryMax {
			delay = sessionRetryMin
		}
		logger.Error("Group session failed, retrying", "err", err, "delay", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(2*delay, sessionRetryMax)
	}
}

// serve connects the group's scale set and starts runners for assigned jobs
// until the session fails or ctx is canceled.
func serve(ctx context.Context, cfg *Config, group *Group, runnerDir string, runners *sync.WaitGroup, running *atomic.Int32) error {
	logger := slog.Default().With("group", group.Name)
	info := scaleset.SystemInfo{System: "hrc", Version: Version}

	var err error
	var client *scaleset.Client
	if group.GitHubApp.Validate() == nil {
		client, err = scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
			GitHubConfigURL: group.Url, GitHubAppAuth: group.GitHubApp, SystemInfo: info,
		})
	} else {
		var token string
		if token, err = group.Token.Take(); err != nil {
			return fmt.Errorf("read token: %w", err)
		}
		client, err = scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
			GitHubConfigURL: group.Url, PersonalAccessToken: strings.TrimSpace(token), SystemInfo: info,
		})
	}
	if err != nil {
		return fmt.Errorf("create scaleset client: %w", err)
	}

	runnerGroupID := 1
	if group.Group != scaleset.DefaultRunnerGroup {
		runnerGroup, err := client.GetRunnerGroupByName(ctx, group.Group)
		if err != nil {
			return fmt.Errorf("get runner group: %w", err)
		}
		runnerGroupID = runnerGroup.ID
	}

	scaleSet, err := ensureRunnerScaleSet(ctx, client, runnerGroupID, group)
	if err != nil {
		return err
	}
	logger.Info("Using runner scale set", "id", scaleSet.ID)

	info.ScaleSetID = scaleSet.ID

	client.SetSystemInfo(info)

	session, err := client.MessageSessionClient(ctx, scaleSet.ID, cfg.Id)
	if err != nil {
		return fmt.Errorf("create message session: %w", err)
	}
	defer func() {
		if err := session.Close(context.WithoutCancel(ctx)); err != nil {
			logger.Error("Close message session", "err", err)
		}
	}()

	statistics := session.Session().Statistics
	lastMessageID := 0
	for ctx.Err() == nil {
		if statistics != nil {
			missing := statistics.TotalAssignedJobs - int(running.Load())
			for range missing {
				if ctx.Err() != nil {
					break
				}
				running.Add(1)
				runners.Go(func() {
					defer running.Add(-1)
					if err := runRunner(ctx, client, scaleSet.ID, cfg, runnerDir); err != nil {
						logger.Error("Runner failed", "err", err)
					}
				})
			}
		}

		message, err := session.GetMessage(ctx, lastMessageID, math.MaxInt32)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("get scaleset message: %w", err)
		}
		if message == nil {
			continue
		}
		if err := session.DeleteMessage(ctx, message.MessageID); err != nil {
			return fmt.Errorf("delete scaleset message: %w", err)
		}

		lastMessageID = message.MessageID
		if len(message.JobAvailableMessages) > 0 {
			ids := make([]int64, 0, len(message.JobAvailableMessages))
			for _, job := range message.JobAvailableMessages {
				ids = append(ids, job.RunnerRequestID)
			}
			if _, err := session.AcquireJobs(ctx, ids); err != nil {
				return fmt.Errorf("acquire jobs: %w", err)
			}
		}

		statistics = message.Statistics
	}

	return nil
}

func ensureRunnerScaleSet(ctx context.Context, client *scaleset.Client, runnerGroupID int, group *Group) (*scaleset.RunnerScaleSet, error) {
	scaleSet, err := client.GetRunnerScaleSet(ctx, runnerGroupID, group.Name)
	if err != nil {
		return nil, fmt.Errorf("get scale set: %w", err)
	}
	if scaleSet != nil {
		return scaleSet, nil
	}

	labels := make([]scaleset.Label, 0, len(group.Labels))
	for _, label := range group.Labels {
		labels = append(labels, scaleset.Label{Name: label})
	}
	scaleSet, err = client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name: group.Name, RunnerGroupID: runnerGroupID, Labels: labels,
		RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
	})
	if err == nil {
		return scaleSet, nil
	}
	// Another host can create the same scale set between our lookup and POST.
	// The API also returns a conflict when a retried POST already succeeded.
	if existing, lookupErr := client.GetRunnerScaleSet(ctx, runnerGroupID, group.Name); lookupErr == nil && existing != nil {
		return existing, nil
	}
	return nil, fmt.Errorf("create scale set: %w", err)
}

func runRunner(ctx context.Context, client *scaleset.Client, scaleSetID int, cfg *Config, runnerDir string) error {
	dir, err := os.MkdirTemp(cfg.DirBuilds, "runner-")
	if err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.Error("Remove runner directory", "dir", dir, "err", err)
		}
	}()
	if err := os.CopyFS(dir, os.DirFS(runnerDir)); err != nil {
		return fmt.Errorf("copy runner: %w", err)
	}
	version, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		return fmt.Errorf("read runner version: %w", err)
	}
	name := cfg.Id + "-" + filepath.Base(dir)
	jit, err := client.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
		Name: name, WorkFolder: "_work",
	}, scaleSetID)
	if err != nil {
		return fmt.Errorf("generate runner config: %w", err)
	}
	registration, err := parseRunnerRegistration(jit.EncodedJITConfig)
	if err != nil {
		return fmt.Errorf("decode runner config: %w", err)
	}
	local, err := worker.New(registration.workerConfig(dir))
	if err != nil {
		return fmt.Errorf("create Worker: %w", err)
	}
	defer local.Close()
	api, err := listener.NewClient(listener.ClientConfig{
		BrokerURL: registration.settings.ServerURLV2, Version: strings.TrimSpace(string(version)),
		Credentials: registration.credentials, PrivateKey: registration.key,
	})
	if err != nil {
		return fmt.Errorf("create runner API: %w", err)
	}
	defer api.Close()
	network, err := listener.New(api, registration.listenerConfig(strings.TrimSpace(string(version))))
	if err != nil {
		return fmt.Errorf("create Listener: %w", err)
	}
	result, err := runJob(ctx, network, local, time.Minute)
	if err != nil {
		return fmt.Errorf("run job: %w", err)
	}
	slog.Info("Runner job completed", "runner", name, "result", result.String())
	return nil
}

// runJob connects independent network supervision and local execution. It owns
// cancellation policy, grace periods and completion after infrastructure failure.
func runJob(ctx context.Context, network *listener.Listener, local *worker.Worker, cancelTimeout time.Duration) (result worker.Result, err error) {
	result = worker.Failed
	var job *listener.RunJob
	completed := false
	defer func() {
		// Keep renewal alive until local execution and server completion settle.
		err = errors.Join(err, local.Close())
		if job != nil && !completed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			conclusion := result
			if !conclusion.Valid() || conclusion == worker.Succeeded || conclusion == worker.SucceededWithIssues {
				conclusion = worker.Failed
			}
			request := &listener.CompleteJobRequest{Conclusion: conclusion.String()}
			if err != nil {
				request.Annotations = []listener.JobAnnotation{{Level: "failure", IsInfrastructureIssue: true,
					Message: "Runner infrastructure failed before normal job completion."}}
			}
			if e := network.CompleteJob(cleanup, request); e != nil {
				err = errors.Join(err, fmt.Errorf("complete job: %w", e))
			}
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		err = errors.Join(err, network.Close(cleanup))
	}()
	if cancelTimeout < 0 {
		return result, errors.New("negative job cancellation timeout")
	}
	if cancelTimeout == 0 {
		cancelTimeout = time.Minute
	}
	job, err = network.WaitJob(ctx)
	if errors.Is(err, listener.ErrShutdown) {
		return worker.Canceled, nil
	}
	if errors.Is(err, listener.ErrJobUnavailable) {
		return worker.Skipped, nil
	}
	if err != nil {
		return result, err
	}
	result, completed, err = executeJob(ctx, network, local, job, cancelTimeout)
	return result, err
}

func executeJob(ctx context.Context, network *listener.Listener, local *worker.Worker, job *listener.RunJob, grace time.Duration) (worker.Result, bool, error) {
	if err := local.StartJob(ctx, job.Raw); err != nil {
		return worker.Failed, false, err
	}
	var cause error
	result, shutdownRequested := worker.Canceled, false
	for cause == nil {
		select {
		case <-local.Done():
			return jobOutcome(local)
		case <-ctx.Done():
			cause, shutdownRequested = ctx.Err(), true
		case event, ok := <-network.Events():
			if !ok {
				cause = errors.New("runner network stopped while job was running")
				break
			}
			switch event.Type {
			case listener.JobCancellation:
				if event.Timeout != nil {
					// Upstream grants at least 60 seconds, then reserves 15 for
					// completion, leaving Worker at least 45 seconds to clean up.
					grace = max(time.Minute, *event.Timeout) - 15*time.Second
				}
				cause = errJobCanceled
			case listener.RunnerShutdown:
				cause, shutdownRequested = errJobCanceled, true
			case listener.LeaseLost:
				cause, result = event.Err, worker.Abandoned
			case listener.NetworkError:
				cause = event.Err
			default:
				cause = errors.New("unsupported runner network event")
			}
		}
	}
	shutdown, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancelShutdown()
	send, cancel := context.WithTimeout(shutdown, 30*time.Second)
	var sendErr error
	if shutdownRequested {
		sendErr = local.Shutdown(send, worker.ShutdownRequested)
	} else {
		sendErr = local.CancelJob(send)
	}
	cancel()
	// A broken pipe can mean execution has completed. Reap before deciding
	// whether the Controller must complete the job through the runner API.
	select {
	case <-local.Done():
		actual, completed, exitErr := jobOutcome(local)
		if errors.Is(cause, errJobCanceled) {
			cause = nil
		}
		return actual, completed, errors.Join(cause, exitErr)
	case <-shutdown.Done():
	}
	closeErr := local.Close()
	if errors.Is(cause, errJobCanceled) {
		cause = errors.New("Worker exceeded cancellation timeout")
	}
	return result, false, errors.Join(cause, sendErr, closeErr)
}

var errJobCanceled = errors.New("job canceled by server")

func jobOutcome(local *worker.Worker) (worker.Result, bool, error) {
	result, err := local.Wait()
	if !result.Valid() {
		return worker.Failed, false, err
	}
	return result, true, err
}
