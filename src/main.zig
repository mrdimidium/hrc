// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const Io = std.Io;

/// Runner backends share download(io, allocator, dst, version).
/// dst is a new installation directory for either backend.
pub const github = @import("github.zig");
pub const forgejo = @import("forgejo.zig");

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const arena: std.mem.Allocator = init.arena.allocator();

    const args = try init.minimal.args.toSlice(arena);
    for (args) |arg| {
        std.log.info("arg: {s}", .{arg});
    }

    var stdout_buffer: [1024]u8 = undefined;
    var stdout_file_writer: Io.File.Writer = .init(.stdout(), io, &stdout_buffer);
    const stdout_writer = &stdout_file_writer.interface;

    try stdout_writer.print("Run `zig build test` to run the tests.\n", .{});
    try stdout_writer.flush();
}

test "runner backends implement the same download interface" {
    const Download = *const fn (Io, std.mem.Allocator, []const u8, []const u8) anyerror!void;
    const backends = [_]Download{ github.download, forgejo.download };
    for (backends) |download| {
        try std.testing.expectError(error.InvalidVersion, download(std.testing.io, std.testing.allocator, "unused", "../../etc"));
        try std.testing.expectError(error.UnknownVersion, download(std.testing.io, std.testing.allocator, "unused", "0.0.0"));
    }
}
