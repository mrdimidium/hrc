# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Expose upstream sources and generate constants for the pinned release."""

load("@bazel_skylib//rules:expand_template.bzl", "expand_template")
load("@bazel_skylib//rules:write_file.bzl", "write_file")

# Repository BUILD setup declares fixed target names, not a reusable target instance.
# buildifier: disable=unnamed-macro
def runner_sources(version):
    """Declare source inputs and constants for an upstream Runner version.

    Args:
        version: Pinned upstream Runner release version.
    """
    native.exports_files(native.glob(["**"]))
    native.filegroup(
        name = "msbuild_sources",
        srcs = native.glob([
            "Directory.Build.props",
            "Sdk/**",
            "Runner.Sdk/**",
            "Runner.Common/**",
            "Runner.Worker/**",
        ], exclude = ["**/bin/**", "**/obj/**", "Runner.Sdk/BuildConstants.cs"]),
        visibility = ["//visibility:public"],
    )
    platforms = {
        os + "_" + arch: (os, arch)
        for os in ["linux", "osx", "win"]
        for arch in ["x64", "arm64"]
    }
    for platform, (os, arch) in platforms.items():
        native.config_setting(
            name = "constants_" + platform,
            constraint_values = [
                "@platforms//os:" + {"linux": "linux", "osx": "osx", "win": "windows"}[os],
                "@platforms//cpu:" + {"arm64": "aarch64", "x64": "x86_64"}[arch],
            ],
        )
    substitutions = select({
        ":constants_" + platform: {
            "@ARCH@": arch,
            "@OS@": os,
            "@VERSION@": version,
        }
        for platform, (os, arch) in platforms.items()
    })
    write_file(
        name = "constants_template",
        out = "BuildConstants.cs.tpl",
        content = ["""\
namespace GitHub.Runner.Sdk {
    public static class BuildConstants {
        public static class Source {
            public static readonly string CommitHash = "unknown";
        }
        public static class RunnerPackage {
            public static readonly string Version = "@VERSION@";
            public static readonly string PackageName = "@OS@-@ARCH@";
        }
    }
}
"""],
    )
    expand_template(
        name = "build_constants",
        template = ":constants_template",
        out = "BuildConstants.cs",
        substitutions = substitutions,
        visibility = ["//visibility:public"],
    )
