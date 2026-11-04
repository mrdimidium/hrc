#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi

if [ -f @SHELL_CONFIG@ ]; then
    chown root:@GROUP@ @SHELL_CONFIG@
    chmod 0640 @SHELL_CONFIG@
fi

# The caller places service definitions and config. Do not enable or start jobs
# before the administrator configures providers and secrets.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi
