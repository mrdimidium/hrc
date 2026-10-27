#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Resolve the root VERSION and Git metadata into stable Bazel stamp values."""

from dataclasses import dataclass, replace
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int

    pre: str = ""
    build: str = ""

    @classmethod
    def parse(cls, value: str) -> "Version":
        match = re.fullmatch(
            r"(?P<major>0|[1-9][0-9]*)\."
            r"(?P<minor>0|[1-9][0-9]*)\."
            r"(?P<patch>0|[1-9][0-9]*)"
            r"(?:-(?P<pre>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
            r"(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?",
            value,
        )
        if match is None:
            raise ValueError("invalid semantic version: " + value)
        pre = match["pre"] or ""
        if any(part.isdigit() and len(part) > 1 and part.startswith("0") for part in pre.split(".")):
            raise ValueError("invalid semantic version: " + value)
        return cls(
            major=int(match["major"]),
            minor=int(match["minor"]),
            patch=int(match["patch"]),
            pre=pre,
            build=match["build"] or "",
        )

    def __str__(self) -> str:
        value = f"{self.major}.{self.minor}.{self.patch}"
        if self.pre:
            value += "-" + self.pre
        if self.build:
            value += "+" + self.build
        return value


def git(*args):
    try:
        result = subprocess.run(["git", *args], cwd=ROOT, capture_output=True, text=True)
    except OSError:
        return None
    return result.stdout.strip() if result.returncode == 0 else None

def resolve_mode(base: str) -> str:
    ref = os.environ.get("GITHUB_REF", "")
    if ref.startswith("refs/tags/release-"):
        if ref.removeprefix("refs/tags/release-") != base:
            raise ValueError(f"release tag {ref} does not match version {base}")
        return "release"

    return "dev" if ref == "refs/heads/main" else "snapshot"


def resolve_version(base: str, mode: str, commit: str) -> Version:
    version = Version.parse(base)
    if mode == "release":
        return version

    if mode not in ("dev", "snapshot"):
        raise ValueError("HRC_BUILD_MODE must be release, dev or snapshot")

    ref = (git("merge-base", "HEAD", "origin/main") or "HEAD") if mode == "snapshot" else "HEAD"
    tag = git("describe", "--tags", "--abbrev=0", "--match", "release-[0-9]*", ref)
    cnt = git("rev-list", "--count", "--first-parent", f"{tag}..{ref}" if tag else ref) or "65536"
    pre = f"dev.{int(cnt)}" + (".snapshot" if mode == "snapshot" else "")
    return replace(version, pre=pre, build=commit)


def main():
    base = os.environ.get("HRC_VERSION") or (ROOT / "VERSION").read_text().strip()
    mode = os.environ.get("HRC_BUILD_MODE") or resolve_mode(base)
    commit = os.environ.get("HRC_COMMIT") or git("rev-parse", "--short=12", "HEAD") or "unknown"

    print("STABLE_HRC_VERSION", resolve_version(base, mode, commit))


if __name__ == "__main__":
    try:
        main()
    except ValueError as error:
        print(error, file=sys.stderr)
        sys.exit(1)
