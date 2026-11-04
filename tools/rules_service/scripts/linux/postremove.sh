#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu
# @COMMON@
if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi
select_init

if [ "$init" = systemd ] && [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi
