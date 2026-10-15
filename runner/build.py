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
    with tempfile.TemporaryDirectory(prefix="hrc-runner-") as temporary:
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
                           WarningsNotAsErrors="NU1901;NU1902;NU1903;NU1904")
        bash = environment.get("BAZEL_SH", "bash")
        subprocess.run([bash, "dev.sh", "layout", "Release", rid],
                       cwd=checkout / "src", env=environment, check=True)
        shutil.copytree(checkout / "_layout", output, dirs_exist_ok=True)
        shutil.copyfile(source.parent / "LICENSE", output / "LICENSE")
        shutil.copyfile(source / "runnerversion", output / "VERSION")


if __name__ == "__main__":
    build(*sys.argv[1:])
