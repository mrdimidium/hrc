// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package worker

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type workerPlatform struct {
	mu       sync.Mutex
	job      windows.Handle
	assigned bool
}

func (w *Worker) configurePlatform() error {
	// os.Pipe uses synchronous Windows pipe handles, as required by .NET's
	// AnonymousPipeClientStream. Only the two child ends are inheritable.
	for i, f := range w.pipes {
		flags := uint32(0)
		if i == 0 || i == 3 {
			flags = windows.HANDLE_FLAG_INHERIT
		}
		if err := windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, flags); err != nil {
			return err
		}
	}
	input, output := w.pipes[0].Fd(), w.pipes[3].Fd()
	w.cmd.Args = append(w.cmd.Args, "spawnclient", strconv.FormatUint(uint64(input), 10), strconv.FormatUint(uint64(output), 10))
	w.cmd.SysProcAttr = &syscall.SysProcAttr{
		AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(input), syscall.Handle(output)},
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	w.platform.job = job
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if !w.config.KeepDescendants {
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	return err
}

func (w *Worker) startedPlatform() error {
	// Runner.Worker is still waiting for its initial NewJobRequest. Associate it
	// before sending the job, so all processes launched by job steps join this job.
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(w.cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(w.platform.job, process); err != nil {
		return err
	}
	w.platform.assigned = true
	return nil
}

// keepDescendants leaves the job's remaining processes running: Bazel and
// similar servers start detached but cannot leave the job without breakaway.
func (w *Worker) keepDescendants() bool { return w.config.KeepDescendants }

func (w *Worker) killProcessTree() error {
	w.platform.mu.Lock()
	defer w.platform.mu.Unlock()
	if w.platform.job != 0 && w.platform.assigned {
		if err := windows.TerminateJobObject(w.platform.job, 1); err != nil {
			return err
		}
		return nil
	}
	err := w.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (w *Worker) closePlatform() {
	w.platform.mu.Lock()
	defer w.platform.mu.Unlock()
	if w.platform.job != 0 {
		// KILL_ON_JOB_CLOSE also removes descendants left behind after Worker exit.
		windows.CloseHandle(w.platform.job)
		w.platform.job = 0
	}
}
