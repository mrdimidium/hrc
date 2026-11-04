#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

# Inlined into each macOS package hook at build time.
config=@SHELL_CONFIG@
config_dir=@SHELL_CONFIG_DIR@

log_dir=@SHELL_LOG_DIR@
state_dir=@SHELL_STATE_DIR@
