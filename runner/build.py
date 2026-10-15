# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Build the upstream runner in an isolated checkout using its own scripts."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def build(dev, output, rid):
    source = Path(dev).resolve().parent
    output = Path(output).resolve()
    # Upstream dev.sh expects /c/... paths on Windows, not Git Bash's /tmp mount.
    temporary_root = Path.cwd() if sys.platform == "win32" else None
    with tempfile.TemporaryDirectory(prefix="hrc-runner-", dir=temporary_root) as temporary:
        checkout = Path(temporary) / "runner"
        subprocess.run(["git", "clone", "--quiet", "--shared", "--no-checkout",
                        str(source.parent), str(checkout)], check=True)
        subprocess.run(["git", "read-tree", "HEAD"], cwd=checkout, check=True)
        shutil.copytree(source, checkout / "src", ignore=shutil.ignore_patterns("bin", "obj"))
        # Keep NuGet audit findings visible without making advisories fatal.
        environment = dict(os.environ, DOTNET_CLI_TELEMETRY_OPTOUT="1",
                           DOTNET_GENERATE_ASPNET_CERTIFICATE="false", DOTNET_NOLOGO="1",
                           DOTNET_CLI_HOME=str(checkout / ".dotnet"),
                           DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE="true",
                           DOTNET_CLI_USE_MSBUILD_SERVER="false",
                           MSBUILDDISABLENODEREUSE="1",
                           UseSharedCompilation="false",
                           WarningsNotAsErrors="NU1901;NU1902;NU1903;NU1904")
        bash = environment.get("BAZEL_SH", "bash")
        try:
            subprocess.run([bash, "dev.sh", "layout", "Release", rid],
                           cwd=checkout / "src", env=environment, check=True)
        finally:
            if sys.platform == "win32":
                # Release SDK DLLs before TemporaryDirectory removes the checkout.
                for dotnet in (checkout / "_dotnetsdk").glob("*/dotnet.exe"):
                    subprocess.run([str(dotnet), "build-server", "shutdown"],
                                   cwd=checkout, env=environment, check=True, timeout=30)
        shutil.copytree(checkout / "_layout", output, dirs_exist_ok=True)
        shutil.copyfile(source.parent / "LICENSE", output / "LICENSE")
        shutil.copyfile(source / "runnerversion", output / "VERSION")


if __name__ == "__main__":
    build(*sys.argv[1:])
