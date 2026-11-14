// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestAcknowledgeTimeout(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"oauth", "acknowledge"} {
		for _, parentLimit := range []time.Duration{0, time.Second} {
			t.Run(stage+"/"+parentLimit.String(), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					calls := 0
					client := oauthTestClient(t, key, func(r *http.Request) (*http.Response, error) {
						if r.URL.Path == "/v1" && stage != "oauth" {
							return oauthTestResponse(200, `{"access_token":"token","expires_in":3600}`), nil
						}
						calls++
						<-r.Context().Done()
						return nil, r.Context().Err()
					}, false, false)
					ctx := t.Context()
					if parentLimit != 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, parentLimit)
						defer cancel()
					}
					start := time.Now()
					err := client.Acknowledge(ctx, "session", "request", nil)
					want := 5 * time.Second
					if parentLimit != 0 {
						want = parentLimit
					}
					if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || time.Since(start) != want {
						t.Fatalf("acknowledge: elapsed=%v calls=%d err=%v", time.Since(start), calls, err)
					}
					if parentLimit == 0 && ctx.Err() != nil {
						t.Fatalf("acknowledge canceled acquisition context: %v", ctx.Err())
					}
				})
			})
		}
	}
}

func TestListenerAcquiresAfterAcknowledgeTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		polls, acquired := 0, false
		client, err := NewClient(ClientConfig{BrokerURL: "https://runner.test/", HTTPClient: &http.Client{Transport: oauthTestTransport(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/session":
				return oauthTestResponse(200, `{"sessionId":"session"}`), nil
			case "/message":
				polls++
				if polls == 1 {
					body, _ := json.Marshal(BrokerMessage{MessageType: "RunnerJobRequest", Body: `{"runner_request_id":"request","run_service_url":"https://runner.test/","should_acknowledge":true}`})
					return oauthTestResponse(200, string(body)), nil
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			case "/acknowledge":
				<-r.Context().Done()
				return nil, r.Context().Err()
			case "/acquirejob":
				acquired = true
				if time.Since(start) != 5*time.Second {
					t.Errorf("acquisition delayed by %v", time.Since(start))
				}
				return oauthTestResponse(200, `{"jobId":"job","messageType":"RunnerJobRequest","plan":{"planId":"plan"},"resources":{"endpoints":[{"name":"SystemVssConnection","url":"https://runner.test/","authorization":{"parameters":{"AccessToken":"job-token"}}}]}}`), nil
			case "/renewjob":
				body, _ := json.Marshal(RenewJobResponse{LockedUntil: time.Now().Add(time.Minute)})
				return oauthTestResponse(200, string(body)), nil
			default:
				t.Errorf("unexpected request %s", r.URL.Path)
				return oauthTestResponse(404, ""), nil
			}
		})}})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		client.token, client.tokenExpiry = "token", time.Now().Add(time.Hour)
		listener, err := New(client, Config{ID: 7, Name: "runner", Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close(t.Context())
		if job, err := listener.WaitJob(t.Context()); err != nil || job == nil || !acquired {
			t.Fatalf("acquisition after acknowledge timeout: job=%v acquired=%v err=%v", job, acquired, err)
		}
	})
}
