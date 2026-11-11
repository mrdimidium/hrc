# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Run the live documentation server against editable workspace sources."""

import argparse
import gzip
import os
from pathlib import Path
import re
import shlex
import shutil
import sys
import tarfile
import tempfile


def stage(workspace, destination):
    docs = workspace / "docs"
    destination.mkdir(parents=True, exist_ok=True)
    for source in docs.glob("*.rst"):
        target = destination / source.relative_to(docs)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    licenses = destination.parent / "LICENSES"
    licenses.mkdir(exist_ok=True)
    pages = destination / "LICENSES"
    pages.mkdir(exist_ok=True)
    for license_file in sorted((workspace / "LICENSES").glob("*.txt")):
        shutil.copyfile(license_file, licenses / license_file.name)
        title = f"{license_file.stem} license"
        (pages / f"{license_file.stem}.rst").write_text(
            f":orphan:\n\n{title}\n{'=' * len(title)}\n\n"
            f".. literalinclude:: ../../LICENSES/{license_file.name}\n"
            "   :language: text\n"
            "   :class: document-content__license\n",
            encoding="utf-8",
        )


def archive(directory, output):
    # Stable metadata and gzip headers keep immutable release archives identical
    # when the same documentation is rebuilt on another machine or on retry.
    with output.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as package:
                for path in sorted(directory.rglob("*")):
                    info = package.gettarinfo(str(path), path.relative_to(directory).as_posix())
                    info.uid = info.gid = info.mtime = 0
                    info.uname = info.gname = ""
                    info.mode = 0o755 if info.isdir() else 0o644
                    if info.isfile():
                        with path.open("rb") as content:
                            package.addfile(info, content)
                    else:
                        package.addfile(info)


def parse_args(argv=None):
    # Public options (including --help) belong to sphinx-autobuild. Parse only
    # our internal build hooks, without consuming abbreviations of its flags.
    parser = argparse.ArgumentParser(add_help=False, allow_abbrev=False)
    hooks = parser.add_mutually_exclusive_group()
    hooks.add_argument("--stage", nargs=2, type=Path, metavar=("WORKSPACE", "DESTINATION"))
    hooks.add_argument("--text", nargs=3, type=Path, metavar=("WORKSPACE", "SOURCE", "OUTPUT"))
    hooks.add_argument("--archive", nargs=2, type=Path, metavar=("DIRECTORY", "OUTPUT"))
    parser.add_argument("--version-file", type=Path)
    parser.add_argument("--status-file", type=Path)
    args, forwarded = parser.parse_known_args(argv)
    if args.archive and args.version_file is None:
        parser.error("--archive requires --version-file")
    if (args.version_file or args.status_file) and not args.archive:
        parser.error("--version-file and --status-file require --archive")
    if (args.stage or args.text or args.archive) and forwarded:
        parser.error(f"unrecognized arguments: {' '.join(forwarded)}")
    return args, forwarded


def main():
    args, forwarded = parse_args()
    if args.stage:
        stage(*args.stage)
        return

    if args.archive:
        directory, output = args.archive
        version = args.version_file.read_text().strip()
        if args.status_file:
            for line in args.status_file.read_text().splitlines():
                key, _, value = line.partition(" ")
                if key == "STABLE_HRC_VERSION":
                    version = value.strip()
                    break
        if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+[-+A-Za-z0-9.]*", version):
            raise ValueError(f"Invalid documentation version: {version}")
        output.mkdir(parents=True, exist_ok=True)
        archive(directory, output / f"hrc-docs-{version}.tar.gz")
        return

    if args.text:
        from sphinx.cmd.build import build_main

        workspace, source, output = args.text
        sys.exit(build_main([
            "-b", "text", "-n", "-W", "--keep-going",
            "-c", str(workspace / "tools/site"),
            "-d", str(output.parent / "text-doctrees"),
            str(source), str(output),
        ]))

    from sphinx_autobuild.__main__ import main as autobuild

    workspace = Path(os.environ["BUILD_WORKSPACE_DIRECTORY"])
    # Autobuild runs Sphinx in a child interpreter: preserve Bazel's imports.
    os.environ["PYTHONPATH"] = os.pathsep.join(sys.path)
    os.chdir(workspace)
    with tempfile.TemporaryDirectory(prefix="hrc-site-") as directory:
        source = Path(directory) / "src"
        source.mkdir()
        autobuild([
            str(source), str(Path(directory) / "html"),
            "-c", str(workspace / "tools/site"),
            "-n", "-a", "-E",
            "--watch", str(workspace / "docs"),
            "--watch", str(workspace / "tools/site"),
            "--watch", str(workspace / "LICENSES"),
            "--ignore", "*/internal/*",
            "--ignore", str(source),
            "--pre-build", shlex.join([
                sys.executable, str(Path(__file__).resolve()),
                "--stage", str(workspace), str(source),
            ]),
            "--post-build", shlex.join([
                sys.executable, str(Path(__file__).resolve()),
                "--text", str(workspace), str(source), str(Path(directory) / "html"),
            ]),
            *forwarded,
        ])


if __name__ == "__main__":
    main()
