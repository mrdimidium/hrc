// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const Io = std.Io;
const Sha256 = std.crypto.hash.sha2.Sha256;
const Allocator = std.mem.Allocator;

/// Scripted HTTP peer: checks the actual wire protocol, not mocked client methods.
pub const TestServer = struct {
    listener: Io.net.Server,
    url: []u8,

    pub const Step = struct {
        method: []const u8,
        target: []const u8,
        authorization: []const u8,
        body: ?[]const u8 = null,
        reply: []const u8 = "",
        status: u16 = 200,
        capacity: ?[]const u8 = null,
        entered: ?*Io.Event = null,
        release: ?*Io.Event = null,
        name_prefix: ?[]const u8 = null,
        ephemeral: ?bool = null,
        work_folder: ?[]const u8 = null,
    };

    pub fn init() !TestServer {
        const address = try Io.net.IpAddress.parseIp4("127.0.0.1", 0);
        var listener = try address.listen(std.testing.io, .{});
        errdefer listener.deinit(std.testing.io);
        return .{
            .listener = listener,
            .url = try std.fmt.allocPrint(std.testing.allocator, "http://127.0.0.1:{d}", .{listener.socket.address.getPort()}),
        };
    }

    pub fn deinit(server: *TestServer) void {
        server.listener.deinit(std.testing.io);
        std.testing.allocator.free(server.url);
    }

    pub fn serve(server: *TestServer, steps: []const Step) !void {
        const io = std.testing.io;
        for (steps) |step| {
            const stream = try server.listener.accept(io);
            defer stream.close(io);
            var scratch = std.heap.ArenaAllocator.init(std.testing.allocator);
            defer scratch.deinit();
            const a = scratch.allocator();
            var read_buffer: [8192]u8 = undefined;
            var reader = stream.reader(io, &read_buffer);
            const request_line = try reader.interface.takeDelimiterInclusive('\n');
            const expected = try std.fmt.allocPrint(a, "{s} {s} HTTP/1.1\r\n", .{ step.method, step.target });
            try std.testing.expectEqualStrings(expected, request_line);
            var length: usize = 0;
            var authorization: ?[]const u8 = null;
            var capacity: ?[]const u8 = null;
            while (true) {
                const line = try reader.interface.takeDelimiterInclusive('\n');
                if (std.mem.eql(u8, line, "\r\n")) break;
                const colon = std.mem.indexOfScalar(u8, line, ':') orelse return error.InvalidHttpHeader;
                const value = std.mem.trim(u8, line[colon + 1 ..], " \r\n");
                if (std.ascii.eqlIgnoreCase(line[0..colon], "content-length")) length = try std.fmt.parseInt(usize, value, 10);
                if (std.ascii.eqlIgnoreCase(line[0..colon], "authorization")) authorization = try a.dupe(u8, value);
                if (std.ascii.eqlIgnoreCase(line[0..colon], "x-scalesetmaxcapacity")) capacity = try a.dupe(u8, value);
            }
            try std.testing.expectEqualStrings(step.authorization, authorization orelse "");
            if (step.capacity) |want| try std.testing.expectEqualStrings(want, capacity orelse "");
            const body = try reader.interface.readAllocAll(a, length);
            if (step.body) |want| try std.testing.expectEqualStrings(want, body);
            if (step.name_prefix != null or step.ephemeral != null or step.work_folder != null) {
                const parsed = try std.json.parseFromSlice(std.json.Value, a, body, .{});
                const object = parsed.value.object;
                if (step.name_prefix) |prefix| try std.testing.expect(std.mem.startsWith(u8, object.get("name").?.string, prefix));
                if (step.ephemeral) |value| try std.testing.expectEqual(value, object.get("ephemeral").?.bool);
                if (step.work_folder) |value| try std.testing.expectEqualStrings(value, object.get("workFolder").?.string);
            }
            if (step.entered) |event| event.set(io);
            if (step.release) |event| try event.wait(io);
            const reply = try std.mem.replaceOwned(u8, a, step.reply, "$URL", server.url);
            var write_buffer: [8192]u8 = undefined;
            var writer = stream.writer(io, &write_buffer);
            try writer.interface.print("HTTP/1.1 {d} Test\r\nContent-Type: application/json\r\nContent-Length: {d}\r\nConnection: close\r\n\r\n{s}", .{ step.status, reply.len, reply });
            try writer.interface.flush();
        }
    }
};

pub const Response = struct {
    allocator: Allocator,

    status: std.http.Status,
    body: []u8,

    pub fn deinit(self: Response) void {
        self.allocator.free(self.body);
    }

    pub fn parse(self: Response, comptime T: type, allocator: Allocator) !std.json.Parsed(T) {
        return std.json.parseFromSlice(T, allocator, self.body, .{
            .allocate = .alloc_always,
            .ignore_unknown_fields = true,
        });
    }
};

/// Shared transport. The allocator must be thread-safe; requests own their buffers.
/// Destroy only after all queues and outstanding requests have finished.
pub const Api = struct {
    client: std.http.Client,

    pub fn init(io: std.Io, allocator: Allocator) Api {
        return .{ .client = .{ .io = io, .allocator = allocator } };
    }

    pub fn deinit(self: *Api) void {
        self.client.deinit();
    }

    pub fn request(
        self: *Api,
        method: std.http.Method,
        uri: std.Uri,
        authorization: []const u8,
        body: ?[]u8,
        extra_headers: []const std.http.Header,
    ) !Response {
        if (std.mem.indexOfAny(u8, authorization, "\r\n") != null)
            return error.InvalidHeader;

        var req = try self.client.request(method, uri, .{
            .redirect_behavior = .not_allowed,
            .headers = .{
                .user_agent = .{ .override = "hrc" },
                .authorization = .{ .override = authorization },
                .accept_encoding = .{ .override = "identity" },
            },
            .extra_headers = extra_headers,
        });
        defer req.deinit();

        const sent = if (body) |bytes|
            req.sendBodyComplete(bytes)
        else if (method.requestHasBody())
            req.sendBodyComplete(&.{})
        else
            req.sendBodiless();
        sent catch |err| {
            if (err == error.WriteFailed) {
                if (req.connection) |connection| {
                    if (connection.stream_writer.err) |cause| {
                        return cause;
                    }
                }
            }
            return err;
        };

        var head_buffer: [16 * 1024]u8 = undefined;
        var response = req.receiveHead(&head_buffer) catch |err| {
            if (err == error.ReadFailed) {
                return req.connection.?.getReadError() orelse err;
            }
            return err;
        };
        switch (response.head.status) {
            .unauthorized => return error.Unauthorized,
            .forbidden => return error.Forbidden,
            .not_found => return error.NotFound,
            .conflict => return error.Conflict,
            .too_many_requests => return error.RateLimited,
            else => if (@backingInt(response.head.status) < 200 or @backingInt(response.head.status) >= 300)
                return error.UnexpectedHttpStatus,
        }
        if (response.head.content_encoding != .identity) return error.UnexpectedContentEncoding;
        var transfer_buffer: [16 * 1024]u8 = undefined;
        return .{
            .allocator = self.client.allocator,
            .status = response.head.status,
            .body = response.reader(&transfer_buffer).allocRemaining(self.client.allocator, .limited(4 * 1024 * 1024)) catch |err| {
                if (err == error.ReadFailed) return req.connection.?.getReadError() orelse err;
                return err;
            },
        };
    }

    /// Downloads to <path>.tmp, verifies SHA-256, then renames to path.
    /// SHA is a 64-character hexadecimal SHA-256 digest.
    /// Existing destination and temporary files are preserved; failures remove our temporary file.
    pub fn download(
        self: *Api,
        uri: std.Uri,
        sha: []const u8,
        path: []const u8,
    ) !void {
        const io = self.client.io;
        const expected = try parseSha(sha);
        var path_buffer: [std.Io.Dir.max_path_bytes]u8 = undefined;
        const temporary_path = try std.fmt.bufPrint(&path_buffer, "{s}.tmp", .{path});

        const cwd = std.Io.Dir.cwd();
        if (std.fs.path.dirname(path)) |parent| try cwd.createDirPath(io, parent);

        const archive = try cwd.createFile(io, temporary_path, .{ .exclusive = true });
        errdefer cwd.deleteFile(io, temporary_path) catch {};
        defer archive.close(io);

        var writer_buf: [64 * 1024]u8 = undefined;
        var writer = archive.writer(io, &writer_buf);
        var hash_buf: [64 * 1024]u8 = undefined;
        var hashed = writer.interface.hashed(Sha256.init(.{}), &hash_buf);

        var req = try self.client.request(.GET, uri, .{
            .redirect_behavior = @fromBackingInt(@intCast(5)),
            .headers = .{ .accept_encoding = .{ .override = "identity" } },
        });
        defer req.deinit();

        try req.sendBodiless();

        var redirect_buffer: [16 * 1024]u8 = undefined;
        var transfer_buffer: [16 * 1024]u8 = undefined;

        var response = try req.receiveHead(&redirect_buffer);
        if (response.head.status != .ok)
            return error.UnexpectedHttpStatus;
        if (response.head.content_encoding != .identity)
            return error.UnexpectedContentEncoding;

        const reader = response.reader(&transfer_buffer);
        _ = try reader.streamRemaining(&hashed.writer);

        try hashed.writer.flush();
        try writer.interface.flush();

        if (!std.crypto.timing_safe.eql(Hash, hashed.hasher.finalResult(), expected))
            return error.ShaMismatch;

        try cwd.renamePreserve(temporary_path, cwd, path, io);
    }
};

const Hash = [Sha256.digest_length]u8;

pub fn escape(allocator: Allocator, value: []const u8) ![]u8 {
    var writer: std.Io.Writer.Allocating = .init(allocator);
    defer writer.deinit();

    for (value) |byte| {
        if (std.ascii.isAlphanumeric(byte) or std.mem.indexOfScalar(u8, "-._~", byte) != null) {
            try writer.writer.writeByte(byte);
        } else {
            try writer.writer.print("%{X:0>2}", .{byte});
        }
    }
    return writer.toOwnedSlice();
}

fn parseSha(hex: []const u8) !Hash {
    if (hex.len != Sha256.digest_length * 2)
        return error.InvalidSha;

    var digest: Hash = undefined;
    _ = std.fmt.hexToBytes(&digest, hex) catch return error.InvalidSha;

    return digest;
}

test "escape query and path components without interpreting separators" {
    const escaped = try escape(std.testing.allocator, "host /name&x=1");
    defer std.testing.allocator.free(escaped);
    try std.testing.expectEqualStrings("host%20%2Fname%26x%3D1", escaped);
}

test "invalid input and existing temporary file never start a download" {
    const io = std.testing.io;
    const allocator = std.testing.allocator;
    var transport = Api.init(io, allocator);
    defer transport.deinit();

    var tmp = std.testing.tmpDir(.{});
    defer tmp.cleanup();

    const uri = try std.Uri.parse("https://example.com/archive.tar.gz");

    try std.testing.expectError(error.InvalidSha, transport.download(uri, "", "unused"));
    try std.testing.expectError(error.InvalidSha, transport.download(uri, "bad", "unused"));

    try tmp.dir.writeFile(io, .{ .sub_path = "existing.tmp", .data = "keep" });

    var path_buffer: [std.Io.Dir.max_path_bytes]u8 = undefined;
    const root = path_buffer[0..try tmp.dir.realPath(io, &path_buffer)];

    const folder = try std.fs.path.join(allocator, &.{ root, "existing" });
    defer allocator.free(folder);

    const hex = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";
    try std.testing.expectError(error.PathAlreadyExists, transport.download(uri, hex, folder));

    const existing = try tmp.dir.openFile(io, "existing.tmp", .{});
    defer existing.close(io);
}

test "hashed file writer writes data and computes SHA-256" {
    const io = std.testing.io;

    var tmp = std.testing.tmpDir(.{});
    defer tmp.cleanup();

    const file = try tmp.dir.createFile(io, "download", .{ .read = true });
    defer file.close(io);

    var buffer: [2]u8 = undefined;
    var file_writer = file.writer(io, &buffer);
    const writer = &file_writer.interface;

    var hash_buffer: [2]u8 = undefined;
    var hashed = writer.hashed(Sha256.init(.{}), &hash_buffer);
    // Network readers request writable space rather than calling writeAll.
    for ("abc") |byte| {
        const slice = try hashed.writer.writableSliceGreedy(1);
        slice[0] = byte;
        hashed.writer.advance(1);
    }
    try hashed.writer.flush();

    try writer.flush();

    const expected = try parseSha("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    try std.testing.expectEqualSlices(u8, &expected, &hashed.hasher.finalResult());

    var read_buffer: [8]u8 = undefined;
    var reader = file.reader(io, &read_buffer);

    var actual: [3]u8 = undefined;
    try reader.interface.readSliceAll(&actual);

    try std.testing.expectEqualStrings("abc", &actual);
}

test "GitHub queue authenticates, acquires, acknowledges and refreshes queue credentials" {
    const github = @import("github.zig");
    var server = try TestServer.init();
    defer server.deinit();
    const steps = [_]TestServer.Step{
        .{ .method = "POST", .target = "/orgs/acme/actions/runners/registration-token", .authorization = "Bearer pat", .reply = "{\"token\":\"registration\"}" },
        .{ .method = "POST", .target = "/actions/runner-registration", .authorization = "RemoteAuth registration", .body = "{\"url\":\"https://github.com/acme\",\"runner_event\":\"register\"}", .reply = "{\"url\":\"$URL\",\"token\":\"admin\"}" },
        .{ .method = "GET", .target = "/_apis/runtime/runnerscalesets?runnerGroupId=1&name=host-build&api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"value\":[]}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets?api-version=6.0-preview", .authorization = "Bearer admin", .body = "{\"name\":\"host-build\",\"runnerGroupId\":1,\"labels\":[{\"name\":\"linux\",\"type\":\"System\"}],\"RunnerSetting\":{\"disableUpdate\":true}}", .reply = "{\"id\":7}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/sessions?api-version=6.0-preview", .authorization = "Bearer admin", .body = "{\"ownerName\":\"host-build\"}", .reply = "{\"sessionId\":\"session\",\"messageQueueUrl\":\"$URL/queue?session=x\",\"messageQueueAccessToken\":\"old\",\"statistics\":{\"totalAssignedJobs\":0}}" },
        .{ .method = "GET", .target = "/queue?session=x&lastMessageId=0", .authorization = "Bearer old", .status = 401, .capacity = "1" },
        .{ .method = "PATCH", .target = "/_apis/runtime/runnerscalesets/7/sessions/session?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"sessionId\":\"session\",\"messageQueueUrl\":\"$URL/queue?session=x\",\"messageQueueAccessToken\":\"new\"}" },
        .{ .method = "GET", .target = "/queue?session=x&lastMessageId=0", .authorization = "Bearer new", .status = 202, .capacity = "1" },
        .{ .method = "GET", .target = "/queue?session=x&lastMessageId=0", .authorization = "Bearer new", .capacity = "1", .reply = "{\"messageId\":42,\"statistics\":{\"totalAssignedJobs\":0},\"body\":\"[{\\\"messageType\\\":\\\"JobAvailable\\\",\\\"runnerRequestId\\\":99}]\"}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/acquirejobs?api-version=6.0-preview", .authorization = "Bearer new", .body = "[99]", .reply = "{\"value\":[99]}" },
        .{ .method = "DELETE", .target = "/queue/42?session=x", .authorization = "Bearer new", .status = 204 },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/generatejitconfig?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"runner\":{\"id\":123},\"encodedJITConfig\":\"opaque-jit\"}", .name_prefix = "host-build-", .work_folder = "_work" },
        .{ .method = "DELETE", .target = "/_apis/distributedtask/pools/0/agents/123?api-version=6.0-preview", .authorization = "Bearer admin", .status = 204 },
        .{ .method = "DELETE", .target = "/_apis/runtime/runnerscalesets/7/sessions/session?api-version=6.0-preview", .authorization = "Bearer admin", .status = 204 },
    };
    var peer = try Io.concurrent(std.testing.io, TestServer.serve, .{ &server, &steps });
    defer peer.cancel(std.testing.io) catch {};
    var transport = Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    var queue = try github.init(&transport, std.testing.allocator, .{
        .version = "2.338.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme",
        .api_url = server.url,
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "pat");
    var alive = true;
    defer if (alive) {
        if (queue.session) |session| session.deinit();
        queue.session = null;
        queue.deinit();
    };
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const request = try queue.next(arena.allocator());
    try std.testing.expectEqualStrings("opaque-jit", request.jit_config);
    try std.testing.expectEqual(@as(u64, 123), request.runner_id);
    try queue.finish(request, false);
    try std.testing.expectEqual(@as(?u64, null), queue.runner_id);
    alive = false;
    queue.deinit();
    try peer.await(std.testing.io);
}

test "Forgejo queue filters labels, registers an ephemeral runner and deduplicates attempts" {
    const forgejo = @import("forgejo.zig");
    var server = try TestServer.init();
    defer server.deinit();
    const steps = [_]TestServer.Step{
        .{ .method = "GET", .target = "/api/v1/user/actions/runners/jobs?labels=linux%2Czig", .authorization = "token pat", .reply = "null" },
        .{ .method = "GET", .target = "/api/v1/user/actions/runners/jobs?labels=linux%2Czig", .authorization = "token pat", .reply = "[{\"handle\":\"wrong\",\"status\":\"waiting\",\"runs_on\":[\"docker\"]},{\"handle\":\"a\",\"status\":\"waiting\",\"runs_on\":[\"linux\",\"zig\"]}]" },
        .{ .method = "POST", .target = "/api/v1/user/actions/runners", .authorization = "token pat", .status = 201, .reply = "{\"id\":1,\"uuid\":\"uuid-a\",\"token\":\"token-a\"}", .name_prefix = "host-build-", .ephemeral = true },
        .{ .method = "DELETE", .target = "/api/v1/user/actions/runners/1", .authorization = "token pat", .status = 404 },
        .{ .method = "GET", .target = "/api/v1/user/actions/runners/jobs?labels=linux%2Czig", .authorization = "token pat", .reply = "[{\"handle\":\"a\",\"status\":\"waiting\",\"runs_on\":[\"linux\"]},{\"handle\":\"b\",\"status\":\"waiting\",\"runs_on\":[\"linux\"]}]" },
        .{ .method = "POST", .target = "/api/v1/user/actions/runners", .authorization = "token pat", .status = 201, .reply = "{\"id\":2,\"uuid\":\"uuid-b\",\"token\":\"token-b\"}" },
        .{ .method = "DELETE", .target = "/api/v1/user/actions/runners/2", .authorization = "token pat", .status = 204 },
    };
    var peer = try Io.concurrent(std.testing.io, TestServer.serve, .{ &server, &steps });
    defer peer.cancel(std.testing.io) catch {};
    var transport = Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    var queue = try forgejo.init(&transport, std.testing.allocator, .{
        .version = "13.0.0-zsf",
        .url = server.url,
        .name = "build",
        .labels = &.{ "linux", "zig" },
        .poll_interval_ms = 1,
    }, "host", "pat");
    defer queue.deinit();
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const first = try queue.next(arena.allocator());
    try std.testing.expectEqualStrings("a", first.handle.?);
    try std.testing.expectEqualStrings("token-a", first.token);
    try std.testing.expectEqualStrings("linux:host", first.labels[0]);
    try queue.finish(first, true);
    const second = try queue.next(arena.allocator());
    try std.testing.expectEqualStrings("b", second.handle.?);
    try queue.finish(second, false);
    try std.testing.expect(!queue.submitted.contains("b"));
    try peer.await(std.testing.io);
}

test "GitHub restores initial demand, refreshes admin auth and waits for completion before another JIT" {
    const github = @import("github.zig");
    var server = try TestServer.init();
    defer server.deinit();
    const steps = [_]TestServer.Step{
        .{ .method = "POST", .target = "/repos/acme/build/actions/runners/registration-token", .authorization = "Bearer pat", .reply = "{\"token\":\"registration\"}" },
        .{ .method = "POST", .target = "/actions/runner-registration", .authorization = "RemoteAuth registration", .reply = "{\"url\":\"$URL\",\"token\":\"old-admin\"}" },
        .{ .method = "GET", .target = "/_apis/runtime/runnerscalesets?runnerGroupId=1&name=host-build&api-version=6.0-preview", .authorization = "Bearer old-admin", .status = 401 },
        .{ .method = "POST", .target = "/repos/acme/build/actions/runners/registration-token", .authorization = "Bearer pat", .reply = "{\"token\":\"registration\"}" },
        .{ .method = "POST", .target = "/actions/runner-registration", .authorization = "RemoteAuth registration", .reply = "{\"url\":\"$URL\",\"token\":\"admin\"}" },
        .{ .method = "GET", .target = "/_apis/runtime/runnerscalesets?runnerGroupId=1&name=host-build&api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"value\":[{\"id\":7}]}" },
        .{ .method = "PATCH", .target = "/_apis/runtime/runnerscalesets/7?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"id\":7}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/sessions?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"sessionId\":\"session\",\"messageQueueUrl\":\"$URL/queue\",\"messageQueueAccessToken\":\"queue-token\",\"statistics\":{\"totalAssignedJobs\":1}}" },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/generatejitconfig?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"runner\":{\"id\":123},\"encodedJITConfig\":\"first\"}" },
        .{ .method = "DELETE", .target = "/_apis/distributedtask/pools/0/agents/123?api-version=6.0-preview", .authorization = "Bearer admin", .status = 404 },
        .{ .method = "GET", .target = "/queue?lastMessageId=0", .authorization = "Bearer queue-token", .reply = "{\"messageId\":1,\"statistics\":{\"totalAssignedJobs\":1},\"body\":\"[{\\\"messageType\\\":\\\"JobStarted\\\",\\\"runnerId\\\":123}]\"}" },
        .{ .method = "DELETE", .target = "/queue/1", .authorization = "Bearer queue-token", .status = 204 },
        .{ .method = "GET", .target = "/queue?lastMessageId=1", .authorization = "Bearer queue-token", .reply = "{\"messageId\":2,\"statistics\":{\"totalAssignedJobs\":1},\"body\":\"[{\\\"messageType\\\":\\\"JobCompleted\\\",\\\"runnerId\\\":123}]\"}" },
        .{ .method = "DELETE", .target = "/queue/2", .authorization = "Bearer queue-token", .status = 204 },
        .{ .method = "POST", .target = "/_apis/runtime/runnerscalesets/7/generatejitconfig?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"runner\":{\"id\":124},\"encodedJITConfig\":\"second\"}" },
        .{ .method = "DELETE", .target = "/_apis/distributedtask/pools/0/agents/124?api-version=6.0-preview", .authorization = "Bearer admin", .status = 204 },
        .{ .method = "DELETE", .target = "/_apis/runtime/runnerscalesets/7/sessions/session?api-version=6.0-preview", .authorization = "Bearer admin", .status = 204 },
    };
    var peer = try Io.concurrent(std.testing.io, TestServer.serve, .{ &server, &steps });
    defer peer.cancel(std.testing.io) catch {};
    var transport = Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    var queue = try github.init(&transport, std.testing.allocator, .{
        .version = "2.338.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme/build",
        .api_url = server.url,
        .name = "build",
        .labels = &.{"linux"},
    }, "host", "pat");
    var alive = true;
    defer if (alive) {
        if (queue.session) |session| session.deinit();
        queue.session = null;
        queue.deinit();
    };
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const first = try queue.next(arena.allocator());
    try queue.finish(first, true);
    try std.testing.expectEqual(@as(?u64, 123), queue.runner_id);
    const second = try queue.next(arena.allocator());
    try std.testing.expectEqualStrings("second", second.jit_config);
    try queue.finish(second, false);
    alive = false;
    queue.deinit();
    try peer.await(std.testing.io);
}
