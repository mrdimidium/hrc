// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package process

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	platformruntime "gilti.dev/hrc/internal/runtime"
)

const childArg = "hrc-process-child"

func TestMain(m *testing.M) {
	runDescendant()
	if len(os.Args) >= 3 && os.Args[1] == childArg {
		os.Exit(testChild(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

// testChild runs in the child started by startChild. It waits for the test's
// go-ahead on its first inherited file, so that it does nothing before joining
// its tree, and can report back on the second.
func testChild(mode string, names []string) int {
	if len(names) != 2 {
		return 40
	}
	var files [2]*os.File
	for i, name := range names {
		handle, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			return 41
		}
		files[i] = os.NewFile(uintptr(handle), "inherited")
	}
	var start [1]byte
	if _, err := io.ReadFull(files[0], start[:]); err != nil {
		return 42
	}
	switch mode {
	case "report":
		if _, err := files[1].Write([]byte("ok")); err != nil {
			return 43
		}
		return 0
	case "descendant", "descendant_exit", "detached", "detached_exit":
		if err := startDescendant(os.Getenv(socketEnv), mode == "detached" || mode == "detached_exit"); err != nil {
			return 44
		}
		if mode == "descendant" || mode == "detached" {
			time.Sleep(time.Hour)
		}
		return 17
	case "interrupt":
		signal.Ignore(os.Interrupt)
		if err := testSendInterrupt(); err != nil {
			fmt.Fprintln(os.Stderr, "interrupt:", err)
			return 45
		}
		return 0
	}
	return 46
}

// newTestPGroup creates a group closed when the test ends.
func newTestPGroup(t *testing.T) *PGroup {
	t.Helper()
	g, err := NewPGroup()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	return g
}

// startChild starts this test binary in mode, returning its go-ahead and report
// files. The process belongs to a group closed when the test ends.
func startChild(t *testing.T, mode string, env ...string) (*Process, *os.File, *os.File, *bytes.Buffer) {
	t.Helper()
	return startGroupChild(t, newTestPGroup(t), mode, env...)
}

func startGroupChild(t *testing.T, g *PGroup, mode string, env ...string) (*Process, *os.File, *os.File, *bytes.Buffer) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	startRead, start, err := platformruntime.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	report, reportWrite, err := platformruntime.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	p, err := g.Start(Config{
		Path: self, Dir: t.TempDir(), Args: []string{childArg, mode},
		Env: append(os.Environ(), env...), Stdout: &output, Stderr: &output,
		Files: []*os.File{startRead, reportWrite},
	})
	startRead.Close()
	reportWrite.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Kill()
		p.Wait()
		p.Close()
		start.Close()
		report.Close()
	})
	return p, start, report, &output
}

const (
	descendantArg = "hrc-process-descendant"
	socketEnv     = "HRC_PROCESS_TEST_SOCKET"
)

// runDescendant runs the descendant and exits if this process is one. It
// connects to its socket and holds the connection until it dies.
func runDescendant() {
	if len(os.Args) != 2 || os.Args[1] != descendantArg {
		return
	}
	connection, err := net.Dial("unix", os.Getenv(socketEnv))
	if err != nil {
		os.Exit(2)
	}
	connection.Write([]byte{1})
	// Keep the connection until killed or the test closes its end.
	io.Copy(io.Discard, connection)
	os.Exit(0)
}

// startDescendant starts a descendant of the calling process that connects to
// socket, without waiting for it.
func startDescendant(socket string, detached bool) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, descendantArg)
	cmd.Env = append(os.Environ(), socketEnv+"="+socket)
	if detached {
		testDetachDescendant(cmd)
	}
	return cmd.Start()
}

// descendantListener accepts the descendant's connection.
type descendantListener struct {
	t        *testing.T
	listener *net.UnixListener
	// Socket is the address to pass to startDescendant.
	Socket string
}

// listenDescendant opens a socket for one descendant, closed when the test ends.
func listenDescendant(t *testing.T) *descendantListener {
	t.Helper()
	// Keep the socket path below the AF_UNIX limit even with long test names.
	dir, err := os.MkdirTemp("", "hrc-child-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "child.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return &descendantListener{t: t, listener: listener, Socket: socket}
}

// descendant is a running descendant, as seen through its connection.
type descendant struct {
	t          *testing.T
	connection *net.UnixConn
}

// Accept waits for the descendant to connect and become ready.
func (l *descendantListener) Accept() *descendant {
	t := l.t
	t.Helper()
	if err := l.listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection, err := l.listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	// Closing our end lets a surviving descendant exit.
	t.Cleanup(func() { connection.Close() })
	if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(connection, ready[:]); err != nil {
		t.Fatalf("descendant did not become ready: %v", err)
	}
	return &descendant{t: t, connection: connection}
}

// Check fails the test unless the descendant is alive exactly when alive is
// true. The connection closing proves death even when the orphan remains a
// zombie and its PID still exists; Windows reports it as a reset, not EOF.
func (d *descendant) Check(alive bool) {
	t := d.t
	t.Helper()
	wait := 10 * time.Second
	if alive {
		wait = 500 * time.Millisecond
	}
	if err := d.connection.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	_, err := d.connection.Read(data[:])
	running := errors.Is(err, os.ErrDeadlineExceeded)
	if alive && !running {
		t.Fatalf("kept descendant was terminated: %v", err)
	}
	if !alive && (running || err == nil) {
		t.Fatalf("descendant survived its tree: %v", err)
	}
}

func TestFiles(t *testing.T) {
	p, start, report, output := startChild(t, "report")
	if _, err := start.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(report)
	if err != nil || string(data) != "ok" {
		t.Fatalf("report = %q, %v", data, err)
	}
	if state, err := p.Wait(); err != nil || state.ExitCode() != 0 {
		t.Fatalf("Wait = %v, %v: %s", state, err, output)
	}
}

// Descendants outlive an individual process, even after Kill or Close. Only
// closing their PGroup terminates them, whether or not they changed session.
func TestDescendants(t *testing.T) {
	for _, tt := range []struct {
		name     string
		stop     func(*Process) error
		detached bool
	}{
		{"exit", nil, false},
		{"kill", (*Process).Kill, false},
		{"close", (*Process).Close, false},
		{"exit-detached", nil, true},
		{"kill-detached", (*Process).Kill, true},
		{"close-detached", (*Process).Close, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener := listenDescendant(t)
			mode := "descendant"
			if tt.detached {
				mode = "detached"
			}
			if tt.stop == nil {
				mode += "_exit"
			}
			g := newTestPGroup(t)
			p, start, _, output := startGroupChild(t, g, mode, socketEnv+"="+listener.Socket)
			if _, err := start.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			descendant := listener.Accept()
			if tt.stop != nil {
				if err := tt.stop(p); err != nil {
					t.Fatal(err)
				}
			}
			state, err := p.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if tt.stop == nil && state.ExitCode() != 17 {
				t.Fatalf("exit = %v: %s", state, output)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			descendant.Check(true)
			if err := g.Close(); err != nil {
				t.Fatal(err)
			}
			descendant.Check(false)
		})
	}
}

// An interrupt the child sends to everything around it, as the GitHub runner
// does to cancel a step, must stay in its tree and not reach the caller.
func TestInterruptStaysInTree(t *testing.T) {
	testShareConsole()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	p, start, _, output := startChild(t, "interrupt")
	if _, err := start.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if state, err := p.Wait(); err != nil || state.ExitCode() != 0 {
		t.Fatalf("interrupt = %v, %v: %s", state, err, output)
	}
	select {
	case <-interrupts:
		t.Fatal("the child's interrupt reached the caller")
	case <-time.After(time.Second):
	}
}
