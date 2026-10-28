// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package process

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type platform struct {
	mu       sync.Mutex
	job      windows.Handle
	assigned bool
}

// Pipe returns a pipe whose ends are not inherited unless passed in Files.
// os.Pipe uses synchronous Windows pipe handles, as required by .NET's
// AnonymousPipeClientStream, but makes both ends inheritable.
func Pipe() (*os.File, *os.File, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	for _, f := range []*os.File{read, write} {
		if err := windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			read.Close()
			write.Close()
			return nil, nil, err
		}
	}
	return read, write, nil
}

// configure passes Files as inherited handles named by their values, gives the
// child a console of its own and creates the job it will join.
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
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	p.platform.job = job
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if !p.keep {
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	return err
}

// attach puts the child into the job. Runner.Worker is still waiting for its
// initial NewJobRequest, so every process a job step launches joins the job.
func (p *Process) attach() error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(p.platform.job, process); err != nil {
		return err
	}
	p.platform.mu.Lock()
	p.platform.assigned = true
	p.platform.mu.Unlock()
	return nil
}

// keepDescendants leaves the job's remaining processes running: Bazel and
// similar servers start detached but cannot leave the job without breakaway.
func (p *Process) keepDescendants() bool { return p.keep }

// Kill terminates the child and every descendant in its tree.
func (p *Process) Kill() error {
	p.platform.mu.Lock()
	defer p.platform.mu.Unlock()
	if p.platform.job != 0 && p.platform.assigned {
		return windows.TerminateJobObject(p.platform.job, 1)
	}
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *Process) release() {
	p.platform.mu.Lock()
	defer p.platform.mu.Unlock()
	if p.platform.job != 0 {
		// KILL_ON_JOB_CLOSE also removes descendants left behind after exit.
		windows.CloseHandle(p.platform.job)
		p.platform.job = 0
	}
}
