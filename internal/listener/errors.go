// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var ErrCredentials = errors.New("runner credentials are not configured")
var errTransport = errors.New("runner API transport failed")

// RequestError describes a failed runner API request without including tokens or
// server response bodies in its error message.
type RequestError struct {
	StatusCode int
	Kind       string
	RetryAfter time.Duration
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("runner API returned HTTP %d (%s)", e.StatusCode, e.Kind)
}

func newRequestError(response *http.Response, body []byte) *RequestError {
	err := &RequestError{StatusCode: response.StatusCode}
	var data struct {
		Kind   string `json:"errorKind"`
		Source string `json:"source"`
	}
	if json.Unmarshal(body, &data) == nil && data.Source == "actions-broker-listener" {
		switch data.Kind {
		case "AcknowledgeJobNotFound", "RunnerNotFound", "RunnerVersionTooOld", "HostedRunnerDeprovisioned", "RunnerSessionInvalid":
			err.Kind = data.Kind
		}
	}
	if n, e := strconv.Atoi(response.Header.Get("Retry-After")); e == nil && n > 0 {
		err.RetryAfter = time.Duration(n) * time.Second
	} else if date, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil {
		err.RetryAfter = max(0, time.Until(date))
	}
	return err
}

func retryable(err error) bool {
	var response *RequestError
	if errors.As(err, &response) {
		return (response.Kind == "" || response.Kind == "OAuth") &&
			(response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500)
	}
	return errors.Is(err, errTransport)
}

func statusIs(err error, statuses ...int) bool {
	var response *RequestError
	if !errors.As(err, &response) {
		return false
	}
	for _, status := range statuses {
		if response.StatusCode == status {
			return true
		}
	}
	return false
}

func validURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http") && u.RawQuery == "" && u.Fragment == ""
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
