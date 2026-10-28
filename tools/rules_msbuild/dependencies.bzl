# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""PoC dependency acquisition; versions are not yet locked."""

def _feed_impl(ctx):
    for source in ctx.attr.srcs:
        ctx.file(source.name, ctx.read(source))
    for rid in ctx.attr.rids:
        result = ctx.execute(
            [str(ctx.path(ctx.attr.sdk)), "restore", ctx.attr.project, "--runtime", rid, "--packages", str(ctx.path("packages"))] +
            ["-p:" + key + "=" + value for key, value in ctx.attr.properties.items()],
            environment = {
                "DOTNET_CLI_HOME": str(ctx.path(".home")),
                "DOTNET_CLI_TELEMETRY_OPTOUT": "1",
                "DOTNET_CLI_USE_MSBUILD_SERVER": "false",
                "MSBUILDDISABLENODEREUSE": "1",
            },
            timeout = 600,
        )
        if result.return_code:
            fail("Restore for " + rid + " failed:\n" + result.stdout + result.stderr)
    ctx.file("BUILD.bazel", 'filegroup(name = "packages", srcs = glob(["packages/**/*.nupkg"]), visibility = ["//visibility:public"])\n')

_feed = repository_rule(
    implementation = _feed_impl,
    attrs = {
        "project": attr.string(mandatory = True),
        "properties": attr.string_dict(),
        "rids": attr.string_list(mandatory = True),
        "sdk": attr.label(mandatory = True),
        "srcs": attr.label_list(mandatory = True),
    },
)

def _dependencies_impl(ctx):
    os = ctx.os.name.lower()
    arm = ctx.os.arch in ["aarch64", "arm64"]
    arch = "arm64" if arm else "x64"
    if os == "mac os x":
        platform = ("aarch64" if arm else "x86_64") + "-apple-darwin"
        rid = "osx-" + arch
    elif "windows" in os:
        platform = ("arm64" if arm else "x86_64") + "-pc-windows-msvc"
        rid = "win-" + arch
    else:
        platform = ("arm64" if arm else "x86_64") + "-unknown-linux-gnu"
        rid = "linux-" + arch
    for module in ctx.modules:
        for feed in module.tags.feed:
            _feed(
                name = feed.name,
                sdk = "@dotnet_" + platform + "//:" + ("dotnet.exe" if "windows" in os else "dotnet"),
                rids = feed.rids or [rid],
                project = feed.project,
                properties = feed.properties,
                srcs = feed.srcs,
            )

_feed_tag = tag_class(attrs = {
    "name": attr.string(mandatory = True),
    "project": attr.string(mandatory = True),
    "properties": attr.string_dict(),
    "rids": attr.string_list(),
    "srcs": attr.label_list(mandatory = True),
})

dependencies = module_extension(
    implementation = _dependencies_impl,
    tag_classes = {"feed": _feed_tag},
    os_dependent = True,
    arch_dependent = True,
)
