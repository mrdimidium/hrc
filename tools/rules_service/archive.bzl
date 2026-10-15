# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Build a service's file layout independently of the final package format."""

load("@rules_pkg//pkg:providers.bzl", "PackageFilegroupInfo", "PackageFilesInfo")
load(":scripts.bzl", "inline_script")

def _add(files, source, destination, mode):
    if not destination or destination.startswith("/") or "\\" in destination or any([part in ["", ".", ".."] for part in destination.split("/")]):
        fail("archive destination must be a relative path: " + destination)
    if destination in files:
        fail("duplicate archive destination: " + destination)
    files[destination] = (source, mode)

def _dependency_path(file):
    # Source files retain their package-relative layout (e.g. LICENSES/*.txt).
    # Generated files and trees use their output basename (e.g. runner/).
    if not file.is_source:
        return file.basename
    return file.short_path.removeprefix("../" + file.owner.workspace_name + "/").removeprefix(file.owner.package + "/")

def service_contents(ctx, outputs, settings, executable):
    """Return packaging providers and files for the selected platform's layout.

    Args:
        ctx: Service rule context with the binary and explicit dependencies.
        outputs: Generated definitions and hooks, keyed by output group.
        settings: Escaped shell substitutions for the installer.
        executable: Installed binary basename without a Windows extension.

    Returns:
        DefaultInfo and PackageFilegroupInfo describing the complete layout.
    """
    files = {}
    windows = ctx.attr.os == "windows"
    _add(files, ctx.file.bin, executable + (".exe" if windows else ""), "0755")
    if ctx.file.config:
        _add(files, ctx.file.config, ctx.file.config.basename, "0644")
    if ctx.file.installer:
        installer = ctx.file.installer
        if not windows:
            installer = ctx.actions.declare_file(ctx.label.name + "/installer/" + installer.basename)
            inline_script(ctx, ctx.file.installer, installer, ctx.file._common, settings)
        _add(files, installer, installer.basename, "0644" if windows else "0755")
    kinds = {"darwin": ["launchd"], "linux": ["systemd", "openrc"], "windows": ["winsw"]}[ctx.attr.os]
    for kind in kinds:
        for file in outputs[kind].to_list():
            _add(files, file, "services/" + file.basename, "0755" if kind == "openrc" else "0644")
    for file in outputs["scripts"].to_list():
        _add(files, file, "scripts/" + file.basename, "0755")
    target = ctx.actions.declare_file(ctx.label.name + "/TARGET")
    ctx.actions.write(target, ctx.attr.arch + "-" + ctx.attr.os + "\n")
    _add(files, target, "TARGET", "0644")
    for dep in ctx.attr.deps:
        if PackageFilegroupInfo in dep or PackageFilesInfo in dep:
            fail("service deps accept ordinary targets; pass their files rather than pkg_files mappings: " + str(dep.label))
        info = dep[DefaultInfo]
        for file in info.files.to_list():
            # A single-file filegroup can report a source document as executable.
            # Source deps are data; actual executable rules produce generated files.
            mode = "0755" if file.is_directory or (not file.is_source and info.files_to_run.executable == file) else "0644"
            _add(files, file, _dependency_path(file), mode)
    mappings = []
    for mode in ["0644", "0755"]:
        mappings.append((PackageFilesInfo(
            dest_src_map = {destination: file for destination, (file, file_mode) in files.items() if file_mode == mode},
            attributes = {"mode": mode},
        ), ctx.label))
    return [
        DefaultInfo(files = depset([file for file, _mode in files.values()])),
        PackageFilegroupInfo(pkg_files = mappings, pkg_dirs = [], pkg_symlinks = []),
    ]
