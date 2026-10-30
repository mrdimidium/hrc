// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package runtime extends the standard runtime package with stateless platform
// queries and IPC primitives. It does not cache readings or apply runner
// configuration, reservation or process-lifetime policies.
package runtime

import (
	"bytes"
	"os"
	"strings"
)

func validateProcessEnvArgs(pid int, name string) error {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) || name == "" || strings.ContainsAny(name, "=\x00") {
		return os.ErrInvalid
	}
	return nil
}

func lookupEnvEntry(entry, name string, foldCase bool) (string, bool) {
	key, value, ok := strings.Cut(entry, "=")
	if ok && (key == name || (foldCase && strings.EqualFold(key, name))) {
		return value, true
	}
	return "", false
}

func lookupEnvBlock(data []byte, name string) (string, bool) {
	for len(data) > 0 {
		entry, rest, terminated := bytes.Cut(data, []byte{0})
		if len(entry) == 0 {
			break
		}
		if value, found := lookupEnvEntry(string(entry), name, false); found {
			return value, true
		}
		if !terminated {
			break
		}
		data = rest
	}
	return "", false
}
