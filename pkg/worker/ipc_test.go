// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package worker

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestWriteFrame(t *testing.T) {
	var frame bytes.Buffer
	if err := writeFrame(&frame, messageNewJob, []byte(`{"text":"😀é"}`)); err != nil {
		t.Fatal(err)
	}
	// Little-endian type=1, byte length=28, UTF-16 including a surrogate pair.
	want, err := hex.DecodeString("010000001c0000007b002200740065007800740022003a0022003dd800dee90022007d00")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Bytes(), want) {
		t.Fatalf("frame = %x, want %x", frame.Bytes(), want)
	}
}
