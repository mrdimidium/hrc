# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Supported release architectures and operating systems."""

ARCHES = ["amd64", "arm64"]
OSES = ["linux", "darwin", "windows"]
PLATFORMS = [(arch, os) for arch in ARCHES for os in OSES]
