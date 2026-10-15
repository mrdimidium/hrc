#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
. "$(dirname "$0")/common.sh"
require_root
select_init

if [ "$os" = Linux ] && [ "$init" = systemd ] && [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi
