# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Stage declared inputs and publish without network package sources."""

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(fromfile_prefix_chars="@")
    for name in ("sdk", "output", "rid", "self_contained", "project", "assembly_name", "version"):
        parser.add_argument(name)
    parser.add_argument("--property", action="append", default=[])
    parser.add_argument("--staged", nargs=2, action="append", default=[])
    parser.add_argument("--source", nargs=2, action="append", default=[])
    parser.add_argument("--package", action="append", default=[])
    args = parser.parse_args()
    sdk, output = Path(args.sdk).resolve(), Path(args.output).resolve()
    version = Path(args.version).read_text().strip()
    with tempfile.TemporaryDirectory(prefix="bazel-msbuild-") as temporary:
        # macOS aliases /var to /private/var. NuGet and MSBuild must use
        # the same canonical project paths to resolve transitive references.
        root = Path(temporary).resolve()
        for source, relative in args.source:
            destination = root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, destination)
        for source, relative in args.staged:
            destination = root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, destination)
        feed = root / "feed"
        feed.mkdir()
        for package in args.package:
            shutil.copyfile(package, feed / Path(package).name)
        config = root / "NuGet.Config"
        config.write_text(f'<configuration><packageSources><clear/><add key="local" value="{feed}"/></packageSources></configuration>')
        environment = dict(os.environ, DOTNET_CLI_HOME=str(root / "home"),
                           DOTNET_CLI_TELEMETRY_OPTOUT="1", DOTNET_CLI_USE_MSBUILD_SERVER="false",
                           MSBUILDDISABLENODEREUSE="1", NUGET_PACKAGES=str(root / "packages"),
                           NUGET_HTTP_CACHE_PATH=str(root / "http"))
        if os.name == "nt":
            # Bazel actions do not inherit a full Windows user environment.
            # NuGet initializes machine/user settings even with --configfile.
            windows_directories = {
                "APPDATA": root / "home" / "AppData" / "Roaming",
                "LOCALAPPDATA": root / "home" / "AppData" / "Local",
                "PROGRAMFILES": root / "ProgramFiles",
                "PROGRAMFILES(X86)": root / "ProgramFilesX86",
                "USERPROFILE": root / "home",
            }
            for name, directory in windows_directories.items():
                directory.mkdir(parents=True, exist_ok=True)
                environment[name] = str(directory)
        project = root / args.project
        properties = [f"-p:RuntimeIdentifier={args.rid}", "-p:RuntimeIdentifiers=",
                      f"-p:SelfContained={args.self_contained}",
                      "-p:PublishSingleFile=true", "-p:UseAppHost=true",
                      "-p:IncludeNativeLibrariesForSelfExtract=true",
                      "-p:IncludeAllContentForSelfExtract=false", "-p:PublishReadyToRun=false",
                      "-p:PublishReadyToRunComposite=false", "-p:NuGetAudit=false",
                      "-p:EnableSingleFileAnalyzer=false", "-p:DebugType=None",
                      f"-p:Version={version}"] + ["-p:" + value for value in args.property]
        subprocess.run([str(sdk), "restore", str(project), "--configfile", str(config), *properties],
                       env=environment, check=True)
        publish = root / "publish"
        subprocess.run([str(sdk), "publish", str(project), "--no-restore", "-c", "Release",
                        "-o", str(publish), *properties], env=environment, check=True)
        executable = publish / (args.assembly_name + (".exe" if args.rid.startswith("win-") else ""))
        # Never silently discard dependencies that were not bundled.
        extras = [path.name for path in publish.iterdir() if path != executable]
        if extras:
            raise RuntimeError(f"Unexpected files outside single-file bundle: {extras}")
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(executable, output)
        output.chmod(0o755)


if __name__ == "__main__":
    main()
