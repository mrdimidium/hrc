// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package github

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dimidiumlabs.io/hrc/internal/provider"
	"dimidiumlabs.io/hrc/internal/provider-github/listener"
	"dimidiumlabs.io/hrc/internal/provider-github/worker"
	"dimidiumlabs.io/hrc/internal/slot"

	"github.com/actions/scaleset"
)

// acquire registers independently of a workspace. Only the returned Job can
// create a Run, after its consumer has selected an execution slot.
func (q *Queue) acquire(ctx context.Context, client *scaleset.Client, scaleSetID int) (provider.Job, error) {
	jit, err := client.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
		Name: q.config.ID + "-runner-" + rand.Text(), WorkFolder: ".",
	}, scaleSetID)
	if err != nil {
		return nil, fmt.Errorf("generate runner config: %w", err)
	}
	registration, err := parseRunnerRegistration(jit.EncodedJITConfig)
	if err != nil {
		return nil, fmt.Errorf("decode runner config: %w", err)
	}
	api, err := listener.NewClient(listener.ClientConfig{
		BrokerURL: registration.settings.ServerURLV2, Version: q.config.RunnerVersion,
		Credentials: registration.credentials, PrivateKey: registration.key,
	})
	if err != nil {
		return nil, fmt.Errorf("create runner API: %w", err)
	}
	network, err := listener.New(api, registration.listenerConfig(q.config.RunnerVersion))
	if err != nil {
		return nil, errors.Join(err, api.Close())
	}
	raw, err := network.WaitJob(ctx)
	j := &job{
		config: q.config, registration: registration, network: network, api: api, raw: raw,
		events: make(chan provider.Event), closed: make(chan struct{}),
	}
	go j.watch()
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if raw != nil {
			err = errors.Join(err, j.Complete(cleanup, provider.Failed, err))
		}
		closeErr := j.Close(cleanup)
		if errors.Is(err, listener.ErrShutdown) || errors.Is(err, listener.ErrJobUnavailable) {
			return nil, closeErr
		}
		return nil, errors.Join(err, closeErr)
	}
	return j, nil
}

type job struct {
	config       Config
	registration *runnerRegistration
	network      *listener.Listener
	api          *listener.Client
	raw          *listener.RunJob
	events       chan provider.Event
	closed       chan struct{}
	local        *worker.Worker
	started      bool
	closeOnce    sync.Once
	closeErr     error
}

func (j *job) ID() string                    { return j.raw.JobID }
func (j *job) Events() <-chan provider.Event { return j.events }

func (j *job) watch() {
	defer close(j.events)
	for {
		select {
		case event, ok := <-j.network.Events():
			if !ok {
				return
			}
			converted := provider.Event{Err: event.Err}
			switch event.Type {
			case listener.JobCancellation:
				converted.Type = provider.Cancellation
				if event.Timeout != nil {
					// GitHub grants at least a minute, reserving 15s for completion.
					converted.Grace = max(time.Minute, *event.Timeout) - 15*time.Second
				}
			case listener.RunnerShutdown:
				converted.Type = provider.Shutdown
			case listener.LeaseLost:
				converted.Type = provider.LeaseLost
			case listener.NetworkError:
				converted.Type = provider.NetworkError
			default:
				converted.Type = provider.NetworkError
				converted.Err = errors.New("unsupported runner network event")
			}
			select {
			case j.events <- converted:
			case <-j.closed:
				return
			}
		case <-j.closed:
			return
		}
	}
}

func (j *job) Run(ctx context.Context, target slot.Slot) (provider.Run, error) {
	select {
	case <-j.closed:
		return nil, errors.New("job is closed")
	default:
	}
	if j.started {
		return nil, errors.New("job already started a run")
	}
	j.started = true
	id := j.ID()
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\:") {
		return nil, fmt.Errorf("unsafe job ID: %q", id)
	}
	if target == nil {
		return nil, errors.New("execution slot is required")
	}
	config := j.registration.workerConfig(target.Dir())
	config.Slot = target
	config.Path, config.Dir = j.config.Worker.Path, j.config.Worker.Dir
	config.Env = j.config.Worker.Env
	config.Stdout, config.Stderr = j.config.Worker.Stdout, j.config.Worker.Stderr
	local, err := worker.New(config)
	if err != nil {
		return nil, fmt.Errorf("create Worker: %w", err)
	}
	j.local = local
	diag := filepath.Join(j.config.LogRoot, "jobs", id)
	if err := os.MkdirAll(diag, 0o700); err != nil {
		return nil, errors.Join(fmt.Errorf("create job diagnostics: %w", err), local.Close())
	}
	if err := local.StartJob(ctx, j.raw.Raw, diag); err != nil {
		return nil, errors.Join(err, local.Close())
	}
	return &run{local: local}, nil
}

func (j *job) Complete(ctx context.Context, result provider.Result, runErr error) error {
	if j.local != nil {
		select {
		case <-j.local.Done():
		default:
			return errors.New("cannot complete a job while its Worker is running")
		}
		if actual, _ := j.local.Wait(); actual.Valid() {
			// The .NET Worker reports normal completion itself.
			return nil
		}
	}
	if !result.Valid() || result == provider.Succeeded || result == provider.SucceededWithIssues {
		result = provider.Failed
	}
	request := &listener.CompleteJobRequest{Conclusion: result.String()}
	if runErr != nil {
		request.Annotations = []listener.JobAnnotation{{
			Level: "failure", IsInfrastructureIssue: true,
			Message: "Runner infrastructure failed before normal job completion.",
		}}
	}
	return j.network.CompleteJob(ctx, request)
}

func (j *job) Close(ctx context.Context) error {
	j.closeOnce.Do(func() {
		close(j.closed)
		j.closeErr = errors.Join(j.network.Close(ctx), j.api.Close())
	})
	return j.closeErr
}

type run struct{ local *worker.Worker }

func (r *run) Done() <-chan struct{} { return r.local.Done() }
func (r *run) Wait() (provider.Result, error) {
	result, err := r.local.Wait()
	return provider.Result(result.String()), err
}
func (r *run) Cancel(ctx context.Context) error { return r.local.CancelJob(ctx) }
func (r *run) Shutdown(ctx context.Context) error {
	return r.local.Shutdown(ctx, worker.ShutdownRequested)
}
func (r *run) Close() error { return r.local.Close() }
