// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package listener implements runner v2 Broker and Run Service communication.
// It acquires and supervises one job without controlling its execution.
package listener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type EventType int

const (
	JobCancellation EventType = iota
	RunnerShutdown
	LeaseLost
	NetworkError
)

// Event describes a server request or a network supervision failure.
// Timeout is the server's cancellation timeout, without local grace adjustments.
type Event struct {
	Type    EventType
	Timeout *time.Duration
	Err     error
}

var (
	ErrClosed         = errors.New("listener is closed")
	ErrAlreadyStarted = errors.New("listener already waited for a job")
	ErrShutdown       = errors.New("runner shutdown requested by server")
	ErrJobUnavailable = errors.New("acknowledged job is no longer available")
	ErrNoJob          = errors.New("listener has not acquired a job")
)

// Listener acquires and supervises one v2 job through the runner API. It never
// executes the job or decides how its consumer responds to server events.
// The caller owns api and must keep it alive until Close and other calls finish.
type Listener struct {
	api    *Client
	config Config
	mu     sync.Mutex

	started bool
	closed  bool
	cancel  context.CancelFunc
	session string
	job     *RunJob
	events  chan Event
	done    chan struct{}

	closeOnce sync.Once
	closeErr  error
}

func New(api *Client, config Config) (*Listener, error) {
	if api == nil {
		return nil, errors.New("runner API is required")
	}
	if !validURL(api.brokerURL) {
		return nil, errors.New("Broker URL is required")
	}
	if config.ID == 0 || config.Name == "" || config.Version == "" {
		return nil, errors.New("runner ID, name and version are required")
	}
	if config.OwnerName == "" {
		host, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		config.OwnerName = fmt.Sprintf("%s (PID: %d)", host, os.Getpid())
	}
	return &Listener{api: api, config: config, events: make(chan Event), done: make(chan struct{})}, nil
}

// WaitJob creates a session, acquires a job and obtains its initial lease.
// The context limits acquisition only. After success, polling and renewal
// continue until Close, including while the caller handles cancellation.
// A job returned together with an error has been acquired and may need explicit
// CompleteJob. Treat the returned job as immutable while Listener is active.
func (l *Listener) WaitJob(ctx context.Context) (job *RunJob, err error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, ErrClosed
	}
	if l.started {
		l.mu.Unlock()
		return nil, ErrAlreadyStarted
	}
	acquire, cancel := context.WithCancel(ctx)
	l.started, l.cancel = true, cancel
	l.mu.Unlock()
	watching := false
	defer func() {
		cancel()
		if !watching {
			l.end()
		}
	}()
	l.session, err = l.createSession(acquire)
	if err != nil {
		return nil, fmt.Errorf("create Broker session: %w", err)
	}
	job, err = l.acquire(acquire)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.job = job
	l.mu.Unlock()
	lease, err := l.initialRenew(acquire, job)
	if err != nil {
		return job, fmt.Errorf("initial job renew: %w", err)
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return job, ErrClosed
	}
	background, stop := context.WithCancel(context.WithoutCancel(ctx))
	l.cancel = stop
	l.mu.Unlock()
	watching = true
	l.watch(background, stop, job, lease)
	return job, nil
}

func (l *Listener) acquire(ctx context.Context) (*RunJob, error) {
	for {
		message, err := l.receive(ctx, "Online")
		if err != nil {
			return nil, fmt.Errorf("receive job: %w", err)
		}
		switch strings.ToLower(message.MessageType) {
		case "runnerjobrequest":
			var ref BrokerJobReference
			if json.Unmarshal([]byte(message.Body), &ref) != nil || ref.RunnerRequestID == "" || !validURL(ref.RunServiceURL) {
				return nil, errors.New("invalid v2 job reference")
			}
			if ref.ShouldAcknowledge {
				err = l.api.Acknowledge(ctx, l.session, ref.RunnerRequestID, l.messageOptions("Online"))
				var api *RequestError
				if errors.As(err, &api) && api.Kind == "AcknowledgeJobNotFound" {
					return nil, ErrJobUnavailable
				}
				if err != nil {
					slog.Warn("Runner acknowledge failed", "err", err)
				}
			}
			job, err := l.api.AcquireJob(ctx, ref.RunServiceURL, &AcquireJobRequest{JobMessageID: ref.RunnerRequestID, RunnerOS: runnerOS(), BillingOwnerID: ref.BillingOwnerID})
			if statusIs(err, 404, 409, 422) {
				if err := wait(ctx, time.Second); err != nil {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("acquire job: %w", err)
			}
			return job, nil
		case "forcetokenrefresh":
			l.api.RefreshRunnerToken()
		case "runnershutdown":
			return nil, ErrShutdown
		case "jobcancellation", "runnerrefresh", "agentrefresh":
			// There is no active job yet; updating an installation is external.
		default:
			return nil, fmt.Errorf("unsupported v2 message type %q", message.MessageType)
		}
	}
}

func (l *Listener) watch(ctx context.Context, cancel context.CancelFunc, job *RunJob, lease time.Time) {
	var wg sync.WaitGroup
	wg.Go(func() { l.poll(ctx, job) })
	wg.Go(func() {
		if err := l.renewLoop(ctx, job, lease); err != nil && ctx.Err() == nil {
			l.emit(ctx, Event{Type: LeaseLost, Err: err})
		}
	})
	go func() {
		wg.Wait()
		cancel()
		l.end()
	}()
}

func (l *Listener) poll(ctx context.Context, job *RunJob) {
	for {
		message, err := l.receive(ctx, "Busy")
		if err != nil {
			if ctx.Err() == nil {
				l.emit(ctx, Event{Type: NetworkError, Err: fmt.Errorf("receive runner event: %w", err)})
			}
			return // Lease renewal continues independently until Close.
		}
		switch strings.ToLower(message.MessageType) {
		case "jobcancellation":
			var request struct {
				JobID   string `json:"jobId"`
				Timeout string `json:"timeout"`
			}
			if err = json.Unmarshal([]byte(message.Body), &request); err != nil || request.JobID == "" {
				err = errors.New("invalid job cancellation")
				break
			}
			if !strings.EqualFold(request.JobID, job.JobID) {
				continue
			}
			event := Event{Type: JobCancellation}
			if request.Timeout != "" {
				var timeout time.Duration
				timeout, err = runnerTimeSpan(request.Timeout)
				if err != nil {
					break
				}
				event.Timeout = &timeout
			}
			l.emit(ctx, event)
		case "runnershutdown":
			l.emit(ctx, Event{Type: RunnerShutdown})
		case "forcetokenrefresh":
			l.api.RefreshRunnerToken()
		case "runnerjobrequest", "runnerrefresh", "agentrefresh":
			// This listener owns its single job; installation updates are external.
		default:
			err = fmt.Errorf("unsupported active v2 message %q", message.MessageType)
		}
		if err != nil {
			l.emit(ctx, Event{Type: NetworkError, Err: err})
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (l *Listener) emit(ctx context.Context, event Event) {
	select {
	case l.events <- event:
	case <-ctx.Done():
	}
}

func (l *Listener) Events() <-chan Event  { return l.events }
func (l *Listener) Done() <-chan struct{} { return l.done }

// CompleteJob reports exactly the conclusion supplied by the caller. It does not
// infer execution outcomes or stop renewal. Call Close after completion settles.
func (l *Listener) CompleteJob(ctx context.Context, request *CompleteJobRequest) error {
	l.mu.Lock()
	job, closed := l.job, l.closed
	l.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if job == nil {
		return ErrNoJob
	}
	err := l.api.CompleteJob(ctx, job, request)
	if statusIs(err, 404, 409) {
		return nil
	}
	return err
}

// Close stops polling and renewal, joins all requests and deletes the session.
// It does not complete the job or close the caller-owned API client.
func (l *Listener) Close(ctx context.Context) error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		started, cancel := l.started, l.cancel
		l.mu.Unlock()
		if started {
			cancel()
		} else {
			l.end()
		}
		<-l.done
		if l.session != "" {
			err := l.api.DeleteSession(ctx, l.session)
			if err != nil && !statusIs(err, 401, 403, 404) {
				l.closeErr = fmt.Errorf("delete Broker session: %w", err)
			}
		}
	})
	return l.closeErr
}

func (l *Listener) end() { close(l.events); close(l.done) }

func (l *Listener) receive(ctx context.Context, status string) (*BrokerMessage, error) {
	for {
		message, err := l.api.GetMessage(ctx, l.session, l.messageOptions(status))
		var api *RequestError
		if errors.As(err, &api) && api.Kind == "RunnerSessionInvalid" && !l.config.SkipSessionRecover {
			var replacement string
			replacement, err = l.createSession(ctx)
			if err == nil {
				l.session = replacement
				continue
			}
		}
		if message != nil || err != nil {
			return message, err
		}
	}
}

func (l *Listener) messageOptions(status string) *BrokerMessageOptions {
	return &BrokerMessageOptions{Status: status, RunnerVersion: l.config.Version, OS: runnerOS(), Architecture: runnerArch(), DisableUpdate: l.config.DisableUpdate}
}

func (l *Listener) createSession(ctx context.Context) (string, error) {
	session, err := l.api.CreateSession(ctx, &BrokerSession{
		OwnerName: l.config.OwnerName,
		Agent: &BrokerAgent{ID: l.config.ID, Name: l.config.Name, Version: l.config.Version,
			OSDescription: runtime.GOOS + " " + runtime.GOARCH},
	})
	if err != nil {
		return "", err
	}
	return session.SessionID, nil
}

func (l *Listener) renew(ctx context.Context, job *RunJob) (time.Time, error) {
	response, err := l.api.RenewJob(ctx, job)
	if err != nil {
		return time.Time{}, err
	}
	return response.LockedUntil, nil
}

func (l *Listener) initialRenew(ctx context.Context, job *RunJob) (time.Time, error) {
	deadline, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for attempt := 0; ; attempt++ {
		lease, err := l.renew(deadline, job)
		if err == nil || !retryable(err) || attempt >= 5 {
			return lease, err
		}
		delay := 5 * time.Second
		var api *RequestError
		if errors.As(err, &api) {
			delay = max(delay, api.RetryAfter)
		}
		if err = wait(deadline, delay); err != nil {
			return time.Time{}, err
		}
	}
}

func (l *Listener) renewLoop(ctx context.Context, job *RunJob, lease time.Time) error {
	for {
		delay := min(time.Minute, time.Until(lease)/2)
		if delay <= 0 {
			return errors.New("job lease expired")
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
		for {
			attempt, cancel := context.WithDeadline(ctx, lease)
			next, err := l.renew(attempt, job)
			cancel()
			if err == nil {
				lease = next
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !retryable(err) || !lease.After(time.Now()) {
				return fmt.Errorf("job lease lost: %w", err)
			}
			delay = 5 * time.Second
			var api *RequestError
			if errors.As(err, &api) {
				delay = max(delay, api.RetryAfter)
			}
			if err = wait(ctx, min(delay, time.Until(lease))); err != nil {
				return err
			}
		}
	}
}

func runnerOS() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	default:
		return "Linux"
	}
}

func runnerArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "X64"
	case "arm64":
		return "ARM64"
	case "386":
		return "X86"
	default:
		return "ARM"
	}
}

func runnerTimeSpan(value string) (time.Duration, error) {
	// Newtonsoft's TimeSpan is [d.]hh:mm:ss[.fffffff], not a Go duration.
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, errors.New("invalid cancellation TimeSpan")
	}
	days, hours := int64(0), parts[0]
	if d, h, ok := strings.Cut(hours, "."); ok {
		var err error
		days, err = strconv.ParseInt(d, 10, 32)
		if err != nil {
			return 0, errors.New("invalid cancellation days")
		}
		hours = h
	}
	h, e1 := strconv.ParseInt(hours, 10, 32)
	m, e2 := strconv.ParseInt(parts[1], 10, 32)
	s, e3 := strconv.ParseFloat(parts[2], 64)
	if e1 != nil || e2 != nil || e3 != nil || days < 0 || days > 30 || h < 0 || h > 23 || m < 0 || m > 59 || !(s >= 0 && s < 60) {
		return 0, errors.New("invalid cancellation timeout")
	}
	return time.Duration(days*24+h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s*float64(time.Second)), nil
}
