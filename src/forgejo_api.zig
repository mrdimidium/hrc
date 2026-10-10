// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const api = @import("api.zig");

const Attempt = struct {
    handle: []const u8,
    status: []const u8,
    runs_on: []const []const u8,
};

/// Base URI includes /api/v1 and the selected user/admin/org/repo scope.
pub const Client = api.HttpClient(.{
    .list_jobs = .{
        .method = .GET,
        .path = "/actions/runners/jobs",
        .Query = struct { labels: []const u8 },
        .Response = ?[]const Attempt,
    },
    .register_runner = .{
        .method = .POST,
        .path = "/actions/runners",
        .Body = struct { name: []const u8, ephemeral: bool },
        .Response = struct { id: u64, uuid: []const u8, token: []const u8 },
    },
    .delete_runner = .{
        .method = .DELETE,
        .path = "/actions/runners/{runnerId}",
        .Path = struct { runnerId: u64 },
    },
});
