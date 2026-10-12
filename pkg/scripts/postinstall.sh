#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
. "$(dirname "$0")/common.sh"

case ${1:-install} in
    install|configure|upgrade) ;;
    abort-upgrade) exit 0 ;;
    *) echo "hrc: unsupported postinstall action: $1" >&2; exit 1 ;;
esac

hrc_require_root
os=$hrc_os
config=$(hrc_path "$hrc_config")
if [ -f "$config" ]; then
    if ! hrc_staging; then chown root:hrc "$config"; fi
    chmod 0640 "$config"
fi
if hrc_staging; then exit 0; fi
hrc_select_init

# control owns service definitions and config placement. Do not start jobs or
# enable a fresh installation before the administrator configures its providers.
case $os in
    Linux)
        if [ "$hrc_init" = systemd ]; then
            if [ -d /run/systemd/system ]; then systemctl daemon-reload; fi
            echo 'hrc: configure /etc/hrc/hrc.zon, then systemctl enable --now hrc.service'
        else
            echo 'hrc: configure /etc/hrc/hrc.zon, then rc-update add hrc default and rc-service hrc start'
        fi
        ;;
    FreeBSD)
        echo 'hrc: configure /usr/local/etc/hrc/hrc.zon, then sysrc hrc_enable=YES and service hrc start'
        ;;
    Darwin)
        plist=/Library/LaunchDaemons/org.hrc.plist
        if [ -f "$plist" ]; then plutil -lint "$plist"; fi
        echo 'hrc: configure /usr/local/etc/hrc/hrc.zon, then launchctl enable system/org.hrc'
        echo 'hrc: start with launchctl bootstrap system /Library/LaunchDaemons/org.hrc.plist'
        ;;
    *) echo 'hrc: unsupported OS' >&2; exit 1 ;;
esac
