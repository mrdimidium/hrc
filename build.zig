// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");

const BuildMode = enum { release, dev, snapshot };

pub fn build(b: *std.Build) !void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const strip = b.option(bool, "strip", "Strip debug symbols") orelse true;

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
            .strip = strip,
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
    files.mode = .tmp;
    const packaged_binary = files.addCopyFile(exe.getEmittedBin(), binary);
    for ([_][]const u8{ "README.md", "LICENSE", "pkg/hrc.zon", "LICENSES/BSD-3-Clause.txt", "LICENSES/CC-BY-4.0.txt" }) |file|
        _ = files.addCopyFile(b.path(file), file);
    _ = files.add("TARGET", b.fmt("{s}-{s}\n", .{ @tagName(target.result.cpu.arch), @tagName(target.result.os.tag) }));

    const services: []const []const u8 = switch (target.result.os.tag) {
        .linux => &.{ "hrc.systemd", "hrc.alpine" },
        .freebsd => &.{"hrc.freebsd"},
        .openbsd => &.{"hrc.openbsd"},
        .netbsd => &.{"hrc.netbsd"},
        .macos => &.{"org.hrc.plist"},
        else => &.{},
    };
    if (windows) {
        _ = files.addCopyFile(b.path("pkg/control.ps1"), "control.ps1");
        _ = files.addCopyFile(b.path("pkg/services/hrc.winsw.xml"), "pkg/services/hrc.winsw.xml");
    }
    var executable_permissions: ?*std.Build.Step.Run = null;
    if (services.len != 0) {
        const control = files.addCopyFile(b.path("pkg/control"), "control");
        for ([_][]const u8{ "common", "preinstall", "postinstall", "preremove", "postremove" }) |hook| {
            const path = b.fmt("pkg/scripts/{s}.sh", .{hook});
            _ = files.addCopyFile(b.path(path), path);
        }
        for (services) |service| {
            const path = b.fmt("pkg/services/{s}", .{service});
            _ = files.addCopyFile(b.path(path), path);
        }

        // Mutate only the temporary staging directory, never shared cache outputs.
        const permissions = b.addSystemCommand(&.{ "chmod", "0755" });
        permissions.addFileArg(packaged_binary);
        permissions.addFileArg(control);
        executable_permissions = permissions;
    }

    const name = b.fmt("hrc-{s}-{s}-{f}.tar.gz", .{
        @tagName(target.result.cpu.arch), @tagName(target.result.os.tag), version,
    });

    const pack = b.addSystemCommand(&.{ "tar", "-czf" });
    if (executable_permissions) |permissions| pack.step.dependOn(&permissions.step);
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

    const tests = b.addTest(.{ .root_module = exe.root_module });
    const test_name = b.fmt("hrc-{s}-{s}{s}", .{
        @tagName(target.result.cpu.arch), @tagName(target.result.os.tag),
        if (windows) ".exe" else "",
    });
    b.step("test-build", "Build tests for execution on the selected target").dependOn(
        &b.addInstallFileWithDir(tests.getEmittedBin(), .prefix, b.fmt("tests/{s}", .{test_name})).step,
    );
    const run_test = b.addRunArtifact(tests);
    b.step("test", "Run tests").dependOn(&run_test.step);
}

fn git(b: *std.Build, argv: []const []const u8) ?[]const u8 {
    // Git refs and history can change without changes to build.zig.
    b.graph.poisonCache();

    var code: u8 = undefined;
    const out = b.runAllowFail(argv, &code, .ignore) catch return null;
    return std.mem.trim(u8, out, " \n\r\t");
}
