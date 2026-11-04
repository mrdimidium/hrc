#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause
# Target: @ARCH@-@OS@

set -eu
# @COMMON@

if [ "$(id -u)" != 0 ]; then
    echo '@NAME@: run this command as root' >&2; exit 1
fi
if [ -f "$config" ]; then
    chown root:@GROUP@ "$config"
    chmod 0640 "$config"
fi
select_init

# The caller places service definitions and config. Do not enable or start jobs
# before the administrator configures providers and secrets.
if [ "$init" = systemd ]; then
    if [ -d /run/systemd/system ]; then systemctl daemon-reload; fi
    echo '@NAME@: configure /etc/@NAME@/@NAME@.json, then systemctl enable --now @NAME@.service'
elif [ "$init" = openrc ]; then
    echo '@NAME@: configure /etc/@NAME@/@NAME@.json, then rc-update add @NAME@ default and rc-service @NAME@ start'
fi
