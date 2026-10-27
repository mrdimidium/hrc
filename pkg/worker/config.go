// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Config describes the local runtime, regardless of how the runner is registered.
type Config struct {
	// Dir contains the complete runner installation, including bin/Runner.Worker.
	// It must be dedicated to this invocation. Runtime configuration files are
	// ignored and left untouched. The caller owns its workspaces and diagnostics.
	Dir     string
	WorkDir string

	// Env overrides the inherited environment and the installation's .env file.
	// RUNNER_CONFIG and RUNNER_DIAG are reserved and set by Worker.
	Env []string

	// Name defaults to the hostname. ID and group identify the runner in logs
	// and request metadata; they do not register it or acquire a job.
	Name      string
	ID        uint64
	GroupID   int
	GroupName string

	// Server metadata lets .NET distinguish GitHub.com from GitHub Enterprise.
	// HostedServer explicitly overrides its normal URL-based inference.
	GitHubURL    string
	ServerURL    string
	ServerURLV2  string
	HostedServer *bool

	MonitorSocket string
	DisableUpdate bool

	// KeepDescendants leaves processes that outlive a Worker which exited on
	// its own, so build servers can serve later jobs. On Unix it changes
	// nothing: daemons leave the Worker's process group with setsid and survive
	// anyway, while other descendants are still killed. On Windows, job
	// membership is inherited and only breakaway leaves it, so every remaining
	// process is kept, including after the controller itself crashes. Forced
	// termination by Close or a failed startup still kills the whole tree.
	KeepDescendants bool

	// Stdout and Stderr default to os.Stdout and os.Stderr. A writer shared by
	// both streams must support concurrent Write calls.
	Stdout, Stderr io.Writer
}

func (c *Config) validate() error {
	if c.Dir == "" {
		return errors.New("runner directory is required")
	}

	var err error
	c.Dir, err = filepath.Abs(c.Dir)
	if err != nil {
		return err
	}

	info, err := os.Stat(filepath.Join(c.Dir, "bin", binaryName()))
	if err != nil {
		return fmt.Errorf("find Worker binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("Worker binary must be a regular file")
	}

	c.Env = slices.Clone(c.Env)
	if err := validateEnv(c.Env); err != nil {
		return err
	}

	if c.Name == "" {
		c.Name, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("get Worker hostname: %w", err)
		}
	}
	if c.WorkDir == "" {
		c.WorkDir = "_work"
	}
	if c.HostedServer != nil {
		hosted := *c.HostedServer
		c.HostedServer = &hosted
	}

	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}

	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}

	return nil
}

// renderRunner translates runtime values into .NET's RunnerSettings document.
func (c *Config) renderRunner() ([]byte, error) {
	return json.Marshal(struct {
		AgentID              uint64 `json:"agentId,omitempty"`
		AgentName            string `json:"agentName"`
		PoolID               int    `json:"poolId,omitempty"`
		PoolName             string `json:"poolName,omitempty"`
		WorkFolder           string `json:"workFolder"`
		GitHubURL            string `json:"gitHubUrl,omitempty"`
		ServerURL            string `json:"serverUrl,omitempty"`
		ServerURLV2          string `json:"serverUrlV2,omitempty"`
		HostedServer         *bool  `json:"IsHostedServer,omitempty"`
		MonitorSocketAddress string `json:"monitorSocketAddress,omitempty"`
		DisableUpdate        bool   `json:"disableUpdate,omitempty"`
	}{
		AgentID: c.ID, AgentName: c.Name, PoolID: c.GroupID, PoolName: c.GroupName,
		WorkFolder: c.WorkDir, GitHubURL: c.GitHubURL, ServerURL: c.ServerURL,
		ServerURLV2: c.ServerURLV2, HostedServer: c.HostedServer,
		MonitorSocketAddress: c.MonitorSocket, DisableUpdate: c.DisableUpdate,
	})
}

func validateEnv(entries []string) error {
	for _, entry := range entries {
		if k, _, ok := strings.Cut(entry, "="); !ok || k == "" || strings.ContainsRune(entry, 0) {
			return errors.New("invalid Worker environment entry")
		}
	}
	return nil
}
