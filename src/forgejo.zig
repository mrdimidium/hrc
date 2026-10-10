// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const builtin = @import("builtin");
const Io = std.Io;

const fetch = @import("fetch.zig");

pub const Config = struct {
    version: []const u8,
};

const TripletHashes = std.StaticStringMap([]const u8);

const version_template =
    "https://codeberg.org/ziglang/runner/releases/download/v{s}/forgejo-runner.{s}{s}";

/// SHA-256 computed from the uncompressed binaries downloaded from:
/// https://codeberg.org/ziglang/runner/releases/tag/v13.0.0-zsf
const version_checksum = std.StaticStringMap(TripletHashes).initComptime(.{
    .{ "13.0.0-zsf", TripletHashes.initComptime(.{
        .{ "aarch64-freebsd", "2c6b7c48aefe3d0ed2c4f9bd96d6e70f048add55469ab310975e1694c79709d5" },
        .{ "aarch64-linux", "283c9f6edda4755ffbf622f2699e79568a6dc703544eab5271223fe4921a5211" },
        .{ "aarch64-macos", "f122a742f11bd12e42f500537a7bea842dfad19e2b8ea71935d20bb89235efa6" },
        .{ "aarch64-netbsd", "ee23d72182623d28b7f434c83a6b26199356c69c829d782d6d5d5be234cc9048" },
        .{ "aarch64-openbsd", "3bd8e93a6e940353e03fdcf05e7f5d0004f3a49334821d9115ba292e29538642" },
        .{ "aarch64-windows", "fde957643abac1907fd5458291f278bdc99c2c066241df01ef77ab4e112f58c8" },
        .{ "loongarch64-linux", "8e87eaa553975dcd29853b02de21d670e60c537995b973f1d9c6f7a8dad6e8e8" },
        .{ "powerpc64le-linux", "ed1fa6dd98f6218bd94b4a154451db55d67adde4b827bdd6def8f063485006a9" },
        .{ "riscv64-linux", "faffa068c0af51a0ca2008d46cdfbeb6506edcaa909c50566f6f7ae2f74d6a26" },
        .{ "s390x-linux", "76cf57e98df5e4f6d4b0ea3e3e8be0f457306e07408872844ce0d3b0ff69232e" },
        .{ "x86_64-dragonfly", "ba46fffd2853b527ddffc41e23df6d048fa82882a0955029459455389ea7bf21" },
        .{ "x86_64-freebsd", "ea6c2511d53d033bb30f132034560c95cfefd81dcd8dffb66966dec1b04e5479" },
        .{ "x86_64-illumos", "9a042554e1fb82961845536fdd0e4b6f54c5d66ea37ede0393b65515355d8264" },
        .{ "x86_64-linux", "4277ba9fc9af6b2cd08ace5982875bdea3a2386cdd4250a170a6049183f145e5" },
        .{ "x86_64-netbsd", "1d3ba01432fa920f0a24420d129208ab82f93ada8853eb8286692711b3679cec" },
        .{ "x86_64-openbsd", "469ff987a535d25f18f8708f14cae937433d26e5c64627dd4569b22fafd43869" },
        .{ "x86_64-windows", "cf0183bdeb9d6b4568c841632cb6c391a4a643ea5f5582760599b3bb69264387" },
    }) },
});

/// Installs the host Forgejo runner in a new dst directory as forgejo-runner.
/// Existing destination directories are preserved.
pub fn download(io: Io, allocator: std.mem.Allocator, dst: []const u8, version: []const u8) anyerror!void {
    _ = try std.SemanticVersion.parse(version);

    const platform = @tagName(builtin.cpu.arch) ++ "-" ++ @tagName(builtin.os.tag);
    const triplets = version_checksum.get(version) orelse return error.UnknownVersion;

    var url_buffer: [1024]u8 = undefined;
    var path_buffer: [Io.Dir.max_path_bytes]u8 = undefined;

    const sha = triplets.get(platform) orelse return error.UnsupportedPlatform;
    const ext = if (builtin.os.tag == .windows) ".exe" else "";
    const url = try std.fmt.bufPrint(&url_buffer, version_template, .{ version, platform, ext });
    const path = try std.fmt.bufPrint(&path_buffer, "{s}{s}forgejo-runner{s}", .{ dst, std.fs.path.sep_str, ext });

    const cwd = Io.Dir.cwd();
    if (std.fs.path.dirname(dst)) |parent| try cwd.createDirPath(io, parent);

    try cwd.createDir(io, dst, .default_dir);
    errdefer cwd.deleteTree(io, dst) catch {};

    try fetch.download(io, allocator, try std.Uri.parse(url), sha, path);

    const file = try cwd.openFile(io, path, .{});
    defer file.close(io);

    if (builtin.os.tag != .windows) {
        try file.setPermissions(io, .fromMode(0o755));
    }
}

test {
    std.testing.refAllDecls(@This());
}

test "ZSF release contains hashes for all published platforms" {
    const triplets = version_checksum.get("13.0.0-zsf").?;
    try std.testing.expectEqual(@as(usize, 17), triplets.values().len);
    for (triplets.values()) |hex| {
        var digest: [32]u8 = undefined;
        try std.testing.expectEqual(@as(usize, 32), (try std.fmt.hexToBytes(&digest, hex)).len);
    }
    try std.testing.expect(triplets.get("x86_64-windows") != null);
    try std.testing.expect(triplets.get("aarch64-macos") != null);
    try std.testing.expect(triplets.get("aarch64-linux") != null);
    try std.testing.expect(triplets.get("x86-linux") == null);
    _ = try std.SemanticVersion.parse("13.0.0-zsf");
}
