// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package worker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

func TestMain(m *testing.M) {
	if os.Getenv("HRC_WORKER_PROCESS_TEST") == "1" && len(os.Args) == 4 && os.Args[1] == "spawnclient" {
		os.Exit(workerProcessTestChild())
	}
	os.Exit(m.Run())
}

// Decode independently of the production encoder in a real child process.
func workerProcessTestChild() int {
	fd, err := strconv.ParseUint(os.Args[2], 10, 64)
	if err != nil {
		return 20
	}
	input := os.NewFile(uintptr(fd), "worker-test-input")
	defer input.Close()
	// macOS may resolve /var to /private/var; compare directories, not paths.
	cwd, err := os.Stat(".")
	want, wantErr := os.Stat(filepath.Join(os.Getenv("HRC_WORKER_TEST_DIR"), "bin"))
	if err != nil || wantErr != nil || !os.SameFile(cwd, want) {
		return 21
	}
	fmt.Fprintln(os.Stdout, "ready")
	read := func() (int32, []byte, error) {
		var header [8]byte
		if _, err := io.ReadFull(input, header[:]); err != nil {
			return 0, nil, err
		}
		size := binary.LittleEndian.Uint32(header[4:])
		if size > 50_000_000 || size%2 != 0 {
			return 0, nil, errors.New("invalid frame size")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(input, data); err != nil {
			return 0, nil, err
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[2*i:])
		}
		return int32(binary.LittleEndian.Uint32(header[:])), []byte(string(utf16.Decode(units))), nil
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "blocked" {
		time.Sleep(time.Hour)
		return 22
	}
	if expected := os.Getenv("HRC_WORKER_TEST_EXPECT_ENV"); expected != "" &&
		(os.Getenv("HRC_WORKER_TEST_LAYER") != expected || os.Getenv("HRC_WORKER_TEST_KEEP") != "inherited") {
		return 29
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "payload" {
		kind, body, err := read()
		if err != nil || kind != 1 || string(body) != os.Getenv("HRC_WORKER_TEST_PAYLOAD") {
			return 30
		}
		code, _ := strconv.Atoi(os.Getenv("HRC_WORKER_TEST_EXIT"))
		return code
	}
	count, _ := strconv.Atoi(os.Getenv("HRC_WORKER_TEST_COUNT"))
	seen := map[int]bool{}
	for range count {
		kind, body, err := read()
		if err != nil || kind != 1 {
			return 23
		}
		var message struct {
			Index int
			Text  string
			Data  string
		}
		if json.Unmarshal(body, &message) != nil || message.Text != "Привет 🌍" || len(message.Data) != 20_000 || seen[message.Index] {
			return 24
		}
		seen[message.Index] = true
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "cancel" {
		kind, body, err := read()
		if err != nil || kind < 2 || kind > 4 || len(body) != 0 {
			return 25
		}
		fmt.Fprintln(os.Stderr, "cancel", kind)
		return 103
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "controls" {
		count, _ := strconv.Atoi(os.Getenv("HRC_WORKER_TEST_COMMAND_COUNT"))
		for range count {
			kind, body, err := read()
			if err != nil || kind < 2 || kind > 4 || len(body) != 0 {
				return 31
			}
		}
		return 103
	}
	if os.Getenv("HRC_WORKER_TEST_MODE") == "ignore" {
		time.Sleep(time.Hour)
	}
	return 100
}

type workerProcessTestOutput struct {
	mu    sync.Mutex
	data  bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *workerProcessTestOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.data.Write(data)
	if strings.Contains(w.data.String(), "ready\n") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *workerProcessTestOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String()
}

func workerTestConfig(t *testing.T, mode string, count int) (Config, *workerProcessTestOutput) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bin", binaryName()), data, 0700); err != nil {
		t.Fatal(err)
	}
	output := &workerProcessTestOutput{ready: make(chan struct{})}
	env := []string{"HRC_WORKER_PROCESS_TEST=1", "HRC_WORKER_TEST_DIR=" + dir, "HRC_WORKER_TEST_MODE=" + mode, "HRC_WORKER_TEST_COUNT=" + strconv.Itoa(count)}
	return Config{Dir: dir, Env: env, Stdout: output, Stderr: output}, output
}

func workerProcessTestJob(index int) []byte {
	data, _ := json.Marshal(map[string]any{"index": index, "text": "Привет 🌍", "data": strings.Repeat("x", 20_000)})
	return data
}

func newTestWorker(t *testing.T, config Config) *Worker {
	t.Helper()
	w, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w
}

func waitTestWorker(t *testing.T, w *Worker) (Result, error) {
	t.Helper()
	select {
	case <-w.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("Worker did not finish")
	}
	return w.Wait()
}

func TestWorkerPayloadAndResults(t *testing.T) {
	// Preserve formatting, unknown fields, Unicode and integers beyond float64.
	payload := json.RawMessage(" {\n\"unknown\":9007199254740993, \"text\":\"Привет 🌍\" } ")
	for _, code := range []int{100, 101, 102, 103, 104, 105, 0, 17, 106} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			config, _ := workerTestConfig(t, "payload", 0)
			config.Env = append(config.Env, "HRC_WORKER_TEST_PAYLOAD="+string(payload), "HRC_WORKER_TEST_EXIT="+strconv.Itoa(code))
			w := newTestWorker(t, config)
			if err := w.StartJob(t.Context(), payload); err != nil {
				t.Fatal(err)
			}
			result, err := waitTestWorker(t, w)
			if code >= 100 && code <= 105 {
				if err != nil || result != Result(code-100) {
					t.Fatalf("Wait = %v, %v; exit code %d", result, err, code)
				}
			} else {
				var exit *ExitError
				if !errors.As(err, &exit) || exit.Code != code || result != Unknown {
					t.Fatalf("Wait = %v, %v; expected infrastructure exit %d", result, err, code)
				}
			}
			if err := w.StartJob(t.Context(), payload); !errors.Is(err, ErrAlreadyStarted) {
				t.Fatalf("second StartJob: %v", err)
			}
		})
	}
}

func TestWorkerCommands(t *testing.T) {
	for _, test := range []struct {
		name string
		kind int
		send func(*Worker, context.Context) error
	}{
		{"cancel", 2, (*Worker).CancelJob},
		{"shutdown", 3, func(w *Worker, ctx context.Context) error { return w.Shutdown(ctx, ShutdownRequested) }},
		{"os_shutdown", 4, func(w *Worker, ctx context.Context) error { return w.Shutdown(ctx, ShutdownOperatingSystem) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, output := workerTestConfig(t, "cancel", 1)
			w := newTestWorker(t, config)
			ctx, cancel := context.WithCancel(t.Context())
			if err := w.StartJob(ctx, workerProcessTestJob(0)); err != nil {
				t.Fatal(err)
			}
			cancel() // Startup context does not own the running job.
			if err := w.CancelJob(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled command: %v", err)
			}
			if err := w.StartJob(t.Context(), workerProcessTestJob(1)); !errors.Is(err, ErrAlreadyStarted) {
				t.Fatalf("second StartJob while running: %v", err)
			}
			if err := test.send(w, t.Context()); err != nil {
				t.Fatal(err)
			}
			var waiters sync.WaitGroup
			for range 4 {
				waiters.Go(func() {
					if result, err := waitTestWorker(t, w); err != nil || result != Canceled {
						t.Errorf("Wait = %v, %v", result, err)
					}
				})
			}
			waiters.Wait()
			if !strings.Contains(output.String(), "cancel "+strconv.Itoa(test.kind)) {
				t.Fatalf("incorrect command: %s", output.String())
			}
		})
	}
}

func TestWorkerConcurrentCommandsAndWait(t *testing.T) {
	const count = 8
	config, _ := workerTestConfig(t, "controls", 1)
	config.Env = append(config.Env, "HRC_WORKER_TEST_COMMAND_COUNT="+strconv.Itoa(count))
	w := newTestWorker(t, config)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := w.StartJob(ctx, workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	var commands sync.WaitGroup
	for range count {
		commands.Go(func() {
			if err := w.CancelJob(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	commands.Wait()
	var waiters sync.WaitGroup
	for range 4 {
		waiters.Go(func() {
			if result, err := waitTestWorker(t, w); result != Canceled || err != nil {
				t.Errorf("Wait = %v, %v", result, err)
			}
		})
	}
	waiters.Wait()
	if err := w.CancelJob(ctx); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("CancelJob after exit: %v", err)
	}
}

func TestWorkerFilesAndEnvironment(t *testing.T) {
	t.Setenv("HRC_WORKER_TEST_LAYER", "inherited")
	t.Setenv("HRC_WORKER_TEST_KEEP", "inherited")
	config, _ := workerTestConfig(t, "cancel", 1)
	if err := os.WriteFile(filepath.Join(config.Dir, ".env"), []byte("HRC_WORKER_TEST_LAYER=dotenv\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config.Env = append(config.Env, "HRC_WORKER_TEST_EXPECT_ENV=override", "HRC_WORKER_TEST_LAYER=override")
	hosted := false
	config.Name, config.ID = "test-runner", 7
	config.GroupID, config.GroupName = 3, "test-group"
	config.GitHubURL, config.HostedServer = "https://github.example/org", &hosted
	config.ServerURL, config.ServerURLV2 = "https://pipelines.example/", "https://broker.example/"
	config.MonitorSocket, config.DisableUpdate = "monitor.sock", true
	w := newTestWorker(t, config)
	config.Name = "mutated"
	hosted = true
	config.Env[len(config.Env)-1] = "HRC_WORKER_TEST_LAYER=mutated"
	if err := w.StartJob(t.Context(), workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(config.Dir, ".runner"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(got, &settings); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"agentName": "test-runner", "agentId": float64(7), "workFolder": "_work",
		"poolId": float64(3), "poolName": "test-group",
		"gitHubUrl": "https://github.example/org", "IsHostedServer": false,
		"serverUrl": "https://pipelines.example/", "serverUrlV2": "https://broker.example/",
		"monitorSocketAddress": "monitor.sock", "disableUpdate": true,
	} {
		if settings[key] != want {
			t.Errorf("%s = %v, want %v", key, settings[key], want)
		}
	}
	for _, name := range []string{".credentials", ".credentials_rsaparams"} {
		if _, err := os.Stat(filepath.Join(config.Dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Worker materialized registration credentials %s: %v", name, err)
		}
	}
	if err := w.CancelJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if result, err := waitTestWorker(t, w); err != nil || result != Canceled {
		t.Fatalf("Wait = %v, %v (child also checks the merged environment)", result, err)
	}
	if _, err := os.Stat(filepath.Join(config.Dir, ".runner")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("owned runtime configuration survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.Dir, ".env")); err != nil {
		t.Fatalf("pre-existing environment file removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.Dir, "bin", binaryName())); err != nil {
		t.Fatalf("installation removed: %v", err)
	}
}

func TestWorkerExistingConfiguration(t *testing.T) {
	config, _ := workerTestConfig(t, "frames", 1)
	path := filepath.Join(config.Dir, ".runner")
	settings := []byte(`{"ephemeral":false}`)
	if err := os.WriteFile(path, settings, 0600); err != nil {
		t.Fatal(err)
	}
	w := newTestWorker(t, config)
	if err := w.StartJob(t.Context(), workerProcessTestJob(0)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing configuration was not rejected: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(settings) {
		t.Fatalf("existing registration changed: %q, %v", got, err)
	}
}

func TestWorkerResultSurvivesCleanupFailure(t *testing.T) {
	config, _ := workerTestConfig(t, "cancel", 1)
	w, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.StartJob(t.Context(), workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	// Make removal fail independently of the child's normal TaskResult.
	path := filepath.Join(config.Dir, ".runner")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "hold"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.CancelJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if result, err := waitTestWorker(t, w); result != Canceled || !result.Valid() || err == nil {
		t.Fatalf("lost normal TaskResult on cleanup failure: %v, %v", result, err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("Close lost the cleanup failure")
	}
}

func TestWorkerStartupFailureCleanup(t *testing.T) {
	for _, mode := range []string{"existing_file", "bad_environment", "bad_executable"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := workerTestConfig(t, "frames", 1)
			switch mode {
			case "existing_file":
				if err := os.WriteFile(filepath.Join(config.Dir, ".runner"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "bad_environment":
				if err := os.Mkdir(filepath.Join(config.Dir, ".env"), 0700); err != nil {
					t.Fatal(err)
				}
			case "bad_executable":
				if err := os.WriteFile(filepath.Join(config.Dir, "bin", binaryName()), []byte("not an executable"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			w := newTestWorker(t, config)
			if err := w.StartJob(t.Context(), workerProcessTestJob(0)); err == nil {
				t.Fatal("startup unexpectedly succeeded")
			}
			if _, err := waitTestWorker(t, w); err == nil {
				t.Fatal("startup failure lost by Wait")
			}
			settings, err := os.ReadFile(filepath.Join(config.Dir, ".runner"))
			if mode == "existing_file" {
				if err != nil || string(settings) != "preserve" {
					t.Fatalf("existing settings changed: %q, %v", settings, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("settings survived failed startup: %v", err)
			}
		})
	}
}

func TestWorkerCloseDuringStartup(t *testing.T) {
	config, output := workerTestConfig(t, "blocked", 0)
	w := newTestWorker(t, config)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := make(chan error, 1)
	payload := json.RawMessage(`{"data":"` + strings.Repeat("x", 1<<20) + `"}`)
	go func() { started <- w.StartJob(ctx, payload) }()
	select {
	case <-output.ready:
	case <-ctx.Done():
		t.Fatal("child never started")
	}
	var closers sync.WaitGroup
	for range 4 {
		closers.Go(func() {
			if err := w.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	closers.Wait()
	if err := <-started; err == nil {
		t.Fatal("blocked startup survived Close")
	}
	if _, err := waitTestWorker(t, w); err == nil {
		t.Fatal("interrupted startup reported success")
	}
	if _, err := os.Stat(filepath.Join(config.Dir, ".runner")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup files survived Close: %v", err)
	}
}

func TestWorkerStartupTimeout(t *testing.T) {
	config, _ := workerTestConfig(t, "blocked", 0)
	w := newTestWorker(t, config)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	payload := json.RawMessage(`{"data":"` + strings.Repeat("x", 1<<20) + `"}`)
	if err := w.StartJob(ctx, payload); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked StartJob: %v", err)
	}
	if result, err := waitTestWorker(t, w); result != Unknown || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait after startup timeout = %v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(config.Dir, ".runner")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup configuration survived timeout: %v", err)
	}
}

func TestWorkerCloseRunningJob(t *testing.T) {
	config, _ := workerTestConfig(t, "ignore", 1)
	w := newTestWorker(t, config)
	if err := w.StartJob(t.Context(), workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var exit *ExitError
	if _, err := waitTestWorker(t, w); !errors.As(err, &exit) {
		t.Fatalf("forced exit did not report an infrastructure failure: %v", err)
	}
}

func TestWorkerBeforeStart(t *testing.T) {
	config, _ := workerTestConfig(t, "frames", 1)
	w := newTestWorker(t, config)
	if _, err := w.Wait(); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Wait before startup: %v", err)
	}
	if err := w.CancelJob(t.Context()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("CancelJob before startup: %v", err)
	}
	if err := w.Shutdown(t.Context(), ShutdownRequested); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Shutdown before startup: %v", err)
	}
	if err := w.Shutdown(t.Context(), ShutdownReason(99)); err == nil {
		t.Fatal("accepted an invalid shutdown reason")
	}
	if err := w.StartJob(t.Context(), json.RawMessage(`[]`)); err == nil {
		t.Fatal("accepted a non-object job")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Done():
	default:
		t.Fatal("Close before StartJob did not close Done")
	}
	if err := w.StartJob(t.Context(), workerProcessTestJob(0)); !errors.Is(err, ErrClosed) {
		t.Fatalf("StartJob after Close: %v", err)
	}
}

func TestWorkerRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("accepted an empty runner directory")
	}
	if _, err := New(Config{Dir: t.TempDir()}); err == nil {
		t.Fatal("accepted an installation without Runner.Worker")
	}
	config, _ := workerTestConfig(t, "frames", 1)
	config.Env = []string{"NO_EQUALS"}
	if _, err := New(config); err == nil {
		t.Fatal("accepted an invalid environment override")
	}
}

func TestWorkerSymlinkDirectory(t *testing.T) {
	config, _ := workerTestConfig(t, "frames", 1)
	link := filepath.Join(t.TempDir(), "runner")
	if err := os.Symlink(config.Dir, link); err != nil {
		t.Skipf("cannot create a symlink: %v", err)
	}
	config.Dir = link
	// Force Getwd to resolve the physical path, as it does for /var on macOS.
	config.Env = append(config.Env, "HRC_WORKER_TEST_DIR="+link, "PWD=")
	w := newTestWorker(t, config)
	if err := w.StartJob(t.Context(), workerProcessTestJob(0)); err != nil {
		t.Fatal(err)
	}
	if result, err := waitTestWorker(t, w); result != Succeeded || err != nil {
		t.Fatalf("Wait with symlinked directory = %v, %v", result, err)
	}
}

func TestWriteFrame(t *testing.T) {
	var frame bytes.Buffer
	if err := writeFrame(&frame, messageNewJob, []byte(`{"text":"😀é"}`)); err != nil {
		t.Fatal(err)
	}
	// Little-endian type=1, byte length=28, UTF-16 including a surrogate pair.
	want, err := hex.DecodeString("010000001c0000007b002200740065007800740022003a0022003dd800dee90022007d00")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Bytes(), want) {
		t.Fatalf("frame = %x, want %x", frame.Bytes(), want)
	}
}
