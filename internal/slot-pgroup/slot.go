// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package slotpgroup implements a host slot with a workspace and a process tree.
package slotpgroup

import (
	"errors"
	"os"
	"sync"

	"dimidiumlabs.io/hrc/internal/slot"
)

type Slot struct {
	dir       string
	group     *pgroup
	closeOnce sync.Once
	closeErr  error
}

func New(dir string) (slot.Slot, error) {
	group, err := newPGroup()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.Join(err, group.Close())
	}
	return &Slot{dir: dir, group: group}, nil
}

func (s *Slot) Dir() string { return s.dir }

func (s *Slot) Start(command slot.Command) (slot.Process, error) {
	if command.Dir == "" {
		command.Dir = s.dir
	}
	child, err := s.group.Start(command)
	if err != nil {
		return nil, err
	}
	return child, nil
}

func (s *Slot) Close() error {
	s.closeOnce.Do(func() {
		// Remaining build servers must stop before their workspace disappears.
		s.closeErr = errors.Join(s.group.Close(), os.RemoveAll(s.dir))
	})
	return s.closeErr
}
