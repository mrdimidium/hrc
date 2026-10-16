// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
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
				slog.Error("Group controller failed", "err", err)
			}
		})
	}
	wg.Wait()
}
