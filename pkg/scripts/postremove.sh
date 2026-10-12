#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
. "$(dirname "$0")/common.sh"
hrc_require_root

if ! hrc_staging && [ "$hrc_os" = Linux ] && [ "${HRC_INIT:-}" = systemd ] && [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi
