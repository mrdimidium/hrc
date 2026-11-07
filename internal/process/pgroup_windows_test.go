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
