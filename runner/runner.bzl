# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Run the upstream runner layout script on its native operating system."""

def _layout_impl(ctx):
    payload = ctx.actions.declare_directory(ctx.label.name)
    ctx.actions.run_shell(
        inputs = ctx.files.srcs + [ctx.file.dev, ctx.file.builder, ctx.version_file],
        outputs = [payload],
        command = 'uv run --no-project --python 3.13 python "$1" "$2" "$3" "$4"',
        arguments = [ctx.file.builder.path, ctx.file.dev.path, payload.path, ctx.attr.rid],
        use_default_shell_env = True,
        execution_requirements = {"no-remote": "1", "no-sandbox": "1"},
        mnemonic = "RunnerLayout",
        progress_message = "Building runner layout for " + ctx.attr.rid,
    )
    return [DefaultInfo(files = depset([payload]))]

runner_layout = rule(
    implementation = _layout_impl,
    attrs = {
        "builder": attr.label(default = "//runner:build.py", allow_single_file = True),
        "dev": attr.label(allow_single_file = True, mandatory = True),
        "rid": attr.string(mandatory = True),
        "srcs": attr.label_list(allow_files = True),
    },
)
