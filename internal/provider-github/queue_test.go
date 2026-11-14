// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/scaleset"
)

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
			group := &Config{Name: "smoke", Labels: test.labels}
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
