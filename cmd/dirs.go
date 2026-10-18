// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// dirPool hands out a group's persistent runner directories <group>-<n>. The
// most recently released directory is reused first, so a job lands in the
// warmest workspace. After a restart, numbering starts from 1 again and picks
// up the directories left on disk.
type dirPool struct {
	mu     sync.Mutex
	prefix string
	free   []int
	next   int
}

func newDirPool(root, group string) *dirPool {
	return &dirPool{prefix: filepath.Join(root, group+"-")}
}

func (p *dirPool) take() (dir string, release func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n int
	if len(p.free) > 0 {
		n, p.free = p.free[len(p.free)-1], p.free[:len(p.free)-1]
	} else {
		p.next++
		n = p.next
	}
	return p.prefix + strconv.Itoa(n), func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.free = append(p.free, n)
	}
}

// installedVersion marks a complete runner installation in a reused directory.
const installedVersion = ".hrc-runner"

// installRunner prepares a reused directory for the next job. The runner is
// copied only when the directory lacks a complete installation of the bundled
// version; the workspace in _work always survives. Leftovers of the previous
// job that would break or bloat the next one are removed: .runner remains if
// the controller crashed, and _diag would collect logs of every job.
func installRunner(dir, runnerDir string) error {
	version, err := os.ReadFile(filepath.Join(runnerDir, "VERSION"))
	if err != nil {
		return fmt.Errorf("read runner version: %w", err)
	}
	marker := filepath.Join(dir, installedVersion)
	if installed, err := os.ReadFile(marker); err == nil && bytes.Equal(installed, version) {
		for _, name := range []string{".runner", "_diag"} {
			if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("clean runner directory: %w", err)
			}
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read runner directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() != "_work" {
			err = errors.Join(err, os.RemoveAll(filepath.Join(dir, entry.Name())))
		}
	}
	if err != nil {
		return fmt.Errorf("remove previous runner: %w", err)
	}
	if err := os.CopyFS(dir, os.DirFS(runnerDir)); err != nil {
		return fmt.Errorf("copy runner: %w", err)
	}
	// Written last: an interrupted copy is redone by the next job.
	if err := os.WriteFile(marker, version, 0o600); err != nil {
		return fmt.Errorf("mark runner installation: %w", err)
	}
	return nil
}
