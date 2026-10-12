// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");

/// A secret source. Values and paths are borrowed from the configuration.
pub const Secret = union(enum) {
    env: []const u8,
    file: []const u8,
    @"inline": []const u8,

    /// Returns an owned copy of the secret bytes, without trimming or decoding.
    /// The caller must free the result. Relative file paths use the working directory.
    pub fn load(self: Secret, io: std.Io, allocator: std.mem.Allocator, environ: *const std.process.Environ.Map) ![]u8 {
        return switch (self) {
            .env => |name| allocator.dupe(u8, environ.get(name) orelse return error.MissingSecret),
            .file => |path| std.Io.Dir.cwd().readFileAlloc(io, path, allocator, .limited(1024 * 1024)),
            .@"inline" => |value| allocator.dupe(u8, value),
        };
    }
};

test "ZON accepts each secret source and rejects ambiguous sources" {
    const allocator = std.testing.allocator;
    const sources = [_][:0]const u8{
        ".{ .env = \"TOKEN\" }",
        ".{ .file = \"/etc/hrc/token\" }",
        ".{ .@\"inline\" = \"literal\" }",
    };
    var diagnostics: std.zon.parse.Diagnostics = undefined;
    const tags = [_]std.meta.Tag(Secret){ .env, .file, .@"inline" };
    for (sources, tags) |source, tag| {
        var arena = std.heap.ArenaAllocator.init(allocator);
        defer arena.deinit();
        const secret = try std.zon.parse.fromSlice(Secret, .{ .gpa = allocator, .arena = arena.allocator(), .source = source, .diagnostics = &diagnostics });
        try std.testing.expectEqual(tag, std.meta.activeTag(secret));
    }
    var arena = std.heap.ArenaAllocator.init(allocator);
    defer arena.deinit();
    try std.testing.expectError(error.ParseZon, std.zon.parse.fromSlice(Secret, .{
        .gpa = allocator,
        .arena = arena.allocator(),
        .diagnostics = &diagnostics,
        .source = ".{ .env = \"TOKEN\", .file = \"token\" }",
    }));
}

test "inline and environment sources return owned exact bytes" {
    const allocator = std.testing.allocator;
    var environ = std.process.Environ.Map.init(allocator);
    defer environ.deinit();
    try environ.put("TOKEN", "environment\n");
    const sources = [_]Secret{ .{ .@"inline" = "literal\n" }, .{ .env = "TOKEN" } };
    const expected = [_][]const u8{ "literal\n", "environment\n" };
    for (sources, expected) |source, value| {
        const bytes = try source.load(std.testing.io, allocator, &environ);
        defer allocator.free(bytes);
        try std.testing.expectEqualStrings(value, bytes);
    }
    try std.testing.expectError(error.MissingSecret, (Secret{ .env = "ABSENT" }).load(std.testing.io, allocator, &environ));
}

test "file source preserves bytes and propagates missing file errors" {
    const io = std.testing.io;
    const allocator = std.testing.allocator;
    var environ = std.process.Environ.Map.init(allocator);
    defer environ.deinit();
    var tmp = std.testing.tmpDir(.{});
    defer tmp.cleanup();
    try tmp.dir.writeFile(io, .{ .sub_path = "token", .data = "file-token\r\n" });
    var buffer: [std.Io.Dir.max_path_bytes]u8 = undefined;
    const root = buffer[0..try tmp.dir.realPath(io, &buffer)];
    const path = try std.fs.path.join(allocator, &.{ root, "token" });
    defer allocator.free(path);
    const bytes = try (Secret{ .file = path }).load(io, allocator, &environ);
    defer allocator.free(bytes);
    try std.testing.expectEqualStrings("file-token\r\n", bytes);
    try tmp.dir.deleteFile(io, "token");
    try std.testing.expectError(error.FileNotFound, (Secret{ .file = path }).load(io, allocator, &environ));
}
