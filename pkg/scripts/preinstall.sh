#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
umask 0077
. "$(dirname "$0")/common.sh"

# Hooks prepare accounts and directories; control installs the payload/config.
case ${1:-install} in
    install|upgrade) ;;
    abort-upgrade) exit 0 ;;
    *) echo "hrc: unsupported preinstall action: $1" >&2; exit 1 ;;
esac

hrc_require_root
os=$hrc_os
state_dir=$hrc_state_dir
if hrc_staging; then
    hrc_prepare_dirs
    exit 0
fi

# Stop an existing controller before replacing its binary. Preserve enablement.
hrc_select_init
case $os in
    Linux)
        if [ "$hrc_init" = systemd ]; then
            if [ -d /run/systemd/system ] && systemctl is-active --quiet hrc.service; then
                systemctl stop hrc.service
            fi
        elif [ -x /etc/init.d/hrc ]; then
            if rc-service hrc status >/dev/null 2>&1; then rc-service hrc stop; fi
        fi
        ;;
    Darwin)
        if launchctl print system/org.hrc >/dev/null 2>&1; then
            launchctl bootout system/org.hrc
        fi
        ;;
esac

case $os in
    Linux)
        nologin=/usr/sbin/nologin
        [ -x "$nologin" ] || nologin=/sbin/nologin
        [ -x "$nologin" ] || nologin=/bin/false
        if ! getent group hrc >/dev/null; then
            if command -v groupadd >/dev/null 2>&1; then
                groupadd --system hrc
            else
                addgroup -S hrc
            fi
        fi
        if ! getent passwd hrc >/dev/null; then
            if command -v useradd >/dev/null 2>&1; then
                useradd --system --gid hrc --home-dir "$state_dir" --no-create-home --shell "$nologin" hrc
            else
                adduser -S -H -D -G hrc -h "$state_dir" -s "$nologin" hrc
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
            echo "hrc: no unused system IDs available" >&2
            return 1
        }
        if ! dscl . -read /Groups/hrc >/dev/null 2>&1; then
            gid=$(next_id /Groups PrimaryGroupID)
            dscl . -create /Groups/hrc PrimaryGroupID "$gid"
            dscl . -create /Groups/hrc RealName 'Hosted Runner Controller'
            dscl . -create /Groups/hrc Password '*'
        fi
        if ! dscl . -read /Users/hrc >/dev/null 2>&1; then
            group=$(dscl . -read /Groups/hrc PrimaryGroupID)
            gid=$(printf '%s\n' "$group" | awk '{ print $2 }')
            uid=$(next_id /Users UniqueID)
            dscl . -create /Users/hrc UniqueID "$uid"
            dscl . -create /Users/hrc PrimaryGroupID "$gid"
            dscl . -create /Users/hrc RealName 'Hosted Runner Controller'
            dscl . -create /Users/hrc NFSHomeDirectory "$state_dir"
            dscl . -create /Users/hrc UserShell /usr/bin/false
            dscl . -create /Users/hrc Password '*'
            dscl . -create /Users/hrc IsHidden 1
        fi
        ;;
esac

# Refuse privileged identities and a conflicting primary group.
if [ "$(id -u hrc)" = 0 ] || [ "$(id -g hrc)" = 0 ] || [ "$(id -gn hrc)" != hrc ]; then
    echo 'hrc: existing account must be unprivileged with primary group hrc' >&2
    exit 1
fi

# Never change ownership recursively: build data and existing secrets stay intact.
hrc_prepare_dirs
