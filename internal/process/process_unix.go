// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package process

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

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

func testDetachDescendant(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// Unix has no console to allocate for the test's children.
func testShareConsole() {}

// testSendInterrupt interrupts the caller's process group, as the runner does
// to cancel a step.
func testSendInterrupt() error {
	group, err := os.FindProcess(-os.Getpid())
	if err != nil {
		return err
	}
	defer group.Release()
	return group.Signal(os.Interrupt)
}
