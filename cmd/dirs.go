// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"path/filepath"
	"strconv"
	"sync"
)

// dirPool hands out a group's persistent workspaces <group>-<n>. The
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
