// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Credentials is the runner OAuth credential data supplied at registration.
type Credentials struct {
	Scheme string            `json:"scheme"`
	Data   map[string]string `json:"data"`
}

// .NET CredentialData uses a dictionary with StringComparer.OrdinalIgnoreCase.
func (c Credentials) value(name string) string {
	if value, ok := c.Data[name]; ok {
		return value
	}
	for key, value := range c.Data {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

type BrokerAgent struct {
	ID            uint64 `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	OSDescription string `json:"osDescription"`
}

type BrokerSession struct {
	SessionID     string       `json:"sessionId,omitempty"`
	OwnerName     string       `json:"ownerName,omitempty"`
	Agent         *BrokerAgent `json:"agent,omitempty"`
	BrokerSession bool         `json:"brokerSession"`
}

type BrokerMessage struct {
	MessageType string `json:"messageType"`
	Body        string `json:"body"`
}

type BrokerMessageOptions struct {
	Status        string
	RunnerVersion string
	OS            string
	Architecture  string
	DisableUpdate bool
}

func (o *BrokerMessageOptions) query(sessionID string) url.Values {
	q := url.Values{"sessionId": {sessionID}}
	if o != nil {
		q.Set("status", o.Status)
		q.Set("runnerVersion", o.RunnerVersion)
		q.Set("os", o.OS)
		q.Set("architecture", o.Architecture)
		q.Set("disableUpdate", strconv.FormatBool(o.DisableUpdate))
	}
	return q
}

type BrokerJobReference struct {
	RunnerRequestID   string `json:"runner_request_id"`
	RunServiceURL     string `json:"run_service_url"`
	BillingOwnerID    string `json:"billing_owner_id"`
	ShouldAcknowledge bool   `json:"should_acknowledge"`
}

type AcquireJobRequest struct {
	JobMessageID   string `json:"jobMessageId"`
	RunnerOS       string `json:"runnerOS"`
	BillingOwnerID string `json:"billingOwnerId,omitempty"`
}

// RunJob retains the original JSON for the caller. Future fields, numbers, and
// expressions are forwarded without a lossy decode/re-encode through Go types.
type RunJob struct {
	JobID          string `json:"jobId"`
	MessageType    string `json:"messageType"`
	BillingOwnerID string `json:"billingOwnerId"`
	Plan           struct {
		PlanID string `json:"planId"`
	} `json:"plan"`
	Resources struct {
		Endpoints []struct {
			Name          string `json:"name"`
			URL           string `json:"url"`
			Authorization struct {
				Parameters map[string]string `json:"parameters"`
			} `json:"authorization"`
		} `json:"endpoints"`
	} `json:"resources"`
	Raw json.RawMessage `json:"-"`
}

func (j *RunJob) connection() (string, string, error) {
	if j == nil || !strings.EqualFold(j.MessageType, "RunnerJobRequest") || j.JobID == "" || j.Plan.PlanID == "" {
		return "", "", errors.New("only v2 Run Service jobs are supported")
	}
	for _, endpoint := range j.Resources.Endpoints {
		if !strings.EqualFold(endpoint.Name, "SystemVssConnection") {
			continue
		}
		for key, token := range endpoint.Authorization.Parameters {
			if strings.EqualFold(key, "AccessToken") && token != "" && validURL(endpoint.URL) {
				return endpoint.URL, token, nil
			}
		}
	}
	return "", "", errors.New("job is missing its SystemVssConnection URL or access token")
}

type RenewJobResponse struct {
	LockedUntil time.Time `json:"lockedUntil"`
}

type JobAnnotation struct {
	Level                 string `json:"level"`
	Message               string `json:"message"`
	IsInfrastructureIssue bool   `json:"isInfrastructureIssue,omitempty"`
}

type CompleteJobRequest struct {
	Conclusion                    string                     `json:"conclusion"`
	Outputs                       map[string]json.RawMessage `json:"outputs,omitempty"`
	StepResults                   []json.RawMessage          `json:"stepResults,omitempty"`
	Annotations                   []JobAnnotation            `json:"annotations,omitempty"`
	EnvironmentURL                string                     `json:"environmentUrl,omitempty"`
	Telemetry                     []json.RawMessage          `json:"telemetry,omitempty"`
	BillingOwnerID                string                     `json:"billingOwnerId,omitempty"`
	InfrastructureFailureCategory string                     `json:"infrastructureFailureCategory,omitempty"`
}
