# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Language-independent target selection shared by all build rules."""

OS = select({
    "@platforms//os:linux": "linux",
    "@platforms//os:osx": "darwin",
    "@platforms//os:windows": "windows",
}, no_match_error = "Supported target operating systems: linux, darwin, windows")

ARCH = select({
    "@platforms//cpu:aarch64": "arm64",
    "@platforms//cpu:x86_64": "amd64",
}, no_match_error = "Supported target architectures: amd64, arm64")
