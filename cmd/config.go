// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json/v2"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/actions/scaleset"
)

type Secret struct {
	Src   string `json:"type"`
	Value string `json:"value"`
}

func (c *Secret) Validate() error {
	if c.Src != "file" && c.Src != "env" && c.Src != "inline" {
		return fmt.Errorf("secret type must one of: 'inline', 'file', 'env'")
	}
	if c.Value == "" {
		return fmt.Errorf("secret value could not be empty")
	}
	return nil
}

// Take returns the secret without surrounding whitespace, such as the newline
// that ends a secret file. An empty secret is an error.
func (c *Secret) Take() (string, error) {
	var value string
	switch c.Src {
	case "env":
		value = os.Getenv(c.Value)

	case "file":
		data, err := os.ReadFile(c.Value)
		if err != nil {
			return "", err
		}
		value = string(data)

	case "inline":
		value = c.Value

	default:
		return "", fmt.Errorf("unknown secret type")
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s secret %q is empty", c.Src, c.Value)
	}

	return value, nil
}

// GitHubApp authenticates as an installation of a GitHub App. Only the
// private key is a secret.
type GitHubApp struct {
	PrivateKey     Secret `json:"privateKey"`
	ClientID       string `json:"clientId"`
	InstallationID int64  `json:"installationId"`
}

func (a *GitHubApp) Validate() error {
	if a.ClientID == "" {
		return fmt.Errorf("client ID is required")
	}
	if a.InstallationID <= 0 {
		return fmt.Errorf("installation ID is required")
	}
	if err := a.PrivateKey.Validate(); err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}
	return nil
}

// auth reads the private key, which is kept only as long as the client.
func (a *GitHubApp) auth() (scaleset.GitHubAppAuth, error) {
	key, err := a.PrivateKey.Take()
	if err != nil {
		return scaleset.GitHubAppAuth{}, fmt.Errorf("read private key: %w", err)
	}
	return scaleset.GitHubAppAuth{
		PrivateKey:     key,
		ClientID:       a.ClientID,
		InstallationID: a.InstallationID,
	}, nil
}

type Group struct {
	Url    string   `json:"url"`
	Name   string   `json:"name"`
	Group  string   `json:"group"`
	Labels []string `json:"labels"`

	Token     Secret    `json:"pat"`
	GitHubApp GitHubApp `json:"app"`

	// ReuseDirs keeps slots, with their workspaces and build servers, between
	// jobs. Controller shutdown expires all slots, killing their remaining
	// processes and deleting their directories.
	ReuseDirs bool `json:"reuseDirs"`
}

func (c *Group) defaults() {
	if c.Group == "" {
		c.Group = scaleset.DefaultRunnerGroup
	}
}

func (c *Group) Validate() error {
	c.defaults()

	u, err := url.ParseRequestURI(c.Url)
	if err != nil {
		return fmt.Errorf("invalid registration URL: %w, it should be the full URL of where you want to register your scale set, e.g. 'https://github.com/org/repo'", err)
	}
	if u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid registration URL: expected a full HTTP(S) URL with a host")
	}

	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	// The name becomes a directory name when directories are reused.
	if c.ReuseDirs && (strings.ContainsAny(c.Name, `/\:`) || c.Name == "." || c.Name == "..") {
		return fmt.Errorf("name %q cannot be a directory name, which reuseDirs requires", c.Name)
	}

	for i, label := range c.Labels {
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("label at index %d is empty", i)
		}
	}

	tknError := c.Token.Validate()
	appError := c.GitHubApp.Validate()
	if tknError != nil && appError != nil {
		return fmt.Errorf("no credentials provided: either GitHub App (client id, installation id and private key) (recommended) or a Personal Access Token are required")
	}

	return nil
}

// ByteSize is a memory amount written as a JSON string: plain bytes or an
// integer with a KiB, MiB, GiB or TiB suffix, for example "8GiB".
type ByteSize uint64

var byteUnits = []struct {
	suffix string
	size   ByteSize
}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}}

func (b *ByteSize) UnmarshalText(text []byte) error {
	value, unit := string(text), ByteSize(1)
	for _, u := range byteUnits {
		if number, ok := strings.CutSuffix(value, u.suffix); ok {
			value, unit = number, u.size
			break
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n > math.MaxUint64/uint64(unit) {
		return fmt.Errorf("invalid memory size %q: expected bytes or an integer with KiB, MiB, GiB or TiB", text)
	}
	*b = ByteSize(n) * unit
	return nil
}

func (b ByteSize) String() string {
	for _, u := range byteUnits {
		if b >= u.size && b%u.size == 0 {
			return strconv.FormatUint(uint64(b/u.size), 10) + u.suffix
		}
	}
	return strconv.FormatUint(uint64(b), 10)
}

// Resources is an amount of host CPU threads and memory. Zero leaves a
// resource unaccounted.
type Resources struct {
	CPU    float64  `json:"cpu"`
	Memory ByteSize `json:"memory"`
}

func (r *Resources) Validate() error {
	if r.CPU < 0 || math.IsInf(r.CPU, 0) || math.IsNaN(r.CPU) {
		return fmt.Errorf("cpu must be a non-negative number")
	}
	return nil
}

type Config struct {
	Id     string  `json:"id"`
	Groups []Group `json:"groups"`

	// Every runner holds Quota from the host, which keeps Reserve for itself.
	// Without a quota, the number of runners is unlimited.
	Quota   Resources `json:"quota"`
	Reserve Resources `json:"reserve"`

	PathWorker string `json:"pathWorker"`
	DirBuilds  string `json:"dirBuilds"`
	DirLogs    string `json:"dirLogs"`
	DirRunner  string `json:"dirRunner"`
}

func (c *Config) defaults() {
	if c.PathWorker == "" {
		c.PathWorker = "./hrc-worker"
	}
	if c.DirRunner == "" {
		c.DirRunner = "./runner"
	}
	if c.DirBuilds == "" {
		c.DirBuilds = "./hrc/build"
	}
	if c.DirLogs == "" {
		c.DirLogs = "./hrc/log"
	}
}

func (c *Config) Validate() error {
	c.defaults()

	if c.Id == "" {
		return fmt.Errorf("id is required")
	}
	if err := c.Quota.Validate(); err != nil {
		return fmt.Errorf("invalid quota: %w", err)
	}
	if err := c.Reserve.Validate(); err != nil {
		return fmt.Errorf("invalid reserve: %w", err)
	}

	names := make(map[string]int, len(c.Groups))
	for i := range c.Groups {
		if err := c.Groups[i].Validate(); err != nil {
			return fmt.Errorf("group %d is invalid: %w", i, err)
		}
		name := c.Groups[i].Name
		if previous, exists := names[name]; exists {
			return fmt.Errorf("group %d has duplicate name %q: already used by group %d", i, name, previous)
		}
		names[name] = i
	}

	return nil
}

func LoadConfig(pathname string) (*Config, error) {
	bytes, err := os.ReadFile(pathname)
	if err != nil {
		return nil, fmt.Errorf("failed to read the config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse json: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}
