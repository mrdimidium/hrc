# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Runner layout and pinned Node distributions for runner externals."""

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")
load("@platforms//host:constraints.bzl", "HOST_CONSTRAINTS")
load("@rules_pkg//pkg:mappings.bzl", "pkg_attributes", "pkg_filegroup", "pkg_files", "strip_prefix")

_HOST_OS = [constraint.split(":")[1] for constraint in HOST_CONSTRAINTS if "//os:" in constraint][0]

# Versions match upstream src/Misc/externals.sh. Hashes come from Node's
# SHASUMS256.txt (official and unofficial-builds) and actions/alpine_nodejs
# release asset digests. Alpine arm64 uses the experimental unofficial builds.
_NODE = {
    "20": ("20.20.2", {
        "darwin_arm64": "466e05f3477c20dfb723054dfebffe55bc74660ee77f612166fca121dacb65b6",
        "darwin_x64": "8be6f5e4bb128c82774f8a0b8d7a1cc1365a7977d9657cece0ca647b3fe04e61",
        "linux_glibc_arm64": "47ef73d543ecf6eb19435f6c03a0ac4809b3bf0dd6b26c7c571efc2a6572a74d",
        "linux_glibc_x64": "19e56f0825510207dd904f087fe52faa0a4eb6b2aab5f0ea7a33830d04888b8b",
        "linux_musl_arm64": "7549872a8d1be4670d5219f39dc0aed6e6432c976f3cd25543bc249aa850f041",
        "linux_musl_x64": "f21a2253025a5d1a14332a0b1ed48871689c5ca9aa37a6141428944b75de7d91",
        "win_arm64": "d5c5b1d56f7f9469830eb1f57efeec0a6a9078c0a9e88cd5b4b4b48f46c22069",
        "win_x64": "dc3700fdd57a63eedb8fd7e3c7baaa32e6a740a1b904167ff4204bc68ed8bf77",
    }),
    "24": ("24.21.0", {
        "darwin_arm64": "bed7eea5325e1108f32ce5228ddd6a5f0f08a499ee42aa7442aea583702f6057",
        "darwin_x64": "1462cb3b3046b815cf8ea436d3da450ec1a9f11dac7e5a46b0ada5305d7e8097",
        "linux_glibc_arm64": "724282c3b43aec998aa9527380465b45d229e021b58035f5f4f63095eabfe5d5",
        "linux_glibc_x64": "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff",
        "linux_musl_arm64": "3048b0811e158ca0d8672b59c839861763e144492980d8d29b369dfee45747e4",
        "linux_musl_x64": "2a4a9c6af9b6f1dd67e4c460474a6fbb64fafffe49e1166af80715bfd4e50775",
        "win_arm64": "8779b1bde1d39f8d420e3b57aa657b39891af434d3de44a919044cec06785921",
        "win_x64": "158f7685b44de51f6c0df1d153526cbcd3e1bc739a8dfc607721cef75de9e541",
    }),
}

_NODE_BUILD = """filegroup(
    name = "files",
    srcs = ["bin/node", "LICENSE"],
    visibility = ["//visibility:public"],
)
"""

_NODE_WINDOWS_BUILD = """exports_files(["node.exe"])
filegroup(
    name = "files",
    srcs = ["node.exe", "LICENSE"],
    visibility = ["//visibility:public"],
)
"""

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

def _node_repositories(_ctx):
    for major, (version, hashes) in _NODE.items():
        for platform, sha256 in hashes.items():
            parts = platform.split("_")
            os, arch = parts[:2]
            libc = ""
            if os == "linux":
                libc, arch = parts[1:]
            name = "node" + major + "_" + platform
            archive = "node-v%s-%s-%s" % (version, os, arch)
            extension = "zip" if os == "win" else "tar.gz"
            url = "https://nodejs.org/dist/v%s/%s.%s" % (version, archive, extension)
            archive_prefix = archive
            if libc == "musl":
                archive = "node-v%s-alpine-%s" % (version, arch)
                url = "https://github.com/actions/alpine_nodejs/releases/download/v%s/%s.tar.gz" % (version, archive)
                archive_prefix = ""
                if arch == "arm64":
                    archive = "node-v%s-linux-arm64-musl" % version
                    url = "https://unofficial-builds.nodejs.org/download/release/v%s/%s.tar.gz" % (version, archive)
                    archive_prefix = archive
            http_archive(
                name = name,
                urls = [url],
                sha256 = sha256,
                strip_prefix = archive_prefix,
                build_file_content = _NODE_WINDOWS_BUILD if os == "win" else _NODE_BUILD,
            )

node_repositories = module_extension(implementation = _node_repositories)

def node_externals(name):
    """Map the upstream Node payload into runner/externals using rules_pkg.

    Args:
        name: Name of the package filegroup and prefix for platform mappings.
    """
    platforms = {}
    for os in ["linux", "darwin", "win"]:
        for arch in ["x64", "arm64"]:
            platform = os + "_" + arch
            condition = name + "_" + platform
            native.config_setting(
                name = condition,
                constraint_values = [
                    "@platforms//os:" + {"darwin": "osx", "linux": "linux", "win": "windows"}[os],
                    "@platforms//cpu:" + {"arm64": "aarch64", "x64": "x86_64"}[arch],
                ],
            )
            files = []
            for major in _NODE:
                target = name + "_node" + major + "_" + platform
                srcs = ["@node%s_%s//:files" % (major, platform)]
                renames = {}
                if os == "linux":
                    srcs = ["@node%s_linux_glibc_%s//:files" % (major, arch)]
                elif os == "win":
                    renames = {
                        "@node%s_%s//:node.exe" % (major, platform): "bin/node.exe",
                    }
                pkg_files(
                    name = target,
                    srcs = srcs,
                    prefix = "runner/externals/node" + major,
                    strip_prefix = strip_prefix.from_root(),
                    renames = renames,
                    attributes = pkg_attributes(mode = "0755"),
                )
                files.append(":" + target)
                if os == "linux":
                    alpine = target + "_alpine"
                    pkg_files(
                        name = alpine,
                        srcs = ["@node%s_linux_musl_%s//:files" % (major, arch)],
                        prefix = "runner/externals/node" + major + "_alpine",
                        strip_prefix = strip_prefix.from_root(),
                        attributes = pkg_attributes(mode = "0755"),
                    )
                    files.append(":" + alpine)
            platforms[":" + condition] = files
    pkg_filegroup(
        name = name,
        srcs = select(platforms),
        visibility = ["//visibility:public"],
    )
