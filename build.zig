// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");

const BuildMode = enum { release, dev, snapshot };

pub fn build(b: *std.Build) !void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const base = b.option([]const u8, "version", "Override version from build.zig.zon") orelse
        @import("build.zig.zon").version;

    const commit = b.option([]const u8, "commit", "Git commit sha") orelse
        git(b, &.{ "git", "rev-parse", "--short=12", "HEAD" });

    const mode: BuildMode = b.option(BuildMode, "build-mode", "Build type") orelse blk: {
        // Re-read the environment even when Git metadata was explicitly overridden.
        b.graph.poisonCache();
        const ref = b.graph.environ_map.get("GITHUB_REF") orelse break :blk .snapshot;

        if (std.mem.cutPrefix(u8, ref, "refs/tags/release-")) |it| {
            if (!std.mem.eql(u8, it, base)) {
                std.log.err("release tag {s} does not match version {s}", .{ ref, base });
                return error.ReleaseVersionMismatch;
            }

            break :blk .release;
        }

        if (std.mem.eql(u8, ref, "refs/heads/main"))
            break :blk .dev;

        break :blk .snapshot;
    };

    const version = if (mode == .release) v: {
        break :v try std.SemanticVersion.parse(base);
    } else v: {
        const ref = if (mode == .snapshot)
            git(b, &.{ "git", "merge-base", "HEAD", "origin/main" }) orelse "HEAD"
        else
            "HEAD";

        const tag = git(b, &.{ "git", "describe", "--tags", "--abbrev=0", "--match", "release-[0-9]*", ref });
        const range = if (tag) |t| b.fmt("{s}..{s}", .{ t, ref }) else ref;

        const ns = git(b, &.{ "git", "rev-list", "--count", "--first-parent", range }) orelse "65536";
        const n = try std.fmt.parseInt(u32, ns, 10);

        var parsed = try std.SemanticVersion.parse(base);
        parsed.pre = if (mode == .dev) b.fmt("dev.{d}", .{n}) else b.fmt("dev.{d}.snapshot", .{n});
        parsed.build = commit orelse "unknown";

        break :v parsed;
    };

    const options = b.addOptions();
    options.addOption(std.SemanticVersion, "version", version);

    const exe = b.addExecutable(.{
        .name = "hrc",
        .root_module = b.createModule(.{
            .root_source_file = b.path("src/main.zig"),
            .target = target,
            .optimize = optimize,
        }),
    });
    exe.root_module.addOptions("build_options", options);
    b.installArtifact(exe);

    const release = b.step("release", "Build a binary archive for the selected target");

    const windows = target.result.os.tag == .windows;
    const binary = if (windows) "hrc.exe" else "hrc";

    const files = b.addWriteFiles();
    _ = files.addCopyFile(exe.getEmittedBin(), binary);
    for ([_][]const u8{ "README.md", "LICENSE", "pkg/hrc.zon" }) |file|
        _ = files.addCopyFile(b.path(file), file);

    const name = b.fmt("hrc-{s}-{s}-{f}.tar.gz", .{
        @tagName(target.result.cpu.arch), @tagName(target.result.os.tag), version,
    });

    const pack = b.addSystemCommand(&.{ "tar", "-czf" });
    const archive = pack.addOutputFileArg(name);
    pack.setCwd(files.getDirectory());
    pack.addArg(".");
    release.dependOn(&b.addInstallFileWithDir(archive, .prefix, b.fmt("release/{s}", .{name})).step);

    // Local metadata for CI, not a published release asset.
    const metadata = b.addWriteFiles();
    const version_file = metadata.add("VERSION", b.fmt("{f}\n", .{version}));
    release.dependOn(&b.addInstallFileWithDir(version_file, .prefix, "release/VERSION").step);

    const run_cmd = b.addRunArtifact(exe);
    run_cmd.step.dependOn(b.getInstallStep());
    run_cmd.addPassthruArgs();
    b.step("run", "Run the app").dependOn(&run_cmd.step);

    const run_test = b.addRunArtifact(b.addTest(.{ .root_module = exe.root_module }));
    b.step("test", "Run tests").dependOn(&run_test.step);
}

fn git(b: *std.Build, argv: []const []const u8) ?[]const u8 {
    // Git refs and history can change without changes to build.zig.
    b.graph.poisonCache();

    var code: u8 = undefined;
    const out = b.runAllowFail(argv, &code, .ignore) catch return null;
    return std.mem.trim(u8, out, " \n\r\t");
}
