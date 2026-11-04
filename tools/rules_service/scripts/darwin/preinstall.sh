#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu
umask 0077
# @COMMON@

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi
if launchctl print system/org.@NAME@ >/dev/null 2>&1; then
    launchctl bootout system/org.@NAME@
fi

# Allocate unused local system IDs; never renumber existing accounts.
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

# Move the account's home out of the legacy /Library/Application Support path.
# dscl moves a value containing spaces to the next line.
home=$(dscl . -read /Users/@USER@ NFSHomeDirectory | sed '1s/^NFSHomeDirectory://' | tr -d '\n' | sed 's/^ *//')
if [ "$home" != "$state_dir" ]; then
    dscl . -create /Users/@USER@ NFSHomeDirectory "$state_dir"
    echo "@NAME@: home of @USER@ moved from $home to $state_dir; remove $home when no longer needed"
fi

# Refuse privileged identities and a conflicting primary group.
if [ "$(id -u @USER@)" = 0 ] || [ "$(id -g @USER@)" = 0 ] || [ "$(id -gn @USER@)" != @GROUP@ ]; then
    echo '@NAME@: existing account must be unprivileged with primary group @GROUP@' >&2
    exit 1
fi

# Keep missing parents accessible despite the hook's private umask.
(umask 022 && mkdir -p "$(dirname "$config_dir")" "$(dirname "$state_dir")" "$(dirname "$log_dir")")

# Do not recursively change ownership of existing secrets or build data.
install -d -m 0750 -o root -g @GROUP@ "$config_dir"
install -d -m 0750 -o root -g @GROUP@ "$config_dir/secrets"
install -d -m 0700 -o @USER@ -g @GROUP@ "$state_dir"
install -d -m 0700 -o @USER@ -g @GROUP@ "$log_dir"
