# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Generate hrc's release-pinned Ansible variables from the release archives."""

import argparse
import hashlib
import json
import re
import tarfile
from pathlib import Path

PLATFORMS = tuple(
    f"{os_name}-{arch}"
    for os_name in ("linux", "darwin", "windows")
    for arch in ("amd64", "arm64")
)


def release_manifest(artifacts: Path) -> tuple[str, dict]:
    version = (artifacts / "VERSION").read_text().strip()
    if not re.fullmatch(
        r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?", version
    ):
        raise ValueError("VERSION must contain a semantic version")

    manifest = {}
    for platform in PLATFORMS:
        os_name, arch = platform.split("-")
        filename = f"hrc-{arch}-{os_name}-{version}.tar.gz"
        path = artifacts / filename
        with tarfile.open(path, "r:gz") as archive:
            target = next(
                member
                for member in archive
                if member.name.removeprefix("./") == "TARGET"
            )

            with archive.extractfile(target) as stream:
                if stream.read().decode().strip() != f"{arch}-{os_name}":
                    raise ValueError(f"TARGET mismatch in {filename}")

        with path.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()

        manifest[platform] = {"filename": filename, "sha256": digest}

    return version, manifest


def write_manifest(artifacts: Path, output: Path):
    version, manifest = release_manifest(artifacts)
    with open(output, "w") as f:
        json.dump(
            {
                "_hrc_release_version": version,
                "_hrc_release_source": "@ARTIFACT_SOURCE@",
                "_hrc_release_artifacts": manifest,
            },
            f,
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifacts", type=Path, nargs="+", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()

    versions = [file for file in args.artifacts if file.name == "VERSION"]
    if len(versions) != 1:
        parser.error("Stage VERSION and all six hrc archives in dist/release")

    write_manifest(versions[0].parent, args.output)


if __name__ == "__main__":
    main()
