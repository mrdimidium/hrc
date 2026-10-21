# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Generate Worker metadata and convert resx files with the pinned SDK."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET


def build(sdk, version_file, rid, constants, assembly_info, *resources):
    sdk = str(Path(sdk).resolve())
    version = Path(version_file).read_text(encoding="utf-8").strip()

    Path(constants).parent.mkdir(parents=True, exist_ok=True)
    Path(constants).write_text("""
namespace GitHub.Runner.Sdk {
    public static class BuildConstants {
        public static class Source { public static readonly string CommitHash = "unknown"; }
        public static class RunnerPackage {
            public static readonly string Version = %s;
            public static readonly string PackageName = %s;
        }
    }
}
""" % (json.dumps(version), json.dumps(rid)), encoding="utf-8")

    Path(assembly_info).write_text(
        f"[assembly: System.Reflection.AssemblyVersion({json.dumps(version + '.0')})]\n"
        f"[assembly: System.Reflection.AssemblyFileVersion({json.dumps(version + '.0')})]\n"
        f"[assembly: System.Reflection.AssemblyInformationalVersion({json.dumps(version)})]\n", encoding="utf-8")

    project = ET.Element("Project")
    items = ET.SubElement(project, "ItemGroup")
    target = ET.SubElement(project, "Target", Name="Build")

    for source, output in zip(resources[::2], resources[1::2], strict=True):
        if not source.endswith(".resx"):
            shutil.copyfile(source, output)
            continue
        item = ET.SubElement(items, "Resx", Include=str(Path(source).resolve()))
        ET.SubElement(item, "ResourceOutput").text = str(Path(output).resolve())

    ET.SubElement(target, "GenerateResource", Sources="@(Resx)",
                  OutputResources="@(Resx->'%(ResourceOutput)')", UsePreserializedResources="false",
                  ExecuteAsTool="false")

    with tempfile.TemporaryDirectory(prefix="hrc-resources-") as temporary:
        path = Path(temporary) / "Resources.proj"
        ET.ElementTree(project).write(path, encoding="utf-8", xml_declaration=True)
        environment = dict(
            os.environ,
            DOTNET_NOLOGO="1",
            DOTNET_CLI_HOME=temporary,
            DOTNET_CLI_TELEMETRY_OPTOUT="1",
            DOTNET_CLI_USE_MSBUILD_SERVER="false",
            MSBUILDDISABLENODEREUSE="1",
        )
        subprocess.run([sdk, "msbuild", str(path), "-target:Build", "-nologo", "-verbosity:quiet"],
                       env=environment, check=True)


if __name__ == "__main__":
    build(*sys.argv[1:])
