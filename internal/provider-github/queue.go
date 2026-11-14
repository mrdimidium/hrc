// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package github implements provider contracts using GitHub scale sets,
// independently supervised Listener jobs and the GitHub Worker.
package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dimidiumlabs.io/hrc/internal/provider"
	"dimidiumlabs.io/hrc/internal/provider-github/worker"

	"github.com/actions/scaleset"
)

type Config struct {
	ID, Name, Group    string
	Labels             []string
	Version            string
	RunnerVersion      string
	NewClient          func() (*scaleset.Client, error)
	Worker             WorkerConfig
	LogRoot            string
	RetryMin, RetryMax time.Duration
}

// WorkerConfig describes the local Worker installation used for execution.
// Acquiring jobs does not require this installation.
type WorkerConfig struct {
	Path, Dir      string
	Env            []string
	Stdout, Stderr io.Writer
}

// Validate checks the installation without acquiring a job or creating a slot.
func (c WorkerConfig) Validate() error {
	_, err := worker.New(worker.Config{Path: c.Path, Dir: c.Dir, Env: c.Env})
	return err
}

type Queue struct{ config Config }

// New needs protocol metadata, not an installed Worker. Worker configuration is
// used only when a delivered Job is run.
func New(config Config) (*Queue, error) {
	if config.NewClient == nil || config.RunnerVersion == "" {
		return nil, errors.New("GitHub client factory and runner version are required")
	}
	if config.Group == "" {
		config.Group = scaleset.DefaultRunnerGroup
	}
	if config.RetryMin == 0 {
		config.RetryMin = 5 * time.Second
	}
	if config.RetryMax == 0 {
		config.RetryMax = 2 * time.Minute
	}
	if config.RetryMin < 0 || config.RetryMax < config.RetryMin {
		return nil, errors.New("invalid session retry delays")
	}
	config.Labels = slices.Clone(config.Labels)
	return &Queue{config: config}, nil
}

func (q *Queue) Serve(ctx context.Context, capacity provider.Capacity, handle func(context.Context, provider.Job) error) error {
	logger := slog.Default().With("group", q.config.Name)
	logger.Info("Starting GitHub queue")
	// Acquired jobs retain their own Broker connections across queue failures.
	var runners sync.WaitGroup
	var running atomic.Int32
	defer runners.Wait()
	delay := q.config.RetryMin
	for {
		started := time.Now()
		err := q.serve(ctx, capacity, handle, &runners, &running)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > q.config.RetryMax {
			delay = q.config.RetryMin
		}
		logger.Error("Group session failed, retrying", "err", err, "delay", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(2*delay, q.config.RetryMax)
	}
}

func (q *Queue) serve(ctx context.Context, capacity provider.Capacity, handle func(context.Context, provider.Job) error, runners *sync.WaitGroup, running *atomic.Int32) error {
	cfg := &q.config
	logger := slog.Default().With("group", cfg.Name)
	info := scaleset.SystemInfo{System: "hrc", Version: cfg.Version}
	client, err := cfg.NewClient()
	if err != nil {
		return fmt.Errorf("create scaleset client: %w", err)
	}
	runnerGroupID := 1
	if cfg.Group != scaleset.DefaultRunnerGroup {
		runnerGroup, err := client.GetRunnerGroupByName(ctx, cfg.Group)
		if err != nil {
			return fmt.Errorf("get runner group: %w", err)
		}
		runnerGroupID = runnerGroup.ID
	}

	scaleSet, err := ensureRunnerScaleSet(ctx, client, runnerGroupID, cfg)
	if err != nil {
		return err
	}
	logger.Info("Using runner scale set", "id", scaleSet.ID)

	info.ScaleSetID = scaleSet.ID

	client.SetSystemInfo(info)

	session, err := client.MessageSessionClient(ctx, scaleSet.ID, cfg.ID)
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
	interrupted := false
	for ctx.Err() == nil {
		// Statistics are stale after an interrupted poll: a runner that just
		// finished may still count as assigned, and would be replaced needlessly.
		if statistics != nil && !interrupted {
			missing := statistics.TotalAssignedJobs - int(running.Load())
			for range missing {
				if ctx.Err() != nil {
					break
				}
				running.Add(1)
				reservation := capacity.Reserve()
				runners.Go(func() {
					err := reservation.Wait(ctx)
					if err == nil {
						var job provider.Job
						job, err = q.acquire(ctx, client, scaleSet.ID)
						if job != nil {
							err = handle(ctx, job)
						}
					}
					// Leave the group's count before advertising freed capacity.
					running.Add(-1)
					err = errors.Join(err, reservation.Close())
					if err != nil && ctx.Err() == nil {
						logger.Error("Runner failed", "err", err)
					}
				})
			}
		}

		// GitHub assigns the scale set at most the advertised capacity: the
		// group's own runners plus the host's free slots. A release restarts
		// the long poll, so GitHub learns about the freed slot immediately.
		// An unacknowledged message is delivered again by the next poll.
		free, released := capacity.State()
		capacity := min(int(running.Load())+free, math.MaxInt32)
		poll, cancelPoll := context.WithCancel(ctx)
		go func() {
			select {
			case <-released:
				cancelPoll()
			case <-poll.Done():
			}
		}()
		message, err := session.GetMessage(poll, lastMessageID, capacity)
		interrupted = err != nil && poll.Err() != nil
		cancelPoll()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if interrupted {
				continue
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

// ensureRunnerScaleSet finds or creates the group's scale set. The Actions
// service fixes a scale set's labels at creation: an update returns success
// but keeps the old labels. A scale set whose labels differ from the
// configuration is therefore deleted and created again, with a new ID.
func ensureRunnerScaleSet(ctx context.Context, client *scaleset.Client, runnerGroupID int, group *Config) (*scaleset.RunnerScaleSet, error) {
	// Without configured labels, GitHub routes by the scale set name.
	labels := []scaleset.Label{{Name: group.Name}}
	if len(group.Labels) > 0 {
		labels = make([]scaleset.Label, 0, len(group.Labels))
		for _, label := range group.Labels {
			labels = append(labels, scaleset.Label{Name: label})
		}
	}
	create := func() (*scaleset.RunnerScaleSet, error) {
		scaleSet, err := client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
			Name: group.Name, RunnerGroupID: runnerGroupID, Labels: slices.Clone(labels),
			RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
		})
		if err == nil {
			return scaleSet, nil
		}
		// Another host can create the same scale set between our lookup and POST.
		// The API also returns a conflict when a retried POST already succeeded.
		existing, lookupErr := client.GetRunnerScaleSet(ctx, runnerGroupID, group.Name)
		if lookupErr != nil || existing == nil {
			return nil, fmt.Errorf("create scale set: %w", err)
		}
		return existing, nil
	}

	scaleSet, err := client.GetRunnerScaleSet(ctx, runnerGroupID, group.Name)
	if err != nil {
		return nil, fmt.Errorf("get scale set: %w", err)
	}
	if scaleSet == nil {
		return create()
	}
	if sameLabels(scaleSet.Labels, labels) {
		return scaleSet, nil
	}
	// Labels change only with the configuration, so this happens when hrc
	// starts, before the group has runners. A failed creation is retried by the
	// next session, which no longer finds the scale set.
	slog.Info("Recreating runner scale set with the configured labels", "group", group.Name, "id", scaleSet.ID,
		"labels", labelNames(scaleSet.Labels), "configured", labelNames(labels))
	if err := client.DeleteRunnerScaleSet(ctx, scaleSet.ID); err != nil {
		return nil, fmt.Errorf("delete scale set with outdated labels: %w", err)
	}
	return create()
}

func labelNames(labels []scaleset.Label) []string {
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		names = append(names, label.Name)
	}
	return names
}

// sameLabels compares label names as sets: runs-on matching ignores order
// and case.
func sameLabels(current, desired []scaleset.Label) bool {
	names := func(labels []scaleset.Label) []string {
		result := make([]string, 0, len(labels))
		for _, label := range labels {
			result = append(result, strings.ToLower(label.Name))
		}
		slices.Sort(result)
		return slices.Compact(result)
	}
	return slices.Equal(names(current), names(desired))
}
