// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package process runs children in a PGroup that owns their descendants across
// child lifetimes. Each child also has an isolated process group on Unix or
// console on Windows, keeping step cancellation away from other children.
package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type Config struct {
	Path string
	Dir  string
	Env  []string
	Args []string // The names of Files follow them.

	Stdout io.Writer
	Stderr io.Writer

	// Files are inherited by the child, which finds them by the names appended
	// to Args. Create them with internal/runtime.Pipe.
	Files []*os.File
}

// Process is one child in a PGroup. Its descendants belong to the group,
// independently of this child's lifetime.
type Process struct {
	cmd    *exec.Cmd
	group  *PGroup
	waited bool
}

// Start starts a child in the group. If the child cannot join its tree, it is
// killed and reaped before Start returns. A closed or nil group cannot start
// children.
func (g *PGroup) Start(config Config) (*Process, error) {
	if g == nil {
		return nil, errors.New("process group is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, os.ErrClosed
	}
	if g.id == "" {
		return nil, errors.New("uninitialized process group")
	}
	p := &Process{group: g}
	p.cmd = exec.Command(config.Path, config.Args...)
	p.cmd.Dir, p.cmd.Env = config.Dir, g.environment(config.Env)
	p.cmd.Stdout, p.cmd.Stderr = config.Stdout, config.Stderr
	p.cmd.WaitDelay = 5 * time.Second
	if err := p.configure(config.Files); err != nil {
		return nil, fmt.Errorf("configure process: %w", err)
	}
	if err := p.cmd.Start(); err != nil {
		return nil, err
	}
	// Join the tree before the caller hands the child any work, so that no
	// descendant can start outside it.
	if err := p.attach(); err != nil {
		killErr := p.Kill()
		_, waitErr := p.Wait()
		return nil, errors.Join(fmt.Errorf("attach process: %w", err), killErr, waitErr)
	}
	return p, nil
}

// Wait waits for the child, once. An exit status is not an error: it is the
// caller's to interpret from the returned state.
func (p *Process) Wait() (*os.ProcessState, error) {
	err := p.cmd.Wait()
	p.waited = true
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = nil
	}
	return p.cmd.ProcessState, err
}

// Kill terminates only this child. Its descendants remain in the PGroup.
func (p *Process) Kill() error {
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// Close terminates a child that has not been waited for. It does not close the
// PGroup or kill descendants. The caller must still Wait to reap the child.
func (p *Process) Close() error {
	if !p.waited {
		return p.Kill()
	}
	return nil
}
