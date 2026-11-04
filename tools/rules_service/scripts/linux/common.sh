#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

# Inlined into each Linux package hook at build time.
config_dir=@SHELL_CONFIG_DIR@
state_dir=@SHELL_STATE_DIR@
log_dir=@SHELL_LOG_DIR@
config=@SHELL_CONFIG@

select_init() {
    init=${SERVICE_INIT:-}
    if [ -z "$init" ]; then
        if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
            init=systemd
        elif command -v openrc-run >/dev/null 2>&1; then
            init=openrc
        fi
    fi
    case $init in
        systemd|openrc) ;;
        *)
            echo "@NAME@: unknown init system ${init:-unknown}; skipping service integration" >&2
            init=none
            ;;
    esac
}
