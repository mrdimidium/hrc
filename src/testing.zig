// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");

/// Returns the temporary directory's absolute path, owned by the testing allocator.
pub fn tmpPath(tmp: std.testing.TmpDir) ![:0]u8 {
    var buffer: [128]u8 = undefined;
    const path = try std.fmt.bufPrint(&buffer, ".zig-cache/tmp/{s}", .{tmp.sub_path});
    return std.Io.Dir.cwd().realPathFileAlloc(std.testing.io, path, std.testing.allocator);
}
