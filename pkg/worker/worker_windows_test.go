// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package worker

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// sendConsoleCtrlC ignores Ctrl+C in the calling process and sends it to every
// process on its console, as the runner does to cancel a step.
func sendConsoleCtrlC() error {
	if r, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, 1); r == 0 {
		return err
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
}

// A Ctrl+C a job sends to its console must not reach the controller, which
// takes it as a request to stop.
func TestWorkerWindowsOwnConsole(t *testing.T) {
	// Give the test a console for the Worker to share, as a service has. This
	// fails when the test already has one.
	windows.NewLazySystemDLL("kernel32.dll").NewProc("AllocConsole").Call()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	config, output := workerTestConfig(t, "ctrl_c", 1)
	w := newTestWorker(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.StartJob(ctx, workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(output.String(), "ctrl_c sent") {
		select {
		case <-w.Done():
			t.Fatalf("Worker exited before sending Ctrl+C: %s", output.String())
		case <-ctx.Done():
			t.Fatal("Worker did not send Ctrl+C")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case <-interrupts:
		t.Fatal("the Worker's Ctrl+C reached the controller")
	case <-time.After(time.Second):
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerWindowsJobKillsDescendants(t *testing.T) {
	config, output := workerTestConfig(t, "descendant", 1)
	w := newTestWorker(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.StartJob(ctx, workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	var pid uint32
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for pid == 0 {
		for _, line := range strings.Split(output.String(), "\n") {
			if strings.HasPrefix(line, "descendant ") {
				fmt.Sscanf(line, "descendant %d", &pid)
			}
		}
		if pid != 0 {
			break
		}
		select {
		case <-poll.C:
		case <-timer.C:
			t.Fatal("Worker did not start a descendant")
		}
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)
	if state, err := windows.WaitForSingleObject(child, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("descendant is not running: %d %v", state, err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if state, err := windows.WaitForSingleObject(child, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant survived job termination: %d %v", state, err)
	}
	select {
	case <-w.Done():
	case <-timer.C:
		t.Fatal("Worker was not reaped")
	}
}

func TestWorkerWindowsKeepDescendants(t *testing.T) {
	config, output := workerTestConfig(t, "descendant_exit", 1)
	config.Env = append(config.Env, "HRC_WORKER_TEST_EXIT=100")
	config.KeepDescendants = true
	w := newTestWorker(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.StartJob(ctx, workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	var pid uint32
	for pid == 0 && ctx.Err() == nil {
		for _, line := range strings.Split(output.String(), "\n") {
			if strings.HasPrefix(line, "descendant ") {
				fmt.Sscanf(line, "descendant %d", &pid)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("Worker did not start a descendant")
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)
	defer windows.TerminateProcess(child, 1)
	if err := w.CancelJob(ctx); err != nil {
		t.Fatal(err)
	}
	if result, err := waitTestWorker(t, w); result != Succeeded || err != nil {
		t.Fatalf("worker exit = %v, %v", result, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Neither exit nor Close may terminate a kept descendant.
	if state, err := windows.WaitForSingleObject(child, 500); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("kept descendant was terminated: %d %v", state, err)
	}
}
