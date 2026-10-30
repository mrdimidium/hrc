// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build darwin

package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	goruntime "runtime"

	"golang.org/x/sys/unix"
)

// CPUCapacity returns the host's logical CPU capacity using runtime.NumCPU().
func CPUCapacity() (float64, error) {
	return float64(goruntime.NumCPU()), nil
}

// MemoryCapacity returns physical RAM in bytes, not currently free memory.
func MemoryCapacity() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}

// Pipe returns a pipe whose ends close on exec unless explicitly passed to a child.
func Pipe() (*os.File, *os.File, error) { return os.Pipe() }

// ProcessIDs returns a snapshot of process IDs visible to this process,
// including its own ID. A process may exit before it is inspected.
func ProcessIDs() ([]int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(processes))
	for _, process := range processes {
		pids = append(pids, int(process.Proc.P_pid))
	}
	return pids, nil
}

// LookupProcessEnv reads an environment variable from the kernel's process
// argument/environment block. This may reflect the initial environment, not
// subsequent setenv calls. The boolean distinguishes a missing variable from an
// empty value. Reading can fail due to permissions or process exit.
func LookupProcessEnv(pid int, name string) (string, bool, error) {
	if err := validateProcessEnvArgs(pid, name); err != nil {
		return "", false, err
	}
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false, err
	}
	env, err := processArgsEnvironment(data)
	if err != nil {
		return "", false, err
	}
	value, found := lookupEnvBlock(env, name)
	return value, found, nil
}

func processArgsEnvironment(data []byte) ([]byte, error) {
	// KERN_PROCARGS2 contains argc, executable path, NUL padding, argv and
	// environment entries. Skip argv by argc rather than searching for '=':
	// arguments can look exactly like environment entries, or be empty.
	if len(data) < 4 {
		return nil, errors.New("truncated process arguments")
	}
	argc := int(int32(binary.LittleEndian.Uint32(data[:4])))
	if argc < 0 || argc > len(data)-4 {
		return nil, errors.New("invalid process argument count")
	}
	data = data[4:]
	end := bytes.IndexByte(data, 0)
	if end < 0 {
		return nil, errors.New("unterminated process executable path")
	}
	data = bytes.TrimLeft(data[end+1:], "\x00")
	for range argc {
		end := bytes.IndexByte(data, 0)
		if end < 0 {
			return nil, errors.New("unterminated process argument")
		}
		data = data[end+1:]
	}
	return data, nil
}

// testReadProcessEnv exercises the Windows memory reader only.
func testReadProcessEnv(env, name string) (string, bool, error) {
	return "", false, errors.ErrUnsupported
}
