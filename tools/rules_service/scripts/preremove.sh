#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
. "$(dirname "$0")/common.sh"

# On upgrade stop the old process, but retain the administrator's enablement.
case ${1:-remove} in
    remove) remove=yes ;;
    upgrade) remove=no ;;
    *) echo "@NAME@: unsupported preremove action: $1" >&2; exit 1 ;;
esac

require_root
select_init

case $os in
    Linux)
        if [ "$init" = systemd ]; then
            if [ -d /run/systemd/system ] && systemctl is-active --quiet @NAME@.service; then
                systemctl stop @NAME@.service
            fi
            if [ "$remove" = yes ] && [ -f "$service" ]; then systemctl disable @NAME@.service; fi
        elif [ -x /etc/init.d/@NAME@ ]; then
            if rc-service @NAME@ status >/dev/null 2>&1; then rc-service @NAME@ stop; fi
            if [ "$remove" = yes ]; then
                # A fresh installation is not enabled in any runlevel.
                # Remove only existing entries, across all administrator-selected runlevels.
                for entry in /etc/runlevels/*/@NAME@; do
                    [ -e "$entry" ] || [ -L "$entry" ] || continue
                    runlevel=${entry%/@NAME@}
                    rc-update del @NAME@ "${runlevel##*/}"
                done
            fi
        fi
        ;;
    Darwin)
        if launchctl print system/org.@NAME@ >/dev/null 2>&1; then launchctl bootout system/org.@NAME@; fi
        if [ "$remove" = yes ]; then launchctl disable system/org.@NAME@; fi
        ;;
esac

# Accounts, config, tokens, writable runner copies, workspaces and logs are retained.
