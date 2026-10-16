// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package worker

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWorkerUnixSymlinkDirectory(t *testing.T) {
	config, _ := workerTestConfig(t, "frames", 1)
	link := filepath.Join(t.TempDir(), "runner")
	if err := os.Symlink(config.Dir, link); err != nil {
		t.Fatal(err)
	}
	config.Dir = link
	// Force Getwd to resolve the physical path, as it does for /var on macOS.
	config.Env = append(config.Env, "HRC_WORKER_TEST_DIR="+link, "PWD=")
	w := newTestWorker(t, config)
	if err := w.StartJob(t.Context(), workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	if result, err := waitTestWorker(t, w); result != Succeeded || err != nil {
		t.Fatalf("Wait with symlinked directory = %v, %v", result, err)
	}
}

func TestWorkerUnixExitKillsDescendants(t *testing.T) {
	for _, code := range []int{17, 100} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			// Keep the socket path below Unix's limit even with long test names.
			dir, err := os.MkdirTemp("", "hrc-child-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "child.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			config, _ := workerTestConfig(t, "descendant_exit", 1)
			config.Env = append(config.Env, "HRC_WORKER_TEST_CHILD_SOCKET="+socket, "HRC_WORKER_TEST_EXIT="+strconv.Itoa(code))
			w := newTestWorker(t, config)
			if err := w.StartJob(t.Context(), workerProcessTestJob(0)); err != nil {
				t.Fatal(err)
			}
			child, err := listener.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer child.Close()
			if err := child.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var ready [1]byte
			if _, err := io.ReadFull(child, ready[:]); err != nil {
				t.Fatalf("descendant did not become ready: %v", err)
			}
			if err := w.CancelJob(t.Context()); err != nil {
				t.Fatal(err)
			}
			result, err := waitTestWorker(t, w)
			if code == 100 {
				if result != Succeeded || err != nil {
					t.Fatalf("normal worker exit = %v, %v", result, err)
				}
			} else {
				var exit *ExitError
				if result != Unknown || !errors.As(err, &exit) || exit.Code != code {
					t.Fatalf("crashed worker exit = %v, %v", result, err)
				}
			}
			// EOF proves termination even when the orphan remains a zombie and
			// kill(pid, 0) would still report that its PID exists.
			if _, err := child.Read(ready[:]); err != io.EOF {
				t.Fatalf("descendant survived worker exit: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
