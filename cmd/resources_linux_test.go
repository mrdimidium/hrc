// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupLimits(t *testing.T) {
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
	// The tightest limit wins, whether set on the service or on its slice.
	write("cpu.max", "max 100000\n")
	write("system.slice/memory.max", "17179869184\n")
	write("system.slice/cpu.max", "800000 100000\n")
	write("system.slice/hrc.service/memory.max", "max\n")
	write("system.slice/hrc.service/cpu.max", "1200000 100000\n")
	host := Resources{CPU: 32, Memory: 128 << 30}
	got, err := cgroupLimits(root, "/system.slice/hrc.service", host)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Resources{CPU: 8, Memory: 16 << 30}); got != want {
		t.Errorf("limits = %+v, want %+v", got, want)
	}

	write("system.slice/cpu.max", "invalid\n")
	if _, err := cgroupLimits(root, "/system.slice/hrc.service", host); err == nil {
		t.Error("invalid cpu.max accepted")
	}
}

func TestHostResources(t *testing.T) {
	host, err := hostResources()
	if err != nil {
		t.Fatal(err)
	}
	if host.CPU <= 0 || host.Memory == 0 {
		t.Errorf("host resources = %+v", host)
	}
}
