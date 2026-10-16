// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package worker

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

func TestMain(m *testing.M) {
	if os.Getenv("HRC_WORKER_PROCESS_TEST") == "1" && len(os.Args) == 2 && os.Args[1] == "holdchild" {
		if socket := os.Getenv("HRC_WORKER_TEST_CHILD_SOCKET"); socket != "" {
			connection, err := net.Dial("unix", socket)
			if err != nil {
				os.Exit(32)
			}
			connection.Write([]byte{1})
			// Keep the socket open until killed or the test closes its end.
			io.Copy(io.Discard, connection)
			connection.Close()
			os.Exit(0)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if os.Getenv("HRC_WORKER_PROCESS_TEST") == "1" && len(os.Args) == 4 && os.Args[1] == "spawnclient" {
		os.Exit(workerProcessTestChild())
	}
	os.Exit(m.Run())
}

// Decode independently of the production encoder in a real child process.
func workerProcessTestChild() int {
	fd, err := strconv.ParseUint(os.Args[2], 10, 64)
	if err != nil {
		return 20
	}
	input := os.NewFile(uintptr(fd), "worker-test-input")
	defer input.Close()
	// macOS may resolve /var to /private/var; compare directories, not paths.
	cwd, err := os.Stat(".")
	want, wantErr := os.Stat(filepath.Join(os.Getenv("HRC_WORKER_TEST_DIR"), "bin"))
	if err != nil || wantErr != nil || !os.SameFile(cwd, want) {
		return 21
	}
	fmt.Fprintln(os.Stdout, "ready")
	read := func() (int32, []byte, error) {
		var header [8]byte
		if _, err := io.ReadFull(input, header[:]); err != nil {
			return 0, nil, err
		}
		size := binary.LittleEndian.Uint32(header[4:])
		if size > 50_000_000 || size%2 != 0 {
			return 0, nil, errors.New("invalid frame size")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(input, data); err != nil {
			return 0, nil, err
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[2*i:])
		}
		return int32(binary.LittleEndian.Uint32(header[:])), []byte(string(utf16.Decode(units))), nil
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "blocked" {
		time.Sleep(time.Hour)
		return 22
	}
	if expected := os.Getenv("HRC_WORKER_TEST_EXPECT_ENV"); expected != "" &&
		(os.Getenv("HRC_WORKER_TEST_LAYER") != expected || os.Getenv("HRC_WORKER_TEST_KEEP") != "inherited") {
		return 29
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "payload" {
		kind, body, err := read()
		if err != nil || kind != 1 || string(body) != os.Getenv("HRC_WORKER_TEST_PAYLOAD") {
			return 30
		}
		code, _ := strconv.Atoi(os.Getenv("HRC_WORKER_TEST_EXIT"))
		return code
	}
	count, _ := strconv.Atoi(os.Getenv("HRC_WORKER_TEST_COUNT"))
	seen := map[int]bool{}
	for range count {
		kind, body, err := read()
		if err != nil || kind != 1 {
			return 23
		}
		var message struct {
			Index int
			Text  string
			Data  string
		}
		if json.Unmarshal(body, &message) != nil || message.Text != "Привет 🌍" || len(message.Data) != 20_000 || seen[message.Index] {
			return 24
		}
		seen[message.Index] = true
	}
	if mode := os.Getenv("HRC_WORKER_TEST_MODE"); mode == "descendant" || mode == "descendant_exit" {
		self, err := os.Executable()
		if err != nil {
			return 26
		}
		child := exec.Command(self, "holdchild")
		if err = child.Start(); err != nil {
			return 27
		}
		fmt.Fprintln(os.Stdout, "descendant", child.Process.Pid)
		if mode == "descendant_exit" {
			// Let the test observe the live child before exiting the worker.
			kind, _, err := read()
			if err != nil || kind != 2 {
				return 33
			}
			code, _ := strconv.Atoi(os.Getenv("HRC_WORKER_TEST_EXIT"))
			return code
		}
		time.Sleep(time.Hour)
		return 28
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "cancel" {
		kind, body, err := read()
		if err != nil || kind < 2 || kind > 4 || len(body) != 0 {
			return 25
		}
		fmt.Fprintln(os.Stderr, "cancel", kind)
		return 103
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "controls" {
		count, _ := strconv.Atoi(os.Getenv("HRC_WORKER_TEST_COMMAND_COUNT"))
		for range count {
			kind, body, err := read()
			if err != nil || kind < 2 || kind > 4 || len(body) != 0 {
				return 31
			}
		}
		return 103
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "ignore" {
		time.Sleep(time.Hour)
	}
	return 100
}

type workerProcessTestOutput struct {
	mu    sync.Mutex
	data  bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *workerProcessTestOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.data.Write(data)
	if strings.Contains(w.data.String(), "ready\n") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *workerProcessTestOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String()
}

func workerTestConfig(t *testing.T, mode string, count int) (Config, *workerProcessTestOutput) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bin", binaryName()), data, 0700); err != nil {
		t.Fatal(err)
	}
	output := &workerProcessTestOutput{ready: make(chan struct{})}
	env := []string{"HRC_WORKER_PROCESS_TEST=1", "HRC_WORKER_TEST_DIR=" + dir, "HRC_WORKER_TEST_MODE=" + mode, "HRC_WORKER_TEST_COUNT=" + strconv.Itoa(count)}
	return Config{Dir: dir, Env: env, Stdout: output, Stderr: output}, output
}

func workerProcessTestJob(index int) []byte {
	data, _ := json.Marshal(map[string]any{"index": index, "text": "Привет 🌍", "data": strings.Repeat("x", 20_000)})
	return data
}
