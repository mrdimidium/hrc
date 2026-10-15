#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
. "$(dirname "$0")/common.sh"

require_root
if [ -f "$config" ]; then
    chown root:@GROUP@ "$config"
    chmod 0640 "$config"
fi
select_init

# control owns service definitions and config placement. Do not start jobs or
# enable a fresh installation before the administrator configures its providers.
case $os in
    Linux)
        if [ "$init" = systemd ]; then
            if [ -d /run/systemd/system ]; then systemctl daemon-reload; fi
            echo '@NAME@: configure /etc/@NAME@/@NAME@.json, then systemctl enable --now @NAME@.service'
        else
            echo '@NAME@: configure /etc/@NAME@/@NAME@.json, then rc-update add @NAME@ default and rc-service @NAME@ start'
        fi
        ;;
    Darwin)
        plist=/Library/LaunchDaemons/org.@NAME@.plist
        if [ -f "$plist" ]; then plutil -lint "$plist"; fi
        echo "@NAME@: configure $config, then launchctl enable system/org.@NAME@"
        echo '@NAME@: start with launchctl bootstrap system /Library/LaunchDaemons/org.@NAME@.plist'
        ;;
esac
