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
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
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

	total := math.MaxInt32
	if cfg.Quota != (Resources{}) {
		host, err := hostResources()
		if err != nil {
			slog.Error("Couldn't detect host resources", "err", err)
			os.Exit(1)
		}
		if total, err = slotCount(host, cfg.Reserve, cfg.Quota); err != nil {
			slog.Error("Invalid quota", "err", err)
			os.Exit(1)
		}
		slog.Info("Host capacity", "cpu", host.CPU, "memory", host.Memory, "runners", total)
	}
	host := newSlots(total)

	// Service managers stop with SIGTERM; Windows delivers console close and
	// shutdown events as SIGTERM as well.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// A group fails only on local errors that a retry cannot fix. Stop the other
	// groups and exit non-zero, so the service manager restarts the controller.
	var failed atomic.Bool
	var wg sync.WaitGroup
	for _, group := range cfg.Groups {
		wg.Go(func() {
			if err := run(ctx, cfg, &group, host); err != nil {
				slog.Error("Group controller failed", "group", group.Name, "err", err)
				failed.Store(true)
				cancel()
			}
		})
	}
	wg.Wait()
	if failed.Load() {
		os.Exit(1)
	}
}
