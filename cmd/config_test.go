// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	} else if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}

func TestSecretValidate(t *testing.T) {
	for _, tt := range []struct {
		secret Secret
		want   string
	}{
		{Secret{"inline", "token"}, ""},
		{Secret{"file", "/token"}, ""},
		{Secret{"env", "TOKEN"}, ""},
		{Secret{"unknown", "token"}, "secret type"},
		{Secret{"inline", ""}, "secret value"},
	} {
		t.Run(tt.secret.Src+"/"+tt.secret.Value, func(t *testing.T) {
			checkError(t, tt.secret.Validate(), tt.want)
		})
	}
}

func TestGroupValidate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Group)
		want   string
	}{
		{"PAT", func(g *Group) {}, ""},
		{"app", func(g *Group) {
			g.Token = Secret{}
			g.GitHubApp.ClientID = "client"
			g.GitHubApp.InstallationID = 1
			g.GitHubApp.PrivateKey = "key"
		}, ""},
		{"URL", func(g *Group) { g.Url = "://invalid" }, "invalid registration URL"},
		{"relative URL", func(g *Group) { g.Url = "/org/repo" }, "invalid registration URL"},
		{"missing host", func(g *Group) { g.Url = "https:/org/repo" }, "invalid registration URL"},
		{"unsupported scheme", func(g *Group) { g.Url = "ftp://github.com/org/repo" }, "invalid registration URL"},
		{"HTTP URL", func(g *Group) { g.Url = "http://github.example/org/repo" }, ""},
		{"name", func(g *Group) { g.Name = "" }, "name is required"},
		{"empty label", func(g *Group) { g.Labels = []string{"linux", ""} }, "label at index 1 is empty"},
		{"blank label", func(g *Group) { g.Labels = []string{" \t"} }, "label at index 0 is empty"},
		{"credentials", func(g *Group) { g.Token = Secret{} }, "no credentials provided"},
		{"reused directory", func(g *Group) { g.ReuseDirs = true }, ""},
		{"reused directory path", func(g *Group) { g.ReuseDirs, g.Name = true, "linux/x64" }, "cannot be a directory name"},
		{"reused directory backslash", func(g *Group) { g.ReuseDirs, g.Name = true, `linux\x64` }, "cannot be a directory name"},
		{"reused directory parent", func(g *Group) { g.ReuseDirs, g.Name = true, ".." }, "cannot be a directory name"},
		{"path without reuse", func(g *Group) { g.Name = "linux/x64" }, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := Group{Url: "https://github.com/org/repo", Name: "runners", Token: Secret{"inline", "token"}}
			tt.change(&g)
			checkError(t, g.Validate(), tt.want)
			if g.Group != "default" {
				t.Fatalf("default group = %q, want default", g.Group)
			}
		})
	}
	t.Run("preserves group and labels", func(t *testing.T) {
		g := Group{Url: "https://github.com/org", Name: "runners", Group: "custom", Labels: []string{"linux"}, Token: Secret{"env", "TOKEN"}}
		checkError(t, g.Validate(), "")
		if g.Group != "custom" || len(g.Labels) != 1 || g.Labels[0] != "linux" {
			t.Fatalf("unexpected group: %+v", g)
		}
	})
}

func TestConfigValidate(t *testing.T) {
	cfg := Config{}
	checkError(t, cfg.Validate(), "id is required")
	cfg.Id = "test-host"
	checkError(t, cfg.Validate(), "")
	if cfg.DirBin != "./hrc/bin" || cfg.DirBuilds != "./hrc/build" {
		t.Fatalf("unexpected default directories: %+v", cfg)
	}
	cfg.DirBin, cfg.DirBuilds, cfg.DirRunner = "/bin", "/builds", "/runner"
	checkError(t, cfg.Validate(), "")
	if cfg.DirBin != "/bin" || cfg.DirBuilds != "/builds" || cfg.DirRunner != "/runner" {
		t.Fatalf("custom directories changed: %+v", cfg)
	}
	cfg.Groups = []Group{
		{Url: "https://github.com/org", Name: "first", Token: Secret{"inline", "token"}},
		{Url: "https://github.com/org", Name: "second", Group: "custom", Token: Secret{"inline", "token"}},
	}
	checkError(t, cfg.Validate(), "")
	if cfg.Groups[0].Group != "default" || cfg.Groups[1].Group != "custom" {
		t.Fatalf("unexpected runner groups: %q, %q", cfg.Groups[0].Group, cfg.Groups[1].Group)
	}
	cfg.Groups[1].Name = "first"
	cfg.Groups[1].Url = "https://github.com/other"
	checkError(t, cfg.Validate(), `group 1 has duplicate name "first": already used by group 0`)
	cfg.Groups = []Group{{}}
	checkError(t, cfg.Validate(), "group 0 is invalid: invalid registration URL")
}

func TestLoadConfig(t *testing.T) {
	for _, tt := range []struct {
		name, content, want string
	}{
		{"valid", `{"id":"host","groups":[{"url":"https://github.com/org","name":"runners","pat":{"type":"inline","value":"token"}}]}`, ""},
		{"missing file", "", "failed to read the config file"},
		{"invalid JSON", `{`, "failed to parse json"},
		{"invalid config", `{}`, "invalid configuration: id is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if tt.content != "" {
				if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := LoadConfig(path)
			checkError(t, err, tt.want)
			if tt.want == "" && (cfg.Id != "host" || len(cfg.Groups) != 1 || cfg.DirBin != "./hrc/bin" || cfg.DirBuilds != "./hrc/build") {
				t.Fatalf("unexpected config: %+v", cfg)
			}
			if tt.want == "" && cfg.Groups[0].Group != "default" {
				t.Fatalf("loaded group = %q, want default", cfg.Groups[0].Group)
			}
			if tt.want != "" && cfg != nil {
				t.Fatalf("expected nil config on error, got %+v", cfg)
			}
		})
	}
}

func TestLoadConfigQuota(t *testing.T) {
	const group = `"groups":[{"url":"https://github.com/org","name":"runners","pat":{"type":"inline","value":"token"}}]`
	for _, tt := range []struct {
		name, resources, want string
		quota, reserve        Resources
	}{
		{name: "absent"},
		{name: "binary units", resources: `"quota":{"cpu":2,"memory":"8GiB"},"reserve":{"cpu":0.5,"memory":"512MiB"},`,
			quota: Resources{CPU: 2, Memory: 8 << 30}, reserve: Resources{CPU: 0.5, Memory: 512 << 20}},
		{name: "bytes and units", resources: `"quota":{"memory":"1048576"},"reserve":{"memory":"1TiB"},`,
			quota: Resources{Memory: 1 << 20}, reserve: Resources{Memory: 1 << 40}},
		{name: "decimal unit", resources: `"quota":{"memory":"8GB"},`, want: "invalid memory size"},
		{name: "fraction", resources: `"quota":{"memory":"1.5GiB"},`, want: "invalid memory size"},
		{name: "number", resources: `"quota":{"memory":1024},`, want: "failed to parse json"},
		{name: "overflow", resources: `"quota":{"memory":"17179869184GiB"},`, want: "invalid memory size"},
		{name: "negative cpu", resources: `"quota":{"cpu":-1},`, want: "invalid quota: cpu"},
		{name: "negative reserve", resources: `"reserve":{"cpu":-1},`, want: "invalid reserve: cpu"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"id":"host",`+tt.resources+group+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			checkError(t, err, tt.want)
			if tt.want == "" && (cfg.Quota != tt.quota || cfg.Reserve != tt.reserve) {
				t.Fatalf("quota %+v reserve %+v, want %+v and %+v", cfg.Quota, cfg.Reserve, tt.quota, tt.reserve)
			}
		})
	}
}

func TestByteSizeString(t *testing.T) {
	for size, want := range map[ByteSize]string{0: "0", 1000: "1000", 1 << 10: "1KiB", 3 << 29: "1536MiB", 8 << 30: "8GiB", 2 << 40: "2TiB"} {
		if got := size.String(); got != want {
			t.Errorf("ByteSize(%d) = %q, want %q", uint64(size), got, want)
		}
	}
}
