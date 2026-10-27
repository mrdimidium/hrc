// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Package worker executes one job using the .NET Runner.Worker process.
// It manages local configuration, environment, IPC and process lifetime. Job
// acquisition, registration and network supervision belong to the caller.
package worker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"gilti.dev/hrc/pkg/process"
)

// Result uses the upstream TaskResult values. Unknown means the process never
// returned a valid TaskResult. A failed workflow is a result, rather than a Go
// error. A known result can accompany an error, for example if cleanup fails.
type Result int

const Unknown Result = -1

const (
	Succeeded Result = iota
	SucceededWithIssues
	Failed
	Canceled
	Skipped
	Abandoned
)

func (r Result) String() string {
	names := [...]string{"succeeded", "succeededWithIssues", "failed", "canceled", "skipped", "abandoned"}
	if !r.Valid() {
		return "unknown"
	}
	return names[r]
}

// Valid reports whether the process returned an upstream TaskResult.
func (r Result) Valid() bool { return r >= Succeeded && r <= Abandoned }

// ShutdownReason distinguishes runner termination from an ordinary job cancel.
// Both shutdown reasons use .NET's runner shutdown path, which can skip steps
// whose conditions would otherwise allow them to run during job cancellation.
type ShutdownReason int

const (
	ShutdownRequested       ShutdownReason = iota // The runner service or its owner is stopping.
	ShutdownOperatingSystem                       // The operating system is shutting down.
)

var (
	ErrAlreadyStarted = errors.New("worker has already started a job")
	ErrNotStarted     = errors.New("worker has not started a job")
	ErrClosed         = errors.New("worker is closed")
)

type messageType int32

const (
	messageNewJob     messageType = 1
	messageCancel     messageType = 2
	messageShutdown   messageType = 3
	messageOSShutdown messageType = 4
)

// ExitError reports an exit outside the .NET TaskResult range (100 through 105).
type ExitError struct{ Code int }

func (e *ExitError) Error() string {
	return fmt.Sprintf("Worker exited with unexpected code %d", e.Code)
}

// Worker owns one process executing one job. StartJob is single-use. Commands,
// Wait, Done and Close are safe for concurrent use, including during startup.
// Use New for each job, including jobs belonging to a long-lived runner.
type Worker struct {
	config Config
	mu     sync.Mutex

	started        bool
	processStarted bool
	running        bool
	closed         bool

	startCancel context.CancelFunc
	process     *process.Process
	done        chan struct{}
	result      Result
	err         error
	waitErr     error
	cleanupErr  error

	input  *os.File
	pipes  []*os.File
	writes chan struct{}

	pipeErr, killErr, closeErr    error
	pipeOnce, killOnce, closeOnce sync.Once
}

// New validates the local configuration and copies its values. It creates no
// files or processes. Runtime settings are passed in RUNNER_CONFIG by StartJob.
func New(config Config) (*Worker, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}

	w := &Worker{
		config: config,
		done:   make(chan struct{}),
		writes: make(chan struct{}, 1),
		result: Unknown,
	}
	w.writes <- struct{}{}
	return w, nil
}

// StartJob starts Runner.Worker and sends the JSON payload without interpreting
// its job schema. The context limits startup and transmission, not job lifetime.
// Initial transmission is also bounded by .NET's 30-second startup timeout.
// Once startup begins, failures consume this Worker and release its resources.
func (w *Worker) StartJob(ctx context.Context, payload json.RawMessage) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if body := bytes.TrimSpace(payload); len(body) == 0 || body[0] != '{' || !json.Valid(body) {
		return errors.New("Worker job must be a JSON object")
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrClosed
	}
	if w.started {
		w.mu.Unlock()
		return ErrAlreadyStarted
	}
	start, cancel := context.WithTimeout(ctx, 30*time.Second)
	w.started, w.startCancel = true, cancel
	w.mu.Unlock()
	defer cancel()

	processStarted := false
	defer func() {
		if err != nil {
			if processStarted {
				err = errors.Join(err, w.kill())
				_, w.waitErr = w.process.Wait()
				err = errors.Join(err, w.waitErr)
			}
			err = w.finish(Unknown, err)
		}
	}()
	settings, err := w.config.renderRunner()
	if err != nil {
		return fmt.Errorf("render Worker settings: %w", err)
	}
	overrides := append([]string{}, w.config.Env...)
	overrides = append(overrides, "RUNNER_CONFIG="+string(settings))
	env, err := environment(w.config.Dir, overrides)
	if err != nil {
		return fmt.Errorf("prepare Worker environment: %w", err)
	}
	if err = start.Err(); err != nil {
		return err
	}
	if err = w.startProcess(env); err != nil {
		return fmt.Errorf("start Worker: %w", err)
	}
	processStarted = true
	w.mu.Lock()
	w.processStarted = true
	w.mu.Unlock()
	if err = w.writeMessage(start, messageNewJob, payload); err != nil {
		return fmt.Errorf("send Worker job: %w", err)
	}
	w.mu.Lock()
	w.running = true
	w.mu.Unlock()
	go func() {
		state, waitErr := w.process.Wait()
		w.waitErr = waitErr
		result, exitErr := Unknown, waitErr
		if state != nil {
			code := state.ExitCode()
			if code >= 100 && code <= 105 {
				result = Result(code - 100)
			} else if exitErr == nil {
				exitErr = &ExitError{Code: code}
			}
		}
		w.finish(result, exitErr)
	}()
	return nil
}

func (w *Worker) startProcess(env []string) error {
	read, write, err := process.Pipe()
	if err != nil {
		return err
	}
	w.pipes = append(w.pipes, read, write)
	backRead, backWrite, err := process.Pipe()
	if err != nil {
		return err
	}
	w.pipes = append(w.pipes, backRead, backWrite)
	w.input = write
	bin := filepath.Join(w.config.Dir, "bin")
	w.process, err = process.Start(process.Config{
		Path: filepath.Join(bin, binaryName()), Dir: bin, Args: []string{"spawnclient"},
		Env: env, Stdout: w.config.Stdout, Stderr: w.config.Stderr,
		Files: []*os.File{read, backWrite}, KeepDescendants: w.config.KeepDescendants,
	})
	if err != nil {
		return err
	}
	read.Close()
	backWrite.Close()
	return nil
}

// CancelJob sends ordinary job cancellation. It does not wait for completion;
// Worker may still execute steps whose conditions permit running after cancel.
// The caller controls the grace period using Done or Wait, followed by Close.
func (w *Worker) CancelJob(ctx context.Context) error { return w.send(ctx, messageCancel) }

// Shutdown sends runner shutdown with the given reason and does not wait for
// completion. Unlike CancelJob, this invokes the .NET host's shutdown path.
func (w *Worker) Shutdown(ctx context.Context, reason ShutdownReason) error {
	switch reason {
	case ShutdownRequested:
		return w.send(ctx, messageShutdown)
	case ShutdownOperatingSystem:
		return w.send(ctx, messageOSShutdown)
	default:
		return errors.New("invalid Worker shutdown reason")
	}
}

func (w *Worker) send(ctx context.Context, kind messageType) error {
	w.mu.Lock()
	closed, running := w.closed, w.running
	w.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if !running {
		return ErrNotStarted
	}
	return w.writeMessage(ctx, kind, nil)
}

// Done closes after startup fails or the process exits and configuration cleanup
// has finished. Close also closes Done if no job was ever started.
func (w *Worker) Done() <-chan struct{} { return w.done }

// Wait returns the job result and any runtime or cleanup error. It returns
// ErrNotStarted immediately if StartJob has not begun. Repeated and concurrent
// waits return the same outcome; workflow failure alone is not an error.
func (w *Worker) Wait() (Result, error) {
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if !started {
		return Unknown, ErrNotStarted
	}
	<-w.done
	return w.result, w.err
}

func (w *Worker) kill() error {
	w.killOnce.Do(func() {
		defer w.closePipes()
		select {
		case <-w.done:
			return
		default:
		}
		w.killErr = w.process.Kill()
	})
	return w.killErr
}

// Close forcibly terminates a still-running process tree, interrupts startup and
// IPC and waits for reaping. It is safe to call repeatedly and concurrently.
// Configuration files, workspaces, diagnostics and the installation belong to
// the caller.
func (w *Worker) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		started, processStarted, cancel := w.started, w.processStarted, w.startCancel
		w.mu.Unlock()
		if !started {
			w.finish(Unknown, nil)
		} else {
			cancel()
			if processStarted {
				w.kill()
			}
		}
		<-w.done
		w.closeErr = errors.Join(w.killErr, w.waitErr, w.cleanupErr)
	})
	return w.closeErr
}

func (w *Worker) finish(result Result, err error) error {
	// The worker can exit before its descendants. Clean up the process tree
	// before publishing Done, even when Close has not been called yet.
	var processErr error
	if w.process != nil {
		processErr = w.process.Close()
	}
	w.closePipes()
	w.cleanupErr = errors.Join(processErr, w.pipeErr)
	w.result, w.err = result, errors.Join(err, w.cleanupErr)
	close(w.done)
	return w.err
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "Runner.Worker.exe"
	}
	return "Runner.Worker"
}

func environment(dir string, overrides []string) ([]string, error) {
	env := make(map[string]string)
	key := func(k string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	put := func(entry string) {
		if k, _, ok := strings.Cut(entry, "="); ok && k != "" {
			env[key(k)] = entry
		}
	}
	for _, entry := range os.Environ() {
		put(entry)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range strings.Split(string(data), "\n") {
		put(strings.TrimSuffix(entry, "\r"))
	}
	if err := validateEnv(overrides); err != nil {
		return nil, err
	}
	for _, entry := range overrides {
		put(entry)
	}
	result := make([]string, 0, len(env))
	for _, entry := range env {
		result = append(result, entry)
	}
	return result, nil
}

// writeMessage serializes complete frames. Interrupting a write closes the pipe:
// a partially transmitted frame cannot safely be resumed.
func (w *Worker) writeMessage(ctx context.Context, kind messageType, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return os.ErrProcessDone
	case <-w.writes:
	}
	defer func() { w.writes <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-w.done:
		return os.ErrProcessDone
	default:
	}
	done := make(chan error, 1)
	go func() { done <- writeFrame(w.input, kind, body) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		w.input.Close()
		<-done
		return ctx.Err()
	}
}

// The frame is Int32(type), Int32(byte length), UTF-16LE JSON without a BOM.
// It is not a JSON stream and must not share stdout/stderr with diagnostics.
func writeFrame(dst io.Writer, kind messageType, body []byte) error {
	text := utf16.Encode([]rune(string(body)))
	if len(text) > 25_000_000 {
		return errors.New("Worker frame exceeds 50 MB")
	}
	data := make([]byte, 8+2*len(text))
	binary.LittleEndian.PutUint32(data, uint32(kind))
	binary.LittleEndian.PutUint32(data[4:], uint32(2*len(text)))
	for i, value := range text {
		binary.LittleEndian.PutUint16(data[8+i*2:], value)
	}
	_, err := io.Copy(dst, bytes.NewReader(data))
	return err
}

func (w *Worker) closePipes() {
	w.pipeOnce.Do(func() {
		for _, f := range w.pipes {
			if err := f.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				w.pipeErr = errors.Join(w.pipeErr, err)
			}
		}
	})
}
