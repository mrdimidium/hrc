# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Publish the target RID on an execution platform with the same OS."""

load("@platforms//host:constraints.bzl", "HOST_CONSTRAINTS")

_HOST_OS = [constraint.split(":")[1] for constraint in HOST_CONSTRAINTS if "//os:" in constraint][0]

def _layout_impl(ctx):
    # This action is local (no-remote/no-sandbox). Upstream derives C# OS
    # constants from the execution host and needs native MSBuild on Windows.
    target_os = {"darwin": "osx", "linux": "linux", "windows": "win"}[ctx.attr.os]
    rid = target_os + "-" + {"amd64": "x64", "arm64": "arm64"}[ctx.attr.arch]
    required_os = {"darwin": "osx", "linux": "linux", "windows": "windows"}[ctx.attr.os]
    if required_os != _HOST_OS:
        fail("Runner %s requires a %s build host; current host is %s. Build //:hrc for cross-OS Go compilation, or build the release on the target OS." % (rid, required_os, _HOST_OS))
    payload = ctx.actions.declare_directory(ctx.label.name)
    ctx.actions.run_shell(
        inputs = ctx.files.srcs + [ctx.file.dev, ctx.file.builder, ctx.version_file],
        outputs = [payload],
        command = 'uv run --no-project --python 3.13 python "$1" "$2" "$3" "$4"',
        arguments = [ctx.file.builder.path, ctx.file.dev.path, payload.path, rid],
        use_default_shell_env = True,
        execution_requirements = {"no-remote": "1", "no-sandbox": "1"},
        mnemonic = "RunnerLayout",
        progress_message = "Building runner layout for " + rid,
    )
    return [DefaultInfo(files = depset([payload]))]

runner_layout = rule(
    implementation = _layout_impl,
    attrs = {
        "arch": attr.string(mandatory = True, values = ["amd64", "arm64"]),
        "builder": attr.label(default = "//runner:build.py", allow_single_file = True),
        "dev": attr.label(allow_single_file = True, mandatory = True),
        "os": attr.string(mandatory = True, values = ["linux", "darwin", "windows"]),
        "srcs": attr.label_list(allow_files = True),
    },
)
