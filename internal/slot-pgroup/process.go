// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package slotpgroup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"dimidiumlabs.io/hrc/internal/slot"
)

// process is one child in a pgroup. Its descendants belong to the group,
// independently of this child's lifetime.
type process struct {
	cmd    *exec.Cmd
	group  *pgroup
	waited bool
}

// Start starts a child in the group. If the child cannot join its tree, it is
// killed and reaped before Start returns. A closed or nil group cannot start
// children.
func (g *pgroup) Start(config slot.Command) (*process, error) {
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
	p := &process{group: g}
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
// caller's to interpret from the returned exit code.
func (p *process) Wait() (int, error) {
	err := p.cmd.Wait()
	p.waited = true
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = nil
	}
	if p.cmd.ProcessState == nil {
		return -1, err
	}
	return p.cmd.ProcessState.ExitCode(), err
}

// Kill terminates only this child. Its descendants remain in the pgroup.
func (p *process) Kill() error {
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// Close terminates a child that has not been waited for. It does not close the
// pgroup or kill descendants. The caller must still Wait to reap the child.
func (p *process) Close() error {
	if !p.waited {
		return p.Kill()
	}
	return nil
}
