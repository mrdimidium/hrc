// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"gilti.dev/hrc/internal/listener"
	"gilti.dev/hrc/internal/worker"
)

type runnerSettings struct {
	AgentID            uint64 `json:"agentId"`
	AgentName          string `json:"agentName"`
	Ephemeral          bool   `json:"ephemeral"`
	UseV2Flow          bool   `json:"useV2Flow"`
	ServerURLV2        string `json:"serverUrlV2"`
	WorkFolder         string `json:"workFolder"`
	DisableUpdate      bool   `json:"disableUpdate"`
	SkipSessionRecover bool   `json:"skipSessionRecover"`
	PoolID             int    `json:"poolId"`
	PoolName           string `json:"poolName"`
	GitHubURL          string `json:"gitHubUrl"`
	ServerURL          string `json:"serverUrl"`
	HostedServer       *bool  `json:"IsHostedServer"`
	MonitorSocket      string `json:"monitorSocketAddress"`
}

type runnerRegistration struct {
	settings    runnerSettings
	credentials listener.Credentials
	key         *rsa.PrivateKey
}

func parseRunnerRegistration(encoded string) (*runnerRegistration, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid JIT base64")
	}
	var files map[string]string
	if json.Unmarshal(decoded, &files) != nil {
		return nil, errors.New("invalid JIT JSON")
	}
	contents := make(map[string][]byte, len(files))
	for name, value := range files {
		switch name {
		case ".runner", ".credentials", ".credentials_rsaparams":
		default:
			return nil, fmt.Errorf("unexpected JIT filename %q", name)
		}
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("invalid JIT file %s", name)
		}
		contents[name] = data
	}
	var registration runnerRegistration
	if registration.settings, err = decodeRunnerSettings(contents[".runner"]); err != nil {
		return nil, errors.New("missing or invalid JIT runner settings")
	}
	settings := registration.settings
	if !settings.Ephemeral || !settings.UseV2Flow || settings.ServerURLV2 == "" || settings.AgentID == 0 || settings.AgentName == "" {
		return nil, errors.New("only ephemeral v2 JIT runners with a Broker URL are supported")
	}
	if settings.WorkFolder == "" {
		return nil, errors.New("JIT workFolder is required")
	}
	if json.Unmarshal(contents[".credentials"], &registration.credentials) != nil {
		return nil, errors.New("missing or invalid JIT credentials")
	}
	registration.key, err = runnerRSAKey(contents[".credentials_rsaparams"])
	if err != nil {
		return nil, err
	}
	return &registration, nil
}

// JIT settings can contain stringified numbers and booleans. Newtonsoft.Json
// converts these when .NET reads .runner; encoding/json requires native values.
func decodeRunnerSettings(data []byte) (runnerSettings, error) {
	var settings runnerSettings
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return settings, err
	}
	for name, raw := range fields {
		field := strings.ToLower(name)
		switch field {
		case "agentid", "poolid", "ephemeral", "usev2flow", "disableupdate", "skipsessionrecover", "ishostedserver":
		default:
			continue
		}
		if len(raw) == 0 || raw[0] != '"' {
			continue // Native JSON values and null require no conversion.
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return settings, err
		}
		if field != "agentid" && field != "poolid" {
			boolean, err := strconv.ParseBool(value)
			if err != nil {
				return settings, errors.New("invalid JIT boolean setting")
			}
			value = strconv.FormatBool(boolean)
		}
		fields[name] = json.RawMessage(value)
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal(normalized, &settings)
	return settings, err
}

func (r *runnerRegistration) listenerConfig(version string) listener.Config {
	return listener.Config{ID: r.settings.AgentID, Name: r.settings.AgentName, Version: version,
		DisableUpdate: true, SkipSessionRecover: r.settings.SkipSessionRecover}
}

func (r *runnerRegistration) workerConfig(dir string, keepDescendants bool) worker.Config {
	return worker.Config{
		Dir: dir, Name: r.settings.AgentName, ID: r.settings.AgentID,
		GroupID: r.settings.PoolID, GroupName: r.settings.PoolName,
		WorkDir: dir, GitHubURL: r.settings.GitHubURL,
		ServerURL: r.settings.ServerURL, ServerURLV2: r.settings.ServerURLV2,
		HostedServer: r.settings.HostedServer, MonitorSocket: r.settings.MonitorSocket,
		DisableUpdate: true, KeepDescendants: keepDescendants,
	}
}

func runnerRSAKey(data []byte) (*rsa.PrivateKey, error) {
	var parameters map[string][]byte
	if json.Unmarshal(data, &parameters) != nil {
		return nil, errors.New("invalid JIT RSA parameters")
	}
	// RSAParametersSerializable uses lower-case names (including inverseQ).
	parts := make(map[string]*big.Int)
	for name, value := range parameters {
		parts[strings.ToLower(name)] = new(big.Int).SetBytes(value)
	}
	for _, name := range []string{"modulus", "exponent", "d", "p", "q"} {
		if parts[name] == nil || parts[name].Sign() <= 0 {
			return nil, errors.New("incomplete JIT RSA parameters")
		}
	}
	if !parts["exponent"].IsInt64() || parts["exponent"].Int64() > 1<<31-1 {
		return nil, errors.New("invalid RSA exponent")
	}
	key := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: parts["modulus"], E: int(parts["exponent"].Int64())}, D: parts["d"], Primes: []*big.Int{parts["p"], parts["q"]}}
	if err := key.Validate(); err != nil {
		return nil, errors.New("invalid JIT RSA key")
	}
	key.Precompute()
	return key, nil
}
