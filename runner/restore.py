# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Export NuGet's resolved Worker dependencies for Bazel."""

import base64
import hashlib
import json
from pathlib import Path
import subprocess
import sys


def restore(sdk, root):
    root = Path(root)
    package_root = root / "packages"
    packages, versions, project_packages, project_references = {}, {}, {}, {}

    def run(*arguments):
        subprocess.run([sdk, *map(str, arguments)], cwd=root, check=True)

    # Bazel's publish rule supplies the target runtime.
    run("restore", "src/Runner.Worker/Runner.Worker.csproj", "--configfile", "src/NuGet.Config",
        "-p:RuntimeIdentifiers=", "-p:SelfContained=false", "-p:TreatWarningsAsErrors=false")
    export_target = root / "References.targets"
    export_target.write_text("""<Project>
  <Target Name="ExportWorkerReferences" DependsOnTargets="ResolveReferences">
    <WriteLinesToFile File="$(MSBuildProjectDirectory)/obj/references.txt" Lines="@(ReferencePath)" Overwrite="true" />
  </Target>
</Project>
""", encoding="utf-8")

    for project in sorted((root / "src").rglob("*.csproj")):
        run("msbuild", project, "-t:ExportWorkerReferences", "-p:Configuration=Release",
            "-p:BuildProjectReferences=false", "-p:SelfContained=false", "-p:RuntimeIdentifiers=",
            f"-p:CustomAfterMicrosoftCommonTargets={export_target}", "-p:TreatWarningsAsErrors=false", "-nologo")
        obj = project.parent / "obj"
        assets = json.loads((obj / "project.assets.json").read_text(encoding="utf-8-sig"))
        sources = sorted(assets["project"]["restore"]["sources"])
        # MSBuild excludes assemblies superseded by the net8 reference pack.
        selected = {
            path.relative_to(package_root).parts[0].lower()
            for line in (obj / "references.txt").read_text(encoding="utf-8-sig").splitlines()
            if (path := Path(line)).is_relative_to(package_root)
        }
        compile_packages, projects = [], []
        for key, item in assets["targets"]["net8.0"].items():
            package_id, version = key.rsplit("/", 1)
            name = package_id.lower()
            if item["type"] == "project":
                projects.append(package_id)
                continue
            if name in selected:
                compile_packages.append(name)
            if versions.setdefault(name, version) != version:
                raise ValueError(f"Conflicting versions of {package_id}: {versions[name]} and {version}")
            archive = package_root / assets["libraries"][key]["path"] / f"{name}.{version.lower()}.nupkg"
            with archive.open("rb") as stream:
                digest = base64.b64encode(hashlib.file_digest(stream, "sha512").digest()).decode("ascii")
            packages[name] = {
                "name": name, "id": package_id, "version": version, "sha512": "sha512-" + digest,
                "sources": sources,
                "dependencies": {"net8.0": sorted(dep.lower() for dep in item.get("dependencies", {}))},
                "targeting_pack_overrides": [], "framework_list": [],
            }
        project_packages[project.stem] = sorted(compile_packages)
        project_references[project.stem] = sorted(projects)

    (root / "packages.json").write_text(json.dumps([packages[name] for name in sorted(packages)]), encoding="utf-8")
    (root / "projects.bzl").write_text(
        "PACKAGES = " + json.dumps(project_packages, sort_keys=True) + "\n" +
        "PROJECTS = " + json.dumps(project_references, sort_keys=True) + "\n", encoding="utf-8")


if __name__ == "__main__":
    restore(*sys.argv[1:])
