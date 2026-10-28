// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
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
