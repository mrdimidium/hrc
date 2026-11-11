// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// configure passes Files as inherited handles named by their values, gives the
// child a console of its own.
func (p *Process) configure(files []*os.File) error {
	handles := make([]syscall.Handle, len(files))
	for i, f := range files {
		handle := windows.Handle(f.Fd())
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return err
		}
		handles[i] = syscall.Handle(handle)
		p.cmd.Args = append(p.cmd.Args, strconv.FormatUint(uint64(handle), 10))
	}
	// The runner cancels a step with GenerateConsoleCtrlEvent on the step's
	// console, which reaches every process attached to it. A console of its own
	// keeps that Ctrl+C away from the controller and the other Workers.
	p.cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags:              windows.CREATE_NO_WINDOW,
		AdditionalInheritedHandles: handles,
	}
	return nil
}

// attach puts the child into the PGroup's crash-cleanup job. Runner.Worker is
// still waiting for its initial NewJobRequest, so its job steps inherit it.
func (p *Process) attach() error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(p.group.platform.job, process)
}

// A PID found by the environment scan can disappear before OpenProcess.
// Windows reports ERROR_INVALID_PARAMETER for a process that no longer exists.
func findTrackedProcess(pid int) (*os.Process, error) {
	p, err := os.FindProcess(pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil, os.ErrProcessDone
	}
	return p, err
}

// TerminateProcess is asynchronous. A subsequent environment scan can still
// find the dying process, and killing an already terminated process returns
// ERROR_ACCESS_DENIED, not os.ErrProcessDone. Wait on the same process handle
// before accepting either success or access denied; a live, unkillable process
// must still be reported as an error. Share the scanner's cleanup deadline.
func killTrackedProcess(p *os.Process, deadline time.Time) error {
	killErr := p.Kill()
	if killErr != nil && !errors.Is(killErr, windows.ERROR_ACCESS_DENIED) {
		return killErr
	}
	var waitErr error
	handleErr := p.WithHandle(func(handle uintptr) {
		remaining := max(time.Until(deadline), 0)
		timeout := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
		state, err := windows.WaitForSingleObject(windows.Handle(handle), timeout)
		if err != nil {
			waitErr = err
		} else if state != windows.WAIT_OBJECT_0 {
			waitErr = errors.New("process termination timed out")
		}
	})
	if handleErr != nil || waitErr != nil {
		return errors.Join(killErr, handleErr, waitErr)
	}
	return nil
}

func testDetachDescendant(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}

// testShareConsole gives the test a console for its children to share, as a
// service has.
func testShareConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	// AllocConsole fails when the test already has a console, which is fine.
	kernel32.NewProc("AllocConsole").Call()
}

// Windows interrupts by console rather than by process group.
func testSendInterrupt() error {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	// Ignore Ctrl+C here, then send it to every process on this console.
	if r, _, err := kernel32.NewProc("SetConsoleCtrlHandler").Call(0, 1); r == 0 {
		return err
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
}
