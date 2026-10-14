// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/actions/scaleset"
)

// Version holds the build version injected by the linker
// -ldflags="-X main.Version=0.0.0"
var Version = "0.0.0-dev.0+unknown"

var (
	help    = flag.Bool("help", false, "Show this help")
	version = flag.Bool("version", false, "Show hrc version")
	config  = flag.String("config", "", "Path to the json config file")
)

func main() {
	flag.Parse()

	if *help {
		flag.Usage()
		return
	}

	if *version {
		fmt.Println(Version)
		return
	}

	if *config == "" {
		flag.Usage()
		os.Exit(1)
	}

	cfg, err := LoadConfig(*config)
	if err != nil {
		slog.Error("Could't read config", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var wg sync.WaitGroup
	for _, group := range cfg.Groups {
		wg.Go(func() {
			if err := run(ctx, cfg, &group); err != nil {
				slog.Error("Group listener failed", "err", err)
			}
		})
	}
	wg.Wait()
}

func run(ctx context.Context, cfg *Config, group *Group) error {
	logger := slog.Default().With("group", group.Name)
	logger.Info("Starting group listener")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	runnerDir := cfg.DirRunner
	if runnerDir == "" {
		version, err := os.ReadFile(filepath.Join(cfg.DirBin, "VERSION"))
		if err != nil {
			return fmt.Errorf("read runner version: %w", err)
		}
		runnerDir = filepath.Join(cfg.DirBin, "github-bundled-"+strings.TrimSpace(string(version)))
	}

	binary := "Runner.Listener"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if _, err := os.Stat(filepath.Join(runnerDir, "bin", binary)); err != nil {
		return fmt.Errorf("find runner binary: %w", err)
	}
	if err := os.MkdirAll(cfg.DirBuilds, 0o700); err != nil {
		return fmt.Errorf("create builds directory: %w", err)
	}

	info := scaleset.SystemInfo{System: "hrc", Version: Version}

	var err error
	var client *scaleset.Client
	if group.GitHubApp.Validate() == nil {
		client, err = scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
			GitHubConfigURL: group.Url, GitHubAppAuth: group.GitHubApp, SystemInfo: info,
		})
	} else {
		var token string
		if token, err = group.Token.Take(); err != nil {
			return fmt.Errorf("read token: %w", err)
		}
		client, err = scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
			GitHubConfigURL: group.Url, PersonalAccessToken: strings.TrimSpace(token), SystemInfo: info,
		})
	}
	if err != nil {
		return fmt.Errorf("create scaleset client: %w", err)
	}

	runnerGroupID := 1
	if group.Group != scaleset.DefaultRunnerGroup {
		runnerGroup, err := client.GetRunnerGroupByName(ctx, group.Group)
		if err != nil {
			return fmt.Errorf("get runner group: %w", err)
		}
		runnerGroupID = runnerGroup.ID
	}

	labels := make([]scaleset.Label, 0, len(group.Labels))
	for _, label := range group.Labels {
		labels = append(labels, scaleset.Label{Name: label})
	}

	scaleSet, err := client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name: group.Name, RunnerGroupID: runnerGroupID, Labels: labels,
		RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
	})
	if err != nil {
		return fmt.Errorf("create scale set: %w", err)
	}

	info.ScaleSetID = scaleSet.ID

	client.SetSystemInfo(info)

	session, err := client.MessageSessionClient(ctx, scaleSet.ID, cfg.Id)
	if err != nil {
		return fmt.Errorf("create message session: %w", err)
	}
	defer func() {
		if err := session.Close(context.WithoutCancel(ctx)); err != nil {
			logger.Error("Close message session", "err", err)
		}
	}()

	var wg sync.WaitGroup
	var running atomic.Int32
	defer func() {
		cancel()
		wg.Wait()
	}()

	statistics := session.Session().Statistics
	lastMessageID := 0
	for ctx.Err() == nil {
		if statistics != nil {
			missing := statistics.TotalAssignedJobs - int(running.Load())
			for range missing {
				if ctx.Err() != nil {
					break
				}
				running.Add(1)
				wg.Go(func() {
					defer running.Add(-1)
					if err := runRunner(ctx, client, scaleSet.ID, cfg, runnerDir); err != nil {
						logger.Error("Runner failed", "err", err)
					}
				})
			}
		}
	
		message, err := session.GetMessage(ctx, lastMessageID, math.MaxInt32)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("get scaleset message: %w", err)
		}
		if message == nil {
			continue
		}
		if err := session.DeleteMessage(ctx, message.MessageID); err != nil {
			return fmt.Errorf("delete scaleset message: %w", err)
		}
	
		lastMessageID = message.MessageID
		if len(message.JobAvailableMessages) > 0 {
			ids := make([]int64, 0, len(message.JobAvailableMessages))
			for _, job := range message.JobAvailableMessages {
				ids = append(ids, job.RunnerRequestID)
			}
			if _, err := session.AcquireJobs(ctx, ids); err != nil {
				return fmt.Errorf("acquire jobs: %w", err)
			}
		}
	
		statistics = message.Statistics
	}

	return nil
}

func runRunner(ctx context.Context, client *scaleset.Client, scaleSetID int, cfg *Config, runnerDir string) error {
	dir, err := os.MkdirTemp(cfg.DirBuilds, "runner-")
	if err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.Error("Remove runner directory", "dir", dir, "err", err)
		}
	}()

	if err := os.CopyFS(dir, os.DirFS(runnerDir)); err != nil {
		return fmt.Errorf("copy runner: %w", err)
	}

	jit, err := client.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
		Name: cfg.Id + "-" + filepath.Base(dir), WorkFolder: "_work",
	}, scaleSetID)
	if err != nil {
		return fmt.Errorf("generate runner config: %w", err)
	}

	binary := "Runner.Listener"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	binaryPath, err := filepath.Abs(filepath.Join(dir, "bin", binary))
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, binaryPath, "run", "--jitconfig", jit.EncodedJITConfig)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
