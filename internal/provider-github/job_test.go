// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package github

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"dimidiumlabs.io/hrc/internal/provider-github/listener"
)

func TestRejectsUnsafeJIT(t *testing.T) {
	for _, name := range []string{"../escape", "bin/hrc-worker", ".env"} {
		files, _ := json.Marshal(map[string]string{name: base64.StdEncoding.EncodeToString([]byte("x"))})
		if _, err := parseRunnerRegistration(base64.StdEncoding.EncodeToString(files)); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestRunRejectsUnsafeDiagnosticPaths(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../escape", "..\\escape", "C:escape", "/absolute"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			j := &job{raw: &listener.RunJob{JobID: id}, config: Config{LogRoot: root}}
			if _, err := j.Run(t.Context(), nil); err == nil {
				t.Fatal("unsafe job ID accepted")
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("rejected job created diagnostics: %v, %v", entries, err)
			}
		})
	}
}
