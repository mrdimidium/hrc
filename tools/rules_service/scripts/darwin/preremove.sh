#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi

if launchctl print system/io.dimidiumlabs.@NAME@ >/dev/null 2>&1; then
    launchctl bootout system/io.dimidiumlabs.@NAME@
fi
launchctl disable system/io.dimidiumlabs.@NAME@

# Accounts, config, secrets, workspaces and logs are retained.
