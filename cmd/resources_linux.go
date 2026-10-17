// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// hostResources returns the CPU threads and memory available to this process:
// the host's, limited by the CPU affinity and by cgroup v2 limits of the
// controller's cgroup and its ancestors. cgroup v1 limits are not applied.
func hostResources() (Resources, error) {
	memory, err := memTotal("/proc/meminfo")
	if err != nil {
		return Resources{}, err
	}
	host := Resources{CPU: float64(runtime.NumCPU()), Memory: memory}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return host, nil // No cgroups: the host's resources apply.
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if group, ok := strings.CutPrefix(line, "0::"); ok {
			return cgroupLimits("/sys/fs/cgroup", group, host)
		}
	}
	return host, nil
}

func memTotal(name string) (ByteSize, error) {
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
			return ByteSize(kib) * 1024, nil
		}
	}
	return 0, errors.Join(errors.New("MemTotal not found in "+name), scanner.Err())
}

// cgroupLimits applies cpu.max and memory.max of the cgroup and its ancestors.
// Missing files mean the controller is not enabled at that level.
func cgroupLimits(root, group string, host Resources) (Resources, error) {
	for group = path.Clean("/" + group); ; group = path.Dir(group) {
		dir := filepath.Join(root, filepath.FromSlash(group))
		if data, err := os.ReadFile(filepath.Join(dir, "memory.max")); err == nil {
			if value := string(bytes.TrimSpace(data)); value != "max" {
				limit, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return host, fmt.Errorf("parse %s/memory.max: %w", dir, err)
				}
				host.Memory = min(host.Memory, ByteSize(limit))
			}
		}
		if data, err := os.ReadFile(filepath.Join(dir, "cpu.max")); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) != 2 {
				return host, fmt.Errorf("parse %s/cpu.max: %q", dir, data)
			}
			if fields[0] != "max" {
				quota, e1 := strconv.ParseUint(fields[0], 10, 64)
				period, e2 := strconv.ParseUint(fields[1], 10, 64)
				if e1 != nil || e2 != nil || period == 0 {
					return host, fmt.Errorf("parse %s/cpu.max: %q", dir, data)
				}
				host.CPU = min(host.CPU, float64(quota)/float64(period))
			}
		}
		if group == "/" {
			return host, nil
		}
	}
}
