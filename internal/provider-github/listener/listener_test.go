// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type listenerFixture struct {
	listener *Listener
	input    chan BrokerMessage
	polls    chan string
	renewed  chan int32
	raw      json.RawMessage

	sessions   atomic.Int32
	renews     atomic.Int32
	completes  atomic.Int32
	deletes    atomic.Int32
	deletedID  atomic.Value
	recover    atomic.Bool
	renewError atomic.Int32
	pollError  atomic.Int32
	ackMissing atomic.Bool
}

func newListenerFixture(t *testing.T) *listenerFixture {
	t.Helper()
	f := &listenerFixture{input: make(chan BrokerMessage, 16), polls: make(chan string, 16), renewed: make(chan int32, 32)}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oauth" {
			io.WriteString(w, `{"access_token":"runner-token","expires_in":3600}`)
			return
		}
		wantAuth := "Bearer runner-token"
		if r.URL.Path == "/job/renewjob" || r.URL.Path == "/job/completejob" {
			wantAuth = "Bearer job-token"
		}
		if r.Header.Get("Authorization") != wantAuth {
			t.Errorf("%s authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/broker/session":
			if r.Method == http.MethodDelete {
				f.deletes.Add(1)
				f.deletedID.Store(r.URL.Query().Get("sessionId"))
				w.WriteHeader(http.StatusNoContent)
				return
			}
			fmt.Fprintf(w, `{"sessionId":"session-%d"}`, f.sessions.Add(1))
		case "/broker/message":
			status := r.URL.Query().Get("status")
			if status != "Online" && status != "Busy" {
				t.Errorf("invalid runner status: %q", status)
			}
			if r.URL.Query().Get("disableUpdate") != "true" {
				t.Error("lost configured runner capability")
			}
			select {
			case f.polls <- status:
			default:
			}
			if f.recover.Swap(false) {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"source":"actions-broker-listener","errorKind":"RunnerSessionInvalid"}`)
				return
			}
			if code := f.pollError.Load(); code != 0 && status == "Busy" {
				w.WriteHeader(int(code))
				return
			}
			select {
			case message := <-f.input:
				json.NewEncoder(w).Encode(message)
			case <-r.Context().Done():
			}
		case "/broker/acknowledge":
			if f.ackMissing.Load() {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"source":"actions-broker-listener","errorKind":"AcknowledgeJobNotFound"}`)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/run/acquirejob":
			w.Write(f.raw)
		case "/job/renewjob":
			n := f.renews.Add(1)
			select {
			case f.renewed <- n:
			default:
			}
			if code := f.renewError.Load(); code != 0 {
				w.WriteHeader(int(code))
				return
			}
			json.NewEncoder(w).Encode(RenewJobResponse{LockedUntil: time.Now().Add(400 * time.Millisecond)})
		case "/job/completejob":
			f.completes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	f.raw = json.RawMessage(fmt.Sprintf(`{"jobId":"job","messageType":"RunnerJobRequest","plan":{"planId":"plan"},"future":9007199254740993,"resources":{"endpoints":[{"name":"SystemVssConnection","url":%q,"authorization":{"parameters":{"AccessToken":"job-token"}}}]}}`, server.URL+"/job/"))
	api, err := NewClient(ClientConfig{BrokerURL: server.URL + "/broker/", Version: "2.test",
		PrivateKey: key, Credentials: Credentials{Scheme: "OAuth", Data: map[string]string{
			"clientId": "client", "authorizationUrl": server.URL + "/oauth",
		}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	f.listener, err = New(api, Config{ID: 7, Name: "runner", Version: "2.test", DisableUpdate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.listener.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	reference, _ := json.Marshal(BrokerJobReference{RunnerRequestID: "request", RunServiceURL: server.URL + "/run/", ShouldAcknowledge: true, BillingOwnerID: "owner"})
	f.input <- BrokerMessage{MessageType: "RunnerJobRequest", Body: string(reference)}
	return f
}

func nextListenerEvent(t *testing.T, l *Listener) Event {
	t.Helper()
	select {
	case event, ok := <-l.Events():
		if !ok {
			t.Fatal("listener closed before delivering the event")
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not deliver the event")
		return Event{}
	}
}

func (f *listenerFixture) waitRenew(t *testing.T, target int32) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case count := <-f.renewed:
			if count >= target {
				return
			}
		case <-deadline.C:
			t.Fatal("lease renewal stopped")
		}
	}
}

func TestListenerJobAndEvents(t *testing.T) {
	f := newListenerFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	job, err := f.listener.WaitJob(ctx)
	if err != nil || job == nil || !bytes.Equal(job.Raw, f.raw) || job.BillingOwnerID != "owner" {
		t.Fatalf("acquired job was changed: %+v, %v", job, err)
	}
	cancel() // Acquisition context does not terminate active supervision.
	f.input <- BrokerMessage{MessageType: "JobCancellation", Body: `{"jobId":"other","timeout":"00:00:01"}`}
	f.input <- BrokerMessage{MessageType: "JobCancellation", Body: `{"jobId":"job","timeout":"00:00:16"}`}
	event := nextListenerEvent(t, f.listener)
	if event.Type != JobCancellation || event.Timeout == nil || *event.Timeout != 16*time.Second || event.Err != nil {
		t.Fatalf("cancellation was changed or belongs to another job: %+v", event)
	}
	if f.completes.Load() != 0 {
		t.Fatal("listener decided to complete the job on cancellation")
	}
	if err := f.listener.CompleteJob(t.Context(), &CompleteJobRequest{Conclusion: "canceled"}); err != nil {
		t.Fatal(err)
	}
	f.waitRenew(t, f.renews.Load()+1)
	if _, err := f.listener.WaitJob(t.Context()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second WaitJob: %v", err)
	}
	if err := f.listener.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.completes.Load() != 1 || f.deletes.Load() != 1 {
		t.Fatalf("complete=%d delete=%d", f.completes.Load(), f.deletes.Load())
	}
}

func TestListenerRenewalSurvivesBlockedEventsAndPollFailure(t *testing.T) {
	for _, mode := range []string{"blocked_event", "poll_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newListenerFixture(t)
			if mode == "poll_failure" {
				f.pollError.Store(http.StatusForbidden)
			}
			if _, err := f.listener.WaitJob(t.Context()); err != nil {
				t.Fatal(err)
			}
			if mode == "blocked_event" {
				f.input <- BrokerMessage{MessageType: "JobCancellation", Body: `{"jobId":"job"}`}
			} else if event := nextListenerEvent(t, f.listener); event.Type != NetworkError || event.Err == nil {
				t.Fatalf("poll failure was not reported: %+v", event)
			}
			f.waitRenew(t, f.renews.Load()+1)
			if err := f.listener.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if f.completes.Load() != 0 {
				t.Fatal("listener inferred a job conclusion")
			}
		})
	}
}

func TestListenerLeaseFailures(t *testing.T) {
	for _, initial := range []bool{true, false} {
		t.Run(strconv.FormatBool(initial), func(t *testing.T) {
			f := newListenerFixture(t)
			if initial {
				f.renewError.Store(http.StatusNotFound)
			}
			job, err := f.listener.WaitJob(t.Context())
			if job == nil {
				t.Fatal("lost acquired job on lease failure")
			}
			if initial {
				if err == nil {
					t.Fatal("initial renewal failure not returned")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				f.renewError.Store(http.StatusNotFound)
				if event := nextListenerEvent(t, f.listener); event.Type != LeaseLost || event.Err == nil {
					t.Fatalf("lease loss not reported: %+v", event)
				}
			}
			if f.completes.Load() != 0 {
				t.Fatal("listener completed the job without instruction")
			}
			if err := f.listener.CompleteJob(t.Context(), &CompleteJobRequest{Conclusion: "failed"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestListenerSessionRecovery(t *testing.T) {
	f := newListenerFixture(t)
	f.recover.Store(true)
	if _, err := f.listener.WaitJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.sessions.Load() != 2 {
		t.Fatal("session was not recovered during acquisition")
	}
	f.recover.Store(true)
	f.input <- BrokerMessage{MessageType: "RunnerRefresh"}
	f.input <- BrokerMessage{MessageType: "RunnerShutdown"}
	if event := nextListenerEvent(t, f.listener); event.Type != RunnerShutdown {
		t.Fatalf("unexpected event after recovery: %+v", event)
	}
	if err := f.listener.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.sessions.Load() != 3 || f.deletedID.Load() != "session-3" {
		t.Fatalf("wrong active session: count=%d, deleted=%v", f.sessions.Load(), f.deletedID.Load())
	}
}

func TestListenerCloseDuringAcquisition(t *testing.T) {
	f := newListenerFixture(t)
	<-f.input // Wait for a job that never arrives.
	finished := make(chan error, 1)
	go func() { _, err := f.listener.WaitJob(t.Context()); finished <- err }()
	select {
	case <-f.polls:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not begin polling")
	}
	var closers sync.WaitGroup
	for range 4 {
		closers.Go(func() {
			if err := f.listener.Close(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	closers.Wait()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitJob was not interrupted: %v", err)
	}
	if f.deletes.Load() != 1 {
		t.Fatalf("session deleted %d times", f.deletes.Load())
	}
}

func TestListenerNoJobAndClosed(t *testing.T) {
	for _, mode := range []string{"shutdown", "missing_job", "closed"} {
		t.Run(mode, func(t *testing.T) {
			f := newListenerFixture(t)
			var want error
			switch mode {
			case "shutdown":
				<-f.input
				f.input <- BrokerMessage{MessageType: "RunnerShutdown"}
				want = ErrShutdown
			case "missing_job":
				f.ackMissing.Store(true)
				want = ErrJobUnavailable
			case "closed":
				if err := f.listener.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				want = ErrClosed
			}
			if job, err := f.listener.WaitJob(t.Context()); job != nil || !errors.Is(err, want) {
				t.Fatalf("WaitJob = %+v, %v; want %v", job, err, want)
			}
			if f.renews.Load() != 0 || f.completes.Load() != 0 {
				t.Fatal("listener supervised a job it never acquired")
			}
		})
	}
}

func TestRunnerTimeSpan(t *testing.T) {
	for value, want := range map[string]time.Duration{"00:00:00": 0, "00:05:30.125": 330125 * time.Millisecond, "1.02:03:04": 26*time.Hour + 3*time.Minute + 4*time.Second} {
		if got, err := runnerTimeSpan(value); err != nil || got != want {
			t.Errorf("%s: %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"garbage", "00:00:NaN", "00:00:60", "-1.00:00:00", "999999999.00:00:00"} {
		if _, err := runnerTimeSpan(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
