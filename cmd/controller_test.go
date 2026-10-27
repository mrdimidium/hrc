// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"gilti.dev/hrc/pkg/listener"
	"gilti.dev/hrc/pkg/worker"

	"github.com/actions/scaleset"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "spawnclient" {
		fd, _ := strconv.Atoi(os.Args[2])
		input := os.NewFile(uintptr(fd), "worker-input")
		read := func() (int, []byte) {
			var header [8]byte
			if _, err := io.ReadFull(input, header[:]); err != nil {
				os.Exit(31)
			}
			n := binary.LittleEndian.Uint32(header[4:])
			if n > 50_000_000 {
				os.Exit(32)
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(input, body); err != nil {
				os.Exit(33)
			}
			units := make([]uint16, len(body)/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(body[2*i:])
			}
			return int(binary.LittleEndian.Uint32(header[:])), []byte(string(utf16.Decode(units)))
		}
		kind, raw := read()
		if kind != 1 {
			os.Exit(34)
		}
		var job struct {
			ID       string `json:"jobId"`
			Marker   string `json:"unknownFutureField"`
			Endpoint string `json:"testEndpoint"`
		}
		if json.Unmarshal(raw, &job) != nil || job.Marker != "Привет 🌍" {
			os.Exit(35)
		}
		if os.Getenv("WORKER_ENV") != "override" || os.Getenv("DOTENV_VALUE") != "present" {
			os.Exit(36)
		}
		diag := os.Getenv("RUNNER_DIAG")
		if diag == "" || filepath.Base(diag) != job.ID {
			os.Exit(41)
		}
		if err := os.MkdirAll(diag, 0700); err != nil {
			os.Exit(42)
		}
		if err := os.WriteFile(filepath.Join(diag, "Worker.log"), []byte("diagnostics"), 0600); err != nil {
			os.Exit(43)
		}
		mode := os.Getenv("WORKER_TEST_MODE")
		// Report delivery, so tests can act on a running job without sleeping.
		started, _ := http.NewRequest("POST", job.Endpoint+"/started", nil)
		started.Header.Set("Authorization", "Bearer job-token")
		if response, err := http.DefaultClient.Do(started); err != nil {
			os.Exit(40)
		} else {
			response.Body.Close()
		}
		if mode == "crash" {
			os.Exit(1)
		}
		result := 0
		if mode == "cancel" || mode == "shutdown" || mode == "kill" || mode == "lease" || mode == "recover" {
			kind, _ = read()
			if kind != 2 && kind != 3 {
				os.Exit(37)
			}
			if mode == "shutdown" && kind != 3 {
				os.Exit(38)
			}
			if mode == "kill" {
				time.Sleep(time.Hour)
			}
			if mode == "cancel" {
				// A server timeout of 16 seconds must still allow cleanup that
				// takes longer than timeout-15s (the old one-second grace).
				time.Sleep(1500 * time.Millisecond)
			}
			result = 3
		} else {
			time.Sleep(650 * time.Millisecond)
		}
		if mode == "failure" {
			result = 2
		}
		payload, _ := json.Marshal(map[string]any{"conclusion": worker.Result(result).String()})
		req, _ := http.NewRequest("POST", job.Endpoint+"/completejob", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer job-token")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			os.Exit(39)
		}
		response.Body.Close()
		os.Exit(100 + result)
	}
	os.Exit(m.Run())
}

func controllerFixture(t *testing.T, mode string) (*testRunner, *atomic.Int32, *atomic.Int32, *atomic.Int32, context.CancelFunc) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	var polls, renews, completes, deletes, tokens, sessions, acquires atomic.Int32
	started := make(chan struct{})
	var startedOnce sync.Once
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oauth" {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			assertion := strings.Split(r.Form.Get("client_assertion"), ".")
			if len(assertion) != 3 {
				t.Error("missing assertion")
				w.WriteHeader(400)
				return
			}
			signature, _ := base64.RawURLEncoding.DecodeString(assertion[2])
			hash := sha256.Sum256([]byte(assertion[0] + "." + assertion[1]))
			if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature) != nil {
				t.Error("bad RSA signature")
			}
			var claims map[string]any
			data, _ := base64.RawURLEncoding.DecodeString(assertion[1])
			json.Unmarshal(data, &claims)
			if claims["aud"] != server.URL+"/oauth" || claims["iss"] != "client" || r.Form.Get("grant_type") != "client_credentials" {
				t.Error("bad OAuth claims/form")
			}
			fmt.Fprintf(w, `{"access_token":"runner-token-%d","expires_in":3600}`, tokens.Add(1))
			return
		}
		expected := "Bearer runner-token-1"
		if mode == "refresh" {
			expected = "Bearer runner-token-2"
		}
		if strings.HasPrefix(r.URL.Path, "/job/") {
			expected = "Bearer job-token"
		}
		if mode == "refresh" && r.URL.Path == "/broker/session" && r.Method == "POST" && r.Header.Get("Authorization") == "Bearer runner-token-1" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != expected {
			t.Errorf("%s wrong auth: %q", r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/broker/session":
			if r.Method == "DELETE" {
				deletes.Add(1)
				w.WriteHeader(204)
				return
			}
			var data map[string]any
			json.NewDecoder(r.Body).Decode(&data)
			if data["ownerName"] == nil || data["brokerSession"] != true || data["sessionName"] != nil {
				t.Error("bad session payload")
			}
			n := sessions.Add(1)
			fmt.Fprintf(w, `{"sessionId":"session-%d"}`, n)
		case "/broker/message":
			if r.URL.Query().Get("disableUpdate") != "true" {
				t.Error("updates enabled")
			}
			if r.URL.Query().Get("status") == "Online" {
				body, _ := json.Marshal(map[string]any{"runner_request_id": "request", "run_service_url": server.URL + "/run/", "should_acknowledge": true, "billing_owner_id": "owner"})
				json.NewEncoder(w).Encode(map[string]any{"messageType": "RunnerJobRequest", "body": string(body)})
				return
			}
			n := polls.Add(1)
			if mode == "recover" && n == 1 {
				w.WriteHeader(400)
				io.WriteString(w, `{"source":"actions-broker-listener","errorKind":"RunnerSessionInvalid"}`)
				return
			}
			if mode == "recover" && r.URL.Query().Get("sessionId") != "session-2" {
				t.Error("session not recovered")
			}
			if mode == "cancel" || mode == "kill" || mode == "recover" {
				body := fmt.Sprintf(`{"jobId":%q,"timeout":"00:00:16"}`, jobID)
				if mode == "kill" {
					// Exercise the caller's short default grace when the server
					// supplies no timeout, without waiting 45 seconds in tests.
					body = fmt.Sprintf(`{"jobId":%q}`, jobID)
				}
				json.NewEncoder(w).Encode(map[string]any{"messageType": "JobCancellation", "body": body})
				return
			}
			<-r.Context().Done()
		case "/broker/acknowledge":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["runnerRequestId"] != "request" {
				t.Error("bad ack")
			}
			w.WriteHeader(204)
		case "/run/acquirejob":
			n := acquires.Add(1)
			if mode == "retry" && n == 1 {
				w.WriteHeader(503)
				return
			}
			var request map[string]string
			json.NewDecoder(r.Body).Decode(&request)
			if request["runnerOS"] != testRunnerOS() || request["billingOwnerId"] != "owner" {
				t.Error("bad acquire payload")
			}
			json.NewEncoder(w).Encode(map[string]any{"jobId": jobID, "messageType": "RunnerJobRequest", "plan": map[string]string{"planId": "plan"}, "unknownFutureField": "Привет 🌍", "testEndpoint": server.URL + "/job", "resources": map[string]any{"endpoints": []any{map[string]any{"name": "SystemVssConnection", "url": server.URL + "/job/", "authorization": map[string]any{"parameters": map[string]string{"AccessToken": "job-token"}}}}}})
		case "/job/renewjob":
			n := renews.Add(1)
			if mode == "lease" && n > 1 {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"lockedUntil": time.Now().Add(500 * time.Millisecond)})
		case "/job/started":
			startedOnce.Do(func() { close(started) })
			w.WriteHeader(204)
		case "/job/completejob":
			completes.Add(1)
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			expected := 0
			if mode == "failure" || mode == "crash" || mode == "startfailure" {
				expected = 2
			}
			if mode == "cancel" || mode == "shutdown" || mode == "lease" || mode == "kill" || mode == "recover" {
				expected = 3
			}
			if body["conclusion"] != worker.Result(expected).String() {
				t.Errorf("wrong completion: %#v", body)
			}
			if mode == "crash" || mode == "kill" {
				if body["planId"] != "plan" || body["jobId"] != jobID || body["billingOwnerId"] != "owner" {
					t.Error("bad fallback completion")
				}
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "bin"), 0700)
	executable, _ := os.Executable()
	binaryData, _ := os.ReadFile(executable)
	os.WriteFile(filepath.Join(dir, "bin", workerBinary()), binaryData, 0700)
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("2.333.0\n"), 0600)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("WORKER_ENV=dotenv\nDOTENV_VALUE=present\n"), 0600)
	files := map[string]string{}
	encode := func(name string, object any) {
		data, _ := json.Marshal(object)
		files[name] = base64.StdEncoding.EncodeToString(data)
	}
	// GitHub's JIT endpoint serializes .runner values as strings, including
	// numbers and booleans, with the same property names as .NET RunnerSettings.
	encode(".runner", map[string]any{"AgentId": "1", "AgentName": "test", "Ephemeral": "True", "UseV2Flow": "True", "ServerUrlV2": server.URL + "/broker/", "WorkFolder": "_work", "PoolId": "1", "PoolName": nil, "DisableUpdate": "True"})
	encode(".credentials", map[string]any{"Scheme": "OAuth", "Data": map[string]string{"ClientId": "client", "AuthorizationUrl": server.URL + "/oauth"}})
	encode(".credentials_rsaparams", map[string][]byte{"modulus": key.N.Bytes(), "exponent": big.NewInt(int64(key.E)).Bytes(), "d": key.D.Bytes(), "p": key.Primes[0].Bytes(), "q": key.Primes[1].Bytes()})
	data, _ := json.Marshal(files)
	encoded := base64.StdEncoding.EncodeToString(data)
	registration, err := parseRunnerRegistration(encoded)
	if err != nil {
		t.Fatal(err)
	}
	localConfig := registration.workerConfig(dir, false)
	localConfig.Env = []string{"WORKER_ENV=override", "WORKER_TEST_MODE=" + mode}
	localConfig.Stdout, localConfig.Stderr = io.Discard, io.Discard
	local, err := worker.New(localConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	api, err := listener.NewClient(listener.ClientConfig{BrokerURL: registration.settings.ServerURLV2, Version: "2.333.0", Credentials: registration.credentials, PrivateKey: registration.key})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	network, err := listener.New(api, registration.listenerConfig("2.333.0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = network.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	runner := &testRunner{network: network, local: local, dir: dir, jit: encoded, ctx: ctx, started: started}
	return runner, &completes, &deletes, &renews, cancel
}

type testRunner struct {
	network *listener.Listener
	local   *worker.Worker
	dir     string
	jit     string
	ctx     context.Context
	started <-chan struct{} // Closed once Worker has received its job.
}

func TestControllerJobLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "failure", "crash", "cancel", "kill", "shutdown", "lease", "refresh", "retry", "recover"} {
		t.Run(mode, func(t *testing.T) {
			w, completed, deleted, renewed, cancel := controllerFixture(t, mode)
			ctx := w.ctx
			if mode == "shutdown" {
				go func() {
					select {
					case <-w.started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result, err := runJob(ctx, w.network, w.local, t.TempDir(), 150*time.Millisecond)
			switch mode {
			case "success", "refresh", "retry":
				if result != worker.Succeeded || err != nil {
					t.Fatalf("result %v, error %v", result, err)
				}
			case "failure":
				if result != worker.Failed || err != nil {
					t.Fatalf("workflow failure should be result: %v, %v", result, err)
				}
			case "cancel", "recover":
				if result != worker.Canceled || err != nil {
					t.Fatalf("cancel: %v, %v", result, err)
				}
			case "crash":
				if result != worker.Failed || err == nil {
					t.Fatalf("crash: %v, %v", result, err)
				}
			case "kill":
				if result != worker.Canceled || err == nil {
					t.Fatalf("kill: %v, %v", result, err)
				}
			case "lease":
				if err == nil {
					t.Fatal("lost lease not reported")
				}
			case "shutdown":
				if result != worker.Canceled || !errors.Is(err, context.Canceled) {
					t.Fatalf("shutdown: %v, %v", result, err)
				}
			}
			if completed.Load() != 1 || deleted.Load() != 1 {
				t.Errorf("complete=%d delete=%d", completed.Load(), deleted.Load())
			}
			if renewed.Load() < 1 {
				t.Error("missing initial renew")
			}
			if mode == "success" && renewed.Load() < 2 {
				t.Error("missing periodic renew")
			}
			for _, name := range []string{".runner", ".credentials", ".credentials_rsaparams"} {
				if _, e := os.Stat(filepath.Join(w.dir, name)); !errors.Is(e, os.ErrNotExist) {
					t.Errorf("credential remains: %s %v", name, e)
				}
			}
			if _, err = runJob(context.Background(), w.network, w.local, t.TempDir(), 150*time.Millisecond); err == nil {
				t.Error("ephemeral runner reused")
			}
		})
	}
}

func TestControllerRejectsUnsafeJIT(t *testing.T) {
	for _, name := range []string{"../escape", "bin/Runner.Worker", ".env"} {
		files, _ := json.Marshal(map[string]string{name: base64.StdEncoding.EncodeToString([]byte("x"))})
		if _, err := parseRunnerRegistration(base64.StdEncoding.EncodeToString(files)); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestExecuteJobRejectsUnsafeDiagnosticPaths(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../escape", `..\escape`, "C:escape", "/absolute"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			// No Worker or Listener is needed: rejection must precede startup.
			result, completed, err := executeJob(t.Context(), nil, nil, &listener.RunJob{JobID: id}, root, time.Second)
			if err == nil || completed || result != worker.Failed {
				t.Fatalf("unsafe ID accepted: %v, %v, %v", result, completed, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected job created diagnostics: %v, %v", entries, err)
			}
		})
	}
}

func TestServerRunRunnerUsesWorker(t *testing.T) {
	for _, mode := range []string{"success", "failure", "crash", "jiterror"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "jiterror" {
				fixtureMode = "success"
			}
			worker, completed, deleted, _, _ := controllerFixture(t, fixtureMode)
			env := "WORKER_ENV=override\nDOTENV_VALUE=present\nWORKER_TEST_MODE=" + fixtureMode + "\n"
			if err := os.WriteFile(filepath.Join(worker.dir, ".env"), []byte(env), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := &Config{Id: "host", DirBuilds: t.TempDir(), DirLogs: t.TempDir()}
			var api *httptest.Server
			api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v3/orgs/org/actions/runners/registration-token":
					w.WriteHeader(http.StatusCreated)
					io.WriteString(w, `{"token":"registration"}`)
				case "/api/v3/actions/runner-registration":
					claims, _ := json.Marshal(map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
					token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
					json.NewEncoder(w).Encode(map[string]string{"url": api.URL + "/", "token": token})
				case "/_apis/runtime/runnerscalesets/7/generatejitconfig":
					var settings map[string]any
					json.NewDecoder(r.Body).Decode(&settings)
					if settings["workFolder"] != "_work" || !strings.HasPrefix(fmt.Sprint(settings["name"]), "host-runner-") {
						t.Errorf("bad JIT settings: %v", settings)
					}
					if mode == "jiterror" {
						w.WriteHeader(403)
						return
					}
					json.NewEncoder(w).Encode(map[string]string{"encodedJITConfig": worker.jit})
				default:
					t.Errorf("unexpected scaleset endpoint: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer api.Close()
			client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{GitHubConfigURL: api.URL + "/org", PersonalAccessToken: "pat"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err = runRunner(ctx, client, 7, cfg, worker.dir, nil)
			if mode == "success" || mode == "failure" {
				if err != nil {
					t.Fatalf("workflow result treated as infrastructure error: %v", err)
				}
			} else if err == nil {
				t.Fatal("expected infrastructure error")
			}
			if mode != "jiterror" && (completed.Load() != 1 || deleted.Load() != 1) {
				t.Errorf("complete=%d delete=%d", completed.Load(), deleted.Load())
			}
			entries, e := os.ReadDir(cfg.DirBuilds)
			if mode == "jiterror" {
				if e != nil || len(entries) != 0 {
					t.Errorf("runner directory remains: %v %v", entries, e)
				}
			} else {
				if e != nil || len(entries) != 0 {
					t.Fatalf("runner directory remains: %v %v", entries, e)
				}
				logs, err := filepath.Glob(filepath.Join(cfg.DirLogs, "jobs", "*", "Worker.log"))
				if err != nil || len(logs) != 1 {
					t.Fatalf("job diagnostics not retained: %v %v", logs, err)
				}
			}
			if _, e = os.Stat(filepath.Join(worker.dir, "bin", workerBinary())); e != nil {
				t.Error("source installation removed")
			}
		})
	}
}

func TestServerRunRunnerReusesDirectory(t *testing.T) {
	// Each job needs its own fixture: every runner has its own credentials.
	runner, completed, _, _, _ := controllerFixture(t, "success")
	second, completedSecond, _, _, _ := controllerFixture(t, "success")
	jits := []string{runner.jit, second.jit}
	env := "WORKER_ENV=override\nDOTENV_VALUE=present\nWORKER_TEST_MODE=success\n"
	if err := os.WriteFile(filepath.Join(runner.dir, ".env"), []byte(env), 0600); err != nil {
		t.Fatal(err)
	}
	var names []string
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/orgs/org/actions/runners/registration-token":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"token":"registration"}`)
		case "/api/v3/actions/runner-registration":
			claims, _ := json.Marshal(map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
			token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
			json.NewEncoder(w).Encode(map[string]string{"url": api.URL + "/", "token": token})
		case "/_apis/runtime/runnerscalesets/7/generatejitconfig":
			var settings map[string]any
			json.NewDecoder(r.Body).Decode(&settings)
			names = append(names, fmt.Sprint(settings["name"]))
			json.NewEncoder(w).Encode(map[string]string{"encodedJITConfig": jits[len(names)-1]})
		default:
			t.Errorf("unexpected scaleset endpoint: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{GitHubConfigURL: api.URL + "/org", PersonalAccessToken: "pat"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &Config{Id: "host", DirBuilds: t.TempDir(), DirLogs: t.TempDir()}
	dirs := newDirPool(cfg.DirBuilds, "smoke")
	cached := filepath.Join(cfg.DirBuilds, "smoke-1", "_work", "cache")
	for job := range 2 {
		if err := runRunner(ctx, client, 7, cfg, runner.dir, dirs); err != nil {
			t.Fatalf("job %d: %v", job, err)
		}
		if job == 0 {
			// The fake Worker creates no workspace; stand in for a real job.
			if err := os.MkdirAll(filepath.Dir(cached), 0700); err != nil {
				t.Fatalf("directory of the first job was removed: %v", err)
			}
			if err := os.WriteFile(cached, []byte("warm"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if data, err := os.ReadFile(cached); err != nil || string(data) != "warm" {
		t.Errorf("workspace not reused: %q %v", data, err)
	}
	if completed.Load() != 1 || completedSecond.Load() != 1 {
		t.Errorf("complete=%d,%d, want both jobs completed", completed.Load(), completedSecond.Load())
	}
	if len(names) != 2 || names[0] == names[1] || !strings.HasPrefix(names[0], "host-smoke-1-") || !strings.HasPrefix(names[1], "host-smoke-1-") {
		t.Errorf("runner names %q: want distinct names of directory smoke-1", names)
	}
	logs, err := filepath.Glob(filepath.Join(cfg.DirLogs, "jobs", "*", "Worker.log"))
	if err != nil || len(logs) != 2 {
		t.Fatalf("reused slot did not retain separate job diagnostics: %v %v", logs, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.DirBuilds, "smoke-1", ".runner")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("runner settings remain: %v", err)
	}
}

func TestEnsureRunnerScaleSet(t *testing.T) {
	for _, test := range []struct {
		name                               string
		exists, conflict                   bool
		lookupErr, createErr, deleteErr    bool
		labels                             []string // Configured labels.
		current                            string   // Labels of the existing scale set.
		wantID                             int      // Scale set ID after the first call.
		wantLabels                         []string // Labels after the first call.
		wantGets, wantCreates, wantDeletes int32
	}{
		{name: "reuse existing", exists: true, wantGets: 2},
		{name: "create then restart", wantGets: 2, wantCreates: 1},
		{name: "concurrent creation", conflict: true, wantGets: 3, wantCreates: 1},
		{name: "lookup failure", lookupErr: true, wantGets: 1},
		{name: "creation failure", createErr: true, wantGets: 2, wantCreates: 1},
		{name: "labels changed", exists: true, labels: []string{"self-hosted", "linux"},
			wantID: 43, wantLabels: []string{"self-hosted", "linux"}, wantGets: 2, wantCreates: 1, wantDeletes: 1},
		// The service returns labels in its own case, such as Linux for linux.
		{name: "labels differ in case and order", exists: true, labels: []string{"linux", "self-hosted"},
			current: `[{"type":"system","name":"Self-Hosted"},{"type":"system","name":"Linux"}]`, wantGets: 2},
		{name: "default label", exists: true, labels: []string{}, wantGets: 2},
		{name: "default label restored", exists: true, labels: []string{},
			current: `[{"type":"system","name":"linux"}]`, wantID: 43, wantLabels: []string{"smoke"}, wantGets: 2, wantCreates: 1, wantDeletes: 1},
		{name: "deletion failure", exists: true, labels: []string{"linux"}, deleteErr: true, wantGets: 1, wantDeletes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.labels == nil {
				test.labels = []string{"smoke"}
			}
			if test.wantID == 0 {
				test.wantID = 42
			}
			// The fake mirrors the Actions service: labels are set only when a
			// scale set is created, and each creation assigns a new ID.
			var mu sync.Mutex
			exists, id, current := test.exists, 42, test.current
			if current == "" {
				current = `[{"type":"system","name":"smoke"}]`
			}
			if !exists {
				id = 42 - 1
			}
			var gets, creates, deletes atomic.Int32
			scaleSet := func() string {
				return fmt.Sprintf(`{"id":%d,"name":"smoke","runnerGroupId":3,"labels":%s}`, id, current)
			}
			var api *httptest.Server
			api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/v3/orgs/org/actions/runners/registration-token":
					w.WriteHeader(http.StatusCreated)
					io.WriteString(w, `{"token":"registration"}`)
				case r.URL.Path == "/api/v3/actions/runner-registration":
					claims, _ := json.Marshal(map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
					token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
					json.NewEncoder(w).Encode(map[string]string{"url": api.URL + "/", "token": token})
				case r.URL.Path == "/_apis/runtime/runnerscalesets" && r.Method == http.MethodGet:
					gets.Add(1)
					if r.URL.Query().Get("name") != "smoke" || r.URL.Query().Get("runnerGroupId") != "3" {
						t.Error("lookup did not use configured name and runner group")
					}
					if test.lookupErr {
						w.WriteHeader(http.StatusBadRequest)
						io.WriteString(w, `{"typeName":"LookupFailure"}`)
					} else if exists {
						io.WriteString(w, `{"count":1,"value":[`+scaleSet()+`]}`)
					} else {
						io.WriteString(w, `{"count":0,"value":[]}`)
					}
				case r.URL.Path == "/_apis/runtime/runnerscalesets" && r.Method == http.MethodPost:
					creates.Add(1)
					var request scaleset.RunnerScaleSet
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					want := test.labels
					if len(want) == 0 {
						want = []string{"smoke"}
					}
					if request.Name != "smoke" || request.RunnerGroupID != 3 || !request.RunnerSetting.DisableUpdate || !slices.Equal(labelNames(request.Labels), want) {
						t.Errorf("incorrect create request: %+v", request)
					}
					switch {
					case test.conflict:
						// Another host created it first.
						exists, id = true, id+1
						w.WriteHeader(http.StatusBadRequest)
						io.WriteString(w, `{"typeName":"GitHub.Actions.Runtime.WebApi.RunnerScaleSetExistsException"}`)
					case test.createErr:
						w.WriteHeader(http.StatusBadRequest)
						io.WriteString(w, `{"typeName":"CreateFailure"}`)
					default:
						data, _ := json.Marshal(request.Labels)
						exists, id, current = true, id+1, string(data)
						io.WriteString(w, scaleSet())
					}
				case r.URL.Path == fmt.Sprintf("/_apis/runtime/runnerscalesets/%d", id) && r.Method == http.MethodDelete:
					deletes.Add(1)
					if test.deleteErr {
						w.WriteHeader(http.StatusBadRequest)
						io.WriteString(w, `{"typeName":"DeleteFailure"}`)
						return
					}
					exists = false
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer api.Close()
			client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{GitHubConfigURL: api.URL + "/org", PersonalAccessToken: "pat"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			group := &Group{Name: "smoke", Labels: test.labels}
			for call := range 2 {
				scaleSet, err := ensureRunnerScaleSet(ctx, client, 3, group)
				if test.lookupErr || test.createErr || test.deleteErr {
					if err == nil {
						t.Fatal("API failure was ignored")
					}
					break
				}
				if err != nil || scaleSet == nil || scaleSet.ID != test.wantID {
					t.Fatalf("scale set: %+v, %v; want ID %d", scaleSet, err, test.wantID)
				}
				if call == 0 && test.wantLabels != nil && !slices.Equal(labelNames(scaleSet.Labels), test.wantLabels) {
					t.Errorf("labels after recreation = %q, want %q", labelNames(scaleSet.Labels), test.wantLabels)
				}
			}
			if gets.Load() != test.wantGets || creates.Load() != test.wantCreates || deletes.Load() != test.wantDeletes {
				t.Errorf("GET=%d POST=%d DELETE=%d; want GET=%d POST=%d DELETE=%d", gets.Load(), creates.Load(), deletes.Load(), test.wantGets, test.wantCreates, test.wantDeletes)
			}
		})
	}
}

func TestRunRetriesSessionWithoutInterruptingRunners(t *testing.T) {
	previousMin, previousMax := sessionRetryMin, sessionRetryMax
	sessionRetryMin, sessionRetryMax = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { sessionRetryMin, sessionRetryMax = previousMin, previousMax })

	runner, completed, _, _, _ := controllerFixture(t, "success")
	env := "WORKER_ENV=override\nDOTENV_VALUE=present\nWORKER_TEST_MODE=success\n"
	if err := os.WriteFile(filepath.Join(runner.dir, ".env"), []byte(env), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var sessions, sessionDeletes, polls, jits atomic.Int32
	var capacities sync.Map // Advertised X-ScaleSetMaxCapacity values.
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v3/orgs/org/actions/runners/registration-token":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"token":"registration"}`)
		case r.URL.Path == "/api/v3/actions/runner-registration":
			claims, _ := json.Marshal(map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
			token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
			json.NewEncoder(w).Encode(map[string]string{"url": api.URL + "/", "token": token})
		case r.URL.Path == "/_apis/runtime/runnerscalesets":
			io.WriteString(w, `{"count":1,"value":[{"id":42,"name":"smoke","runnerGroupId":1,"labels":[{"type":"System","name":"smoke"}]}]}`)
		case r.URL.Path == "/_apis/runtime/runnerscalesets/42/sessions" && r.Method == http.MethodPost:
			n := sessions.Add(1)
			if n == 1 {
				// A previous controller instance still owns the session.
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"typeName":"GitHub.DistributedTask.WebApi.TaskAgentSessionConflictException","message":"conflict"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"sessionId":               fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", n),
				"messageQueueUrl":         api.URL + "/queue",
				"messageQueueAccessToken": "queue-token",
				"statistics":              map[string]int{"totalAssignedJobs": 1},
			})
		case strings.HasPrefix(r.URL.Path, "/_apis/runtime/runnerscalesets/42/sessions/") && r.Method == http.MethodDelete:
			sessionDeletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/queue":
			capacities.Store(r.Header.Get(scaleset.HeaderScaleSetMaxCapacity), true)
			if polls.Add(1) == 1 {
				// Fail the session while the first runner executes its job.
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"typeName":"QueueFailure"}`)
				return
			}
			<-r.Context().Done()
		case r.URL.Path == "/_apis/runtime/runnerscalesets/42/generatejitconfig":
			jits.Add(1)
			json.NewEncoder(w).Encode(map[string]string{"encodedJITConfig": runner.jit})
		default:
			t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	// The poll blocked during the job must be restarted when its slot is
	// released; otherwise the third poll arrives only after ctx expires.
	go func() {
		for ctx.Err() == nil && (completed.Load() == 0 || polls.Load() < 3) {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	cfg := &Config{Id: "host", DirBuilds: t.TempDir(), DirLogs: t.TempDir(), DirRunner: runner.dir}
	group := &Group{Url: api.URL + "/org", Name: "smoke", Group: scaleset.DefaultRunnerGroup, Token: Secret{Src: "inline", Value: "pat"}}
	if err := run(ctx, cfg, group, newSlots(1)); err != nil {
		t.Fatalf("session failures must be retried: %v", err)
	}
	if completed.Load() != 1 {
		t.Errorf("job interrupted by session failure: complete=%d", completed.Load())
	}
	if jits.Load() != 1 {
		t.Errorf("runner started %d times for one assigned job", jits.Load())
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Error("slot release did not restart the long poll")
	}
	capacities.Range(func(value, _ any) bool {
		if value != "1" {
			t.Errorf("advertised capacity %q beyond the host's single slot", value)
		}
		return true
	})
	if sessions.Load() != 3 || sessionDeletes.Load() < 2 {
		t.Errorf("sessions created=%d deleted=%d; want 3 created, failed session deleted", sessions.Load(), sessionDeletes.Load())
	}
}

func workerBinary() string {
	if runtime.GOOS == "windows" {
		return "Runner.Worker.exe"
	}
	return "Runner.Worker"
}

func testRunnerOS() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	default:
		return "Linux"
	}
}
