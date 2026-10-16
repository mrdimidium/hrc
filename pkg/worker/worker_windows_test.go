// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

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
