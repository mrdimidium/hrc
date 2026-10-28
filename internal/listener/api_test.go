// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientAuthAndMethods(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var exchanges, sessions atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Runner-Test", "metadata")
		if r.URL.Path == "/oauth" {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			assertion := strings.Split(r.Form.Get("client_assertion"), ".")
			if len(assertion) != 3 {
				t.Error("missing JWT assertion")
				w.WriteHeader(400)
				return
			}
			signature, _ := base64.RawURLEncoding.DecodeString(assertion[2])
			hash := sha256.Sum256([]byte(assertion[0] + "." + assertion[1]))
			if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature) != nil {
				t.Error("bad JWT signature")
			}
			claimsJSON, err := base64.RawURLEncoding.DecodeString(assertion[1])
			var claims struct {
				NotBefore int64 `json:"nbf"`
				Expires   int64 `json:"exp"`
			}
			if err != nil || json.Unmarshal(claimsJSON, &claims) != nil || claims.Expires-claims.NotBefore != 300 || claims.NotBefore > time.Now().Unix() {
				t.Error("runner assertion must have a validity window of five minutes")
			}
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "client" {
				t.Error("wrong OAuth flow")
			}
			fmt.Fprintf(w, `{"access_token":"runner-%d","expires_in":3600}`, exchanges.Add(1))
			return
		}
		if r.Header.Get("X-GitHub-Api-Version") != "" || r.Header.Get("Accept") != "application/json" {
			t.Error("private request has GitHub REST headers")
		}
		expected := "Bearer runner-2"
		if strings.HasPrefix(r.URL.Path, "/job/") {
			expected = "Bearer job-token"
		}
		if r.URL.Path == "/broker/session" && r.Method == "POST" && sessions.Add(1) == 1 {
			if r.Header.Get("Authorization") != "Bearer runner-1" {
				t.Error("missing initial runner token")
			}
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != expected {
			t.Errorf("%s: wrong auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/broker/session":
			if r.Method == "DELETE" {
				if r.URL.Query().Get("sessionId") != "session" {
					t.Error("wrong deleted session")
				}
				w.WriteHeader(204)
				return
			}
			var body BrokerSession
			json.NewDecoder(r.Body).Decode(&body)
			if !body.BrokerSession || body.OwnerName != "host" || body.Agent.ID != 7 {
				t.Error("invalid session payload")
			}
			io.WriteString(w, `{"sessionId":"session"}`)
		case "/broker/message":
			if r.URL.Query().Get("disableUpdate") != "true" || r.URL.Query().Get("runnerVersion") != "2.333.0" {
				t.Error("missing runner metadata")
			}
			io.WriteString(w, `{"messageType":"RunnerJobRequest","body":"{}"}`)
		case "/broker/acknowledge":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["runnerRequestId"] != "request" {
				t.Error("wrong acknowledgement")
			}
			w.WriteHeader(204)
		case "/run/acquirejob":
			var body AcquireJobRequest
			json.NewDecoder(r.Body).Decode(&body)
			if body.JobMessageID != "request" || body.BillingOwnerID != "owner" {
				t.Error("invalid acquire payload")
			}
			fmt.Fprintf(w, `{"jobId":"job","plan":{"planId":"plan"},"messageType":"RunnerJobRequest","unknown":9007199254740993123,"unicode":"Привет 🌍","resources":{"endpoints":[{"name":"SystemVssConnection","url":%q,"authorization":{"parameters":{"AccessToken":"job-token"}}}]}}`, server.URL+"/job/")
		case "/job/renewjob":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["jobId"] != "job" || body["planId"] != "plan" {
				t.Error("invalid renew payload")
			}
			json.NewEncoder(w).Encode(&RenewJobResponse{LockedUntil: time.Now().Add(time.Minute)})
		case "/job/completejob":
			var body struct {
				PlanID string `json:"planId"`
				JobID  string `json:"jobId"`
				CompleteJobRequest
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.JobID != "job" || body.PlanID != "plan" || body.Conclusion != "failed" || body.BillingOwnerID != "owner" || len(body.Annotations) != 1 || body.Annotations[0].Level != "failure" {
				t.Errorf("invalid completion: %#v", body)
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api, err := NewClient(ClientConfig{BrokerURL: server.URL + "/broker", Version: "2.333.0", Credentials: Credentials{Scheme: "OAuth", Data: map[string]string{"clientId": "client", "authorizationUrl": server.URL + "/oauth"}}, PrivateKey: key})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := api.CreateSession(ctx, &BrokerSession{OwnerName: "host", Agent: &BrokerAgent{ID: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionID != "session" {
		t.Fatal("lost typed result or response metadata")
	}
	options := &BrokerMessageOptions{Status: "Online", RunnerVersion: "2.333.0", OS: "Linux", Architecture: "X64", DisableUpdate: true}
	if _, err = api.GetMessage(ctx, session.SessionID, options); err != nil {
		t.Fatal(err)
	}
	if err = api.Acknowledge(ctx, session.SessionID, "request", options); err != nil {
		t.Fatal(err)
	}
	job, err := api.AcquireJob(ctx, server.URL+"/run", &AcquireJobRequest{JobMessageID: "request", RunnerOS: "Linux", BillingOwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(job.Raw, []byte("9007199254740993123")) || !bytes.Contains(job.Raw, []byte("Привет 🌍")) {
		t.Fatal("job JSON was rewritten")
	}
	if _, err = api.RenewJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = api.CompleteJob(ctx, job, &CompleteJobRequest{Conclusion: "failed", Annotations: []JobAnnotation{{Level: "failure", Message: "infrastructure", IsInfrastructureIssue: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = api.DeleteSession(ctx, session.SessionID); err != nil {
		t.Fatal(err)
	}
	if exchanges.Load() != 2 {
		t.Fatalf("expected a single OAuth refresh, got %d exchanges", exchanges.Load())
	}
	api.Close()
	if _, err = api.GetMessage(ctx, session.SessionID, options); !errors.Is(err, ErrCredentials) {
		t.Fatalf("closed runner auth: %v", err)
	}
}

func TestClientErrorsAndRedirects(t *testing.T) {
	var redirected atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/broker/message":
			w.WriteHeader(403)
			io.WriteString(w, `{"source":"actions-broker-listener","errorKind":"RunnerSessionInvalid","message":"sensitive-job-token"}`)
		case "/broker/acknowledge":
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
		case "/run/acquirejob":
			w.Header().Set("Location", server.URL+"/redirected")
			w.WriteHeader(302)
		case "/redirected":
			redirected.Add(1)
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api, err := NewClient(ClientConfig{BrokerURL: server.URL + "/broker/"})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	// Seed only the runner token; none of these calls needs an OAuth exchange.
	api.token, api.tokenExpiry = "runner-token", time.Now().Add(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = api.GetMessage(ctx, "session", nil)
	var failure *RequestError
	if !errors.As(err, &failure) || failure.Kind != "RunnerSessionInvalid" || failure.StatusCode != 403 || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("incorrect private API error: %v", err)
	}
	err = api.Acknowledge(ctx, "session", "request", nil)
	if !errors.As(err, &failure) || failure.RetryAfter != 2*time.Second {
		t.Fatalf("lost Retry-After: %v", err)
	}
	_, err = api.AcquireJob(ctx, server.URL+"/run/", &AcquireJobRequest{JobMessageID: "request"})
	if !errors.As(err, &failure) || failure.StatusCode != 302 || redirected.Load() != 0 {
		t.Fatalf("followed private POST redirect: %v", err)
	}
	if _, err = api.RenewJob(ctx, nil); err == nil {
		t.Fatal("accepted nil job")
	}
}

func TestClientJobTokenRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer job-token" || r.URL.Path != "/job/completejob" {
			t.Errorf("incorrect retry: %s, %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(ClientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var job RunJob
	raw := fmt.Sprintf(`{"jobId":"job","messageType":"RunnerJobRequest","plan":{"planId":"plan"},"resources":{"endpoints":[{"name":"SystemVssConnection","url":%q,"authorization":{"parameters":{"AccessToken":"job-token"}}}]}}`, server.URL+"/job")
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := client.CompleteJob(ctx, &job, &CompleteJobRequest{Conclusion: "failed"}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("completion was not retried: %d", requests.Load())
	}
}

func TestClientAuthMigration(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		enabled  bool
		v2       bool
		explicit bool
		upper    bool
		want     string
	}{
		{name: "disabled", v2: true, want: "/v1"},
		{name: "enabled", enabled: true, v2: true, want: "/v2"},
		{name: "missing_v2", enabled: true, want: "/v1"},
		{name: "explicit_endpoint", enabled: true, v2: true, explicit: true, want: "/v2"},
		{name: "jit_casing", upper: true, want: "/v1"},
		{name: "migrated_casing", enabled: true, v2: true, explicit: true, upper: true, want: "/v2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantPath := test.want
				if test.explicit {
					wantPath = "/exchange"
				}
				if r.Method != http.MethodPost || r.URL.Path != wantPath {
					t.Errorf("OAuth request = %s %s, want POST %s", r.Method, r.URL.Path, wantPath)
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				parts := strings.Split(r.Form.Get("client_assertion"), ".")
				if len(parts) != 3 {
					t.Error("missing JWT assertion")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				raw, err := base64.RawURLEncoding.DecodeString(parts[1])
				var claims struct {
					Audience string `json:"aud"`
				}
				if err != nil || json.Unmarshal(raw, &claims) != nil || claims.Audience != server.URL+test.want {
					t.Errorf("JWT audience = %q, want %q", claims.Audience, server.URL+test.want)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"access_token":"runner-token","expires_in":3600}`)
			}))
			defer server.Close()
			data := map[string]string{"clientId": "client", "authorizationUrl": server.URL + "/v1"}
			if test.enabled {
				// This is the spelling written by upstream into .credentials.
				data["enableAuthMigrationByDefault"] = "true"
			}
			if test.v2 {
				data["authorizationUrlV2"] = server.URL + "/v2"
			}
			if test.explicit {
				data["oauthEndpointUrl"] = server.URL + "/exchange"
			}
			if test.upper {
				upper := make(map[string]string, len(data))
				for name, value := range data {
					upper[strings.ToUpper(name)] = value
				}
				data = upper
			}
			client, err := NewClient(ClientConfig{PrivateKey: key, Credentials: Credentials{Scheme: "OAuth", Data: data}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.authToken(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
