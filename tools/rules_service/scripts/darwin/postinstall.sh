#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu
# @COMMON@

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi

if [ -f "$config" ]; then
    chown root:@GROUP@ "$config"
    chmod 0640 "$config"
fi

# Do not enable or start jobs before providers and secrets are configured.
plist=/Library/LaunchDaemons/org.@NAME@.plist
if [ -f "$plist" ]; then plutil -lint "$plist"; fi

echo "@NAME@: configure $config, then launchctl enable system/org.@NAME@"
echo '@NAME@: start with launchctl bootstrap system /Library/LaunchDaemons/org.@NAME@.plist'
