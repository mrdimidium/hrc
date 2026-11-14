// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build darwin

package slotpgroup

type pgroupPlatform struct{}

func (g *pgroup) configure() error { return nil }

func (g *pgroup) release() error { return nil }
