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
	"runtime"
	"strconv"
	"testing"
	"time"
)

const childArg = "hrc-process-child"

func TestMain(m *testing.M) {
	runDescendant()
	if len(os.Args) >= 3 && os.Args[1] == childArg {
		os.Exit(testChild(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

// sendInterrupt interrupts every process in the caller's process group, as the
// GitHub runner does to cancel a step. Windows replaces it with a Ctrl+C to
// the caller's console.
var sendInterrupt = func() error {
	group, err := os.FindProcess(-os.Getpid())
	if err != nil {
		return err
	}
	defer group.Release()
	return group.Signal(os.Interrupt)
}

// shareConsole gives the test a console for its children to share, as a
// service has. Only Windows has consoles to share.
var shareConsole = func() {}

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
	case "descendant", "descendant_exit":
		if err := startDescendant(os.Getenv(socketEnv)); err != nil {
			return 44
		}
		if mode == "descendant" {
			time.Sleep(time.Hour)
		}
		return 17
	case "interrupt":
		signal.Ignore(os.Interrupt)
		if err := sendInterrupt(); err != nil {
			fmt.Fprintln(os.Stderr, "interrupt:", err)
			return 45
		}
		return 0
	}
	return 46
}

// startChild starts this test binary as a child in mode. It returns the file
// that gives the child its go-ahead and the one the child reports on.
func startChild(t *testing.T, mode string, keep bool, env ...string) (*Process, *os.File, *os.File, *bytes.Buffer) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	startRead, start, err := Pipe()
	if err != nil {
		t.Fatal(err)
	}
	report, reportWrite, err := Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	p, err := Start(Config{
		Path: self, Dir: t.TempDir(), Args: []string{childArg, mode},
		Env: append(os.Environ(), env...), Stdout: &output, Stderr: &output,
		Files: []*os.File{startRead, reportWrite}, KeepDescendants: keep,
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
func startDescendant(socket string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, descendantArg)
	cmd.Env = append(os.Environ(), socketEnv+"="+socket)
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
	p, start, report, output := startChild(t, "report", false)
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

// Descendants die with the tree. They survive the child only when it exited on
// its own and KeepDescendants holds, and only on Windows: Unix always kills the
// process group, so there only daemons that left it survive.
func TestDescendants(t *testing.T) {
	for _, tt := range []struct {
		name       string
		exit, keep bool
	}{
		{"exit", true, false},
		{"exit-keep", true, true},
		{"kill", false, false},
		{"kill-keep", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener := listenDescendant(t)
			mode := "descendant"
			if tt.exit {
				mode = "descendant_exit"
			}
			p, start, _, output := startChild(t, mode, tt.keep, socketEnv+"="+listener.Socket)
			if _, err := start.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			descendant := listener.Accept()
			if !tt.exit {
				if err := p.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			state, err := p.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if tt.exit && state.ExitCode() != 17 {
				t.Fatalf("exit = %v: %s", state, output)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			descendant.Check(tt.exit && tt.keep && runtime.GOOS == "windows")
		})
	}
}

// An interrupt the child sends to everything around it, as the GitHub runner
// does to cancel a step, must stay in its tree and not reach the caller.
func TestInterruptStaysInTree(t *testing.T) {
	shareConsole()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	p, start, _, output := startChild(t, "interrupt", false)
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
