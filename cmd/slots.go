// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"fmt"
	"math"
	"sync"
)

// slotCount returns how many runners fit on the host, each holding quota after
// the host keeps reserve. Without a quota the count is unlimited.
func slotCount(host, reserve, quota Resources) (int, error) {
	total := math.MaxInt32
	if quota.CPU > 0 {
		// The epsilon keeps quotas such as 0.1 from losing a slot to rounding.
		n := math.Floor((host.CPU-reserve.CPU)/quota.CPU + 1e-9)
		total = min(total, int(max(n, 0)))
	}
	if quota.Memory > 0 {
		n := ByteSize(0)
		if host.Memory > reserve.Memory {
			n = (host.Memory - reserve.Memory) / quota.Memory
		}
		total = min(total, int(min(n, math.MaxInt32)))
	}
	if total == 0 {
		return 0, fmt.Errorf("quota (cpu %g, memory %s) exceeds host resources (cpu %g, memory %s) minus reserve (cpu %g, memory %s)",
			quota.CPU, quota.Memory, host.CPU, host.Memory, reserve.CPU, reserve.Memory)
	}
	return total, nil
}

// slots is the host capacity shared by all groups. A runner holds a slot from
// before its directory is created until the directory is removed, which covers
// the whole lifetime of its Listener. Runners that wait for a slot form the
// local queue for jobs that GitHub assigned beyond the free capacity.
type slots struct {
	mu       sync.Mutex
	total    int
	used     int
	waiting  int
	released chan struct{} // Closed and replaced on every release.
}

func newSlots(total int) *slots {
	return &slots{total: total, released: make(chan struct{})}
}

// queue counts a runner as waiting before its goroutine starts, so the slot
// it will take is not advertised meanwhile. Each queue requires one acquire.
func (s *slots) queue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiting++
}

// acquire waits for a free slot for a queued runner or until ctx is canceled.
func (s *slots) acquire(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.waiting-- }()
	for s.used >= s.total {
		released := s.released
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			return ctx.Err()
		case <-released:
		}
		s.mu.Lock()
	}
	s.used++
	return nil
}

func (s *slots) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.used--
	close(s.released)
	s.released = make(chan struct{})
}

// state returns the slots not held or awaited by any runner, and a channel
// closed on the next release, so a caller can refresh what it advertised.
func (s *slots) state() (free int, released <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return max(s.total-s.used-s.waiting, 0), s.released
}
