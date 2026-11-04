#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu
umask 0077

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi

# Stop the old controller before replacing files, preserving enablement.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    if systemctl is-active --quiet @NAME@.service; then systemctl stop @NAME@.service; fi
elif command -v openrc-run >/dev/null 2>&1; then
    if [ -x /etc/init.d/@NAME@ ] && rc-service @NAME@ status >/dev/null 2>&1; then
        rc-service @NAME@ stop
    fi
else
    echo '@NAME@: unknown init system; skipping service integration' >&2
fi

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
        useradd --system --gid @GROUP@ --home-dir @SHELL_STATE_DIR@ --no-create-home --shell "$nologin" @USER@
    else
        adduser -S -H -D -G @GROUP@ -h @SHELL_STATE_DIR@ -s "$nologin" @USER@
    fi
fi

# Refuse privileged identities and a conflicting primary group.
if [ "$(id -u @USER@)" = 0 ] || [ "$(id -g @USER@)" = 0 ] || [ "$(id -gn @USER@)" != @GROUP@ ]; then
    echo '@NAME@: existing account must be unprivileged with primary group @GROUP@' >&2
    exit 1
fi

# Keep missing parents accessible despite the hook's private umask.
(umask 022 && mkdir -p "$(dirname @SHELL_CONFIG_DIR@)" "$(dirname @SHELL_STATE_DIR@)" "$(dirname @SHELL_LOG_DIR@)")

# Do not recursively change ownership of existing secrets or build data.
install -d -m 0750 -o root -g @GROUP@ @SHELL_CONFIG_DIR@
install -d -m 0750 -o root -g @GROUP@ @SHELL_CONFIG_DIR@/secrets
install -d -m 0700 -o @USER@ -g @GROUP@ @SHELL_STATE_DIR@
install -d -m 0700 -o @USER@ -g @GROUP@ @SHELL_LOG_DIR@
