#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    if systemctl is-active --quiet @NAME@.service; then systemctl stop @NAME@.service; fi
    if [ -f /etc/systemd/system/@NAME@.service ]; then systemctl disable @NAME@.service; fi
elif command -v openrc-run >/dev/null 2>&1; then
    if [ -x /etc/init.d/@NAME@ ]; then
        if rc-service @NAME@ status >/dev/null 2>&1; then rc-service @NAME@ stop; fi
        # Remove only existing entries, across all selected runlevels.
        for entry in /etc/runlevels/*/@NAME@; do
            [ -e "$entry" ] || [ -L "$entry" ] || continue
            runlevel=${entry%/@NAME@}
            rc-update del @NAME@ "${runlevel##*/}"
        done
    fi
else
    echo '@NAME@: unknown init system; skipping service integration' >&2
fi

# Accounts, config, secrets, workspaces and logs are retained.
