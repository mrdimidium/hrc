// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json/v2"
	"fmt"
	"net/url"
	"os"
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

func (c *Secret) Take() (string, error) {
	switch c.Src {
	case "env":
		return os.Getenv(c.Value), nil
	case "file":
		data, err := os.ReadFile(c.Value)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "inline":
		return c.Value, nil
	}
	return "", fmt.Errorf("unknown secret type")
}

type Group struct {
	Url    string   `json:"url"`
	Name   string   `json:"name"`
	Group  string   `json:"group"`
	Labels []string `json:"labels"`

	Token     Secret                 `json:"pat"`
	GitHubApp scaleset.GitHubAppAuth `json:"app"`
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

type Config struct {
	Id     string  `json:"id"`
	Groups []Group `json:"groups"`

	DirBin    string `json:"dirBin"`
	DirBuilds string `json:"dirBuilds"`
	DirRunner string `json:"dirRunner"`
}

func (c *Config) defaults() {
	if c.DirBin == "" {
		c.DirBin = "./hrc/bin"
	}
	if c.DirBuilds == "" {
		c.DirBuilds = "./hrc/build"
	}
}

func (c *Config) Validate() error {
	c.defaults()

	if c.Id == "" {
		return fmt.Errorf("id is required")
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
