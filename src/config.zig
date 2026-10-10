// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");

/// Build a config schema using the caller's provider type.
pub fn Config(comptime Provider: type) type {
    return struct {
        const Self = @This();

        /// Stable host identity used as the listener owner and runner name prefix.
        id: []const u8,

        /// Persistent directory for downloaded runner binaries, grouped by provider and version.
        bindir: []const u8 = "./hrc/bin",

        /// Parent directory for temporary per-job workspaces.
        rundir: []const u8 = "./hrc/build",

        /// Provider configurations; each group creates an independent queue.
        groups: []const Provider,

        /// Read a UTF-8 ZON file, limiting its size to 1 MiB.
        pub fn load(
            io: std.Io,
            gpa: std.mem.Allocator,
            arena: std.mem.Allocator,
            path: []const u8,
            diagnostics: *std.zon.parse.Diagnostics,
        ) !Self {
            const bytes = try std.Io.Dir.cwd().readFileAlloc(io, path, gpa, .limited(1024 * 1024));
            defer gpa.free(bytes);

            const source = try gpa.dupeSentinel(u8, bytes, 0);
            defer gpa.free(source);

            return parse(gpa, arena, source, diagnostics);
        }

        /// Strings, groups and diagnostics belong to arena and live until it is released.
        fn parse(
            gpa: std.mem.Allocator,
            arena: std.mem.Allocator,
            source: [:0]const u8,
            diagnostics: *std.zon.parse.Diagnostics,
        ) !Self {
            return std.zon.parse.fromSlice(Self, .{
                .gpa = gpa,
                .arena = arena,
                .source = source,
                .diagnostics = diagnostics,
            });
        }
    };
}

const TestProvider = union(enum) {
    github: struct { version: []const u8 },
    forgejo: struct { version: []const u8 },
};

test "parse groups for both providers and default rundir" {
    const Provider = TestProvider;
    const ConfigType = Config(Provider);
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    var diagnostics: std.zon.parse.Diagnostics = undefined;
    const config = try ConfigType.parse(std.testing.allocator, arena.allocator(),
        \\.{ .id = "build-01", .groups = .{
        \\    .{ .github = .{ .version = "2.338.0" } },
        \\    .{ .forgejo = .{ .version = "13.0.0-zsf" } },
        \\} }
    , &diagnostics);
    try std.testing.expectEqualStrings("build-01", config.id);
    try std.testing.expectEqualStrings("./hrc/build", config.rundir);
    try std.testing.expectEqual(@as(usize, 2), config.groups.len);
    try std.testing.expectEqual(std.meta.Tag(Provider).github, std.meta.activeTag(config.groups[0]));
    try std.testing.expectEqualStrings("2.338.0", config.groups[0].github.version);
    try std.testing.expectEqual(std.meta.Tag(Provider).forgejo, std.meta.activeTag(config.groups[1]));
    try std.testing.expectEqualStrings("13.0.0-zsf", config.groups[1].forgejo.version);
}

test "parse custom rundir and empty groups" {
    const ConfigType = Config(TestProvider);
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    var diagnostics: std.zon.parse.Diagnostics = undefined;
    const config = try ConfigType.parse(std.testing.allocator, arena.allocator(),
        \\.{ .id = "build-02", .rundir = "/tmp/hrc-runners", .groups = .{} }
    , &diagnostics);
    try std.testing.expectEqualStrings("build-02", config.id);
    try std.testing.expectEqualStrings("/tmp/hrc-runners", config.rundir);
    try std.testing.expectEqual(@as(usize, 0), config.groups.len);
}

test "reject invalid ZON and incomplete provider settings" {
    const ConfigType = Config(TestProvider);
    const sources = [_][:0]const u8{
        ".{ .groups =",
        ".{ .groups = .{} }", // Missing id.
        ".{ .id = \"host\" }", // Missing groups.
        ".{ .id = \"host\", .groups = .{}, .unknown = true }",
        ".{ .id = \"host\", .groups = .{ .{ .other = .{ .version = \"1.0.0\" } } } }",
        ".{ .id = \"host\", .groups = .{ .{ .github = .{} } } }",
        ".{ .id = \"host\", .groups = .{ .{ .forgejo = .{} } } }",
        ".{ .id = \"host\", .groups = .{ .{ .github = .{ .version = 123 } } } }",
        ".{ .id = \"host\", .groups = .{ .{ .forgejo = .{ .version = \"1.0.0\", .unknown = true } } } }",
    };
    for (sources) |source| {
        var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
        defer arena.deinit();
        var diagnostics: std.zon.parse.Diagnostics = undefined;
        try std.testing.expectError(error.ParseZon, ConfigType.parse(std.testing.allocator, arena.allocator(), source, &diagnostics));
        try std.testing.expect(diagnostics.errors.len > 0);
    }
}

test "config accepts a caller-defined provider schema" {
    const CustomProvider = struct { command: []const u8, capacity: u32 };
    const ConfigType = Config(CustomProvider);
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    var diagnostics: std.zon.parse.Diagnostics = undefined;
    const config = try ConfigType.parse(std.testing.allocator, arena.allocator(),
        \\.{ .id = "custom", .groups = .{ .{ .command = "runner", .capacity = 3 } } }
    , &diagnostics);
    try std.testing.expectEqualStrings("runner", config.groups[0].command);
    try std.testing.expectEqual(@as(u32, 3), config.groups[0].capacity);
}
