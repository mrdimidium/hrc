#!/bin/bash
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -euo pipefail

bazel build //:archives --jobs=2
uv run --no-project --python 3.13 python tools/release.py stage
