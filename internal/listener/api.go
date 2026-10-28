// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// CreateSession creates a Broker session for the supplied runner identity.
func (c *Client) CreateSession(ctx context.Context, session *BrokerSession) (*BrokerSession, error) {
	if session == nil {
		return nil, errors.New("Broker session is required")
	}
	request := *session
	request.BrokerSession = true
	var result BrokerSession
	if err := c.do(ctx, http.MethodPost, c.brokerURL, "session", nil, &request, "", 5, &result); err != nil {
		return nil, err
	}
	if result.SessionID == "" {
		return nil, errors.New("invalid Broker session response")
	}
	return &result, nil
}

// DeleteSession releases a runner's Broker session.
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	return c.do(ctx, http.MethodDelete, c.brokerURL, "session", url.Values{"sessionId": {sessionID}}, nil, "", 3, nil)
}

// GetMessage long-polls Broker. An empty response returns (nil, nil).
func (c *Client) GetMessage(ctx context.Context, sessionID string, options *BrokerMessageOptions) (*BrokerMessage, error) {
	var result *BrokerMessage
	if err := c.do(ctx, http.MethodGet, c.brokerURL, "message", options.query(sessionID), nil, "", 10, &result); err != nil {
		return nil, err
	}
	if result != nil && result.MessageType == "" {
		return nil, errors.New("invalid Broker message")
	}
	return result, nil
}

// Acknowledge accepts a job reference delivered over the Broker session.
func (c *Client) Acknowledge(ctx context.Context, sessionID, requestID string, options *BrokerMessageOptions) error {
	// Acknowledge is best-effort and must not hold up job acquisition. Bound
	// the entire operation, including OAuth exchange and token refresh.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.do(ctx, http.MethodPost, c.brokerURL, "acknowledge", options.query(sessionID), map[string]string{"runnerRequestId": requestID}, "", 1, nil)
}

// AcquireJob resolves a v2 job reference while retaining its original JSON.
func (c *Client) AcquireJob(ctx context.Context, serviceURL string, request *AcquireJobRequest) (*RunJob, error) {
	if request == nil || request.JobMessageID == "" {
		return nil, errors.New("job message ID is required")
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, serviceURL, "acquirejob", nil, request, "", 5, &raw); err != nil {
		return nil, err
	}
	var result RunJob
	if json.Unmarshal(raw, &result) != nil {
		return nil, errors.New("invalid acquired job JSON")
	}
	if result.BillingOwnerID == "" {
		result.BillingOwnerID = request.BillingOwnerID
	}
	if _, _, err := result.connection(); err != nil {
		return nil, err
	}
	result.Raw = raw
	return &result, nil
}

// RenewJob extends the lease using the acquired job's access token.
func (c *Client) RenewJob(ctx context.Context, job *RunJob) (*RenewJobResponse, error) {
	base, token, err := job.connection()
	if err != nil {
		return nil, err
	}
	var result RenewJobResponse
	request := map[string]string{"planId": job.Plan.PlanID, "jobId": job.JobID}
	if err := c.do(ctx, http.MethodPost, base, "renewjob", nil, request, token, 1, &result); err != nil {
		return nil, err
	}
	if !result.LockedUntil.After(time.Now()) {
		return nil, errors.New("invalid renewjob lease")
	}
	return &result, nil
}

// CompleteJob reports a conclusion and optional outputs, annotations and telemetry.
func (c *Client) CompleteJob(ctx context.Context, job *RunJob, request *CompleteJobRequest) error {
	base, token, err := job.connection()
	if err != nil {
		return err
	}
	if request == nil {
		return errors.New("job conclusion is required")
	}
	switch request.Conclusion {
	case "succeeded", "succeededWithIssues", "failed", "canceled", "skipped", "abandoned":
	default:
		return errors.New("invalid job conclusion")
	}
	copyRequest := *request
	if copyRequest.BillingOwnerID == "" {
		copyRequest.BillingOwnerID = job.BillingOwnerID
	}
	payload := struct {
		PlanID string `json:"planId"`
		JobID  string `json:"jobId"`
		CompleteJobRequest
	}{job.Plan.PlanID, job.JobID, copyRequest}
	return c.do(ctx, http.MethodPost, base, "completejob", nil, payload, token, 5, nil)
}
