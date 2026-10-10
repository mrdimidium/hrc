// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const builtin = @import("builtin");
const api = @import("api.zig");
const Io = std.Io;
const Self = @This();

/// A host-owned scale set listener with capacity one until runner supervision is added.
/// next/finish are called sequentially; init only validates and allocates local state.
allocator: std.mem.Allocator,
transport: *api.Api,
config: Config,
name: []u8,
registration_url: []u8,
authorization: []u8,
connection: ?std.json.Parsed(Connection) = null,
session: ?std.json.Parsed(Session) = null,
scale_set_id: u64 = 0,
last_message_id: u64 = 0,
pending_ack: ?u64 = null,
assigned: u32 = 0,
runner_id: ?u64 = null,

pub const Config = struct {
    /// GitHub runner release version to install.
    version: []const u8,

    /// Unique group suffix; scale set name is <host id>-<name>.
    name: []const u8 = "",

    /// Organization or repository URL, e.g. https://github.com/mrdimidium/hrc.
    url: []const u8 = "",

    /// Workflow labels advertised by the runner scale set.
    labels: []const []const u8 = &.{},

    /// Explicit GitHub runner group ID in which to create the scale set.
    runner_group_id: u64,

    /// Environment variable containing a PAT with permission to manage runners.
    token_env: []const u8 = "GITHUB_TOKEN",

    /// GitHub.com by default; can point to a GHES API or a local test server.
    api_url: []const u8 = "https://api.github.com",
};

pub const Job = struct {
    /// A fresh encoded JIT configuration obtained from GitHub.
    jit_config: []const u8,
    runner_id: u64 = 0,
};

const Operation = union(enum) { poll, ack: u64, acquire };

const Connection = struct { url: []const u8, token: []const u8 };

const Statistics = struct { totalAssignedJobs: u32 = 0 };

const Session = struct {
    sessionId: []const u8,
    messageQueueUrl: []const u8,
    messageQueueAccessToken: []const u8,
    statistics: ?Statistics = null,
};

const ScaleSet = struct { id: u64 };

const Label = struct { name: []const u8, type: []const u8 = "System" };

const Message = struct { messageId: u64, body: []const u8 = "", statistics: Statistics };

const Event = struct { messageType: []const u8, runnerRequestId: u64 = 0, runnerId: u64 = 0 };

const Acquired = struct { value: []u64 };

const JitConfig = struct { runner: struct { id: u64 }, encodedJITConfig: []const u8 };

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

const json_headers = [_]std.http.Header{
    .{ .name = "Content-Type", .value = "application/json" },
    .{ .name = "Accept", .value = "application/json; api-version=6.0-preview" },
};

pub fn init(transport: *api.Api, allocator: std.mem.Allocator, cfg: Config, host: []const u8, token: []const u8) !Self {
    if (cfg.url.len == 0 or cfg.name.len == 0 or cfg.labels.len == 0 or token.len == 0)
        return error.InvalidQueueConfig;
    for (cfg.labels) |label| if (label.len == 0) return error.InvalidLabels;
    var scratch = std.heap.ArenaAllocator.init(allocator);
    defer scratch.deinit();
    const uri = try std.Uri.parse(cfg.url);
    const path = try uri.path.toRawMaybeAlloc(scratch.allocator());
    var parts = std.mem.tokenizeScalar(u8, path, '/');
    const owner = try api.escape(allocator, parts.next() orelse return error.InvalidQueueConfig);
    defer allocator.free(owner);
    const scope = if (parts.next()) |repo| blk: {
        const escaped = try api.escape(allocator, repo);
        defer allocator.free(escaped);
        if (parts.next() != null) return error.InvalidQueueConfig;
        break :blk try std.fmt.allocPrint(allocator, "repos/{s}/{s}", .{ owner, escaped });
    } else try std.fmt.allocPrint(allocator, "orgs/{s}", .{owner});
    defer allocator.free(scope);
    const registration_url = try std.fmt.allocPrint(allocator, "{s}/{s}/actions/runners/registration-token", .{ std.mem.trimEnd(u8, cfg.api_url, "/"), scope });
    errdefer allocator.free(registration_url);
    const name = try std.fmt.allocPrint(allocator, "{s}-{s}", .{ host, cfg.name });
    errdefer allocator.free(name);
    return .{
        .allocator = allocator,
        .transport = transport,
        .config = cfg,
        .name = name,
        .registration_url = registration_url,
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
        const path = std.fmt.allocPrint(a, "_apis/runtime/runnerscalesets/{d}/sessions/{s}", .{ q.scale_set_id, session.value.sessionId }) catch null;
        if (path) |p| {
            const response = q.service(a, .DELETE, p, null) catch |err| blk: {
                std.log.warn("cannot close GitHub listener '{s}': {s}", .{ q.name, @errorName(err) });
                break :blk null;
            };
            if (response) |value| value.deinit();
        }
        session.deinit();
    }
    if (q.connection) |connection| connection.deinit();
    q.allocator.free(q.name);
    q.allocator.free(q.registration_url);
    q.allocator.free(q.authorization);
}

/// Downloads and extracts the configured host runner package into a new directory.
pub fn download(self: *Self, dst: []const u8) anyerror!void {
    const transport = self.transport;
    const io = transport.client.io;
    const version = self.config.version;
    _ = try std.SemanticVersion.parse(version);

    const platform = switch (builtin.os.tag) {
        .linux => switch (builtin.cpu.arch) {
            .x86_64 => "linux-x64",
            .aarch64 => "linux-arm64",
            .arm => "linux-arm",
            else => return error.UnsupportedPlatform,
        },
        .macos => switch (builtin.cpu.arch) {
            .x86_64 => "osx-x64",
            .aarch64 => "osx-arm64",
            else => return error.UnsupportedPlatform,
        },
        .windows => switch (builtin.cpu.arch) {
            .x86_64 => "win-x64",
            .aarch64 => "win-arm64",
            else => return error.UnsupportedPlatform,
        },
        else => return error.UnsupportedPlatform,
    };
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

    try transport.download(try std.Uri.parse(url), sha, path);
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

pub fn next(q: *Self, arena: std.mem.Allocator) !Job {
    if (q.session == null) try q.connect();
    while (true) {
        if (q.pending_ack) |id| {
            var scratch = std.heap.ArenaAllocator.init(q.allocator);
            defer scratch.deinit();
            const acknowledged = try q.queueRequest(scratch.allocator(), .{ .ack = id }, null);
            acknowledged.deinit();
            q.last_message_id = id;
            q.pending_ack = null;
        }
        if (q.assigned > 0 and q.runner_id == null) {
            var scratch = std.heap.ArenaAllocator.init(q.allocator);
            defer scratch.deinit();
            const a = scratch.allocator();
            const name = try runnerName(q.transport.client.io, a, q.name);
            const body = try std.json.Stringify.valueAlloc(a, .{ .name = name, .workFolder = "_work" }, .{});
            const path = try std.fmt.allocPrint(a, "_apis/runtime/runnerscalesets/{d}/generatejitconfig", .{q.scale_set_id});
            const response = try q.service(a, .POST, path, body);
            defer response.deinit();
            const jit = try std.json.parseFromSlice(JitConfig, arena, response.body, .{
                .allocate = .alloc_always,
                .ignore_unknown_fields = true,
            });
            if (jit.value.encodedJITConfig.len == 0) return error.InvalidResponse;
            q.runner_id = jit.value.runner.id;
            return .{ .jit_config = jit.value.encodedJITConfig, .runner_id = jit.value.runner.id };
        }
        var scratch = std.heap.ArenaAllocator.init(q.allocator);
        defer scratch.deinit();
        const a = scratch.allocator();
        const response = try q.queueRequest(a, .poll, null);
        defer response.deinit();
        if (response.status == .accepted) continue;
        const message = try std.json.parseFromSlice(Message, a, response.body, .{ .ignore_unknown_fields = true });
        const events = if (message.value.body.len == 0) null else try std.json.parseFromSlice([]Event, a, message.value.body, .{ .ignore_unknown_fields = true });
        q.assigned = message.value.statistics.totalAssignedJobs;
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
                    const body = try std.json.Stringify.valueAlloc(a, &[_]u64{event.runnerRequestId}, .{});
                    const acquired = try q.queueRequest(a, .acquire, body);
                    defer acquired.deinit();
                    const ids = try std.json.parseFromSlice(Acquired, a, acquired.body, .{ .ignore_unknown_fields = true });
                    if (ids.value.value.len > 0) {
                        q.assigned = 1;
                        break;
                    }
                }
            }
        }
        q.pending_ack = message.value.messageId;
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
    const root = root_buffer[0..try directory.realPath(io, &root_buffer)];
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
    const path = try std.fmt.allocPrint(a, "_apis/distributedtask/pools/0/agents/{d}", .{request.runner_id});
    const response = q.service(a, .DELETE, path, null) catch |err| switch (err) {
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
    const registration = try q.transport.request(.POST, try std.Uri.parse(q.registration_url), q.authorization, null, &json_headers);
    defer registration.deinit();
    const token = try std.json.parseFromSlice(struct { token: []const u8 }, a, registration.body, .{ .ignore_unknown_fields = true });
    const url = try std.fmt.allocPrint(a, "{s}/actions/runner-registration", .{std.mem.trimEnd(u8, q.config.api_url, "/")});
    const auth = try std.fmt.allocPrint(a, "RemoteAuth {s}", .{token.value.token});
    const body = try std.json.Stringify.valueAlloc(a, .{ .url = q.config.url, .runner_event = "register" }, .{});
    const response = try q.transport.request(.POST, try std.Uri.parse(url), auth, body, &json_headers);
    defer response.deinit();
    const connection = try response.parse(Connection, q.allocator);
    if (connection.value.url.len == 0 or connection.value.token.len == 0) {
        connection.deinit();
        return error.InvalidResponse;
    }
    if (q.connection) |old| old.deinit();
    q.connection = connection;
}

fn service(q: *Self, allocator: std.mem.Allocator, method: std.http.Method, path: []const u8, body: ?[]u8) !api.Response {
    var retried = false;
    while (true) {
        if (q.connection == null) try q.refreshConnection();
        const connection = q.connection.?.value;
        const separator: []const u8 = if (std.mem.indexOfScalar(u8, path, '?') == null) "?" else "&";
        const url = try std.fmt.allocPrint(allocator, "{s}/{s}{s}api-version=6.0-preview", .{ std.mem.trimEnd(u8, connection.url, "/"), path, separator });
        const auth = try std.fmt.allocPrint(allocator, "Bearer {s}", .{connection.token});
        return q.transport.request(method, try std.Uri.parse(url), auth, body, &json_headers) catch |err| {
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
    const name = try api.escape(a, q.name);
    const path = try std.fmt.allocPrint(a, "_apis/runtime/runnerscalesets?runnerGroupId={d}&name={s}", .{ q.config.runner_group_id, name });
    const found = try q.service(a, .GET, path, null);
    defer found.deinit();
    const list = try std.json.parseFromSlice(struct { value: []ScaleSet }, a, found.body, .{ .ignore_unknown_fields = true });
    if (list.value.value.len > 1) return error.InvalidResponse;
    const labels = try a.alloc(Label, q.config.labels.len);
    for (q.config.labels, labels) |label, *out| out.* = .{ .name = label };
    const settings = try std.json.Stringify.valueAlloc(a, .{
        .name = q.name,
        .runnerGroupId = q.config.runner_group_id,
        .labels = labels,
        .RunnerSetting = .{ .disableUpdate = true },
    }, .{});
    if (list.value.value.len == 1) {
        q.scale_set_id = list.value.value[0].id;
        const update_path = try std.fmt.allocPrint(a, "_apis/runtime/runnerscalesets/{d}", .{q.scale_set_id});
        const updated = try q.service(a, .PATCH, update_path, settings);
        updated.deinit();
    } else {
        const response = try q.service(a, .POST, "_apis/runtime/runnerscalesets", settings);
        defer response.deinit();
        const created = try std.json.parseFromSlice(ScaleSet, a, response.body, .{ .ignore_unknown_fields = true });
        q.scale_set_id = created.value.id;
    }
    const session_path = try std.fmt.allocPrint(a, "_apis/runtime/runnerscalesets/{d}/sessions", .{q.scale_set_id});
    const body = try std.json.Stringify.valueAlloc(a, .{ .ownerName = q.name }, .{});
    const response = try q.service(a, .POST, session_path, body);
    defer response.deinit();
    try q.replaceSession(response);
    q.assigned = if (q.session.?.value.statistics) |stats| stats.totalAssignedJobs else 0;
}

fn replaceSession(q: *Self, response: api.Response) !void {
    const session = try response.parse(Session, q.allocator);
    if (session.value.sessionId.len == 0 or session.value.messageQueueUrl.len == 0 or session.value.messageQueueAccessToken.len == 0) {
        session.deinit();
        return error.InvalidResponse;
    }
    if (q.session) |old| old.deinit();
    q.session = session;
}

fn queueRequest(q: *Self, allocator: std.mem.Allocator, operation: Operation, body: ?[]u8) !api.Response {
    var retried = false;
    while (true) {
        const session = q.session.?.value;
        const url = switch (operation) {
            .poll => try std.fmt.allocPrint(allocator, "{s}{s}lastMessageId={d}", .{
                session.messageQueueUrl,
                if (std.mem.indexOfScalar(u8, session.messageQueueUrl, '?') == null) "?" else "&",
                q.last_message_id,
            }),
            .ack => |id| blk: {
                const query = std.mem.indexOfScalar(u8, session.messageQueueUrl, '?') orelse session.messageQueueUrl.len;
                break :blk try std.fmt.allocPrint(allocator, "{s}/{d}{s}", .{ session.messageQueueUrl[0..query], id, session.messageQueueUrl[query..] });
            },
            .acquire => try std.fmt.allocPrint(allocator, "{s}/_apis/runtime/runnerscalesets/{d}/acquirejobs?api-version=6.0-preview", .{
                std.mem.trimEnd(u8, q.connection.?.value.url, "/"), q.scale_set_id,
            }),
        };
        const auth = try std.fmt.allocPrint(allocator, "Bearer {s}", .{session.messageQueueAccessToken});
        const method: std.http.Method = switch (operation) {
            .poll => .GET,
            .ack => .DELETE,
            .acquire => .POST,
        };
        const headers: []const std.http.Header = if (operation == .poll) &.{
            .{ .name = "X-ScaleSetMaxCapacity", .value = "1" },
            .{ .name = "Content-Type", .value = "application/json" },
            .{ .name = "Accept", .value = "application/json; api-version=6.0-preview" },
        } else &json_headers;
        return q.transport.request(method, try std.Uri.parse(url), auth, body, headers) catch |err| {
            if (err != error.Unauthorized or retried) return err;
            retried = true;
            const path = try std.fmt.allocPrint(allocator, "_apis/runtime/runnerscalesets/{d}/sessions/{s}", .{ q.scale_set_id, session.sessionId });
            const refreshed = try q.service(allocator, .PATCH, path, null);
            defer refreshed.deinit();
            try q.replaceSession(refreshed);
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
    var buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const root = buffer[0..try tmp.dir.realPath(io, &buffer)];
    var job = std.testing.tmpDir(.{});
    defer job.cleanup();
    var job_buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const workdir = job_buffer[0..try job.dir.realPath(io, &job_buffer)];
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
