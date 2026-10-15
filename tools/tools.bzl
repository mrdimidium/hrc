# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Expose the prebuilt Buildifier tool directly to lint actions."""

def _buildifier_impl(ctx):
    tool = ctx.toolchains["@buildifier_prebuilt//buildifier:toolchain"]._tool
    executable = ctx.actions.declare_file(ctx.label.name)
    ctx.actions.symlink(output = executable, target_file = tool, is_executable = True)
    return [DefaultInfo(executable = executable)]

buildifier_tool = rule(
    implementation = _buildifier_impl,
    executable = True,
    toolchains = ["@buildifier_prebuilt//buildifier:toolchain"],
)
