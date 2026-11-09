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

# Do not enable or start jobs before providers and secrets are configured.
plist=/Library/LaunchDaemons/io.dimidiumlabs.@NAME@.plist
if [ -f "$plist" ]; then plutil -lint -s "$plist"; fi
