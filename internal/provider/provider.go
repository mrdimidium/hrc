// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package provider separates job acquisition and supervision from execution.
package provider

import (
	"context"
	"time"

	"dimidiumlabs.io/hrc/internal/slot"
)

// Queue delivers acquired jobs. Serve may call handle concurrently, after
// reserving capacity. It retains reservations until handle has returned and
// waits for all handlers before returning. Job lifetime is independent of an
// individual queue polling session. Each handler owns its Job and must complete
// and close it before returning, even when execution never starts.
type Queue interface {
	Serve(context.Context, Capacity, func(context.Context, Job) error) error
}

// Capacity admits job acquisition without creating an execution environment.
// Reserve accounts for demand immediately, before a goroutine starts waiting.
type Capacity interface {
	Reserve() Reservation
	State() (available int, changed <-chan struct{})
}

// Reservation must be closed even if Wait fails. Waiting acquires capacity,
// without creating a slot or starting a Run.
type Reservation interface {
	Wait(context.Context) error
	Close() error
}

// Job is already acquired and supervised when delivered. Complete runs after
// execution stops; a provider may already have reported normal completion.
// Run starts at most once. Close is idempotent and releases supervision and
// credentials, including when no run starts; it does not stop an active Run.
type Job interface {
	ID() string
	Events() <-chan Event
	Run(context.Context, slot.Slot) (Run, error)
	Complete(context.Context, Result, error) error
	Close(context.Context) error
}

// Run owns one execution, while its slot can outlive it. Close must stop and
// reap execution before Job.Complete and is safe to call repeatedly.
type Run interface {
	Done() <-chan struct{}
	Wait() (Result, error)
	Cancel(context.Context) error
	Shutdown(context.Context) error
	Close() error
}

type EventType int

const (
	Cancellation EventType = iota
	Shutdown
	LeaseLost
	NetworkError
)

// Events stop execution. A positive Grace overrides the controller's default;
// provider-specific timeout encodings and adjustments are resolved beforehand.
type Event struct {
	Type  EventType
	Grace time.Duration
	Err   error
}

type Result string

const (
	Unknown             Result = "unknown"
	Succeeded           Result = "succeeded"
	SucceededWithIssues Result = "succeededWithIssues"
	Failed              Result = "failed"
	Canceled            Result = "canceled"
	Skipped             Result = "skipped"
	Abandoned           Result = "abandoned"
)

func (r Result) String() string { return string(r) }

func (r Result) Valid() bool {
	switch r {
	case Succeeded, SucceededWithIssues, Failed, Canceled, Skipped, Abandoned:
		return true
	default:
		return false
	}
}
