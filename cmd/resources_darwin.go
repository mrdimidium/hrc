// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// hostResources returns the host's CPU threads and physical memory.
func hostResources() (Resources, error) {
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Resources{}, err
	}
	return Resources{CPU: float64(runtime.NumCPU()), Memory: ByteSize(memory)}, nil
}
