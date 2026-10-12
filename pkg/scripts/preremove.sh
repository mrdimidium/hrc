#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
. "$(dirname "$0")/common.sh"

# On upgrade stop the old process, but retain the administrator's enablement.
case ${1:-remove} in
    remove|deinstall) remove=yes ;;
    upgrade) remove=no ;;
    failed-upgrade|abort-install|abort-upgrade) exit 0 ;;
    *) echo "hrc: unsupported preremove action: $1" >&2; exit 1 ;;
esac

hrc_require_root
hrc_select_init
if hrc_staging; then
    exit 0
fi

case $hrc_os in
    Linux)
        if [ "$hrc_init" = systemd ]; then
            if [ -d /run/systemd/system ] && systemctl is-active --quiet hrc.service; then
                systemctl stop hrc.service
            fi
            if [ "$remove" = yes ] && [ -f "$hrc_service" ]; then systemctl disable hrc.service; fi
        elif [ -x /etc/init.d/hrc ]; then
            if rc-service hrc status >/dev/null 2>&1; then rc-service hrc stop; fi
            if [ "$remove" = yes ]; then
                # A fresh installation is not enabled in any runlevel.
                # Remove only existing entries, across all administrator-selected runlevels.
                for entry in /etc/runlevels/*/hrc; do
                    [ -e "$entry" ] || [ -L "$entry" ] || continue
                    runlevel=${entry%/hrc}
                    rc-update del hrc "${runlevel##*/}"
                done
            fi
        fi
        ;;
    FreeBSD)
        if [ -x /usr/local/etc/rc.d/hrc ] && service hrc onestatus >/dev/null 2>&1; then
            service hrc onestop
        fi
        if [ "$remove" = yes ]; then sysrc -x hrc_enable || true; fi
        ;;
    Darwin)
        if launchctl print system/org.hrc >/dev/null 2>&1; then launchctl bootout system/org.hrc; fi
        if [ "$remove" = yes ]; then launchctl disable system/org.hrc; fi
        ;;
    *) echo 'hrc: unsupported OS' >&2; exit 1 ;;
esac

# Accounts, config, tokens, downloaded runners, workspaces and logs are retained.
