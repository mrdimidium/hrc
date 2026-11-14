// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dimidiumlabs.io/hrc/internal/slot"
	slotpgroup "dimidiumlabs.io/hrc/internal/slot-pgroup"
	spacehost "dimidiumlabs.io/hrc/internal/space-host"
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
	if free, _ := p.State(); free != math.MaxInt32 {
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
	environment, err := spacehost.New(root, slotpgroup.New)
	if err != nil {
		t.Fatal(err)
	}
	p := newScheduler(environment, total)
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}

func takeSlot(t *testing.T, p *scheduler, group *Group) (*allocation, func()) {
	t.Helper()
	reservation := p.Reserve()
	if err := reservation.Wait(t.Context()); err != nil {
		reservation.Close()
		t.Fatal(err)
	}
	s, err := p.acquire(t.Context(), group)
	if err != nil {
		reservation.Close()
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() {
		if err := errors.Join(p.release(s), reservation.Close()); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(release)
	return s, release
}

func TestReservationDoesNotPrepareSlot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "builds")
	p := newTestScheduler(t, root, 1)
	first := p.Reserve()
	defer first.Close()
	if available, _ := p.State(); available != 0 {
		t.Fatalf("pending acquisition was advertised: %d", available)
	}
	if err := first.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reservation prepared a workspace: %v", err)
	}
	second := p.Reserve()
	defer second.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	acquired := make(chan error, 1)
	go func() { acquired <- second.Wait(ctx) }()
	select {
	case err := <-acquired:
		t.Fatalf("acquired beyond capacity: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	_, changed := p.State()
	first.Close()
	select {
	case <-changed:
	default:
		t.Fatal("release did not notify queues")
	}
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	second.Close()
	if available, _ := p.State(); available != 1 {
		t.Fatalf("released reservations leaked capacity: %d", available)
	}
}

func TestReservationCancellationAndShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "shutdown"}[shutdown], func(t *testing.T) {
			p := newTestScheduler(t, t.TempDir(), 1)
			first := p.Reserve()
			defer first.Close()
			if err := first.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			waiting := p.Reserve()
			defer waiting.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- waiting.Wait(ctx) }()
			expected := context.Canceled
			if shutdown {
				expected = os.ErrClosed
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, expected) {
					t.Fatalf("waiting acquisition: %v, want %v", err, expected)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("waiter was not woken")
			}
			waiting.Close()
			first.Close()
			if !shutdown {
				if available, _ := p.State(); available != 1 {
					t.Fatalf("canceled reservation leaked capacity: %d", available)
				}
			}
		})
	}
}

func TestSchedulerReuseAndOwnership(t *testing.T) {
	root := t.TempDir()
	p := newTestScheduler(t, root, 2)
	group := &Group{Name: "smoke", ReuseDirs: true}
	first, releaseFirst := takeSlot(t, p, group)
	second, releaseSecond := takeSlot(t, p, group)
	if first.Dir() != filepath.Join(root, "smoke-1") || second.Dir() != filepath.Join(root, "smoke-2") {
		t.Fatalf("unexpected directories: %q, %q", first.Dir(), second.Dir())
	}
	if first.Slot == second.Slot {
		t.Fatal("different allocations share a slot")
	}
	cache := filepath.Join(second.Dir(), "cache")
	if err := os.WriteFile(cache, []byte("warm"), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseFirst()
	releaseSecond()
	reused, releaseReused := takeSlot(t, p, group)
	if reused != second {
		t.Fatal("warmest slot was not reused")
	}
	releaseReused()
	other, releaseOther := takeSlot(t, p, &Group{Name: "other", ReuseDirs: true})
	if other.Slot == second.Slot {
		t.Fatal("different owners shared a slot")
	}
	if _, err := os.Stat(filepath.Join(other.Dir(), "cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("another owner's cache is visible: %v", err)
	}
	releaseOther()
	reused, _ = takeSlot(t, p, group)
	if reused != second {
		t.Fatal("owner's retained slot was lost")
	}
	if data, err := os.ReadFile(cache); err != nil || string(data) != "warm" {
		t.Fatalf("slot state was lost: %q, %v", data, err)
	}
}

func TestEphemeralSlotRelease(t *testing.T) {
	p := newTestScheduler(t, t.TempDir(), 1)
	first, release := takeSlot(t, p, &Group{})
	release()
	if _, err := os.Stat(first.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral workspace survived: %v", err)
	}
	if _, err := first.Start(slot.Command{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("ephemeral process group survived: %v", err)
	}
	second, _ := takeSlot(t, p, &Group{})
	if second.Slot == first.Slot || second.Dir() == first.Dir() {
		t.Fatal("ephemeral slot was reused")
	}
}

func TestSlotPreparationFailure(t *testing.T) {
	for _, reusable := range []bool{false, true} {
		t.Run(map[bool]string{false: "ephemeral", true: "reusable"}[reusable], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "builds")
			if err := os.WriteFile(root, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			p := newTestScheduler(t, root, 1)
			reservation := p.Reserve()
			if err := reservation.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			group := &Group{Name: "smoke", ReuseDirs: reusable}
			if _, err := p.acquire(t.Context(), group); err == nil {
				t.Fatal("prepared a workspace under a file")
			}
			reservation.Close()
			if available, _ := p.State(); available != 1 {
				t.Fatalf("preparation leaked capacity: %d", available)
			}
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			takeSlot(t, p, group)
		})
	}
}

func TestCanceledSlotPreparation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "builds")
	p := newTestScheduler(t, root, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.acquire(ctx, &Group{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled preparation created workspace: %v", err)
	}
}

func TestSlotClose(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(map[bool]string{false: "busy", true: "idle"}[idle], func(t *testing.T) {
			pool := newTestScheduler(t, t.TempDir(), 1)
			s, release := takeSlot(t, pool, &Group{Name: "smoke", ReuseDirs: true})
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child, err := s.Start(slot.Command{Path: self, Args: []string{"hrc-slot-child"}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { child.Kill(); child.Wait(); child.Close() })
			if idle {
				release()
			}
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			if code, err := child.Wait(); err != nil || code == 0 {
				t.Fatalf("slot process survived: %d, %v", code, err)
			}
			if _, err := os.Stat(s.Dir()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("slot directory survived: %v", err)
			}
		})
	}
}
