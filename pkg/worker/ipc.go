// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package worker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"
)

type messageType int32

const (
	messageNewJob     messageType = 1
	messageCancel     messageType = 2
	messageShutdown   messageType = 3
	messageOSShutdown messageType = 4
)

// The frame is Int32(type), Int32(byte length), UTF-16LE JSON without a BOM.
// It is not a JSON stream and must not share stdout/stderr with diagnostics.
func writeFrame(dst io.Writer, kind messageType, body []byte) error {
	text := utf16.Encode([]rune(string(body)))
	if len(text) > 25_000_000 {
		return errors.New("Worker frame exceeds 50 MB")
	}
	data := make([]byte, 8+2*len(text))
	binary.LittleEndian.PutUint32(data, uint32(kind))
	binary.LittleEndian.PutUint32(data[4:], uint32(2*len(text)))
	for i, value := range text {
		binary.LittleEndian.PutUint16(data[8+i*2:], value)
	}
	_, err := io.Copy(dst, bytes.NewReader(data))
	return err
}
