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
	"strconv"
	"strings"
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
			Marker   string `json:"unknownFutureField"`
			Endpoint string `json:"testEndpoint"`
		}
		if json.Unmarshal(raw, &job) != nil || job.Marker != "Привет 🌍" {
			os.Exit(35)
		}
		if os.Getenv("WORKER_ENV") != "override" || os.Getenv("DOTENV_VALUE") != "present" {
			os.Exit(36)
		}
		mode := os.Getenv("WORKER_TEST_MODE")
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
	var polls, renews, completes, deletes, tokens, sessions, acquires atomic.Int32
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
				body := `{"jobId":"job","timeout":"00:00:16"}`
				if mode == "kill" {
					// Exercise the caller's short default grace when the server
					// supplies no timeout, without waiting 45 seconds in tests.
					body = `{"jobId":"job"}`
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
			json.NewEncoder(w).Encode(map[string]any{"jobId": "job", "messageType": "RunnerJobRequest", "plan": map[string]string{"planId": "plan"}, "unknownFutureField": "Привет 🌍", "testEndpoint": server.URL + "/job", "resources": map[string]any{"endpoints": []any{map[string]any{"name": "SystemVssConnection", "url": server.URL + "/job/", "authorization": map[string]any{"parameters": map[string]string{"AccessToken": "job-token"}}}}}})
		case "/job/renewjob":
			n := renews.Add(1)
			if mode == "lease" && n > 1 {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"lockedUntil": time.Now().Add(500 * time.Millisecond)})
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
				if body["planId"] != "plan" || body["jobId"] != "job" || body["billingOwnerId"] != "owner" {
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
	localConfig := registration.workerConfig(dir)
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
	runner := &testRunner{network: network, local: local, dir: dir, jit: encoded, ctx: ctx}
	return runner, &completes, &deletes, &renews, cancel
}

type testRunner struct {
	network *listener.Listener
	local   *worker.Worker
	dir     string
	jit     string
	ctx     context.Context
}

func TestControllerJobLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "failure", "crash", "cancel", "kill", "shutdown", "lease", "refresh", "retry", "recover"} {
		t.Run(mode, func(t *testing.T) {
			w, completed, deleted, renewed, cancel := controllerFixture(t, mode)
			ctx := w.ctx
			if mode == "shutdown" {
				time.AfterFunc(200*time.Millisecond, cancel)
			}
			result, err := runJob(ctx, w.network, w.local, 150*time.Millisecond)
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
			if _, err = runJob(context.Background(), w.network, w.local, 150*time.Millisecond); err == nil {
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
			cfg := &Config{Id: "host", DirBuilds: t.TempDir()}
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
			err = runRunner(ctx, client, 7, cfg, worker.dir)
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
			if e != nil || len(entries) != 0 {
				t.Errorf("runner directory remains: %v %v", entries, e)
			}
			if _, e = os.Stat(filepath.Join(worker.dir, "bin", workerBinary())); e != nil {
				t.Error("source installation removed")
			}
		})
	}
}

func TestEnsureRunnerScaleSet(t *testing.T) {
	for _, test := range []struct {
		name                  string
		exists, conflict      bool
		lookupErr, createErr  bool
		wantGets, wantCreates int32
	}{
		{name: "reuse existing", exists: true, wantGets: 2},
		{name: "create then restart", wantGets: 2, wantCreates: 1},
		{name: "concurrent creation", conflict: true, wantGets: 3, wantCreates: 1},
		{name: "lookup failure", lookupErr: true, wantGets: 1},
		{name: "creation failure", createErr: true, wantGets: 2, wantCreates: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var exists atomic.Bool
			exists.Store(test.exists)
			var gets, creates atomic.Int32
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
				case "/_apis/runtime/runnerscalesets":
					switch r.Method {
					case http.MethodGet:
						gets.Add(1)
						if r.URL.Query().Get("name") != "smoke" || r.URL.Query().Get("runnerGroupId") != "3" {
							t.Error("lookup did not use configured name and runner group")
						}
						if test.lookupErr {
							w.WriteHeader(http.StatusBadRequest)
							io.WriteString(w, `{"typeName":"LookupFailure"}`)
						} else if exists.Load() {
							io.WriteString(w, `{"count":1,"value":[{"id":42,"name":"smoke","runnerGroupId":3}]}`)
						} else {
							io.WriteString(w, `{"count":0,"value":[]}`)
						}
					case http.MethodPost:
						creates.Add(1)
						var request scaleset.RunnerScaleSet
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
						}
						if request.Name != "smoke" || request.RunnerGroupID != 3 || !request.RunnerSetting.DisableUpdate || len(request.Labels) != 1 || request.Labels[0].Name != "smoke" {
							t.Errorf("incorrect create request: %+v", request)
						}
						if test.conflict {
							exists.Store(true)
							w.WriteHeader(http.StatusBadRequest)
							io.WriteString(w, `{"typeName":"GitHub.Actions.Runtime.WebApi.RunnerScaleSetExistsException"}`)
						} else if test.createErr {
							w.WriteHeader(http.StatusBadRequest)
							io.WriteString(w, `{"typeName":"CreateFailure"}`)
						} else {
							exists.Store(true)
							io.WriteString(w, `{"id":42,"name":"smoke","runnerGroupId":3}`)
						}
					default:
						t.Errorf("unexpected method: %s", r.Method)
						w.WriteHeader(http.StatusMethodNotAllowed)
					}
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
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
			group := &Group{Name: "smoke", Labels: []string{"smoke"}}
			for range 2 {
				scaleSet, err := ensureRunnerScaleSet(ctx, client, 3, group)
				if test.lookupErr || test.createErr {
					if err == nil {
						t.Fatal("API failure was ignored")
					}
					break
				}
				if err != nil || scaleSet == nil || scaleSet.ID != 42 {
					t.Fatalf("scale set: %+v, %v", scaleSet, err)
				}
			}
			if gets.Load() != test.wantGets || creates.Load() != test.wantCreates {
				t.Errorf("GET=%d POST=%d; want GET=%d POST=%d", gets.Load(), creates.Load(), test.wantGets, test.wantCreates)
			}
		})
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
