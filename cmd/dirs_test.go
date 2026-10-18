// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirPool(t *testing.T) {
	root := t.TempDir()
	p := newDirPool(root, "smoke")
	first, releaseFirst := p.take()
	second, releaseSecond := p.take()
	if first != filepath.Join(root, "smoke-1") || second != filepath.Join(root, "smoke-2") {
		t.Fatalf("directories %q, %q", first, second)
	}
	releaseFirst()
	releaseSecond()
	// The most recently released directory is the warmest one.
	if dir, _ := p.take(); dir != second {
		t.Errorf("took %q, want %q", dir, second)
	}
	if dir, _ := p.take(); dir != first {
		t.Errorf("took %q, want %q", dir, first)
	}
	if dir, _ := p.take(); dir != filepath.Join(root, "smoke-3") {
		t.Errorf("took %q, want a new directory", dir)
	}
}

func TestInstallRunner(t *testing.T) {
	write := func(name, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	exists := func(name string) bool {
		_, err := os.Stat(name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return err == nil
	}
	source, dir := t.TempDir(), filepath.Join(t.TempDir(), "smoke-1")
	write(filepath.Join(source, "VERSION"), "2.333.0\n")
	write(filepath.Join(source, "bin", "Runner.Worker"), "old")

	if err := installRunner(dir, source); err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(dir, "bin", "Runner.Worker")) != "old" {
		t.Fatal("runner not installed")
	}

	// A job leaves its workspace, diagnostics and, after a crash, .runner.
	write(filepath.Join(dir, "_work", "repo", "node_modules", "dep"), "cached")
	write(filepath.Join(dir, "_diag", "Worker.log"), "log")
	write(filepath.Join(dir, ".runner"), "{}")
	write(filepath.Join(dir, "bin", "Runner.Worker"), "installed")
	if err := installRunner(dir, source); err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(dir, "bin", "Runner.Worker")) != "installed" {
		t.Error("current installation copied again")
	}
	if exists(filepath.Join(dir, ".runner")) || exists(filepath.Join(dir, "_diag")) {
		t.Error("previous job's .runner or _diag remains")
	}
	if read(filepath.Join(dir, "_work", "repo", "node_modules", "dep")) != "cached" {
		t.Error("workspace lost on reuse")
	}

	// A new bundled version replaces the installation, but not the workspace.
	write(filepath.Join(source, "VERSION"), "2.334.0\n")
	write(filepath.Join(source, "bin", "Runner.Worker"), "new")
	write(filepath.Join(dir, "bin", "stale"), "removed upstream")
	if err := installRunner(dir, source); err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(dir, "bin", "Runner.Worker")) != "new" || exists(filepath.Join(dir, "bin", "stale")) {
		t.Error("runner not replaced on version change")
	}
	if read(filepath.Join(dir, "VERSION")) != "2.334.0\n" || read(filepath.Join(dir, installedVersion)) != "2.334.0\n" {
		t.Error("installed version not recorded")
	}
	if read(filepath.Join(dir, "_work", "repo", "node_modules", "dep")) != "cached" {
		t.Error("workspace lost on upgrade")
	}
}
