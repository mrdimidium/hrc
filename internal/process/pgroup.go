// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package process

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	platformruntime "gilti.dev/hrc/internal/runtime"
)

const trackingGroupEnv = "RUNNER_TRACKING_GROUP"

// PGroup owns processes across multiple child lifetimes, such as the build
// servers shared by successive jobs in one runner slot. On every platform,
// Close finds processes by their inherited RUNNER_TRACKING_GROUP, even after
// changing process group or reparenting. This is best effort: processes can
// discard the environment and process enumeration races with forks. Windows
// also keeps a shared job object as a crash-cleanup safety net.
// Start and Close are safe for concurrent use. Use NewPGroup to create a group;
// a PGroup must not be copied.
type PGroup struct {
	mu       sync.Mutex
	id       string
	closed   bool
	closeErr error
	platform pgroupPlatform
}

// NewPGroup creates an empty group with a fresh tracking identity.
func NewPGroup() (*PGroup, error) {
	g := &PGroup{id: rand.Text()}
	if err := g.configure(); err != nil {
		return nil, err
	}
	return g, nil
}

// environment overrides inherited or caller-supplied tracking identities.
// An explicit Env retains exec.Cmd's semantics: nil inherits, empty does not.
func (g *PGroup) environment(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		match := name == trackingGroupEnv
		if runtime.GOOS == "windows" {
			match = strings.EqualFold(name, trackingGroupEnv)
		}
		if !match {
			result = append(result, entry)
		}
	}
	return append(result, trackingGroupEnv+"="+g.id)
}

// Close prevents new starts, terminates remaining members and frees the group.
// It is idempotent. Callers must still Wait and Close their Process objects.
// On Unix this requires a live owner: SIGKILL or a controller crash cannot run
// environment-based cleanup. On Windows the last job handle closing also kills
// the group's processes.
func (g *PGroup) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.id == "" {
		return errors.New("uninitialized process group")
	}
	if !g.closed {
		g.closed = true
		g.closeErr = g.terminate()
	}
	return g.closeErr
}

func (g *PGroup) terminate() error {
	err := g.killTracked()
	return errors.Join(err, g.release())
}

func trackedProcesses(id string) ([]int, error) {
	pids, err := platformruntime.ProcessIDs()
	if err != nil {
		return nil, err
	}
	var tracked []int
	for _, pid := range pids {
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		value, found, err := platformruntime.LookupProcessEnv(pid, trackingGroupEnv)
		// Other users' processes can be inaccessible, and any process can
		// exit after enumeration. Only readable matching members are ours.
		if err == nil && found && value == id {
			tracked = append(tracked, pid)
		}
	}
	return tracked, nil
}

func (g *PGroup) killTracked() error {
	// Rescan after killing: a member may have forked after the snapshot. Bound
	// cleanup so an unkillable process cannot block controller shutdown forever.
	deadline := time.Now().Add(5 * time.Second)
	for {
		pids, err := trackedProcesses(g.id)
		if err != nil || len(pids) == 0 {
			return err
		}
		var killErr error
		for _, pid := range pids {
			p, err := os.FindProcess(pid)
			if err == nil {
				err = p.Kill()
				p.Release()
			}
			if err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
				killErr = errors.Join(killErr, fmt.Errorf("kill group member %d: %w", pid, err))
			}
		}
		if killErr != nil {
			return killErr
		}
		if time.Now().After(deadline) {
			return errors.New("process group cleanup timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
