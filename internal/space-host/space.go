// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package spacehost provides host capacity and a root for slot workspaces.
package spacehost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dimidiumlabs.io/hrc/internal/runtime"
	"dimidiumlabs.io/hrc/internal/slot"
	"dimidiumlabs.io/hrc/internal/space"
)

type Space struct {
	root   string
	create func(string) (slot.Slot, error)
}

// New does not create any workspaces or processes.
func New(root string, create func(string) (slot.Slot, error)) (*Space, error) {
	if root == "" || create == nil {
		return nil, errors.New("space root and slot factory are required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Space{root: root, create: create}, nil
}

func (s *Space) Capacity() (space.Resources, error) {
	cpu, err := runtime.CPUCapacity()
	if err != nil {
		return space.Resources{}, fmt.Errorf("detect CPU capacity: %w", err)
	}
	memory, err := runtime.MemoryCapacity()
	if err != nil {
		return space.Resources{}, fmt.Errorf("detect memory capacity: %w", err)
	}
	return space.Resources{CPU: cpu, Memory: memory}, nil
}

func (s *Space) CreateSlot(name string) (slot.Slot, error) {
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\:") {
		return nil, fmt.Errorf("invalid slot name %q", name)
	}
	dir := filepath.Join(s.root, name)
	if name == "" {
		if err := os.MkdirAll(s.root, 0o700); err != nil {
			return nil, err
		}
		var err error
		dir, err = os.MkdirTemp(s.root, "runner-")
		if err != nil {
			return nil, err
		}
	}
	instance, err := s.create(dir)
	if err != nil && name == "" {
		err = errors.Join(err, os.RemoveAll(dir))
	}
	return instance, err
}
