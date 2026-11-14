// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"crypto/rsa"
	"net/http"
)

// Config describes a runner's identity and Broker capabilities.
// It is independent of registration, local installation and job execution.
type Config struct {
	ID                 uint64
	Name               string
	Version            string
	OwnerName          string // Defaults to hostname and PID.
	DisableUpdate      bool
	SkipSessionRecover bool
}

// ClientConfig supplies runner credentials and HTTP transport configuration.
// These values are independent of how the runner was registered.
type ClientConfig struct {
	HTTPClient  *http.Client
	BrokerURL   string
	Version     string
	Credentials Credentials
	PrivateKey  *rsa.PrivateKey
}
