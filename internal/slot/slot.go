// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package slot describes an execution environment that can outlive a run.
// The scheduler grants exclusive use of a slot to one run at a time.
package slot

import (
	"io"
	"os"
)

type Slot interface {
	Dir() string
	Start(Command) (Process, error)
	Close() error
}

// Command describes one child. Files are local inherited handles whose names
// follow Args, as required by the host GitHub Worker protocol. Remote handle
// transport is deliberately outside this first, host-only implementation.
type Command struct {
	Path, Dir      string
	Args, Env      []string
	Stdout, Stderr io.Writer
	Files          []*os.File
}

// Process owns one child, not the slot's other processes. Wait returns an exit
// code (-1 when unavailable); a nonzero exit code alone is not an error.
type Process interface {
	Wait() (int, error)
	Kill() error
	Close() error
}
