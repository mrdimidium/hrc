// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package space describes the resources and slot creation of an environment.
// Scheduling and reuse policies belong to its scheduler.
package space

import "dimidiumlabs.io/hrc/internal/slot"

type Resources struct {
	CPU    float64
	Memory uint64
}

type Space interface {
	Capacity() (Resources, error)
	// An empty name requests a fresh anonymous workspace.
	CreateSlot(name string) (slot.Slot, error)
}
