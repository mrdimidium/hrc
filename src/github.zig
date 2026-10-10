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
    "https://github.com/actions/runner/releases/download/v{s}/actions-runner-{s}-{s}.{s}";

const version_checksum = std.StaticStringMap(TripletHashes).initComptime(.{
    .{ "2.338.0", TripletHashes.initComptime(.{
        .{ "linux-x64", "af4b794c1bc41d73d40535e3fe092a39f9679cd8d965954c2aca25a05ca41d32" },
        .{ "linux-arm64", "628b4a7258487b80c1d3c221095a7ded349f5ae0175fd3dfab708d07428f041b" },
        .{ "linux-arm", "1d6a7688d2aca1a1fecd4df5d473e4aa4226c1f1785ff915cf84049665a3483f" },
        .{ "osx-x64", "dea7a58796ce215fc424a8b27c0fdd9b813fa5fc03d109a4a1d38131d6bfa1a3" },
        .{ "osx-arm64", "df4cebda25c86a886ed204e49fee63f5c2e7cec5f447b5c98440a826bbdf9df2" },
        .{ "win-x64", "f48e0750a21812bca5f82de5f7f5aeae71abee647fab5a582f1742d07eba455f" },
        .{ "win-arm64", "a57a4d29c0dcf02210a16b01906ed76cb8091b26d728c41232736c0f627a557e" },
    }) },
});

/// Selects the host runner package and delegates downloading to download.
pub fn download(io: Io, allocator: std.mem.Allocator, dst: []const u8, version: []const u8) anyerror!void {
    _ = try std.SemanticVersion.parse(version);

    const platform = try hostPlatform();
    const triplets = version_checksum.get(version) orelse return error.UnknownVersion;

    var url_buffer: [1024]u8 = undefined;
    var path_buffer: [Io.Dir.max_path_bytes]u8 = undefined;

    const sha = triplets.get(platform) orelse return error.UnsupportedPlatform;
    const ext = if (builtin.os.tag == .windows) "zip" else "tar.gz";
    const url = try std.fmt.bufPrint(&url_buffer, version_template, .{ version, platform, version, ext });
    const path = try std.fmt.bufPrint(&path_buffer, "{s}.{s}", .{ dst, ext });

    const cwd = Io.Dir.cwd();
    if (std.fs.path.dirname(dst)) |parent| try cwd.createDirPath(io, parent);

    try cwd.createDir(io, dst, .default_dir);
    errdefer cwd.deleteTree(io, dst) catch {};

    var dir = try cwd.openDir(io, dst, .{});
    defer dir.close(io);

    try fetch.download(io, allocator, try std.Uri.parse(url), sha, path);
    defer cwd.deleteFile(io, path) catch {};

    const archive = try cwd.openFile(io, path, .{});
    defer archive.close(io);

    var read_buffer: [64 * 1024]u8 = undefined;
    var reader = archive.reader(io, &read_buffer);

    if (builtin.os.tag == .windows) {
        try std.zip.extract(dir, &reader, .{ .allow_backslashes = true });
    } else {
        var inflate_buffer: [std.compress.flate.max_window_len]u8 = undefined;
        var gzip: std.compress.flate.Decompress = .init(&reader.interface, .gzip, &inflate_buffer);
        try std.tar.extract(io, dir, &gzip.reader, .{});
        _ = try gzip.reader.discardRemaining();
    }
}

fn hostPlatform() ![]const u8 {
    return switch (builtin.os.tag) {
        .linux => switch (builtin.cpu.arch) {
            .x86_64 => "linux-x64",
            .aarch64 => "linux-arm64",
            .arm => "linux-arm",
            else => error.UnsupportedPlatform,
        },
        .macos => switch (builtin.cpu.arch) {
            .x86_64 => "osx-x64",
            .aarch64 => "osx-arm64",
            else => error.UnsupportedPlatform,
        },
        .windows => switch (builtin.cpu.arch) {
            .x86_64 => "win-x64",
            .aarch64 => "win-arm64",
            else => error.UnsupportedPlatform,
        },
        else => error.UnsupportedPlatform,
    };
}

test {
    std.testing.refAllDecls(@This());
}
