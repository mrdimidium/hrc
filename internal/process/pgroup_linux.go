// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package process

type pgroupPlatform struct{}

func (g *PGroup) configure() error { return nil }

func (g *PGroup) release() error { return nil }
