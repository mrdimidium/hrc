// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestSlotCount(t *testing.T) {
	const gib = 1 << 30
	host := Resources{CPU: 32, Memory: 128 * gib}
	for _, tt := range []struct {
		name           string
		reserve, quota Resources
		want           int
		err            string
	}{
		{name: "unlimited", want: math.MaxInt32},
		{name: "hetzner", reserve: Resources{CPU: 2, Memory: 8 * gib}, quota: Resources{CPU: 2, Memory: 8 * gib}, want: 15},
		{name: "memory bound", quota: Resources{CPU: 1, Memory: 16 * gib}, want: 8},
		{name: "cpu bound", quota: Resources{CPU: 4, Memory: gib}, want: 8},
		{name: "cpu only", quota: Resources{CPU: 3}, want: 10},
		{name: "memory only", reserve: Resources{Memory: 64 * gib}, quota: Resources{Memory: 16 * gib}, want: 4},
		{name: "fractional cpu", quota: Resources{CPU: 0.1}, want: 320},
		{name: "quota exceeds host", quota: Resources{CPU: 64}, err: "exceeds host resources"},
		{name: "reserve exceeds host", reserve: Resources{Memory: 256 * gib}, quota: Resources{Memory: gib}, err: "exceeds host resources"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := slotCount(host, tt.reserve, tt.quota)
			checkError(t, err, tt.err)
			if got != tt.want {
				t.Errorf("slotCount = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSlotsQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := newSlots(1)
	if free, _ := s.state(); free != 1 {
		t.Fatalf("free = %d, want 1", free)
	}
	s.queue()
	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	free, released := s.state()
	if free != 0 {
		t.Fatalf("free = %d, want 0", free)
	}

	// A queued runner reserves the next free slot, so no group advertises it.
	s.queue()
	acquired := make(chan error)
	go func() { acquired <- s.acquire(ctx) }()
	time.Sleep(10 * time.Millisecond) // Give a broken acquire time to succeed.
	select {
	case err := <-acquired:
		t.Fatalf("acquired a slot beyond capacity: %v", err)
	default:
	}

	if free, _ := s.state(); free != 0 {
		t.Fatalf("free = %d with a queued runner, want 0", free)
	}
	s.release()
	select {
	case <-released:
	default:
		t.Fatal("release did not notify")
	}
	if err := <-acquired; err != nil {
		t.Fatalf("queued acquire: %v", err)
	}
	if free, _ := s.state(); free != 0 {
		t.Fatalf("free = %d after handing the slot over, want 0", free)
	}

	canceled, cancelWait := context.WithCancel(ctx)
	s.queue()
	go func() { acquired <- s.acquire(canceled) }()
	cancelWait()
	if err := <-acquired; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire: %v", err)
	}
	s.release()
	if free, _ := s.state(); free != 1 {
		t.Fatalf("free = %d after cancel and release, want 1", free)
	}
}
