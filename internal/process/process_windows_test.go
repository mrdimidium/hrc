// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package process

import "golang.org/x/sys/windows"

// Windows interrupts by console rather than by process group.
func init() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	// AllocConsole fails when the test already has a console, which is fine.
	shareConsole = func() { kernel32.NewProc("AllocConsole").Call() }
	sendInterrupt = func() error {
		// Ignore Ctrl+C here, then send it to every process on this console.
		if r, _, err := kernel32.NewProc("SetConsoleCtrlHandler").Call(0, 1); r == 0 {
			return err
		}
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
	}
}
