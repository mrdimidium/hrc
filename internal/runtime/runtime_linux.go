// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package runtime

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
)

// CPUCapacity returns runtime.NumCPU(), limited by cgroup v2 CPU quotas of the
// process's cgroup and its ancestors. The capacity may be fractional. Cgroup v1
// quotas are not applied.
func CPUCapacity() (float64, error) {
	capacity := float64(goruntime.NumCPU())
	if group, ok := processCgroup(); ok {
		return cgroupCPUCapacity("/sys/fs/cgroup", group, capacity)
	}
	return capacity, nil
}

// MemoryCapacity returns physical RAM in bytes, limited by cgroup v2 memory
// limits of the process's cgroup and its ancestors. This is capacity, not free
// memory. Cgroup v1 limits are not applied.
func MemoryCapacity() (uint64, error) {
	capacity, err := memTotal("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	if group, ok := processCgroup(); ok {
		return cgroupMemoryCapacity("/sys/fs/cgroup", group, capacity)
	}
	return capacity, nil
}

func processCgroup() (string, bool) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if group, ok := strings.CutPrefix(line, "0::"); ok {
				return group, true
			}
		}
	}
	return "", false
}

func memTotal(name string) (uint64, error) {
	f, err := os.Open(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
			kib, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				break
			}
			return kib * 1024, nil
		}
	}
	return 0, errors.Join(errors.New("MemTotal not found in "+name), scanner.Err())
}

func cgroupDirectories(root, group string) []string {
	var dirs []string
	for group = path.Clean("/" + group); ; group = path.Dir(group) {
		dirs = append(dirs, filepath.Join(root, filepath.FromSlash(group)))
		if group == "/" {
			return dirs
		}
	}
}

// Missing files mean the cgroup controller is not enabled at that level.
func cgroupCPUCapacity(root, group string, capacity float64) (float64, error) {
	for _, dir := range cgroupDirectories(root, group) {
		data, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) != 2 {
			return 0, fmt.Errorf("parse %s/cpu.max: %q", dir, data)
		}
		if fields[0] != "max" {
			quota, e1 := strconv.ParseUint(fields[0], 10, 64)
			period, e2 := strconv.ParseUint(fields[1], 10, 64)
			if e1 != nil || e2 != nil || period == 0 {
				return 0, fmt.Errorf("parse %s/cpu.max: %q", dir, data)
			}
			capacity = min(capacity, float64(quota)/float64(period))
		}
	}
	return capacity, nil
}

func cgroupMemoryCapacity(root, group string, capacity uint64) (uint64, error) {
	for _, dir := range cgroupDirectories(root, group) {
		data, err := os.ReadFile(filepath.Join(dir, "memory.max"))
		if err != nil {
			continue
		}
		if value := string(bytes.TrimSpace(data)); value != "max" {
			limit, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse %s/memory.max: %w", dir, err)
			}
			capacity = min(capacity, limit)
		}
	}
	return capacity, nil
}

// Pipe returns a pipe whose ends close on exec unless explicitly passed to a child.
func Pipe() (*os.File, *os.File, error) { return os.Pipe() }

// ProcessIDs returns a snapshot of process IDs visible to this process,
// including its own ID. A process may exit before it is inspected.
func ProcessIDs() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry.Name()); err == nil && entry.IsDir() {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// LookupProcessEnv reads an environment variable from /proc/<pid>/environ.
// This normally reflects the environment at exec, not subsequent setenv calls.
// The boolean distinguishes a missing variable from an empty value. Reading
// another process's environment may fail due to permissions or process exit.
func LookupProcessEnv(pid int, name string) (string, bool, error) {
	if err := validateProcessEnvArgs(pid, name); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return "", false, err
	}
	value, found := lookupEnvBlock(data, name)
	return value, found, nil
}

// testReadProcessEnv exercises the Windows memory reader only.
func testReadProcessEnv(env, name string) (string, bool, error) {
	return "", false, errors.ErrUnsupported
}
