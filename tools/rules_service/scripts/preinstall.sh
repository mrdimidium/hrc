#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
umask 0077
. "$(dirname "$0")/common.sh"

require_root

# Stop an existing controller before replacing its binary. Preserve enablement.
select_init
case $os in
    Linux)
        if [ "$init" = systemd ]; then
            if [ -d /run/systemd/system ] && systemctl is-active --quiet @NAME@.service; then
                systemctl stop @NAME@.service
            fi
        elif [ -x /etc/init.d/@NAME@ ]; then
            if rc-service @NAME@ status >/dev/null 2>&1; then rc-service @NAME@ stop; fi
        fi
        ;;
    Darwin)
        if launchctl print system/org.@NAME@ >/dev/null 2>&1; then
            launchctl bootout system/org.@NAME@
        fi
        ;;
esac

case $os in
    Linux)
        nologin=/usr/sbin/nologin
        [ -x "$nologin" ] || nologin=/sbin/nologin
        [ -x "$nologin" ] || nologin=/bin/false
        if ! getent group @GROUP@ >/dev/null; then
            if command -v groupadd >/dev/null 2>&1; then
                groupadd --system @GROUP@
            else
                addgroup -S @GROUP@
            fi
        fi
        if ! getent passwd @USER@ >/dev/null; then
            if command -v useradd >/dev/null 2>&1; then
                useradd --system --gid @GROUP@ --home-dir "$state_dir" --no-create-home --shell "$nologin" @USER@
            else
                adduser -S -H -D -G @GROUP@ -h "$state_dir" -s "$nologin" @USER@
            fi
        fi
        ;;
    Darwin)
        # Allocate unused local system IDs; existing accounts are never renumbered.
        next_id() {
            entries=$(dscl . -list "$1" "$2") || return 1
            value=400
            while [ "$value" -lt 500 ]; do
                if ! printf '%s\n' "$entries" | awk -v value="$value" '$NF == value { found = 1 } END { exit !found }'; then
                    printf '%s\n' "$value"
                    return 0
                fi
                value=$((value + 1))
            done
            echo "@NAME@: no unused system IDs available" >&2
            return 1
        }
        if ! dscl . -read /Groups/@GROUP@ >/dev/null 2>&1; then
            gid=$(next_id /Groups PrimaryGroupID)
            dscl . -create /Groups/@GROUP@ PrimaryGroupID "$gid"
            dscl . -create /Groups/@GROUP@ RealName @SHELL_DESCRIPTION@
            dscl . -create /Groups/@GROUP@ Password '*'
        fi
        if ! dscl . -read /Users/@USER@ >/dev/null 2>&1; then
            group=$(dscl . -read /Groups/@GROUP@ PrimaryGroupID)
            gid=$(printf '%s\n' "$group" | awk '{ print $2 }')
            uid=$(next_id /Users UniqueID)
            dscl . -create /Users/@USER@ UniqueID "$uid"
            dscl . -create /Users/@USER@ PrimaryGroupID "$gid"
            dscl . -create /Users/@USER@ RealName @SHELL_DESCRIPTION@
            dscl . -create /Users/@USER@ NFSHomeDirectory "$state_dir"
            dscl . -create /Users/@USER@ UserShell /usr/bin/false
            dscl . -create /Users/@USER@ Password '*'
            dscl . -create /Users/@USER@ IsHidden 1
        fi
        ;;
esac

# Refuse privileged identities and a conflicting primary group.
if [ "$(id -u @USER@)" = 0 ] || [ "$(id -g @USER@)" = 0 ] || [ "$(id -gn @USER@)" != @GROUP@ ]; then
    echo '@NAME@: existing account must be unprivileged with primary group @GROUP@' >&2
    exit 1
fi

# Never change ownership recursively: build data and existing secrets stay intact.
prepare_dirs
