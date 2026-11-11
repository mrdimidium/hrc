// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package process

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestFindTrackedProcessErrors(t *testing.T) {
	// Waiting for a child does not guarantee that its PID is gone: external
	// handles can keep the exited process object alive indefinitely. Use an
	// invalid PID instead of depending on object teardown (or PID reuse).
	// OpenProcess converts -1 to DWORD 0xffffffff, which is not a process ID.
	// OpenProcess also returns ERROR_INVALID_PARAMETER for PID 0 (System Idle).
	for _, test := range []struct {
		name string
		pid  int
		want error
	}{
		{"missing", -1, os.ErrProcessDone},
		{"system idle", 0, os.ErrProcessDone},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracked, err := findTrackedProcess(test.pid)
			if tracked != nil {
				tracked.Release()
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("find process %d = %v, want %v", test.pid, err, test.want)
			}
		})
	}
}

func TestFindTrackedProcessRunning(t *testing.T) {
	p, start, _, _ := startChild(t, "report")
	tracked, err := findTrackedProcess(p.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer tracked.Release()
	if tracked.Pid != p.cmd.Process.Pid {
		t.Fatalf("found PID %d, want %d", tracked.Pid, p.cmd.Process.Pid)
	}
	if _, err := start.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if state, err := p.Wait(); err != nil || state.ExitCode() != 0 {
		t.Fatalf("Wait = %v, %v", state, err)
	}
}

func TestKillTrackedProcessAlreadyExited(t *testing.T) {
	p, start, _, _ := startChild(t, "report")
	tracked, err := os.FindProcess(p.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer tracked.Release()
	if _, err := start.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if state, err := p.Wait(); err != nil || state.ExitCode() != 0 {
		t.Fatalf("Wait = %v, %v", state, err)
	}
	// The independent scanner handle keeps the exited process object alive.
	// This deterministically reproduces the error from the CI failure without
	// relying on the timing between enumeration and process teardown.
	if err := tracked.Kill(); !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		t.Fatalf("Kill after exit = %v, want ERROR_ACCESS_DENIED", err)
	}
	if err := killTrackedProcess(tracked, time.Now()); err != nil {
		t.Fatalf("cleanup of exited process: %v", err)
	}
}

func TestKillTrackedProcessWaitsForExit(t *testing.T) {
	p, _, _, _ := startChild(t, "report")
	tracked, err := os.FindProcess(p.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer tracked.Release()
	if err := killTrackedProcess(tracked, time.Now().Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tracked.WithHandle(func(handle uintptr) {
		state, err := syscall.WaitForSingleObject(syscall.Handle(handle), 0)
		if err != nil || state != syscall.WAIT_OBJECT_0 {
			t.Errorf("cleanup returned before process exit: state=%d, err=%v", state, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
}
