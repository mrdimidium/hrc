# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Stage native release archives or merge the three CI build artifacts."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import sys

DIRECTORY = Path("dist/release")


def checksum(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def stage():
    system = {"linux": "linux", "darwin": "darwin", "win32": "windows"}[sys.platform]
    status = dict(line.split(" ", 1) for line in Path("bazel-out/stable-status.txt").read_text().splitlines())
    version = status["STABLE_HRC_VERSION"]
    DIRECTORY.mkdir(parents=True, exist_ok=True)
    shutil.copyfile("bazel-bin/release_platforms.json", DIRECTORY / "platforms.json")
    platforms = json.loads((DIRECTORY / "platforms.json").read_text())
    names = []
    for arch, target_os in platforms:
        if target_os != system:
            continue
        name = f"hrc-{arch}-{system}-{version}.tar.gz"
        shutil.copyfile(Path("bazel-bin") / f"{arch}_{system}.tar.gz", DIRECTORY / name)
        names.append(name)
    (DIRECTORY / "VERSION").write_text(version + "\n")
    manifest = "".join(f"{checksum(DIRECTORY / name)}  {name}\n" for name in sorted(names))
    (DIRECTORY / f"SHASUMS256-{system}.txt").write_text(manifest)
    if output := os.environ.get("GITHUB_OUTPUT"):
        with Path(output).open("a") as stream:
            stream.write(f"version={version}\nchecksums={DIRECTORY}/SHASUMS256-{system}.txt\n")
    print("Release version:", version)


def merge():
    version = (DIRECTORY / "VERSION").read_text().strip()
    platforms = json.loads((DIRECTORY / "platforms.json").read_text())
    expected = {f"hrc-{arch}-{system}-{version}.tar.gz" for arch, system in platforms}
    entries = {}
    for system in sorted({system for _, system in platforms}):
        for line in (DIRECTORY / f"SHASUMS256-{system}.txt").read_text().splitlines():
            digest, name = line.split("  ", 1)
            if name not in expected or name in entries or checksum(DIRECTORY / name) != digest:
                raise ValueError("Invalid release artifact: " + name)
            entries[name] = digest
    if set(entries) != expected:
        raise ValueError("Missing release archives: " + ", ".join(sorted(expected - set(entries))))
    (DIRECTORY / "SHASUMS256.txt").write_text("".join(f"{entries[name]}  {name}\n" for name in sorted(entries)))
    print(f"Verified {len(expected)} release archives")


if __name__ == "__main__":
    {"stage": stage, "merge": merge}[sys.argv[1]]()
