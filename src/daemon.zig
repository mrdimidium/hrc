// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const builtin = @import("builtin");

const Io = std.Io;

const api = @import("api.zig");
const config_schema = @import("config.zig");
const github = @import("github.zig");

const Self = @This();

io: Io,
allocator: std.mem.Allocator,

config: Config,
environ: *const std.process.Environ.Map,

/// Stateful implementations share one tag across provider, configuration and job.
const Provider = union(enum) {
    github: github,

    pub const Config = providerData("Config");
    pub const Job = providerData("Job");

    fn init(
        transport: *api.Api,
        allocator: std.mem.Allocator,
        environ: *const std.process.Environ.Map,
        cfg: Provider.Config,
        host: []const u8,
    ) !Provider {
        return switch (cfg) {
            inline else => |settings, tag| blk: {
                const bytes = try settings.token.load(transport.client.io, allocator, environ);
                defer {
                    std.crypto.secureZero(u8, bytes);
                    allocator.free(bytes);
                }
                // Text token files may end in LF or CRLF; other bytes are significant.
                const token = if (std.mem.endsWith(u8, bytes, "\r\n"))
                    bytes[0 .. bytes.len - 2]
                else if (std.mem.endsWith(u8, bytes, "\n"))
                    bytes[0 .. bytes.len - 1]
                else
                    bytes;
                if (token.len == 0 or std.mem.indexOfAny(u8, token, "\r\n") != null)
                    return error.InvalidToken;
                break :blk @unionInit(Provider, @tagName(tag), try @FieldType(Provider, @tagName(tag)).init(
                    transport,
                    allocator,
                    settings,
                    host,
                    token,
                ));
            },
        };
    }

    fn deinit(self: *Provider) void {
        switch (self.*) {
            inline else => |*backend| backend.deinit(),
        }
    }

    fn download(self: *Provider, dir: []const u8) !void {
        switch (self.*) {
            inline else => |*backend| try backend.download(dir),
        }
    }

    fn next(self: *Provider, allocator: std.mem.Allocator) !Job {
        return switch (self.*) {
            inline else => |*backend, tag| @unionInit(Job, @tagName(tag), try backend.next(allocator)),
        };
    }

    fn run(self: *Provider, job: Job, bindir: []const u8, dir: []const u8) !std.process.Child.Term {
        if (std.meta.activeTag(self.*) != std.meta.activeTag(job))
            return error.ProviderMismatch;

        return switch (self.*) {
            inline else => |*backend, tag| backend.run(@field(job, @tagName(tag)), bindir, dir),
        };
    }

    fn finish(self: *Provider, job: Job, succeeded: bool) !void {
        if (std.meta.activeTag(self.*) != std.meta.activeTag(job))
            return error.ProviderMismatch;

        switch (self.*) {
            inline else => |*backend, tag| try backend.finish(@field(job, @tagName(tag)), succeeded),
        }
    }
};

pub const Config = config_schema.Config(Provider.Config);

test "providers retain resolved tokens and reject malformed tokens" {
    const allocator = std.testing.allocator;
    var environ = std.process.Environ.Map.init(allocator);
    defer environ.deinit();
    try environ.put("TOKEN", "secret\r\n");
    var transport = api.Api.init(std.testing.io, allocator);
    defer transport.deinit();
    const configs = [_]Provider.Config{
        .{ .github = .{ .version = "2.338.0", .name = "github", .url = "https://github.com/org", .labels = &.{"linux"}, .runner_group_id = 1, .token = .{ .env = "TOKEN" } } },
    };
    for (configs) |cfg| {
        var provider = try Provider.init(&transport, allocator, &environ, cfg, "host");
        defer provider.deinit();
        switch (provider) {
            .github => |backend| try std.testing.expectEqualStrings("Bearer secret", backend.authorization),
        }
    }
    for ([_][]const u8{ "", "\n", "token\nextra", "token\n\n", "token\r" }) |token| {
        var cfg = configs[0];
        cfg.github.token = .{ .@"inline" = token };
        try std.testing.expectError(error.InvalidToken, Provider.init(&transport, allocator, &environ, cfg, "host"));
    }
}

/// A request owns its arena until the synchronous runner has exited.
const PreparedJob = struct {
    group: usize,
    arena: std.heap.ArenaAllocator,
    result: anyerror!Provider.Job,
};

const QueueEvent = union(enum) { ready: PreparedJob };

/// Config strings and the environment are borrowed and must outlive run.
/// Initialization allocates no resources; run cleans queues and HTTP state on exit.
pub fn init(
    io: Io,
    allocator: std.mem.Allocator,
    environ: *const std.process.Environ.Map,
    cfg: Config,
) Self {
    return .{
        .io = io,
        .allocator = allocator,
        .environ = environ,
        .config = cfg,
    };
}

pub fn run(self: *Self) !void {
    if (self.config.groups.len == 0)
        return;

    var transport = api.Api.init(self.io, self.allocator);
    defer transport.deinit();

    const queues = try self.allocator.alloc(Provider, self.config.groups.len);
    defer self.allocator.free(queues);

    var initialized: usize = 0;
    defer {
        for (queues[0..initialized]) |*queue|
            queue.deinit();
    }

    for (self.config.groups, queues, 0..) |provider, *queue, index| {
        queue.* = Provider.init(&transport, self.allocator, self.environ, provider, self.config.id) catch |err| {
            std.log.err("cannot initialize queue {d}: {s}", .{ index, @errorName(err) });
            return err;
        };
        initialized += 1;
    }

    const installations = try self.allocator.alloc([]const u8, queues.len);
    defer self.allocator.free(installations);
    var installed: usize = 0;
    defer {
        for (installations[0..installed]) |path|
            self.allocator.free(path);
    }

    for (queues, installations) |*queue, *installation| {
        installation.* = try self.prepareRunner(queue);
        installed += 1;
    }

    // One outstanding next() per queue: cancellation can always fit all results.
    const buffer = try self.allocator.alloc(QueueEvent, queues.len);
    defer self.allocator.free(buffer);

    var select: Io.Select(QueueEvent) = .init(self.io, buffer);
    defer {
        while (select.cancel()) |event| {
            var ready = event.ready;
            defer ready.arena.deinit();

            if (ready.result) |request| {
                queues[ready.group].finish(request, false) catch |err|
                    std.log.warn("cannot remove unused runner for queue {d}: {s}", .{ ready.group, @errorName(err) });
            } else |_| {}
        }
    }

    for (queues, 0..) |*queue, index| {
        try select.concurrent(.ready, waitQueue, .{ self, queue, index });
    }

    while (true) {
        var ready = (try select.await()).ready;
        defer ready.arena.deinit();

        const request = try ready.result;
        const term = self.runJob(&queues[ready.group], request, installations[ready.group]) catch |err| {
            queues[ready.group].finish(request, false) catch |cleanup_err|
                std.log.warn("cannot remove failed runner for queue {d}: {s}", .{ ready.group, @errorName(cleanup_err) });
            if (err == error.Canceled) return err;
            std.log.err("runner for queue {d}: {s}", .{ ready.group, @errorName(err) });
            try Io.sleep(self.io, .fromSeconds(5), .awake);
            try select.concurrent(.ready, waitQueue, .{ self, &queues[ready.group], ready.group });
            continue;
        };
        queues[ready.group].finish(request, term.success()) catch |err|
            std.log.warn("cannot remove runner for queue {d}: {s}", .{ ready.group, @errorName(err) });
        std.log.info("runner for queue {d}: {f}", .{ ready.group, term });

        try select.concurrent(.ready, waitQueue, .{ self, &queues[ready.group], ready.group });
    }
}

/// Runs installed binaries with a unique temporary directory under rundir.
/// Request strings are borrowed for the duration of this call.
fn runJob(
    self: *Self,
    provider: *Provider,
    request: Provider.Job,
    installation: []const u8,
) !std.process.Child.Term {
    if (std.meta.activeTag(provider.*) != std.meta.activeTag(request))
        return error.ProviderMismatch;

    const cwd = Io.Dir.cwd();
    try cwd.createDirPath(self.io, self.config.rundir);

    var path_buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const path = while (true) {
        var random: [16]u8 = undefined;
        try self.io.randomSecure(&random);

        const path = try std.fmt.bufPrint(&path_buffer, "{s}{s}{x}", .{ self.config.rundir, std.fs.path.sep_str, &random });
        cwd.createDir(self.io, path, if (builtin.os.tag == .windows) .default_dir else .fromMode(0o700)) catch |err| switch (err) {
            error.PathAlreadyExists => continue,
            else => return err,
        };
        break path;
    };
    defer cwd.deleteTree(self.io, path) catch |err| {
        std.log.err("cannot clean runner directory '{s}': {s}", .{ path, @errorName(err) });
    };

    return provider.run(request, installation, path);
}

/// Publish complete installations atomically; repeated groups and restarts reuse them.
fn prepareRunner(self: *Self, provider: *Provider) ![]const u8 {
    const version = switch (provider.*) {
        inline else => |backend| backend.config.version,
    };
    _ = try std.SemanticVersion.parse(version);

    const cwd = Io.Dir.cwd();
    _ = try cwd.createDirPathStatus(self.io, self.config.bindir, if (builtin.os.tag == .windows) .default_dir else .fromMode(0o700));

    var directory = try cwd.openDir(self.io, self.config.bindir, .{});
    defer directory.close(self.io);

    var root_buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const root = root_buffer[0..try cwd.realPathFile(self.io, self.config.bindir, &root_buffer)];

    const name = try std.fmt.allocPrint(self.allocator, "{s}-{s}", .{ @tagName(std.meta.activeTag(provider.*)), version });
    defer self.allocator.free(name);

    const path = try std.fs.path.join(self.allocator, &.{ root, name });
    errdefer self.allocator.free(path);

    const marker = try std.fs.path.join(self.allocator, &.{ path, ".complete" });
    defer self.allocator.free(marker);

    if (cwd.access(self.io, marker, .{})) |_| return path else |err| switch (err) {
        error.FileNotFound => {},
        else => return err,
    }

    var random: [16]u8 = undefined;
    try self.io.randomSecure(&random);
    const staging = try std.fmt.allocPrint(self.allocator, "{s}-install-{x}", .{ path, &random });
    defer self.allocator.free(staging);

    // download owns creation and cleanup on failure; successful staging is ours.
    try provider.download(staging);
    defer cwd.deleteTree(self.io, staging) catch {};
    {
        var installed = try cwd.openDir(self.io, staging, .{});
        defer installed.close(self.io);
        const complete = try installed.createFile(self.io, ".complete", .{ .exclusive = true });
        complete.close(self.io);
    }
    try cwd.renamePreserve(staging, cwd, path, self.io);
    return path;
}

fn waitQueue(
    self: *Self,
    queue: *Provider,
    index: usize,
) PreparedJob {
    var arena = std.heap.ArenaAllocator.init(self.allocator);

    while (true) {
        const request = queue.next(arena.allocator()) catch |err| {
            if (!transient(err))
                return .{ .group = index, .arena = arena, .result = err };

            std.log.warn("queue {d}: {s}; retrying in 5 seconds", .{ index, @errorName(err) });
            _ = arena.reset(.free_all);
            Io.sleep(self.io, .fromSeconds(5), .awake) catch |sleep_err|
                return .{ .group = index, .arena = arena, .result = sleep_err };
            continue;
        };

        return .{ .group = index, .arena = arena, .result = request };
    }
}

fn transient(err: anyerror) bool {
    return switch (err) {
        error.Forbidden,
        error.Unauthorized,
        error.InvalidQueueConfig,
        error.InvalidToken,
        error.InvalidHeader,
        error.InvalidResponse,
        error.InvalidLabels,
        error.NotFound,
        => false,
        else => err != error.Canceled,
    };
}

/// Preserve provider field names and tags while selecting each nested data type.
fn providerData(comptime name: []const u8) type {
    const info = @typeInfo(Provider).@"union";
    var types: [info.field_types.len]type = undefined;

    for (info.field_types, 0..) |Backend, index| {
        types[index] = @field(Backend, name);
    }

    return @Union(.auto, info.tag_type, info.field_names, &types, &@as([types.len]std.lang.Type.Union.FieldAttributes, @splat(.{})));
}

// Explicit provider contract; changing a signature fails during compilation.
comptime {
    for (@typeInfo(Provider).@"union".field_types) |Backend| {
        const initialize: *const fn (*api.Api, std.mem.Allocator, Backend.Config, []const u8, []const u8) anyerror!Backend = Backend.init;
        const destroy: *const fn (*Backend) void = Backend.deinit;
        const download: *const fn (*Backend, []const u8) anyerror!void = Backend.download;
        const next: *const fn (*Backend, std.mem.Allocator) anyerror!Backend.Job = Backend.next;
        const execute: *const fn (*Backend, Backend.Job, []const u8, []const u8) anyerror!std.process.Child.Term = Backend.run;
        const finish: *const fn (*Backend, Backend.Job, bool) anyerror!void = Backend.finish;
        _ = .{ initialize, destroy, download, next, execute, finish };
    }
}

test "runner backends reject invalid and unknown release versions" {
    var transport = api.Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    inline for (@typeInfo(Provider).@"union".field_types) |Backend| {
        const cfg: Backend.Config = .{
            .version = "../../etc",
            .runner_group_id = 1,
            .url = "https://github.com/acme",
            .name = "build",
            .labels = &.{"linux"},
        };
        var provider = try Backend.init(&transport, std.testing.allocator, cfg, "host", "token");
        defer provider.deinit();
        try std.testing.expectError(error.InvalidVersion, provider.download("unused"));
        provider.config.version = "0.0.0";
        try std.testing.expectError(error.UnknownVersion, provider.download("unused"));
    }
}

test "runJob cleans failed workspaces" {
    const io = std.testing.io;
    var tmp = std.testing.tmpDir(.{ .iterate = true });
    defer tmp.cleanup();
    const root = try @import("testing.zig").tmpPath(tmp);
    defer std.testing.allocator.free(root);
    var transport = api.Api.init(io, std.testing.allocator);
    defer transport.deinit();
    var environ = std.process.Environ.Map.init(std.testing.allocator);
    defer environ.deinit();
    var daemon = init(io, std.testing.allocator, &environ, .{ .id = "host", .rundir = root, .groups = &.{} });
    const request: Provider.Job = .{ .github = .{ .jit_config = "" } };
    var github_provider: Provider = .{ .github = try github.init(&transport, std.testing.allocator, .{
        .version = "0.0.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme",
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "token") };
    defer github_provider.deinit();
    try std.testing.expectError(error.InvalidJobRequest, daemon.runJob(&github_provider, request, root));
    var entries = tmp.dir.iterate();
    try std.testing.expectEqual(@as(?Io.Dir.Entry, null), try entries.next(io));
}

test "Select receives a ready queue while another is blocked in HTTP and cancels that wait" {
    const Server = api.TestServer;
    const io = std.testing.io;
    const allocator = std.testing.allocator;
    var blocked = try Server.init();
    defer blocked.deinit();
    var available = try Server.init();
    defer available.deinit();
    var entered: Io.Event = .unset;
    var release: Io.Event = .unset;
    const blocked_steps = [_]Server.Step{
        .{ .method = "POST", .target = "/orgs/acme/actions/runners/registration-token", .authorization = "Bearer pat", .reply = "{\"token\":\"registration\"}" },
        .{ .method = "POST", .target = "/actions/runner-registration", .authorization = "RemoteAuth registration", .reply = "{\"url\":\"$URL\",\"token\":\"admin\"}" },
        .{ .method = "GET", .target = "/_apis/runtime/runnerscalesets?runnerGroupId=1&name=host-build&api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"value\":[{\"id\":7}]}" },
        .{ .method = "PATCH", .target = "/_apis/runtime/runnerscalesets/7?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"id\":7}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/sessions?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"sessionId\":\"session\",\"messageQueueUrl\":\"$URL/queue\",\"messageQueueAccessToken\":\"queue-token\",\"statistics\":{\"totalAssignedJobs\":1}}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/generatejitconfig?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"runner\":{\"id\":123},\"encodedJITConfig\":\"first\"}", .entered = &entered, .release = &release },
        .{ .method = "DELETE", .target = "/_apis/runtime/runnerscalesets/7/sessions/session?api-version=6.0-preview", .authorization = "Bearer admin", .status = 204 },
    };
    const available_steps = [_]Server.Step{
        .{ .method = "POST", .target = "/orgs/acme/actions/runners/registration-token", .authorization = "Bearer pat", .reply = "{\"token\":\"registration\"}" },
        .{ .method = "POST", .target = "/actions/runner-registration", .authorization = "RemoteAuth registration", .reply = "{\"url\":\"$URL\",\"token\":\"admin\"}" },
        .{ .method = "GET", .target = "/_apis/runtime/runnerscalesets?runnerGroupId=1&name=host-build&api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"value\":[{\"id\":7}]}" },
        .{ .method = "PATCH", .target = "/_apis/runtime/runnerscalesets/7?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"id\":7}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/sessions?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"sessionId\":\"session\",\"messageQueueUrl\":\"$URL/queue\",\"messageQueueAccessToken\":\"queue-token\",\"statistics\":{\"totalAssignedJobs\":1}}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/generatejitconfig?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"runner\":{\"id\":123},\"encodedJITConfig\":\"first\"}" },
        .{ .method = "DELETE", .target = "/_apis/distributedtask/pools/0/agents/123?api-version=6.0-preview", .authorization = "Bearer admin", .status = 404 },
        .{ .method = "DELETE", .target = "/_apis/runtime/runnerscalesets/7/sessions/session?api-version=6.0-preview", .authorization = "Bearer admin", .status = 204 },
    };
    var blocked_peer = try Io.concurrent(io, Server.serve, .{ &blocked, &blocked_steps });
    defer blocked_peer.cancel(io) catch {};
    var available_peer = try Io.concurrent(io, Server.serve, .{ &available, &available_steps });
    defer available_peer.cancel(io) catch {};
    var transport = api.Api.init(io, allocator);
    defer transport.deinit();
    var environ = std.process.Environ.Map.init(allocator);
    defer environ.deinit();
    var daemon = init(io, allocator, &environ, .{ .id = "host", .groups = &.{} });
    var queues: [2]Provider = undefined;
    queues[0] = .{ .github = try github.init(&transport, allocator, .{
        .version = "2.338.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme",
        .api_url = blocked.url,
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "pat") };
    defer queues[0].deinit();
    queues[1] = .{ .github = try github.init(&transport, allocator, .{
        .version = "2.338.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme",
        .api_url = available.url,
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "pat") };
    var available_alive = true;
    defer if (available_alive) queues[1].deinit();
    var buffer: [2]QueueEvent = undefined;
    var select: Io.Select(QueueEvent) = .init(io, &buffer);
    defer while (select.cancel()) |event| {
        var ready = event.ready;
        ready.arena.deinit();
    };
    try select.concurrent(.ready, waitQueue, .{ &daemon, &queues[0], @as(usize, 0) });
    try entered.wait(io);
    try select.concurrent(.ready, waitQueue, .{ &daemon, &queues[1], @as(usize, 1) });
    var ready = (try select.await()).ready;
    defer ready.arena.deinit();
    try std.testing.expectEqual(@as(usize, 1), ready.group);
    const request = try ready.result;
    try std.testing.expectEqualStrings("first", request.github.jit_config);
    try queues[1].finish(request, false);
    queues[1].deinit();
    available_alive = false;
    try available_peer.await(io);
    var canceled = select.cancel().?.ready;
    defer canceled.arena.deinit();
    try std.testing.expectEqual(@as(usize, 0), canceled.group);
    try std.testing.expectError(error.Canceled, canceled.result);
    // The mock peer is still blocked in the canceled request.
    queues[0].github.session.?.deinit();
    queues[0].github.session = null;
}

test "cached runner serves repeated jobs without downloading or retaining workspaces" {
    if (builtin.os.tag == .windows) return error.SkipZigTest;
    const io = std.testing.io;
    const allocator = std.testing.allocator;
    var tmp = std.testing.tmpDir(.{ .iterate = true });
    defer tmp.cleanup();
    try tmp.dir.createDirPath(io, "github-2.338.0/bin");
    try tmp.dir.writeFile(io, .{ .sub_path = "github-2.338.0/.complete", .data = "" });
    try tmp.dir.writeFile(io, .{ .sub_path = "github-2.338.0/bin/Runner.Listener", .data =
        \\#!/bin/sh
        \\[ "$1" = run ] && [ "$2" = --jitconfig ] && [ ! -d bin ] || exit 98
        \\touch job-output
        \\exit 0
        \\
    });
    const file = try tmp.dir.openFile(io, "github-2.338.0/bin/Runner.Listener", .{});
    defer file.close(io);
    try file.setPermissions(io, .fromMode(0o755));
    const root = try @import("testing.zig").tmpPath(tmp);
    defer std.testing.allocator.free(root);
    const workdir = try std.fs.path.join(allocator, &.{ root, "jobs" });
    defer allocator.free(workdir);
    var environ = std.process.Environ.Map.init(allocator);
    defer environ.deinit();
    var daemon = init(io, allocator, &environ, .{ .id = "host", .bindir = root, .rundir = workdir, .groups = &.{} });
    var transport = api.Api.init(io, allocator);
    defer transport.deinit();
    var provider: Provider = .{ .github = try github.init(&transport, allocator, .{
        .version = "2.338.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme",
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "token") };
    defer provider.deinit();
    const installation = try daemon.prepareRunner(&provider);
    defer allocator.free(installation);
    try std.testing.expect(std.fs.path.isAbsolute(installation));
    const reused = try daemon.prepareRunner(&provider);
    defer allocator.free(reused);
    try std.testing.expectEqualStrings(installation, reused);
    var restarted = init(io, allocator, &environ, daemon.config);
    const after_restart = try restarted.prepareRunner(&provider);
    defer allocator.free(after_restart);
    try std.testing.expectEqualStrings(installation, after_restart);
    const request: Provider.Job = .{ .github = .{ .jit_config = "eyIucnVubmVyIjoiZXlKM2IzSnJSbTlzWkdWeUlqb2lYM2R2Y21zaUxDSmhaMlZ1ZEVsa0lqb3hNak45IiwiLmNyZWRlbnRpYWxzIjoiYjNCaGNYVmxJR055WldSbGJuUnBZV3h6In0=" } };
    for (0..2) |_| {
        const term = try daemon.runJob(&provider, request, installation);
        try std.testing.expect(term.success());
        var jobs = try tmp.dir.openDir(io, "jobs", .{ .iterate = true });
        defer jobs.close(io);
        var entries = jobs.iterate();
        try std.testing.expectEqual(@as(?Io.Dir.Entry, null), try entries.next(io));
        try tmp.dir.access(io, "github-2.338.0/bin/Runner.Listener", .{});
        try std.testing.expectError(error.FileNotFound, tmp.dir.access(io, "github-2.338.0/.credentials", .{}));
    }
}
