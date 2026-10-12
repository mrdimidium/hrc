// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const builtin = @import("builtin");
const api = @import("api.zig");
const http = @import("github_api.zig");
const Io = std.Io;
const Self = @This();

config: Config,

/// A host-owned scale set listener with capacity one until runner supervision is added.
/// next/finish are called sequentially; init only validates and allocates local state.
allocator: std.mem.Allocator,

transport: *api.Api,

name: []u8,
owner: []u8,
repo: ?[]u8,

authorization: []u8,

connection: ?std.json.Parsed(http.Connection) = null,
session: ?std.json.Parsed(http.Session) = null,
scale_set_id: u64 = 0,
last_message_id: u64 = 0,
pending_ack: ?u64 = null,
assigned: u32 = 0,
runner_id: ?u64 = null,

pub const Config = struct {
    /// Must match the runner shipped with this hrc build.
    version: []const u8 = @import("build_options").runner_version,

    /// Unique group suffix; scale set name is <host id>-<name>.
    name: []const u8 = "",

    /// Organization or repository URL, e.g. https://github.com/mrdimidium/hrc.
    url: []const u8 = "",

    /// Workflow labels advertised by the runner scale set.
    labels: []const []const u8 = &.{},

    /// Explicit GitHub runner group ID in which to create the scale set.
    runner_group_id: u64,

    /// Secret containing a PAT with permission to manage runners.
    token: @import("secret.zig").Secret = .{ .env = "GITHUB_TOKEN" },

    /// GitHub.com by default; can point to a GHES API or a local test server.
    api_url: []const u8 = "https://api.github.com",
};

pub const Job = struct {
    /// A fresh encoded JIT configuration obtained from GitHub.
    jit_config: []const u8,
    runner_id: u64 = 0,
};

const json_headers = [_]std.http.Header{.{ .name = "Accept", .value = "application/json; api-version=6.0-preview" }};

pub fn init(transport: *api.Api, allocator: std.mem.Allocator, cfg: Config, host: []const u8, token: []const u8) !Self {
    if (cfg.url.len == 0 or cfg.name.len == 0 or cfg.labels.len == 0 or token.len == 0)
        return error.InvalidQueueConfig;

    for (cfg.labels) |label|
        if (label.len == 0) return error.InvalidLabels;

    var scratch = std.heap.ArenaAllocator.init(allocator);
    defer scratch.deinit();

    const uri = try std.Uri.parse(cfg.url);
    const path = try uri.path.toRawMaybeAlloc(scratch.allocator());
    var parts = std.mem.tokenizeScalar(u8, path, '/');

    const owner = try allocator.dupe(u8, parts.next() orelse return error.InvalidQueueConfig);
    errdefer allocator.free(owner);

    const repo = if (parts.next()) |name| try allocator.dupe(u8, name) else null;
    errdefer if (repo) |name| allocator.free(name);

    if (parts.next() != null)
        return error.InvalidQueueConfig;

    const name = try std.fmt.allocPrint(allocator, "{s}-{s}", .{ host, cfg.name });
    errdefer allocator.free(name);

    return .{
        .config = cfg,
        .allocator = allocator,
        .transport = transport,

        .name = name,
        .owner = owner,
        .repo = repo,

        .authorization = try std.fmt.allocPrint(allocator, "Bearer {s}", .{token}),
    };
}

pub fn deinit(q: *Self) void {
    if (q.session) |session| {
        // Cleanup must remain possible after cancellation of a pending poll.
        const old = q.transport.client.io.swapCancelProtection(.blocked);
        defer _ = q.transport.client.io.swapCancelProtection(old);

        var scratch = std.heap.ArenaAllocator.init(q.allocator);
        defer scratch.deinit();
        const a = scratch.allocator();
        const response = q.service(.delete_session, a, .{
            .path = .{ .scaleSetId = q.scale_set_id, .sessionId = session.value.sessionId },
            .query = .{},
        }) catch |err| blk: {
            std.log.warn("cannot close GitHub listener '{s}': {s}", .{ q.name, @errorName(err) });
            break :blk null;
        };
        if (response) |value| value.deinit();
        session.deinit();
        q.session = null;
    }

    if (q.connection) |connection|
        connection.deinit();
    q.allocator.free(q.name);
    q.allocator.free(q.owner);
    if (q.repo) |repo| q.allocator.free(repo);
    q.allocator.free(q.authorization);
}

/// Copy the packaged distribution into a writable installation for JIT credentials.
pub fn install(self: *Self, source: []const u8, dst: []const u8) anyerror!void {
    const io = self.transport.client.io;
    _ = try std.SemanticVersion.parse(self.config.version);
    if (!std.mem.eql(u8, self.config.version, @import("build_options").runner_version))
        return error.RunnerVersionMismatch;
    const cwd = Io.Dir.cwd();
    var from = try cwd.openDir(io, source, .{ .iterate = true });
    defer from.close(io);
    const version = try from.readFileAlloc(io, "VERSION", self.allocator, .limited(128));
    defer self.allocator.free(version);
    if (!std.mem.eql(u8, std.mem.trim(u8, version, " \r\n"), self.config.version))
        return error.RunnerVersionMismatch;
    try cwd.createDir(io, dst, .default_dir);
    errdefer cwd.deleteTree(io, dst) catch {};
    var to = try cwd.openDir(io, dst, .{});
    defer to.close(io);
    var walker = try from.walk(self.allocator);
    defer walker.deinit();
    while (try walker.next(io)) |entry| switch (entry.kind) {
        .directory => try to.createDirPath(io, entry.path),
        .file => try from.copyFile(entry.path, to, entry.path, io, .{}),
        .sym_link => {
            var buffer: [Io.Dir.max_path_bytes]u8 = undefined;
            const size = try from.readLink(io, entry.path, &buffer);
            try to.symLink(io, buffer[0..size], entry.path, .{});
        },
        else => return error.UnexpectedRunnerFile,
    };
}

pub fn next(q: *Self, arena: std.mem.Allocator) !Job {
    if (q.session == null) try q.connect();
    while (true) {
        if (q.pending_ack) |id| {
            var scratch = std.heap.ArenaAllocator.init(q.allocator);
            defer scratch.deinit();
            const acknowledged = try q.queueRequest(.acknowledge_message, scratch.allocator(), .{ .path = .{ .messageId = id } });
            acknowledged.deinit();
            q.last_message_id = id;
            q.pending_ack = null;
        }
        if (q.assigned > 0 and q.runner_id == null) {
            var scratch = std.heap.ArenaAllocator.init(q.allocator);
            defer scratch.deinit();
            const a = scratch.allocator();
            const name = try runnerName(q.transport.client.io, a, q.name);
            const jit = try q.service(.generate_jit_config, a, .{
                .path = .{ .scaleSetId = q.scale_set_id },
                .query = .{},
                .body = .{ .name = name, .workFolder = "_work" },
            });
            defer jit.deinit();
            if (jit.value.encodedJITConfig.len == 0) return error.InvalidResponse;
            errdefer q.finish(.{ .jit_config = jit.value.encodedJITConfig, .runner_id = jit.value.runner.id }, false) catch {};
            const jit_config = try arena.dupe(u8, jit.value.encodedJITConfig);
            q.runner_id = jit.value.runner.id;
            return .{ .jit_config = jit_config, .runner_id = jit.value.runner.id };
        }
        var scratch = std.heap.ArenaAllocator.init(q.allocator);
        defer scratch.deinit();
        const a = scratch.allocator();
        const response = try q.queueRequest(.poll_messages, a, .{
            .query = .{ .lastMessageId = q.last_message_id },
            .headers = .{ .@"X-ScaleSetMaxCapacity" = 1 },
        });
        defer response.deinit();
        if (response.status == .accepted) continue;
        const message = response.value orelse return error.InvalidResponse;
        const events = if (message.body.len == 0) null else try std.json.parseFromSlice([]http.Event, a, message.body, .{ .ignore_unknown_fields = true });
        q.assigned = message.statistics.totalAssignedJobs;
        if (events) |parsed| {
            for (parsed.value) |event| {
                if (std.mem.eql(u8, event.messageType, "JobCompleted")) {
                    if (q.runner_id != null and q.runner_id.? == event.runnerId) q.runner_id = null;
                }
            }
            if (q.runner_id == null and q.assigned == 0) {
                // Acquire at most one job, matching the current single-runner capacity.
                for (parsed.value) |event| {
                    if (!std.mem.eql(u8, event.messageType, "JobAvailable")) continue;
                    const acquired = try q.queueRequest(.acquire_jobs, a, .{
                        .path = .{ .scaleSetId = q.scale_set_id },
                        .query = .{},
                        .body = &.{event.runnerRequestId},
                    });
                    defer acquired.deinit();
                    if (acquired.value.value.len > 0) {
                        q.assigned = 1;
                        break;
                    }
                }
            }
        }
        q.pending_ack = message.messageId;
        // Zero assigned jobs also reconciles a missed completion notification.
        if (q.assigned == 0) q.runner_id = null;
    }
}

/// Runs shared binaries by absolute path with a separate job workspace.
/// The returned status describes the listener process, not the workflow result.
pub fn run(
    self: *Self,
    request: Job,
    bindir: []const u8,
    dir: []const u8,
) !std.process.Child.Term {
    const io = self.transport.client.io;
    const allocator = self.allocator;
    if (request.jit_config.len == 0) return error.InvalidJobRequest;

    if (!std.fs.path.isAbsolute(bindir))
        return error.InvalidRunnerPath;

    var directory = try Io.Dir.cwd().openDir(io, dir, .{});
    defer directory.close(io);
    var root_buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const root = root_buffer[0..try Io.Dir.cwd().realPathFile(io, dir, &root_buffer)];
    const executable = try std.fs.path.join(allocator, &.{ bindir, "bin", if (builtin.os.tag == .windows) "Runner.Listener.exe" else "Runner.Listener" });
    defer allocator.free(executable);

    var scratch = std.heap.ArenaAllocator.init(allocator);
    defer scratch.deinit();
    const jit = try jobConfig(scratch.allocator(), request.jit_config, root);

    // The listener stores JIT credentials beside shared binaries, regardless of cwd.
    // Runs are sequential; remove these credentials even when startup fails.
    defer for ([_][]const u8{ ".runner", ".runner_migrated", ".credentials", ".credentials_migrated", ".credentials_rsaparams" }) |name| {
        const path = std.fs.path.join(scratch.allocator(), &.{ bindir, name }) catch continue;
        Io.Dir.cwd().deleteFile(io, path) catch {};
    };

    // Invoke the listener directly: run.sh/run.cmd can hide its failure status.
    var child = try std.process.spawn(io, .{
        .argv = &.{ executable, "run", "--jitconfig", jit },
        .cwd = .{ .path = root },
        .stdin = .ignore,
    });
    defer child.kill(io);
    return child.wait(io);
}

pub fn finish(q: *Self, request: Job, succeeded: bool) !void {
    if (!succeeded) {
        q.runner_id = null;
        q.assigned = 0; // Poll again before replacing a failed runner.
    }
    var scratch = std.heap.ArenaAllocator.init(q.allocator);
    defer scratch.deinit();
    const a = scratch.allocator();
    const response = q.service(.delete_runner, a, .{
        .path = .{ .runnerId = request.runner_id },
        .query = .{},
    }) catch |err| switch (err) {
        error.NotFound => return,
        else => return err,
    };
    response.deinit();
    // On success, keep the reservation until JobCompleted or a fresh zero-demand snapshot.
}

fn refreshConnection(q: *Self) !void {
    var scratch = std.heap.ArenaAllocator.init(q.allocator);
    defer scratch.deinit();
    const a = scratch.allocator();
    const uri = try std.Uri.parse(q.config.api_url);
    const client = http.Client.init(q.transport, uri, q.authorization);
    const token = if (q.repo) |repo| blk: {
        const result = try client.call(.repo_registration_token, a, .{
            .path = .{ .owner = q.owner, .repo = repo },
            .extra_headers = &json_headers,
        });
        defer result.deinit();
        break :blk try a.dupe(u8, result.value.token);
    } else blk: {
        const result = try client.call(.org_registration_token, a, .{
            .path = .{ .org = q.owner },
            .extra_headers = &json_headers,
        });
        defer result.deinit();
        break :blk try a.dupe(u8, result.value.token);
    };
    const auth = try std.fmt.allocPrint(a, "RemoteAuth {s}", .{token});
    const remote = http.Client.init(q.transport, uri, auth);
    const result = try remote.call(.register, q.allocator, .{
        .body = .{ .url = q.config.url, .runner_event = .register },
        .extra_headers = &json_headers,
    });
    errdefer result.deinit();
    if (result.value.url.len == 0 or result.value.token.len == 0) return error.InvalidResponse;
    if (q.connection) |old| old.deinit();
    q.connection = result.parsed.?;
}

fn service(q: *Self, comptime operation: http.Client.Operation, allocator: std.mem.Allocator, args: http.Client.Args(operation)) !http.Client.Result(operation) {
    var retried = false;
    while (true) {
        if (q.connection == null) try q.refreshConnection();
        const connection = q.connection.?.value;
        const auth = try std.fmt.allocPrint(allocator, "Bearer {s}", .{connection.token});
        defer allocator.free(auth);
        const client = http.Client.init(q.transport, try std.Uri.parse(connection.url), auth);
        var request = args;
        request.extra_headers = &json_headers;
        return client.call(operation, allocator, request) catch |err| {
            if (err != error.Unauthorized or retried) return err;
            retried = true;
            try q.refreshConnection();
            continue;
        };
    }
}

fn connect(q: *Self) !void {
    var scratch = std.heap.ArenaAllocator.init(q.allocator);
    defer scratch.deinit();
    const a = scratch.allocator();
    const found = try q.service(.list_scale_sets, a, .{
        .query = .{ .runnerGroupId = q.config.runner_group_id, .name = q.name },
    });
    defer found.deinit();
    if (found.value.value.len > 1) return error.InvalidResponse;
    const labels = try a.alloc(http.Label, q.config.labels.len);
    for (q.config.labels, labels) |label, *out| out.* = .{ .name = label };
    const settings = http.Client.Args(.create_scale_set){ .query = .{}, .body = .{
        .name = q.name,
        .runnerGroupId = q.config.runner_group_id,
        .labels = labels,
        .RunnerSetting = .{ .disableUpdate = true },
    } };
    if (found.value.value.len == 1) {
        q.scale_set_id = found.value.value[0].id;
        const updated = try q.service(.update_scale_set, a, .{
            .path = .{ .scaleSetId = q.scale_set_id },
            .query = .{},
            .body = settings.body,
        });
        updated.deinit();
    } else {
        const created = try q.service(.create_scale_set, a, settings);
        defer created.deinit();
        q.scale_set_id = created.value.id;
    }
    const response = try q.service(.create_session, q.allocator, .{
        .path = .{ .scaleSetId = q.scale_set_id },
        .query = .{},
        .body = .{ .ownerName = q.name },
    });
    try q.replaceSession(response.parsed.?);
    q.assigned = if (q.session.?.value.statistics) |stats| stats.totalAssignedJobs else 0;
}

/// Takes ownership of parsed session strings, including on validation failure.
fn replaceSession(q: *Self, session: std.json.Parsed(http.Session)) !void {
    if (session.value.sessionId.len == 0 or session.value.messageQueueUrl.len == 0 or session.value.messageQueueAccessToken.len == 0) {
        session.deinit();
        return error.InvalidResponse;
    }
    if (q.session) |old| old.deinit();
    q.session = session;
}

fn queueRequest(q: *Self, comptime operation: http.Client.Operation, allocator: std.mem.Allocator, args: http.Client.Args(operation)) !http.Client.Result(operation) {
    var retried = false;
    while (true) {
        const session = q.session.?.value;
        var request = args;
        request.extra_headers = &json_headers;
        if (operation == .poll_messages) {
            request.uri = try std.Uri.parse(session.messageQueueUrl);
        }
        const auth = try std.fmt.allocPrint(allocator, "Bearer {s}", .{session.messageQueueAccessToken});
        defer allocator.free(auth);
        const base_uri = try std.Uri.parse(if (operation == .acknowledge_message)
            session.messageQueueUrl
        else
            q.connection.?.value.url);
        const client = http.Client.init(q.transport, base_uri, auth);
        return client.call(operation, allocator, request) catch |err| {
            if (err != error.Unauthorized or retried) return err;
            retried = true;
            const refreshed = try q.service(.refresh_session, q.allocator, .{
                .path = .{ .scaleSetId = q.scale_set_id, .sessionId = session.sessionId },
                .query = .{},
            });
            try q.replaceSession(refreshed.parsed.?);
            continue;
        };
    }
}

fn runnerName(io: std.Io, allocator: std.mem.Allocator, prefix: []const u8) ![]u8 {
    var random: [16]u8 = undefined;
    try io.randomSecure(&random);
    return std.fmt.allocPrint(allocator, "{s}-{x}", .{ prefix, &random });
}

/// JIT settings use the binary installation as root; make the work folder absolute.
fn jobConfig(allocator: std.mem.Allocator, encoded: []const u8, dir: []const u8) ![]const u8 {
    const decoder = std.base64.standard.Decoder;
    const encoder = std.base64.standard.Encoder;
    const bytes = try allocator.alloc(u8, try decoder.calcSizeForSlice(encoded));
    try decoder.decode(bytes, encoded);
    const parsed = try std.json.parseFromSlice(std.json.Value, allocator, bytes, .{});
    if (parsed.value != .object) return error.InvalidJobRequest;
    const entry = parsed.value.object.getPtr(".runner") orelse return error.InvalidJobRequest;
    if (entry.* != .string) return error.InvalidJobRequest;
    const settings_bytes = try allocator.alloc(u8, try decoder.calcSizeForSlice(entry.string));
    try decoder.decode(settings_bytes, entry.string);
    var settings = try std.json.parseFromSlice(std.json.Value, allocator, settings_bytes, .{});
    if (settings.value != .object) return error.InvalidJobRequest;
    const work = try std.fs.path.join(allocator, &.{ dir, "_work" });
    try settings.value.object.put(allocator, "workFolder", .{ .string = work });
    const rewritten = try std.json.Stringify.valueAlloc(allocator, settings.value, .{});
    const inner = try allocator.alloc(u8, encoder.calcSize(rewritten.len));
    entry.* = .{ .string = encoder.encode(inner, rewritten) };
    const outer = try std.json.Stringify.valueAlloc(allocator, parsed.value, .{});
    const result = try allocator.alloc(u8, encoder.calcSize(outer.len));
    return encoder.encode(result, outer);
}

test {
    std.testing.refAllDecls(@This());
}

test "run waits for listener and preserves its exit status" {
    if (builtin.os.tag == .windows) return error.SkipZigTest;
    const io = std.testing.io;
    var transport = api.Api.init(io, std.testing.allocator);
    defer transport.deinit();
    var provider = try init(&transport, std.testing.allocator, .{
        .version = "2.338.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme",
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "token");
    defer provider.deinit();
    var tmp = std.testing.tmpDir(.{});
    defer tmp.cleanup();
    try tmp.dir.createDir(io, "bin", .default_dir);
    try tmp.dir.writeFile(io, .{ .sub_path = "bin/Runner.Listener", .data =
        \\#!/bin/sh
        \\[ "$#" = 3 ] && [ "$1" = run ] && [ "$2" = --jitconfig ] || exit 99
        \\case "$0" in /*) ;; *) exit 98 ;; esac
        \\[ ! -d bin ] || exit 97
        \\printf '%s' "$3" > jit-config
        \\touch "$(dirname "$0")/../.credentials"
        \\exit 7
        \\
    });
    const executable = try tmp.dir.openFile(io, "bin/Runner.Listener", .{});
    defer executable.close(io);
    try executable.setPermissions(io, .fromMode(0o755));
    const root = try @import("testing.zig").tmpPath(tmp);
    defer std.testing.allocator.free(root);
    var job = std.testing.tmpDir(.{});
    defer job.cleanup();
    const workdir = try @import("testing.zig").tmpPath(job);
    defer std.testing.allocator.free(workdir);
    const jit = "eyIucnVubmVyIjoiZXlKM2IzSnJSbTlzWkdWeUlqb2lYM2R2Y21zaUxDSmhaMlZ1ZEVsa0lqb3hNak45IiwiLmNyZWRlbnRpYWxzIjoiYjNCaGNYVmxJR055WldSbGJuUnBZV3h6In0=";
    const term = try provider.run(.{ .jit_config = jit }, root, workdir);
    try std.testing.expectEqual(@as(u8, 7), term.exited);
    try std.testing.expectError(error.InvalidJobRequest, provider.run(.{ .jit_config = "" }, root, workdir));
    try std.testing.expectError(error.FileNotFound, tmp.dir.access(io, ".credentials", .{}));
    try tmp.dir.access(io, "bin/Runner.Listener", .{});

    var scratch = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer scratch.deinit();
    const a = scratch.allocator();
    const encoded = try job.dir.readFileAlloc(io, "jit-config", a, .limited(4096));
    const decoder = std.base64.standard.Decoder;
    const bytes = try a.alloc(u8, try decoder.calcSizeForSlice(encoded));
    try decoder.decode(bytes, encoded);
    const config = try std.json.parseFromSlice(std.json.Value, a, bytes, .{});
    try std.testing.expectEqualStrings("b3BhcXVlIGNyZWRlbnRpYWxz", config.value.object.get(".credentials").?.string);
    const settings_encoded = config.value.object.get(".runner").?.string;
    const settings_bytes = try a.alloc(u8, try decoder.calcSizeForSlice(settings_encoded));
    try decoder.decode(settings_bytes, settings_encoded);
    const settings = try std.json.parseFromSlice(std.json.Value, a, settings_bytes, .{});
    const expected_work = try std.fs.path.join(a, &.{ workdir, "_work" });
    try std.testing.expectEqualStrings(expected_work, settings.value.object.get("workFolder").?.string);
    try std.testing.expectEqual(@as(i64, 123), settings.value.object.get("agentId").?.integer);
}
