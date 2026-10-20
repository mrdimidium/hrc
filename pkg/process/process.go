// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package process runs a child in a process tree of its own: a process group
// on Unix, a job object on Windows. The tree holds the child's descendants,
// and interrupts sent within it, such as the GitHub runner's Ctrl+C to a
// step's console, stay there.
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
	Path, Dir      string
	Args           []string // The names of Files follow them.
	Env            []string
	Stdout, Stderr io.Writer
	// Files are inherited by the child, which finds them by the names appended
	// to Args. Create them with Pipe.
	Files []*os.File
	// KeepDescendants lets descendants outlive a child that exits on its own.
	// It takes effect only on Windows: see Process.Close.
	KeepDescendants bool
}

// Process is one child in its tree. Each platform provides Pipe and the
// methods configure (before start), attach (join the tree), Kill,
// keepDescendants and release (free the tree).
type Process struct {
	cmd      *exec.Cmd
	keep     bool
	waited   bool
	platform platform
}

// Start starts the child in its tree. If the child cannot join the tree, it is
// killed and reaped before Start returns.
func Start(config Config) (*Process, error) {
	p := &Process{keep: config.KeepDescendants}
	p.cmd = exec.Command(config.Path, config.Args...)
	p.cmd.Dir, p.cmd.Env = config.Dir, config.Env
	p.cmd.Stdout, p.cmd.Stderr = config.Stdout, config.Stderr
	p.cmd.WaitDelay = 5 * time.Second
	if err := p.configure(config.Files); err != nil {
		p.release()
		return nil, fmt.Errorf("configure process: %w", err)
	}
	if err := p.cmd.Start(); err != nil {
		p.release()
		return nil, err
	}
	// Join the tree before the caller hands the child any work, so that no
	// descendant can start outside it.
	if err := p.attach(); err != nil {
		killErr := p.Kill()
		_, waitErr := p.Wait()
		p.release()
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

// Close kills what is left of the tree and frees it. Call it once, after Wait.
// Descendants survive only a child that exited on its own, and only with
// KeepDescendants on Windows, where nothing can leave the job; a child the
// caller killed took them with it. Unix always kills the process group, so
// there only daemons that left it survive.
func (p *Process) Close() error {
	var err error
	if !p.waited || !p.keepDescendants() {
		err = p.Kill()
	}
	p.release()
	return err
}
