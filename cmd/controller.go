// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"dimidiumlabs.io/hrc/internal/provider"
)

func serve(ctx context.Context, queue provider.Queue, host *scheduler, group *Group) error {
	return queue.Serve(ctx, host, func(ctx context.Context, job provider.Job) error {
		result, err := runJob(ctx, job, host, group, time.Minute)
		slog.Info("Job completed", "group", group.Name, "job", job.ID(), "result", result.String())
		return err
	})
}

// runJob keeps supervision alive through execution cleanup and completion.
func runJob(ctx context.Context, job provider.Job, host *scheduler, group *Group, grace time.Duration) (result provider.Result, err error) {
	result = provider.Failed
	var instance *allocation
	var local provider.Run
	defer func() {
		if local != nil {
			err = errors.Join(err, local.Close())
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		err = errors.Join(err, job.Complete(cleanup, result, err))
		cancel()
		cleanup, cancel = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err = errors.Join(err, job.Close(cleanup))
		cancel()
		if instance != nil {
			err = errors.Join(err, host.release(instance))
		}
	}()
	if grace < 0 {
		return result, errors.New("negative job cancellation timeout")
	}
	if grace == 0 {
		grace = time.Minute
	}
	if stopped, status, cause := pendingStop(ctx, job); stopped {
		return status, cause
	}
	instance, err = host.acquire(ctx, group)
	if err != nil {
		return result, err
	}
	// Cancellation during preparation must not start execution.
	if stopped, status, cause := pendingStop(ctx, job); stopped {
		return status, cause
	}
	local, err = job.Run(ctx, instance.Slot)
	if err != nil {
		return result, err
	}
	return executeJob(ctx, job, local, grace)
}

func pendingStop(ctx context.Context, job provider.Job) (bool, provider.Result, error) {
	if err := ctx.Err(); err != nil {
		return true, provider.Canceled, err
	}
	select {
	case event, ok := <-job.Events():
		if !ok {
			return true, provider.Failed, errors.New("job supervision stopped before execution")
		}
		switch event.Type {
		case provider.Cancellation, provider.Shutdown:
			return true, provider.Canceled, nil
		case provider.LeaseLost:
			return true, provider.Abandoned, eventError(event)
		default:
			return true, provider.Failed, eventError(event)
		}
	default:
		return false, provider.Unknown, nil
	}
}

func eventError(event provider.Event) error {
	if event.Err != nil {
		return event.Err
	}
	return fmt.Errorf("job supervision failed (event %d)", event.Type)
}

func executeJob(ctx context.Context, job provider.Job, local provider.Run, grace time.Duration) (provider.Result, error) {
	var cause error
	result, shutdownRequested := provider.Canceled, false
	for cause == nil {
		select {
		case <-local.Done():
			return jobOutcome(local)
		case <-ctx.Done():
			cause, shutdownRequested = ctx.Err(), true
		case event, ok := <-job.Events():
			if !ok {
				cause = errors.New("job supervision stopped while execution was running")
				break
			}
			switch event.Type {
			case provider.Cancellation:
				if event.Grace > 0 {
					grace = event.Grace
				}
				cause = errJobCanceled
			case provider.Shutdown:
				cause, shutdownRequested = errJobCanceled, true
			case provider.LeaseLost:
				cause, result = eventError(event), provider.Abandoned
			case provider.NetworkError:
				cause = eventError(event)
			default:
				cause = errors.New("unsupported job supervision event")
			}
		}
	}
	shutdown, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancelShutdown()
	send, cancel := context.WithTimeout(shutdown, 30*time.Second)
	var sendErr error
	if shutdownRequested {
		sendErr = local.Shutdown(send)
	} else {
		sendErr = local.Cancel(send)
	}
	cancel()
	// A failed send may mean that the process has already completed.
	select {
	case <-local.Done():
		actual, exitErr := jobOutcome(local)
		if errors.Is(cause, errJobCanceled) {
			cause = nil
		}
		return actual, errors.Join(cause, exitErr)
	case <-shutdown.Done():
	}
	closeErr := local.Close()
	if errors.Is(cause, errJobCanceled) {
		cause = errors.New("run exceeded cancellation timeout")
	}
	return result, errors.Join(cause, sendErr, closeErr)
}

var errJobCanceled = errors.New("job canceled by server")

func jobOutcome(local provider.Run) (provider.Result, error) {
	result, err := local.Wait()
	if !result.Valid() {
		return provider.Failed, err
	}
	return result, err
}
