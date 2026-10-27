# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Resolve the upstream Worker projects with NuGet before Bazel analysis."""

load("@rules_dotnet//dotnet:defs.bzl", "nuget_repo")

_PROJECTS = ["Sdk", "Runner.Sdk", "Runner.Common", "Runner.Worker"]

def _restore_impl(ctx):
    for source in ctx.attr.srcs:
        ctx.file("src/" + source.name.removeprefix("src/"), ctx.read(source))
    ctx.file("restore.py", ctx.read(ctx.attr.generator))
    sdk = str(ctx.path(ctx.attr.sdk))
    environment = {
        "DOTNET_CLI_HOME": str(ctx.path(".dotnet")),
        "DOTNET_CLI_TELEMETRY_OPTOUT": "1",
        "DOTNET_CLI_USE_MSBUILD_SERVER": "false",
        "DOTNET_NOLOGO": "1",
        "DOTNET_SKIP_FIRST_TIME_EXPERIENCE": "1",
        "MSBUILDDISABLENODEREUSE": "1",
        "NUGET_HTTP_CACHE_PATH": str(ctx.path(".nuget-http")),
        "NUGET_PACKAGES": str(ctx.path("packages")),
    }
    result = ctx.execute(
        [str(ctx.path(ctx.attr.python)), "restore.py", sdk, str(ctx.path("."))],
        environment = environment,
        quiet = False,
        timeout = 600,
    )
    if result.return_code:
        fail("Worker dependency restore failed:\n" + result.stdout + result.stderr)
    ctx.file("BUILD.bazel", 'exports_files(["packages.json", "projects.bzl"])\n')

_restore = repository_rule(
    implementation = _restore_impl,
    attrs = {
        "generator": attr.label(default = "//runner:restore.py"),
        "python": attr.label(default = "@python_3_13_host//:python"),
        "sdk": attr.label(mandatory = True),
        "srcs": attr.label_list(mandatory = True),
    },
)

def _restore_extension(ctx):
    os = ctx.os.name.lower()
    windows = "windows" in os
    arm = ctx.os.arch in ["aarch64", "arm64"]
    if os == "mac os x":
        platform = ("aarch64" if arm else "x86_64") + "-apple-darwin"
    else:
        platform = ("arm64" if arm else "x86_64") + ("-pc-windows-msvc" if windows else "-unknown-linux-gnu")
    _restore(
        name = "worker_restore",
        sdk = "@dotnet_" + platform + "//:" + ("dotnet.exe" if windows else "dotnet"),
        srcs = [Label("@actions_runner//:src/" + name) for name in ["Directory.Build.props", "NuGet.Config"] + [project + "/" + project + ".csproj" for project in _PROJECTS]],
    )

restore = module_extension(
    implementation = _restore_extension,
    os_dependent = True,
    arch_dependent = True,
)

def _dependencies(ctx):
    nuget_repo(
        name = "worker_nuget",
        packages = json.decode(ctx.read(Label("@worker_restore//:packages.json"))),
    )

dependencies = module_extension(implementation = _dependencies)
