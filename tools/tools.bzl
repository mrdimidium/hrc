# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Expose the prebuilt Buildifier tool directly to lint actions."""

# Keep author-defined ordering of named arguments when formatting.
BUILDIFIER_ARGS = ["-buildifier_disable=callsort"]

def _buildifier_impl(ctx):
    tool = ctx.toolchains["@buildifier_prebuilt//buildifier:toolchain"]._tool
    executable = ctx.actions.declare_file(ctx.label.name)
    if ctx.attr.lint_only:
        ctx.actions.expand_template(
            template = ctx.file._lint_wrapper,
            output = executable,
            substitutions = {"@BUILDIFIER_RUNFILE@": tool.short_path.removeprefix("../")},
            is_executable = True,
        )
        return [DefaultInfo(executable = executable, runfiles = ctx.runfiles(files = [tool]))]
    ctx.actions.symlink(output = executable, target_file = tool, is_executable = True)
    return [DefaultInfo(executable = executable)]

buildifier_tool = rule(
    implementation = _buildifier_impl,
    executable = True,
    attrs = {
        "lint_only": attr.bool(default = False),
        "_lint_wrapper": attr.label(default = Label("//tools:buildifier_lint.sh"), allow_single_file = True),
    },
    toolchains = ["@buildifier_prebuilt//buildifier:toolchain"],
)
