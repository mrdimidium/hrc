// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const builtin = @import("builtin");
const api = @import("api.zig");
const Io = std.Io;
const Self = @This();

/// Forgejo >= 15 supplies job-attempt handles and ephemeral runner credentials.
/// One thread calls next/finish at a time; all strings in Config are borrowed.
allocator: std.mem.Allocator,
transport: *api.Api,
config: Config,
prefix: []u8,
endpoint: []u8,
authorization: []u8,
submitted: std.StringHashMap(void),

pub const Config = struct {
    /// Forgejo runner release version to install.
    version: []const u8,

    /// Unique group suffix; runner name prefix is <host id>-<name>.
    name: []const u8 = "",

    /// Forgejo instance URL, e.g. https://code.govorov.online.
    url: []const u8 = "",

    /// Workflow labels. The queue maps them to the host backend.
    labels: []const []const u8 = &.{},

    /// Scope for job polling and runner registration: user, admin, org, or repo.
    scope: Scope = .user,

    /// Environment variable containing an API token with access to the chosen scope.
    token_env: []const u8 = "FORGEJO_TOKEN",

    /// Delay in milliseconds between polls when no matching job is available.
    poll_interval_ms: u32 = 5000,
};

pub const Job = struct {
    /// Credentials for an already registered ephemeral runner.
    url: []const u8,
    uuid: []const u8,
    token: []const u8,
    /// Runner labels including their backend, e.g. "linux:host".
    labels: []const []const u8,
    /// Optional job attempt handle (Forgejo >= 15).
    handle: ?[]const u8 = null,
    runner_id: u64 = 0,
};

const Attempt = struct {
    handle: []const u8,
    status: []const u8,
    runs_on: []const []const u8,
};

const Scope = union(enum) {
    user,
    admin,
    org: []const u8,
    repo: struct { owner: []const u8, name: []const u8 },
};

const Registration = struct { id: u64, uuid: []const u8, token: []const u8 };

const TripletHashes = std.StaticStringMap([]const u8);

const version_template =
    "https://codeberg.org/ziglang/runner/releases/download/v{s}/forgejo-runner.{s}{s}";

/// SHA-256 computed from the uncompressed binaries downloaded from:
/// https://codeberg.org/ziglang/runner/releases/tag/v13.0.0-zsf
const version_checksum = std.StaticStringMap(TripletHashes).initComptime(.{
    .{ "13.0.0-zsf", TripletHashes.initComptime(.{
        .{ "aarch64-freebsd", "2c6b7c48aefe3d0ed2c4f9bd96d6e70f048add55469ab310975e1694c79709d5" },
        .{ "aarch64-linux", "283c9f6edda4755ffbf622f2699e79568a6dc703544eab5271223fe4921a5211" },
        .{ "aarch64-macos", "f122a742f11bd12e42f500537a7bea842dfad19e2b8ea71935d20bb89235efa6" },
        .{ "aarch64-netbsd", "ee23d72182623d28b7f434c83a6b26199356c69c829d782d6d5d5be234cc9048" },
        .{ "aarch64-openbsd", "3bd8e93a6e940353e03fdcf05e7f5d0004f3a49334821d9115ba292e29538642" },
        .{ "aarch64-windows", "fde957643abac1907fd5458291f278bdc99c2c066241df01ef77ab4e112f58c8" },
        .{ "loongarch64-linux", "8e87eaa553975dcd29853b02de21d670e60c537995b973f1d9c6f7a8dad6e8e8" },
        .{ "powerpc64le-linux", "ed1fa6dd98f6218bd94b4a154451db55d67adde4b827bdd6def8f063485006a9" },
        .{ "riscv64-linux", "faffa068c0af51a0ca2008d46cdfbeb6506edcaa909c50566f6f7ae2f74d6a26" },
        .{ "s390x-linux", "76cf57e98df5e4f6d4b0ea3e3e8be0f457306e07408872844ce0d3b0ff69232e" },
        .{ "x86_64-dragonfly", "ba46fffd2853b527ddffc41e23df6d048fa82882a0955029459455389ea7bf21" },
        .{ "x86_64-freebsd", "ea6c2511d53d033bb30f132034560c95cfefd81dcd8dffb66966dec1b04e5479" },
        .{ "x86_64-illumos", "9a042554e1fb82961845536fdd0e4b6f54c5d66ea37ede0393b65515355d8264" },
        .{ "x86_64-linux", "4277ba9fc9af6b2cd08ace5982875bdea3a2386cdd4250a170a6049183f145e5" },
        .{ "x86_64-netbsd", "1d3ba01432fa920f0a24420d129208ab82f93ada8853eb8286692711b3679cec" },
        .{ "x86_64-openbsd", "469ff987a535d25f18f8708f14cae937433d26e5c64627dd4569b22fafd43869" },
        .{ "x86_64-windows", "cf0183bdeb9d6b4568c841632cb6c391a4a643ea5f5582760599b3bb69264387" },
    }) },
});

const json_headers = [_]std.http.Header{
    .{ .name = "Content-Type", .value = "application/json" },
    .{ .name = "Accept", .value = "application/json" },
};

pub fn init(transport: *api.Api, allocator: std.mem.Allocator, cfg: Config, host: []const u8, token: []const u8) !Self {
    if (cfg.url.len == 0 or cfg.name.len == 0 or cfg.labels.len == 0 or cfg.poll_interval_ms == 0 or token.len == 0)
        return error.InvalidQueueConfig;
    for (cfg.labels) |label| {
        if (label.len == 0 or std.mem.indexOfAny(u8, label, ",:\r\n") != null) return error.InvalidLabels;
    }
    const scope = switch (cfg.scope) {
        .user => try allocator.dupe(u8, "user"),
        .admin => try allocator.dupe(u8, "admin"),
        .org => |org| blk: {
            const escaped = try api.escape(allocator, org);
            defer allocator.free(escaped);
            break :blk try std.fmt.allocPrint(allocator, "orgs/{s}", .{escaped});
        },
        .repo => |repo| blk: {
            const owner = try api.escape(allocator, repo.owner);
            defer allocator.free(owner);
            const name = try api.escape(allocator, repo.name);
            defer allocator.free(name);
            break :blk try std.fmt.allocPrint(allocator, "repos/{s}/{s}", .{ owner, name });
        },
    };
    defer allocator.free(scope);
    const endpoint = try std.fmt.allocPrint(allocator, "{s}/api/v1/{s}/actions/runners", .{ std.mem.trimEnd(u8, cfg.url, "/"), scope });
    errdefer allocator.free(endpoint);
    const prefix = try std.fmt.allocPrint(allocator, "{s}-{s}", .{ host, cfg.name });
    errdefer allocator.free(prefix);
    return .{
        .allocator = allocator,
        .transport = transport,
        .config = cfg,
        .endpoint = endpoint,
        .prefix = prefix,
        .authorization = try std.fmt.allocPrint(allocator, "token {s}", .{token}),
        .submitted = .init(allocator),
    };
}

pub fn deinit(q: *Self) void {
    var keys = q.submitted.keyIterator();
    while (keys.next()) |key| q.allocator.free(key.*);
    q.submitted.deinit();
    q.allocator.free(q.prefix);
    q.allocator.free(q.endpoint);
    q.allocator.free(q.authorization);
}

/// Installs the host Forgejo runner in a new dst directory as forgejo-runner.
/// Existing destination directories are preserved.
pub fn download(self: *Self, dst: []const u8) anyerror!void {
    const transport = self.transport;
    const io = transport.client.io;
    const version = self.config.version;
    _ = try std.SemanticVersion.parse(version);

    const platform = @tagName(builtin.cpu.arch) ++ "-" ++ @tagName(builtin.os.tag);
    const triplets = version_checksum.get(version) orelse return error.UnknownVersion;

    var url_buffer: [1024]u8 = undefined;
    var path_buffer: [Io.Dir.max_path_bytes]u8 = undefined;

    const sha = triplets.get(platform) orelse return error.UnsupportedPlatform;
    const ext = if (builtin.os.tag == .windows) ".exe" else "";
    const url = try std.fmt.bufPrint(&url_buffer, version_template, .{ version, platform, ext });
    const path = try std.fmt.bufPrint(&path_buffer, "{s}{s}forgejo-runner{s}", .{ dst, std.fs.path.sep_str, ext });

    const cwd = Io.Dir.cwd();
    if (std.fs.path.dirname(dst)) |parent| try cwd.createDirPath(io, parent);

    try cwd.createDir(io, dst, .default_dir);
    errdefer cwd.deleteTree(io, dst) catch {};

    try transport.download(try std.Uri.parse(url), sha, path);

    const file = try cwd.openFile(io, path, .{});
    defer file.close(io);

    if (builtin.os.tag != .windows) {
        try file.setPermissions(io, .fromMode(0o755));
    }
}

pub fn next(q: *Self, arena: std.mem.Allocator) !Job {
    while (true) {
        var scratch = std.heap.ArenaAllocator.init(q.allocator);
        defer scratch.deinit();
        const a = scratch.allocator();
        const labels = try std.mem.join(a, ",", q.config.labels);
        const escaped = try api.escape(a, labels);
        const url = try std.fmt.allocPrint(a, "{s}/jobs?labels={s}", .{ q.endpoint, escaped });
        const response = try q.transport.request(.GET, try std.Uri.parse(url), q.authorization, null, &json_headers);
        defer response.deinit();
        const jobs = try std.json.parseFromSlice(?[]Attempt, a, response.body, .{ .ignore_unknown_fields = true });
        const waiting = jobs.value orelse &.{};
        // Forget attempts once the server stops reporting them as waiting.
        var keys = q.submitted.keyIterator();
        var expired: std.ArrayList([]const u8) = .empty;
        while (keys.next()) |key| {
            const found = for (waiting) |job| {
                if (std.mem.eql(u8, job.status, "waiting") and std.mem.eql(u8, job.handle, key.*)) break true;
            } else false;
            if (!found) try expired.append(a, key.*);
        }
        for (expired.items) |key| {
            _ = q.submitted.remove(key);
            q.allocator.free(key);
        }
        for (waiting) |job| {
            if (!std.mem.eql(u8, job.status, "waiting") or !q.matches(job.runs_on) or q.submitted.contains(job.handle)) continue;
            if (job.handle.len == 0) return error.InvalidResponse;
            const handle = try arena.dupe(u8, job.handle);
            const runner_labels = try arena.alloc([]const u8, q.config.labels.len);
            for (q.config.labels, runner_labels) |label, *runner_label|
                runner_label.* = try std.fmt.allocPrint(arena, "{s}:host", .{label});
            const name = try runnerName(q.transport.client.io, a, q.prefix);
            const body = try std.json.Stringify.valueAlloc(a, .{ .name = name, .ephemeral = true }, .{});
            // Reserve the handle before registration, so no allocation can leak a remote runner afterwards.
            const key = try q.allocator.dupe(u8, handle);
            q.submitted.put(key, {}) catch |err| {
                q.allocator.free(key);
                return err;
            };
            errdefer {
                _ = q.submitted.remove(key);
                q.allocator.free(key);
            }
            const registered = try q.transport.request(.POST, try std.Uri.parse(q.endpoint), q.authorization, body, &json_headers);
            defer registered.deinit();
            const runner = try std.json.parseFromSlice(Registration, arena, registered.body, .{
                .allocate = .alloc_always,
                .ignore_unknown_fields = true,
            });
            if (runner.value.id == 0 or runner.value.uuid.len == 0 or runner.value.token.len == 0)
                return error.InvalidResponse;
            return .{
                .url = q.config.url,
                .uuid = runner.value.uuid,
                .token = runner.value.token,
                .runner_id = runner.value.id,
                .labels = runner_labels,
                .handle = handle,
            };
        }
        try Io.sleep(q.transport.client.io, .fromMilliseconds(q.config.poll_interval_ms), .awake);
    }
}

/// Runs shared binaries by absolute path with a separate job config and workspace.
pub fn run(
    self: *Self,
    request: Job,
    bindir: []const u8,
    dir: []const u8,
) !std.process.Child.Term {
    const io = self.transport.client.io;
    const allocator = self.allocator;
    if (request.url.len == 0 or request.uuid.len == 0 or request.token.len == 0 or request.labels.len == 0)
        return error.InvalidJobRequest;
    if (request.handle) |handle| if (handle.len == 0) return error.InvalidJobRequest;

    if (!std.fs.path.isAbsolute(bindir))
        return error.InvalidRunnerPath;

    var directory = try Io.Dir.cwd().openDir(io, dir, .{});
    defer directory.close(io);
    var root_buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const root = root_buffer[0..try directory.realPath(io, &root_buffer)];
    const executable = try std.fs.path.join(allocator, &.{ bindir, if (builtin.os.tag == .windows) "forgejo-runner.exe" else "forgejo-runner" });
    defer allocator.free(executable);
    const workspace = try std.fs.path.join(allocator, &.{ root, "_work" });
    defer allocator.free(workspace);
    try directory.createDir(io, "_work", .default_dir);

    // JSON is valid YAML; serialization safely quotes credentials and paths.
    // v13.0.0-zsf accepts server.connections directly, without `register`.
    const settings = .{
        .runner = .{ .capacity = @as(u32, 1) },
        .host = .{ .workdir_parent = workspace },
        .server = .{ .connections = .{ .hrc = .{
            .url = request.url,
            .uuid = request.uuid,
            .token = request.token,
            .labels = request.labels,
        } } },
    };
    const file = try directory.createFile(io, "config.yaml", .{
        .exclusive = true,
        .permissions = if (builtin.os.tag == .windows) .default_file else .fromMode(0o600),
    });
    {
        defer file.close(io);
        var buffer: [4096]u8 = undefined;
        var writer = file.writer(io, &buffer);
        try std.json.Stringify.value(settings, .{}, &writer.interface);
        try writer.interface.flush();
    }

    // Fixed argument storage accommodates the optional job handle.
    var args: [7][]const u8 = undefined;
    args[0..5].* = .{ executable, "--config", "config.yaml", "one-job", "--wait" };
    const count: usize = if (request.handle) |handle| count: {
        args[5] = "--handle";
        args[6] = handle;
        break :count 7;
    } else 5;
    var child = try std.process.spawn(io, .{
        .argv = args[0..count],
        .cwd = .{ .path = root },
        .stdin = .ignore,
    });
    defer child.kill(io);
    return child.wait(io);
}

pub fn finish(q: *Self, request: Job, succeeded: bool) !void {
    if (!succeeded) {
        if (request.handle) |handle| {
            if (q.submitted.fetchRemove(handle)) |entry| q.allocator.free(entry.key);
        }
    }
    const url = try std.fmt.allocPrint(q.allocator, "{s}/{d}", .{ q.endpoint, request.runner_id });
    defer q.allocator.free(url);
    const response = q.transport.request(.DELETE, try std.Uri.parse(url), q.authorization, null, &json_headers) catch |err| switch (err) {
        error.NotFound => return, // Ephemeral registrations normally remove themselves.
        else => return err,
    };
    response.deinit();
}

fn matches(q: *const Self, required: []const []const u8) bool {
    if (required.len == 0) return false;
    for (required) |label| {
        const found = for (q.config.labels) |available| {
            if (std.mem.eql(u8, label, available)) break true;
        } else false;
        if (!found) return false;
    }
    return true;
}

fn runnerName(io: std.Io, allocator: std.mem.Allocator, prefix: []const u8) ![]u8 {
    var random: [16]u8 = undefined;
    try io.randomSecure(&random);
    return std.fmt.allocPrint(allocator, "{s}-{x}", .{ prefix, &random });
}

test {
    std.testing.refAllDecls(@This());
}

test "run prepares credentials and workspace and waits for one-job" {
    if (builtin.os.tag == .windows) return error.SkipZigTest;
    const io = std.testing.io;
    var transport = api.Api.init(io, std.testing.allocator);
    defer transport.deinit();
    var provider = try init(&transport, std.testing.allocator, .{
        .version = "13.0.0-zsf",
        .url = "https://forgejo.example",
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "token");
    defer provider.deinit();
    var tmp = std.testing.tmpDir(.{});
    defer tmp.cleanup();
    try tmp.dir.writeFile(io, .{ .sub_path = "forgejo-runner", .data =
        \\#!/bin/sh
        \\[ "$#" = 6 ] && [ "$1" = --config ] && [ "$2" = config.yaml ] && [ "$3" = one-job ] && [ "$4" = --wait ] && [ "$5" = --handle ] && [ "$6" = 'attempt with spaces' ] || exit 99
        \\[ -f config.yaml ] && [ -d _work ] && [ ! -f forgejo-runner ] || exit 98
        \\case "$0" in /*) ;; *) exit 97 ;; esac
        \\exit 7
        \\
    });
    const executable = try tmp.dir.openFile(io, "forgejo-runner", .{});
    defer executable.close(io);
    try executable.setPermissions(io, .fromMode(0o755));
    var buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const root = buffer[0..try tmp.dir.realPath(io, &buffer)];
    const request: Job = .{
        .url = "https://forgejo.example/",
        .uuid = "c9e50be9-a7c3-4aee-ba35-624c4ff8c519",
        .token = "secret\"with\\escapes",
        .labels = &.{"linux:host"},
        .handle = "attempt with spaces",
    };
    var job = std.testing.tmpDir(.{});
    defer job.cleanup();
    var job_buffer: [Io.Dir.max_path_bytes]u8 = undefined;
    const workdir = job_buffer[0..try job.dir.realPath(io, &job_buffer)];
    const term = try provider.run(request, root, workdir);
    try std.testing.expectEqual(@as(u8, 7), term.exited);
    try tmp.dir.access(io, "forgejo-runner", .{});
    try std.testing.expectError(error.FileNotFound, tmp.dir.access(io, "config.yaml", .{}));
    const bytes = try job.dir.readFileAlloc(io, "config.yaml", std.testing.allocator, .limited(4096));
    defer std.testing.allocator.free(bytes);
    const settings = try std.json.parseFromSlice(std.json.Value, std.testing.allocator, bytes, .{});
    defer settings.deinit();
    const conn = settings.value.object.get("server").?.object.get("connections").?.object.get("hrc").?;
    try std.testing.expectEqualStrings(request.token, conn.object.get("token").?.string);
    try std.testing.expectEqualStrings("linux:host", conn.object.get("labels").?.array.items[0].string);
    const workspace = settings.value.object.get("host").?.object.get("workdir_parent").?.string;
    try std.testing.expect(std.fs.path.isAbsolute(workspace));
    try std.testing.expect(std.mem.startsWith(u8, workspace, workdir));
}

test "ZSF release contains hashes for all published platforms" {
    const triplets = version_checksum.get("13.0.0-zsf").?;
    try std.testing.expectEqual(@as(usize, 17), triplets.values().len);
    for (triplets.values()) |hex| {
        var digest: [32]u8 = undefined;
        try std.testing.expectEqual(@as(usize, 32), (try std.fmt.hexToBytes(&digest, hex)).len);
    }
    try std.testing.expect(triplets.get("x86_64-windows") != null);
    try std.testing.expect(triplets.get("aarch64-macos") != null);
    try std.testing.expect(triplets.get("aarch64-linux") != null);
    try std.testing.expect(triplets.get("x86-linux") == null);
    _ = try std.SemanticVersion.parse("13.0.0-zsf");
}

test "Forgejo queue requires all workflow labels" {
    var transport = api.Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    var queue = try init(&transport, std.testing.allocator, .{
        .version = "13.0.0-zsf",
        .url = "http://localhost",
        .name = "build",
        .labels = &.{ "linux", "zig" },
    }, "host", "token");
    defer queue.deinit();
    try std.testing.expect(queue.matches(&.{ "linux", "zig" }));
    try std.testing.expect(!queue.matches(&.{ "linux", "docker" }));
    try std.testing.expect(!queue.matches(&.{}));
}
