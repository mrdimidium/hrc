// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type oauthTestTransport func(*http.Request) (*http.Response, error)

func (f oauthTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func oauthTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func oauthTestClient(t *testing.T, key *rsa.PrivateKey, transport oauthTestTransport, migration, explicit bool) *Client {
	t.Helper()
	data := map[string]string{"clientId": "client", "authorizationUrl": "https://runner.test/v1"}
	if migration {
		data["enableAuthMigrationByDefault"], data["authorizationUrlV2"] = "true", "https://runner.test/v2"
	}
	if explicit {
		data["oauthEndpointUrl"] = "https://runner.test/exchange"
	}
	client, err := NewClient(ClientConfig{BrokerURL: "https://runner.test/", PrivateKey: key,
		Credentials: Credentials{Scheme: "OAuth", Data: data}, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestOAuthRecovery(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"transport", "timeout", "server", "throttle", "permanent", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "deadline" {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, time.Second)
					defer stop()
				}
				calls, sessions := 0, 0
				var firstAssertion string
				start := time.Now()
				client := oauthTestClient(t, key, func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/session" {
						sessions++
						return oauthTestResponse(200, `{"sessionId":"session"}`), nil
					}
					calls++
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					assertion := r.Form.Get("client_assertion")
					if calls > 1 {
						if assertion == firstAssertion {
							t.Error("retry reused JWT assertion")
						}
						return oauthTestResponse(200, `{"access_token":"token","expires_in":3600}`), nil
					}
					firstAssertion = assertion
					switch mode {
					case "transport":
						return nil, errors.New("connection reset: secret-assertion")
					case "timeout", "deadline":
						<-r.Context().Done()
						return nil, r.Context().Err()
					case "cancel":
						cancel()
						return nil, r.Context().Err()
					case "server":
						return oauthTestResponse(503, `{"error":"temporarily_unavailable","error_description":"secret-assertion"}`), nil
					case "throttle":
						response := oauthTestResponse(429, `{"error":"temporarily_unavailable"}`)
						response.Header.Set("Retry-After", "3")
						return response, nil
					default:
						return oauthTestResponse(400, `{"error":"invalid_client","error_description":"secret-assertion"}`), nil
					}
				}, mode == "cancel" || mode == "deadline", false)
				_, err := client.CreateSession(ctx, &BrokerSession{})
				if err != nil && strings.Contains(err.Error(), "secret-assertion") {
					t.Fatal("OAuth failure leaked secrets")
				}
				switch mode {
				case "cancel", "deadline", "permanent":
					if err == nil || calls != 1 || sessions != 0 {
						t.Fatalf("unexpected retry/fallback: calls=%d sessions=%d err=%v", calls, sessions, err)
					}
					if mode != "permanent" && !errors.Is(err, ctx.Err()) {
						t.Fatalf("lost caller cancellation: %v", err)
					}
				default:
					if err != nil || calls != 2 || sessions != 1 {
						t.Fatalf("recovery: calls=%d sessions=%d err=%v", calls, sessions, err)
					}
					if mode == "timeout" && time.Since(start) != 61*time.Second {
						t.Fatalf("exchange timeout/retry = %v", time.Since(start))
					}
					if mode == "throttle" && time.Since(start) != 3*time.Second {
						t.Fatalf("Retry-After ignored: %v", time.Since(start))
					}
				}
			})
		})
	}
}

func TestOAuthMigrationFallback(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"exchange", "transport", "timeout", "unauthorized", "forbidden"} {
		for _, explicit := range []bool{false, true} {
			name := mode
			if explicit {
				name += "_explicit_endpoint"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					var audiences []string
					failed := false
					client := oauthTestClient(t, key, func(r *http.Request) (*http.Response, error) {
						if r.URL.Path == "/session" {
							if !failed && r.Header.Get("Authorization") == "Bearer /v2" && (mode == "unauthorized" || mode == "forbidden") {
								failed = true
								status := 401
								if mode == "forbidden" {
									status = 403
								}
								return oauthTestResponse(status, ""), nil
							}
							return oauthTestResponse(200, `{"sessionId":"session"}`), nil
						}
						if err := r.ParseForm(); err != nil {
							t.Fatal(err)
						}
						parts := strings.Split(r.Form.Get("client_assertion"), ".")
						if len(parts) != 3 {
							t.Fatal("invalid assertion")
						}
						raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
						var claims struct {
							Audience string `json:"aud"`
						}
						if err := json.Unmarshal(raw, &claims); err != nil {
							t.Fatal(err)
						}
						audience := strings.TrimPrefix(claims.Audience, "https://runner.test")
						audiences = append(audiences, audience)
						wantPath := audience
						if explicit {
							wantPath = "/exchange"
						}
						if r.URL.Path != wantPath {
							t.Errorf("exchange endpoint = %s, want %s", r.URL.Path, wantPath)
						}
						if !failed && audience == "/v2" {
							switch mode {
							case "exchange":
								failed = true
								return oauthTestResponse(400, `{"error":"invalid_grant"}`), nil
							case "transport":
								failed = true
								return nil, io.ErrUnexpectedEOF
							case "timeout":
								failed = true
								<-r.Context().Done()
								return nil, r.Context().Err()
							}
						}
						return oauthTestResponse(200, `{"access_token":"`+audience+`","expires_in":3600}`), nil
					}, true, explicit)
					for i := range 3 {
						if i == 2 {
							time.Sleep(time.Hour)
						}
						client.RefreshRunnerToken()
						if _, err := client.CreateSession(t.Context(), &BrokerSession{}); err != nil {
							t.Fatal(err)
						}
					}
					if got := strings.Join(audiences, ","); got != "/v2,/v1,/v1,/v2" {
						t.Fatalf("fallback, refresh, and migration resume: %s", got)
					}
				})
			})
		}
	}
}
