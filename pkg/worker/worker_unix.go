// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package worker

import (
	"errors"
	"os"
	"syscall"
)

type workerPlatform struct{}

func (w *Worker) configurePlatform() error {
	w.cmd.Args = append(w.cmd.Args, "spawnclient", "3", "4")
	w.cmd.ExtraFiles = []*os.File{w.pipes[0], w.pipes[3]}
	w.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func (w *Worker) startedPlatform() error { return nil }
func (w *Worker) closePlatform()         {}

func (w *Worker) killProcessTree() error {
	group, err := os.FindProcess(-w.cmd.Process.Pid)
	if err == nil {
		err = group.Kill()
		group.Release()
	}
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	rootErr := w.cmd.Process.Kill()
	if errors.Is(rootErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(err, rootErr)
}
