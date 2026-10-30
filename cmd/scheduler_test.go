// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gilti.dev/hrc/internal/process"
)

func TestHostSchedulerWithoutQuota(t *testing.T) {
	p, err := newHostScheduler(t.TempDir(), Resources{}, Resources{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	if free, _ := p.state(); free != math.MaxInt32 {
		t.Fatalf("unlimited pool capacity = %d", free)
	}
}

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

func newTestScheduler(t *testing.T, root string, total int) *scheduler {
	t.Helper()
	p := newScheduler(root, total)
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}

func acquireTestSlot(t *testing.T, p *scheduler, group *Group) *slot {
	t.Helper()
	p.queue()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s, err := p.acquire(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type slotAcquisition struct {
	slot *slot
	err  error
}

func TestSchedulerQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := newTestScheduler(t, t.TempDir(), 1)
	group := &Group{}
	if free, _ := p.state(); free != 1 {
		t.Fatalf("free = %d, want 1", free)
	}
	first := acquireTestSlot(t, p, group)
	free, released := p.state()
	if free != 0 {
		t.Fatalf("free = %d, want 0", free)
	}

	// A queued runner reserves the next free slot, so no group advertises it.
	p.queue()
	acquired := make(chan slotAcquisition, 1)
	go func() {
		s, err := p.acquire(ctx, group)
		acquired <- slotAcquisition{s, err}
	}()
	select {
	case got := <-acquired:
		t.Fatalf("acquired a slot beyond capacity: %v", got.err)
	case <-time.After(10 * time.Millisecond):
	}
	if free, _ := p.state(); free != 0 {
		t.Fatalf("free = %d with a queued runner, want 0", free)
	}
	if err := p.release(first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	default:
		t.Fatal("release did not notify")
	}
	second := <-acquired
	if second.err != nil {
		t.Fatalf("queued acquire: %v", second.err)
	}
	if free, _ := p.state(); free != 0 {
		t.Fatalf("free = %d after handing the slot over, want 0", free)
	}

	canceled, cancelWait := context.WithCancel(ctx)
	p.queue()
	go func() {
		s, err := p.acquire(canceled, group)
		acquired <- slotAcquisition{s, err}
	}()
	cancelWait()
	if got := <-acquired; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("canceled acquire: %v", got.err)
	}
	if err := p.release(second.slot); err != nil {
		t.Fatal(err)
	}
	if free, _ := p.state(); free != 1 {
		t.Fatalf("free = %d after cancel and release, want 1", free)
	}
}

func TestSchedulerReuse(t *testing.T) {
	root := t.TempDir()
	p := newTestScheduler(t, root, 3)
	group := &Group{Name: "smoke", ReuseDirs: true}
	first := acquireTestSlot(t, p, group)
	second := acquireTestSlot(t, p, group)
	if first.dir != filepath.Join(root, "smoke-1") || second.dir != filepath.Join(root, "smoke-2") {
		t.Fatalf("directories %q, %q", first.dir, second.dir)
	}
	if first.group == second.group {
		t.Fatal("different slots share a process group")
	}
	if err := p.release(first); err != nil {
		t.Fatal(err)
	}
	if err := p.release(second); err != nil {
		t.Fatal(err)
	}
	// Both the directory and PGroup survive a job. The warmest slot is first.
	if s := acquireTestSlot(t, p, group); s != second {
		t.Errorf("took %v, want second slot", s)
	}
	if s := acquireTestSlot(t, p, group); s != first {
		t.Errorf("took %v, want first slot", s)
	}
	if s := acquireTestSlot(t, p, group); s.dir != filepath.Join(root, "smoke-3") {
		t.Errorf("took %q, want a new slot", s.dir)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("slot workspaces survived: %v, %v", entries, err)
	}
	if _, err := first.group.Start(process.Config{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("slot process group survived: %v", err)
	}
	p.queue()
	if _, err := p.acquire(t.Context(), group); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("acquire after Close: %v", err)
	}
}

func TestSchedulerGroupIsolation(t *testing.T) {
	p := newTestScheduler(t, t.TempDir(), 1)
	firstGroup := &Group{Name: "first", ReuseDirs: true}
	secondGroup := &Group{Name: "second", ReuseDirs: true}
	first := acquireTestSlot(t, p, firstGroup)
	if err := os.WriteFile(filepath.Join(first.dir, "cache"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p.queue()
	acquired := make(chan slotAcquisition, 1)
	go func() {
		s, err := p.acquire(ctx, secondGroup)
		acquired <- slotAcquisition{s, err}
	}()
	select {
	case got := <-acquired:
		t.Fatalf("second group exceeded shared host capacity: %v", got.err)
	case <-time.After(10 * time.Millisecond):
	}
	if err := p.release(first); err != nil {
		t.Fatal(err)
	}
	second := <-acquired
	if second.err != nil {
		t.Fatal(second.err)
	}
	if second.slot == first || second.slot.group == first.group {
		t.Fatal("different GitHub groups shared a slot")
	}
	if _, err := os.Stat(filepath.Join(second.slot.dir, "cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("another group's cache is visible: %v", err)
	}
	if err := p.release(second.slot); err != nil {
		t.Fatal(err)
	}
	if s := acquireTestSlot(t, p, firstGroup); s != first {
		t.Fatal("first group's idle slot was not reused")
	}
	if data, err := os.ReadFile(filepath.Join(first.dir, "cache")); err != nil || string(data) != "private" {
		t.Fatalf("first group's cache changed: %q, %v", data, err)
	}
}

func TestSchedulerEphemeralRelease(t *testing.T) {
	p := newTestScheduler(t, t.TempDir(), 1)
	group := &Group{Name: "smoke"}
	first := acquireTestSlot(t, p, group)
	if err := p.release(first); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral directory survived release: %v", err)
	}
	if _, err := first.group.Start(process.Config{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("ephemeral process group survived release: %v", err)
	}
	second := acquireTestSlot(t, p, group)
	if second == first || second.dir == first.dir || second.group == first.group {
		t.Fatal("ephemeral slot was reused")
	}
}

func TestSchedulerAcquireCanceled(t *testing.T) {
	root := filepath.Join(t.TempDir(), "builds")
	p := newTestScheduler(t, root, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p.queue()
	if _, err := p.acquire(ctx, &Group{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire = %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled acquire created a workspace: %v", err)
	}
	if free, _ := p.state(); free != 1 {
		t.Fatalf("canceled acquire leaked capacity: %d", free)
	}
}

func TestSchedulerAcquireFailure(t *testing.T) {
	for _, reusable := range []bool{false, true} {
		t.Run(map[bool]string{false: "ephemeral", true: "reusable"}[reusable], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "builds")
			if err := os.WriteFile(root, nil, 0600); err != nil {
				t.Fatal(err)
			}
			p := newTestScheduler(t, root, 1)
			group := &Group{Name: "smoke", ReuseDirs: reusable}
			p.queue()
			if _, err := p.acquire(t.Context(), group); err == nil {
				t.Fatal("acquired a workspace under a file")
			}
			if free, _ := p.state(); free != 1 {
				t.Fatalf("failed allocation leaked capacity: %d", free)
			}
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			acquireTestSlot(t, p, group)
		})
	}
}

func TestSchedulerCloseWakesWaiter(t *testing.T) {
	p := newTestScheduler(t, t.TempDir(), 1)
	group := &Group{ReuseDirs: true, Name: "smoke"}
	acquireTestSlot(t, p, group)
	p.queue()
	acquired := make(chan error, 1)
	go func() {
		_, err := p.acquire(t.Context(), group)
		acquired <- err
	}()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acquired:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("waiting acquire = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pool shutdown did not wake a waiting runner")
	}
}

func TestSlotClose(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(map[bool]string{false: "busy", true: "idle"}[idle], func(t *testing.T) {
			pool := newTestScheduler(t, t.TempDir(), 1)
			s := acquireTestSlot(t, pool, &Group{Name: "smoke", ReuseDirs: true})
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child, err := s.group.Start(process.Config{Path: self, Dir: s.dir, Args: []string{"hrc-slot-child"}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { child.Kill(); child.Wait(); child.Close() })
			if idle {
				if err := pool.release(s); err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			if state, err := child.Wait(); err != nil || state.Success() {
				t.Fatalf("slot process survived: %v, %v", state, err)
			}
			if _, err := os.Stat(s.dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("slot directory survived: %v", err)
			}
		})
	}
}
