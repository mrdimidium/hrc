# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Proof of concept: SDK publish as one offline Bazel action."""

def _publish_impl(ctx):
    toolchain = ctx.toolchains["@rules_dotnet//dotnet:toolchain_type"]
    output = ctx.actions.declare_file(ctx.label.name + "/" + ctx.attr.binary_name + (".exe" if ctx.attr.rid.startswith("win-") else ""))
    args = ctx.actions.args()
    args.use_param_file("@%s", use_always = True)
    args.set_param_file_format("multiline")
    args.add(toolchain.dotnetinfo.runtime_path)
    args.add(output)
    args.add(ctx.attr.rid)
    args.add("true" if ctx.attr.self_contained else "false")
    args.add(ctx.attr.project)
    args.add(ctx.attr.assembly_name)
    args.add(ctx.file.version)
    for source, destination in ctx.attr.staged_files.items():
        args.add("--staged")
        args.add(source.files.to_list()[0])
        args.add(destination)
    for key, value in ctx.attr.properties.items():
        args.add("--property=" + key + "=" + value)
    for source in ctx.files.srcs:
        args.add("--source")
        args.add(source)
        args.add(source.path.removeprefix(source.owner.workspace_root + "/") if source.owner.workspace_root else source.path)
    args.add_all(ctx.files.packages, before_each = "--package")
    ctx.actions.run(
        executable = ctx.executable._publish,
        arguments = [args],
        inputs = depset(ctx.files.srcs + ctx.files.packages + [ctx.file.version] + [target.files.to_list()[0] for target in ctx.attr.staged_files]),
        tools = toolchain.dotnetinfo.runtime_files + toolchain.dotnetinfo.csharp_compiler_files,
        outputs = [output],
        mnemonic = "MSBuildPublish",
        progress_message = "Publishing single-file application with MSBuild",
    )
    return [DefaultInfo(files = depset([output]), executable = output)]

msbuild_publish = rule(
    implementation = _publish_impl,
    attrs = {
        "assembly_name": attr.string(mandatory = True),
        "binary_name": attr.string(mandatory = True),
        "packages": attr.label_list(allow_files = [".nupkg"]),
        "project": attr.string(mandatory = True),
        "properties": attr.string_dict(),
        "rid": attr.string(mandatory = True),
        "self_contained": attr.bool(default = True),
        "srcs": attr.label_list(allow_files = True),
        "staged_files": attr.label_keyed_string_dict(allow_files = True),
        "version": attr.label(allow_single_file = True, mandatory = True),
        "_publish": attr.label(default = ":publish", executable = True, cfg = "exec"),
    },
    executable = True,
    toolchains = ["@rules_dotnet//dotnet:toolchain_type"],
)
