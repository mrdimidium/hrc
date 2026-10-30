// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const childArg = "hrc-runtime-child"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == childArg {
		fmt.Fprintln(os.Stdout, "ready")
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCPUCapacity(t *testing.T) {
	capacity, err := CPUCapacity()
	if err != nil {
		t.Fatal(err)
	}
	if capacity <= 0 || capacity > float64(goruntime.NumCPU()) {
		t.Errorf("CPU capacity = %g", capacity)
	}
}

func TestMemoryCapacity(t *testing.T) {
	capacity, err := MemoryCapacity()
	if err != nil {
		t.Fatal(err)
	}
	if capacity == 0 {
		t.Error("memory capacity is zero")
	}
}

func TestPipe(t *testing.T) {
	read, write, err := Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	const message = "pipe data"
	if _, err := io.WriteString(write, message); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(read)
	if err != nil || string(data) != message {
		t.Fatalf("pipe read = %q, %v; want %q and EOF", data, err, message)
	}
}

func TestProcessIDs(t *testing.T) {
	pids, err := ProcessIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(pids, os.Getpid()) {
		t.Fatalf("ProcessIDs omitted the caller %d: %v", os.Getpid(), pids)
	}
	seen := make(map[int]bool)
	for _, pid := range pids {
		if pid < 0 || seen[pid] {
			t.Errorf("invalid or duplicate process ID: %d", pid)
		}
		seen[pid] = true
	}
}

func TestLookupProcessEnv(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	const key = "HRC_RUNTIME_ENV_TEST"
	const argOnly = "HRC_RUNTIME_ARG_ONLY"
	cmd := exec.CommandContext(ctx, self, childArg, key+"=wrong argument", "", argOnly+"=not an environment variable")
	value := "with spaces, = signs, Привет 🌍\nand a newline"
	large := strings.Repeat("x", 20000)
	cmd.Env = append(os.Environ(), key+"="+value, key+"_EMPTY=", key+"_LARGE="+large)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if ready, err := bufio.NewReader(output).ReadString('\n'); err != nil || ready != "ready\n" {
		t.Fatalf("child did not become ready: %q, %v", ready, err)
	}
	pids, err := ProcessIDs()
	if err != nil || !slices.Contains(pids, cmd.Process.Pid) {
		t.Fatalf("ProcessIDs omitted the child: %v, %v", pids, err)
	}
	foldedWant, foldedFound := "", false
	if goruntime.GOOS == "windows" {
		foldedWant, foldedFound = value, true
	}
	for _, test := range []struct {
		name, want string
		found      bool
	}{
		{key, value, true},
		{key + "_EMPTY", "", true},
		{key + "_LARGE", large, true},
		{key + "_ABSENT", "", false},
		{argOnly, "", false},
		{strings.ToLower(key), foldedWant, foldedFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found, err := LookupProcessEnv(cmd.Process.Pid, test.name)
			if err != nil || found != test.found || got != test.want {
				t.Fatalf("LookupProcessEnv = %q, %t, %v; want %q, %t", got, found, err, test.want, test.found)
			}
		})
	}
}

func TestLookupProcessEnvInvalidArgs(t *testing.T) {
	for _, test := range []struct {
		pid  int
		name string
	}{
		{0, "KEY"}, {-1, "KEY"},
		{os.Getpid(), ""}, {os.Getpid(), "KEY=VALUE"}, {os.Getpid(), "KEY\x00"},
	} {
		if _, _, err := LookupProcessEnv(test.pid, test.name); !errors.Is(err, os.ErrInvalid) {
			t.Errorf("LookupProcessEnv(%d, %q) = %v", test.pid, test.name, err)
		}
	}
}

func TestReadProcessEnv(t *testing.T) {
	if goruntime.GOOS != "windows" {
		t.Skip("tests the Windows process memory reader")
	}
	for _, test := range []struct {
		name, env, want string
		found           bool
	}{
		{"empty block", "\x00\x00", "", false},
		{"absent", "OTHER=value\x00\x00", "", false},
		{"empty value", "KEY=\x00\x00", "", true},
		{"exact", "OTHER_KEY=wrong\x00KEY=right\x00\x00", "right", true},
		{"case insensitive", "key=right\x00\x00", "right", true},
		{"unicode", "OTHER=Привет 🌍\x00KEY=right\x00\x00", "right", true},
		{"large", "OTHER=" + strings.Repeat("x", 20000) + "\x00KEY=right\x00\x00", "right", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found, err := testReadProcessEnv(test.env, "KEY")
			if err != nil || got != test.want || found != test.found {
				t.Fatalf("readProcessEnv = %q, %t, %v; want %q, %t", got, found, err, test.want, test.found)
			}
		})
	}
}

func TestLookupEnvBlock(t *testing.T) {
	for _, test := range []struct {
		name, block, key, want string
		found                  bool
	}{
		{"missing", "OTHER=value\x00", "KEY", "", false},
		{"empty", "KEY=\x00", "KEY", "", true},
		{"exact", "OTHER_KEY=wrong\x00KEY=right\x00", "KEY", "right", true},
		{"case sensitive", "key=wrong\x00", "KEY", "", false},
		{"value", "KEY=Привет 🌍 = value\nnext\x00", "KEY", "Привет 🌍 = value\nnext", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found := lookupEnvBlock([]byte(test.block), test.key)
			if got != test.want || found != test.found {
				t.Fatalf("lookupEnvBlock = %q, %t; want %q, %t", got, found, test.want, test.found)
			}
		})
	}
}
