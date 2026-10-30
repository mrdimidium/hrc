// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package process

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type pgroupPlatform struct {
	job windows.Handle
}

func (g *PGroup) configure() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		windows.CloseHandle(job)
		return err
	}
	g.platform.job = job
	return nil
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, whose layout is
// the same on 32- and 64-bit Windows.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime                     int64
	ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses                uint32
	ActiveProcesses, TotalTerminatedProcesses          uint32
}

func (g *PGroup) release() error {
	// Normal cleanup has already scanned RUNNER_TRACKING_GROUP, just as on
	// Unix. The job is only a final safety net, including KILL_ON_JOB_CLOSE
	// if the controller crashes before it can scan environments.
	err := windows.TerminateJobObject(g.platform.job, 1)
	if err == nil {
		// Termination is asynchronous. Wait for process teardown before the
		// slot removes its workspace (open working directories lock Windows
		// directories against deletion).
		deadline := time.Now().Add(5 * time.Second)
		for {
			var info jobAccounting
			err = windows.QueryInformationJobObject(g.platform.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
			if err != nil || info.ActiveProcesses == 0 {
				break
			}
			if time.Now().After(deadline) {
				err = errors.New("process group cleanup timed out")
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	err = errors.Join(err, windows.CloseHandle(g.platform.job))
	g.platform.job = 0
	return err
}
