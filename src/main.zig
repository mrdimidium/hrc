// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const build_options = @import("build_options");

const config = @import("config.zig");

const Io = std.Io;

/// Runner backends share download(io, allocator, dst, version).
/// dst is a new installation directory for either backend.
pub const github = @import("github.zig");
pub const forgejo = @import("forgejo.zig");

const Provider = union(enum) {
    github: github.Config,
    forgejo: forgejo.Config,
};

const Config = config.Config(Provider);

const Args = struct {
    help: bool = false,
    version: bool = false,

    config: []const u8 = "",
};

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const arena: std.mem.Allocator = init.arena.allocator();

    const argv = try init.minimal.args.toSlice(arena);
    const args = parseArgs(Args, argv[1..]) catch |err| {
        std.process.fatal("{s}. Usage: hrc -config <path.zon> (or -help / -version)", .{@errorName(err)});
    };

    var stdout_buffer: [1024]u8 = undefined;
    var stdout_file_writer: Io.File.Writer = .init(.stdout(), io, &stdout_buffer);
    const stdout_writer = &stdout_file_writer.interface;

    if (args.help) {
        try stdout_writer.writeAll(
            \\Usage: hrc -config <path.zon>
            \\       hrc -help
            \\       hrc -version
            \\
            \\Options:
            \\  -config <path.zon>  Read the ZON config and print it to stdout.
            \\  -help               Print this help and exit.
            \\  -version            Print the application version and exit.
            \\
        );
        try stdout_writer.flush();
        return;
    }
    if (args.version) {
        try stdout_writer.print("{s}\n", .{build_options.version});
        try stdout_writer.flush();
        return;
    }
    if (args.config.len == 0)
        std.process.fatal("MissingArgument. Usage: hrc -config <path.zon> (or -help / -version)", .{});

    var diagnostics: std.zon.parse.Diagnostics = undefined;
    const cfg = Config.load(io, init.gpa, arena, args.config, &diagnostics) catch |err| switch (err) {
        error.ParseZon => diagnostics.fatal(args.config),
        else => std.process.fatal("cannot load config '{s}': {s}", .{ args.config, @errorName(err) }),
    };

    try std.zon.stringify.serialize(cfg, .{}, stdout_writer);
    try stdout_writer.writeByte('\n');
    try stdout_writer.flush();
}

/// Parse strings as `-field value` and bools as `-field`.
/// Respect field defaults; absent bools default to false. Slices borrow argv.
fn parseArgs(comptime T: type, argv: []const []const u8) !T {
    const info = @typeInfo(T).@"struct";
    const fields = info.field_names;
    comptime {
        for (info.field_types) |field_type| {
            if (field_type != []const u8 and field_type != bool)
                @compileError("argument fields must have type []const u8 or bool");
        }
    }

    var result: T = undefined;
    var seen: [fields.len]bool = @splat(false);
    inline for (fields, info.field_types, info.field_attrs) |field, field_type, attrs| {
        if (comptime attrs.defaultValue(field_type)) |value| {
            @field(result, field) = value;
        } else if (field_type == bool) {
            @field(result, field) = false;
        }
    }
    var index: usize = 0;
    while (index < argv.len) : (index += 1) {
        const flag = argv[index];
        inline for (fields, 0..) |field, field_index| {
            if (std.mem.eql(u8, flag, "-" ++ field)) {
                if (seen[field_index]) return error.DuplicateArgument;
                if (info.field_types[field_index] == bool) {
                    @field(result, field) = true;
                } else {
                    if (index + 1 == argv.len or argv[index + 1].len == 0 or
                        std.mem.startsWith(u8, argv[index + 1], "-"))
                        return error.MissingArgumentValue;
                    index += 1;
                    @field(result, field) = argv[index];
                }
                seen[field_index] = true;
                break;
            }
        } else return error.UnknownArgument;
    }
    inline for (info.field_types, info.field_attrs, 0..) |field_type, attrs, field_index| {
        if (field_type != bool and attrs.default_value_ptr == null and !seen[field_index])
            return error.MissingArgument;
    }
    return result;
}

test "config argument value is unambiguous" {
    const args = try parseArgs(Args, &.{ "-config", "config.zon" });
    try std.testing.expectEqualStrings("config.zon", args.config);
    try std.testing.expect(!args.help);
    try std.testing.expect(!args.version);
    try std.testing.expectError(error.MissingArgumentValue, parseArgs(Args, &.{"-config"}));
    try std.testing.expectError(error.MissingArgumentValue, parseArgs(Args, &.{ "-config", "" }));
    try std.testing.expectError(error.MissingArgumentValue, parseArgs(Args, &.{ "-config", "-other" }));
    try std.testing.expectError(error.UnknownArgument, parseArgs(Args, &.{"--unknown"}));
    try std.testing.expectError(error.UnknownArgument, parseArgs(Args, &.{ "-config", "config.zon", "extra" }));
    try std.testing.expectError(error.DuplicateArgument, parseArgs(Args, &.{ "-config", "a.zon", "-config", "b.zon" }));
}

test "generic arguments are matched by field name in any order" {
    const Options = struct { config: []const u8, output: []const u8 };
    const args = try parseArgs(Options, &.{ "-output", "out.zon", "-config", "config.zon" });
    try std.testing.expectEqualStrings("config.zon", args.config);
    try std.testing.expectEqualStrings("out.zon", args.output);
    try std.testing.expectError(error.MissingArgument, parseArgs(Options, &.{ "-config", "config.zon" }));
    try std.testing.expectError(error.DuplicateArgument, parseArgs(Options, &.{ "-output", "a", "-output", "b" }));
    try std.testing.expectError(error.UnknownArgument, parseArgs(Options, &.{ "-unknown", "value" }));
}

test "boolean flags do not consume values and preserve defaults" {
    const help = try parseArgs(Args, &.{"-help"});
    try std.testing.expect(help.help);
    try std.testing.expectEqualStrings("", help.config);
    inline for (.{
        &[_][]const u8{ "-help", "-config", "config.zon" },
        &[_][]const u8{ "-config", "config.zon", "-help" },
    }) |argv| {
        const args = try parseArgs(Args, argv);
        try std.testing.expect(args.help);
        try std.testing.expectEqualStrings("config.zon", args.config);
    }
    try std.testing.expectError(error.DuplicateArgument, parseArgs(Args, &.{ "-help", "-help" }));
    try std.testing.expectError(error.UnknownArgument, parseArgs(Args, &.{ "-help", "true" }));
    const Options = struct { verbose: bool, output: []const u8 = "stdout" };
    const defaults = try parseArgs(Options, &.{});
    try std.testing.expect(!defaults.verbose);
    try std.testing.expectEqualStrings("stdout", defaults.output);
    try std.testing.expect((try parseArgs(Options, &.{"-verbose"})).verbose);
}

test "version flag does not require a config" {
    const args = try parseArgs(Args, &.{"-version"});
    try std.testing.expect(args.version);
    try std.testing.expect(!args.help);
    try std.testing.expectEqualStrings("", args.config);
    const combined = try parseArgs(Args, &.{ "-version", "-help", "-config", "config.zon" });
    try std.testing.expect(combined.version and combined.help);
    try std.testing.expectEqualStrings("config.zon", combined.config);
    try std.testing.expectError(error.DuplicateArgument, parseArgs(Args, &.{ "-version", "-version" }));
}

test "runner backends implement the same download interface" {
    const Download = *const fn (Io, std.mem.Allocator, []const u8, []const u8) anyerror!void;
    const backends = [_]Download{ github.download, forgejo.download };
    for (backends) |download| {
        try std.testing.expectError(error.InvalidVersion, download(std.testing.io, std.testing.allocator, "unused", "../../etc"));
        try std.testing.expectError(error.UnknownVersion, download(std.testing.io, std.testing.allocator, "unused", "0.0.0"));
    }
}

test {
    std.testing.refAllDecls(@This());
}
