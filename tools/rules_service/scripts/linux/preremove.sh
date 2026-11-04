#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu
# @COMMON@

# On upgrade stop the old process, retaining administrator-selected enablement.
case ${1:-remove} in
    remove) remove=yes ;;
    upgrade) remove=no ;;
    *) echo "@NAME@: unsupported preremove action: $1" >&2; exit 1 ;;
esac

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi
select_init
if [ "$init" = systemd ]; then
    if [ -d /run/systemd/system ] && systemctl is-active --quiet @NAME@.service; then
        systemctl stop @NAME@.service
    fi
    if [ "$remove" = yes ] && [ -f /etc/systemd/system/@NAME@.service ]; then systemctl disable @NAME@.service; fi
elif [ "$init" = openrc ] && [ -x /etc/init.d/@NAME@ ]; then
    if rc-service @NAME@ status >/dev/null 2>&1; then rc-service @NAME@ stop; fi
    if [ "$remove" = yes ]; then
        # Remove only existing entries, across all selected runlevels.
        for entry in /etc/runlevels/*/@NAME@; do
            [ -e "$entry" ] || [ -L "$entry" ] || continue
            runlevel=${entry%/@NAME@}
            rc-update del @NAME@ "${runlevel##*/}"
        done
    fi
fi

# Accounts, config, secrets, workspaces and logs are retained.
