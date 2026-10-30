// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupCapacities(t *testing.T) {
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		name = filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The tightest limit wins, whether set on the process's cgroup or an
	// ancestor. Missing and unlimited controllers leave capacity unchanged.
	write("cpu.max", "max 100000\n")
	write("system.slice/memory.max", "17179869184\n")
	write("system.slice/cpu.max", "800000 100000\n")
	write("system.slice/hrc.service/memory.max", "max\n")
	write("system.slice/hrc.service/cpu.max", "1200000 100000\n")
	group := "/system.slice/hrc.service"
	if cpu, err := cgroupCPUCapacity(root, group, 32); err != nil || cpu != 8 {
		t.Fatalf("CPU capacity = %g, %v; want 8", cpu, err)
	}
	if memory, err := cgroupMemoryCapacity(root, group, 128<<30); err != nil || memory != 16<<30 {
		t.Fatalf("memory capacity = %d, %v; want 16 GiB", memory, err)
	}

	write("system.slice/cpu.max", "invalid\n")
	if _, err := cgroupCPUCapacity(root, group, 32); err == nil {
		t.Error("invalid cpu.max accepted")
	}
	if memory, err := cgroupMemoryCapacity(root, group, 128<<30); err != nil || memory != 16<<30 {
		t.Fatalf("memory query depended on cpu.max: %d, %v", memory, err)
	}

	write("system.slice/cpu.max", "150000 100000\n")
	write("system.slice/memory.max", "invalid\n")
	if cpu, err := cgroupCPUCapacity(root, group, 32); err != nil || cpu != 1.5 {
		t.Fatalf("CPU query depended on memory.max or lost fractional quota: %g, %v", cpu, err)
	}
	if _, err := cgroupMemoryCapacity(root, group, 128<<30); err == nil {
		t.Error("invalid memory.max accepted")
	}

	write("system.slice/cpu.max", "150000 0\n")
	if _, err := cgroupCPUCapacity(root, group, 32); err == nil {
		t.Error("zero CPU period accepted")
	}
}

func TestCgroupMissingControllers(t *testing.T) {
	root := t.TempDir()
	if cpu, err := cgroupCPUCapacity(root, "/empty/child", 4); err != nil || cpu != 4 {
		t.Fatalf("missing CPU limit changed capacity: %g, %v", cpu, err)
	}
	if memory, err := cgroupMemoryCapacity(root, "/empty/child", 8<<30); err != nil || memory != 8<<30 {
		t.Fatalf("missing memory limit changed capacity: %d, %v", memory, err)
	}
}

func TestMemTotal(t *testing.T) {
	for _, test := range []struct {
		name, content string
		want          uint64
	}{
		{"valid", "MemTotal: 1024 kB\nMemFree: 512 kB\n", 1 << 20},
		{"missing", "MemFree: 512 kB\n", 0},
		{"invalid", "MemTotal: invalid kB\n", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "meminfo")
			if err := os.WriteFile(name, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := memTotal(name)
			if got != test.want || (err != nil) != (test.want == 0) {
				t.Fatalf("memTotal = %d, %v; want %d", got, err, test.want)
			}
		})
	}
}
