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
	"strconv"
	"sync"

	"gilti.dev/hrc/internal/process"
	"gilti.dev/hrc/internal/runtime"
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

// slot owns one workspace and its process group. Both expire together, even
// when the slot is reused across jobs. Scheduler membership and busy are
// protected by the scheduler's mutex.
type slot struct {
	dir       string
	group     *process.PGroup
	owner     string
	reusable  bool
	busy      bool
	closeOnce sync.Once
	closeErr  error
}

func newSlot(dir string) (*slot, error) {
	group, err := process.NewPGroup()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.Join(err, group.Close())
	}
	return &slot{dir: dir, group: group}, nil
}

func (s *slot) Close() error {
	s.closeOnce.Do(func() {
		// Stop processes before deleting files they may still be using.
		s.closeErr = s.group.Close()
		s.closeErr = errors.Join(s.closeErr, os.RemoveAll(s.dir))
	})
	return s.closeErr
}

// scheduler owns the host's capacity and all workspaces and process groups. A runner
// holds one slot for its Listener's entire lifetime. Reusable slots return to
// their GitHub group's idle list, warmest first; other slots expire on release.
// The limit applies to busy slots, as idle build servers may serve later jobs.
type scheduler struct {
	mu       sync.Mutex
	root     string
	total    int
	used     int
	waiting  int
	all      map[*slot]struct{}
	free     map[string][]*slot
	next     map[string]int
	released chan struct{} // Closed and replaced on release or scheduler shutdown.
	closed   bool
	closeErr error
}

// newHostScheduler sizes the scheduler from the host resources and configured
// reserve and per-job quota. Without a quota it need not inspect the host.
func newHostScheduler(root string, reserve, quota Resources) (*scheduler, error) {
	total := math.MaxInt32
	if quota != (Resources{}) {
		cpu, err := runtime.CPUCapacity()
		if err != nil {
			return nil, fmt.Errorf("detect CPU capacity: %w", err)
		}
		memory, err := runtime.MemoryCapacity()
		if err != nil {
			return nil, fmt.Errorf("detect memory capacity: %w", err)
		}
		host := Resources{CPU: cpu, Memory: ByteSize(memory)}
		total, err = slotCount(host, reserve, quota)
		if err != nil {
			return nil, fmt.Errorf("invalid quota: %w", err)
		}
		slog.Info("Host capacity", "cpu", host.CPU, "memory", host.Memory, "runners", total)
	}
	return newScheduler(root, total), nil
}

func newScheduler(root string, total int) *scheduler {
	return &scheduler{
		root: root, total: total,
		all: make(map[*slot]struct{}), free: make(map[string][]*slot),
		next: make(map[string]int), released: make(chan struct{}),
	}
}

// queue counts a runner as waiting before its goroutine starts, so the slot
// it will take is not advertised meanwhile. Each queue requires one acquire.
func (p *scheduler) queue() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waiting++
}

// acquire waits for capacity and returns a slot belonging to the GitHub group.
// It creates the workspace and PGroup together if no reusable slot is idle.
func (p *scheduler) acquire(ctx context.Context, group *Group) (s *slot, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() { p.waiting-- }()
	for {
		if p.closed {
			return nil, os.ErrClosed
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p.used < p.total {
			break
		}
		released := p.released
		p.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-released:
		}
		p.mu.Lock()
	}
	if idle := p.free[group.Name]; group.ReuseDirs && len(idle) > 0 {
		s = idle[len(idle)-1]
		p.free[group.Name] = idle[:len(idle)-1]
	} else {
		var dir string
		if group.ReuseDirs {
			dir = filepath.Join(p.root, group.Name+"-"+strconv.Itoa(p.next[group.Name]+1))
		} else {
			if err := os.MkdirAll(p.root, 0o700); err != nil {
				return nil, err
			}
			dir, err = os.MkdirTemp(p.root, "runner-")
			if err != nil {
				return nil, err
			}
		}
		s, err = newSlot(dir)
		if err != nil {
			return nil, errors.Join(err, os.RemoveAll(dir))
		}
		s.owner, s.reusable = group.Name, group.ReuseDirs
		p.all[s] = struct{}{}
		if group.ReuseDirs {
			p.next[group.Name]++
		}
	}
	s.busy = true
	p.used++
	return s, nil
}

func (p *scheduler) release(s *slot) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if _, ok := p.all[s]; !ok || !s.busy {
		return errors.New("slot is not acquired from this scheduler")
	}
	var err error
	if s.reusable {
		p.free[s.owner] = append(p.free[s.owner], s)
	} else {
		err = s.Close()
		delete(p.all, s)
	}
	s.busy = false
	p.used--
	p.notify()
	return err
}

func (p *scheduler) notify() {
	close(p.released)
	p.released = make(chan struct{})
}

// state returns the slots not held or awaited by any runner, and a channel
// closed on release or shutdown, so a caller can refresh what it advertised.
func (p *scheduler) state() (free int, released <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, p.released
	}
	return max(p.total-p.used-p.waiting, 0), p.released
}

// Close expires all slots, including idle reusable slots. The controller calls
// it after all group controllers and their runners have stopped.
func (p *scheduler) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		for s := range p.all {
			p.closeErr = errors.Join(p.closeErr, s.Close())
		}
		p.all, p.free = nil, nil
		p.used = 0
		p.notify()
	}
	return p.closeErr
}
