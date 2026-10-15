# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Native build host names used by release packaging and the upstream runner."""

load("@platforms//host:constraints.bzl", "HOST_CONSTRAINTS")

_CPU = [constraint.split(":")[1] for constraint in HOST_CONSTRAINTS if "//cpu:" in constraint][0]
_SYSTEM = [constraint.split(":")[1] for constraint in HOST_CONSTRAINTS if "//os:" in constraint][0]

ARCH = {"aarch64": "arm64", "x86_64": "amd64"}[_CPU]
OS = {"linux": "linux", "osx": "darwin", "windows": "windows"}[_SYSTEM]
RID = {"darwin": "osx", "linux": "linux", "windows": "win"}[OS] + "-" + ("x64" if ARCH == "amd64" else "arm64")
