// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"dimidiumlabs.io/hrc/internal/provider"
	"dimidiumlabs.io/hrc/internal/slot"
	"dimidiumlabs.io/hrc/internal/space"
)

type fakeSpace struct {
	created int
	prepare func() error
	calls   *[]string
}

func (f *fakeSpace) Capacity() (space.Resources, error) {
	return space.Resources{CPU: 3, Memory: 8 << 30}, nil
}

func (f *fakeSpace) CreateSlot(string) (slot.Slot, error) {
	f.created++
	if f.prepare != nil {
		if err := f.prepare(); err != nil {
			return nil, err
		}
	}
	return &fakeSlot{calls: f.calls}, nil
}

type fakeSlot struct{ calls *[]string }

func (*fakeSlot) Dir() string { return "unused" }
func (*fakeSlot) Start(slot.Command) (slot.Process, error) {
	return nil, errors.New("unexpected real process")
}
func (s *fakeSlot) Close() error {
	*s.calls = append(*s.calls, "slot-close")
	return nil
}

type fakeQueue struct{ job provider.Job }

func (q *fakeQueue) Serve(ctx context.Context, capacity provider.Capacity, handle func(context.Context, provider.Job) error) error {
	reservation := capacity.Reserve()
	defer reservation.Close()
	if err := reservation.Wait(ctx); err != nil {
		return err
	}
	return handle(ctx, q.job)
}

type fakeJob struct {
	events  chan provider.Event
	local   *fakeRun
	onRun   func()
	calls   *[]string
	result  provider.Result
	failure error
	started bool
}

func (*fakeJob) ID() string                      { return "test-job" }
func (j *fakeJob) Events() <-chan provider.Event { return j.events }
func (j *fakeJob) Run(context.Context, slot.Slot) (provider.Run, error) {
	j.started = true
	if j.onRun != nil {
		j.onRun()
	}
	return j.local, nil
}
func (j *fakeJob) Complete(_ context.Context, result provider.Result, err error) error {
	j.result, j.failure = result, err
	*j.calls = append(*j.calls, "complete")
	return nil
}
func (j *fakeJob) Close(context.Context) error {
	*j.calls = append(*j.calls, "job-close")
	return nil
}

type fakeRun struct {
	done     chan struct{}
	result   provider.Result
	calls    *[]string
	stop     func()
	finished sync.Once
	closed   sync.Once
}

func (r *fakeRun) finish(result provider.Result) {
	r.finished.Do(func() { r.result = result; close(r.done) })
}
func (r *fakeRun) Done() <-chan struct{}          { return r.done }
func (r *fakeRun) Wait() (provider.Result, error) { <-r.done; return r.result, nil }
func (r *fakeRun) Cancel(context.Context) error {
	*r.calls = append(*r.calls, "cancel")
	if r.stop != nil {
		r.stop()
	}
	return nil
}
func (r *fakeRun) Shutdown(context.Context) error {
	*r.calls = append(*r.calls, "shutdown")
	if r.stop != nil {
		r.stop()
	}
	return nil
}
func (r *fakeRun) Close() error {
	r.closed.Do(func() {
		*r.calls = append(*r.calls, "run-close")
		r.finish(provider.Unknown)
	})
	return nil
}

func fakeController(t *testing.T) (*scheduler, *fakeSpace, *fakeJob, *[]string) {
	t.Helper()
	calls := &[]string{}
	environment := &fakeSpace{calls: calls}
	host := newScheduler(environment, 1)
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	})
	run := &fakeRun{done: make(chan struct{}), calls: calls}
	run.stop = func() { run.finish(provider.Canceled) }
	job := &fakeJob{events: make(chan provider.Event, 1), local: run, calls: calls}
	return host, environment, job, calls
}

func TestControllerWithFakeProviderAndSpace(t *testing.T) {
	for _, result := range []provider.Result{provider.Succeeded, provider.Failed} {
		t.Run(result.String(), func(t *testing.T) {
			host, environment, job, calls := fakeController(t)
			job.local.finish(result)
			if err := serve(t.Context(), &fakeQueue{job}, host, &Group{}); err != nil {
				t.Fatal(err)
			}
			if job.result != result || job.failure != nil || environment.created != 1 {
				t.Fatalf("result=%s err=%v created=%d", job.result, job.failure, environment.created)
			}
			if !slices.Equal(*calls, []string{"run-close", "complete", "job-close", "slot-close"}) {
				t.Fatalf("cleanup order: %v", *calls)
			}
			if available, _ := host.State(); available != 1 {
				t.Fatalf("capacity was not released: %d", available)
			}
		})
	}
}

func TestCancellationBeforeExecution(t *testing.T) {
	for _, duringPreparation := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-preparation", true: "during-preparation"}[duringPreparation], func(t *testing.T) {
			host, environment, job, _ := fakeController(t)
			cancel := func() error {
				job.events <- provider.Event{Type: provider.Cancellation}
				return nil
			}
			expected := 0
			if duringPreparation {
				environment.prepare, expected = cancel, 1
			} else {
				cancel()
			}
			if err := serve(t.Context(), &fakeQueue{job}, host, &Group{}); err != nil {
				t.Fatal(err)
			}
			if job.started || job.result != provider.Canceled || environment.created != expected {
				t.Fatalf("started=%v result=%s created=%d", job.started, job.result, environment.created)
			}
		})
	}
}

func TestControllerStopsRuns(t *testing.T) {
	for _, mode := range []string{"cancel", "shutdown", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			host, _, job, calls := fakeController(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			job.onRun = func() {
				if mode == "shutdown" {
					cancel()
				} else {
					job.events <- provider.Event{Type: provider.Cancellation, Grace: 5 * time.Millisecond}
				}
			}
			if mode == "timeout" {
				job.local.stop = nil
			}
			err := serve(ctx, &fakeQueue{job}, host, &Group{})
			if job.result != provider.Canceled || (err != nil) != (mode != "cancel") {
				t.Fatalf("result=%s error=%v", job.result, err)
			}
			if mode == "shutdown" && !errors.Is(err, context.Canceled) {
				t.Fatalf("shutdown cause lost: %v", err)
			}
			command := "cancel"
			if mode == "shutdown" {
				command = "shutdown"
			}
			if !slices.Equal(*calls, []string{command, "run-close", "complete", "job-close", "slot-close"}) {
				t.Fatalf("shutdown order: %v", *calls)
			}
		})
	}
}

func TestPreparationFailureCompletesAcquiredJob(t *testing.T) {
	host, environment, job, calls := fakeController(t)
	failure := errors.New("space is unavailable")
	environment.prepare = func() error { return failure }
	err := serve(t.Context(), &fakeQueue{job}, host, &Group{})
	if !errors.Is(err, failure) || !errors.Is(job.failure, failure) || job.result != provider.Failed || job.started {
		t.Fatalf("started=%v result=%s completion=%v error=%v", job.started, job.result, job.failure, err)
	}
	if !slices.Equal(*calls, []string{"complete", "job-close"}) {
		t.Fatalf("cleanup after preparation failure: %v", *calls)
	}
}

func TestSchedulerUsesSpaceCapacity(t *testing.T) {
	environment := &fakeSpace{}
	host, err := newSpaceScheduler(environment, Resources{}, Resources{CPU: 1, Memory: 2 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if available, _ := host.State(); available != 3 {
		t.Fatalf("capacity=%d, want 3", available)
	}
}
