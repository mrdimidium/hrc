#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu
# @COMMON@

# On upgrade unload the old process, retaining administrator-selected enablement.
case ${1:-remove} in
    remove) remove=yes ;;
    upgrade) remove=no ;;
    *) echo "@NAME@: unsupported preremove action: $1" >&2; exit 1 ;;
esac

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi
if launchctl print system/org.@NAME@ >/dev/null 2>&1; then
    launchctl bootout system/org.@NAME@
fi
if [ "$remove" = yes ]; then launchctl disable system/org.@NAME@; fi

# Accounts, config, secrets, workspaces and logs are retained.
