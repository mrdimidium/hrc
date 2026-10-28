// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package process

import (
	"errors"
	"os"
	"strconv"
	"syscall"
)

type platform struct{}

// Pipe returns a pipe whose ends close on exec unless passed in Files.
func Pipe() (*os.File, *os.File, error) { return os.Pipe() }

// configure passes Files as descriptors 3 and up, and starts the child in a
// process group of its own.
func (p *Process) configure(files []*os.File) error {
	p.cmd.ExtraFiles = files
	for i := range files {
		p.cmd.Args = append(p.cmd.Args, strconv.Itoa(3+i))
	}
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func (p *Process) attach() error { return nil }

func (p *Process) release() {}

func (p *Process) keepDescendants() bool { return false }

// Kill terminates the child and every descendant in its tree.
func (p *Process) Kill() error {
	group, err := os.FindProcess(-p.cmd.Process.Pid)
	if err == nil {
		err = group.Kill()
		group.Release()
	}
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	rootErr := p.cmd.Process.Kill()
	if errors.Is(rootErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(err, rootErr)
}
