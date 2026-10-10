// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const Io = std.Io;
const Sha256 = std.crypto.hash.sha2.Sha256;
const Allocator = std.mem.Allocator;

pub const Response = struct {
    allocator: Allocator,

    status: std.http.Status,
    body: []u8,

    pub fn deinit(self: Response) void {
        self.allocator.free(self.body);
    }

    pub fn parse(
        self: Response,
        comptime T: type,
        allocator: Allocator,
    ) !std.json.Parsed(T) {
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

    pub fn init(
        io: std.Io,
        allocator: Allocator,
    ) Api {
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
        if (response.head.content_encoding != .identity)
            return error.UnexpectedContentEncoding;

        var transfer_buffer: [16 * 1024]u8 = undefined;

        return .{
            .allocator = self.client.allocator,
            .status = response.head.status,
            .body = response.reader(&transfer_buffer).allocRemaining(self.client.allocator, .limited(4 * 1024 * 1024)) catch |err| {
                if (err == error.ReadFailed)
                    return req.connection.?.getReadError() orelse err;
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
        if (std.fs.path.dirname(path)) |parent|
            try cwd.createDirPath(io, parent);

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

pub fn escape(
    allocator: Allocator,
    value: []const u8,
) ![]u8 {
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

/// Builds a typed JSON HTTP client from a comptime struct of endpoint descriptions.
/// Each endpoint has method/path and optional Path, Query, Headers, Body, Response types.
/// The transport, base URI and authorization are borrowed and must outlive calls.
pub fn HttpClient(comptime endpoints: anytype) type {
    @setEvalBranchQuota(100_000);

    const info = @typeInfo(@TypeOf(endpoints)).@"struct";
    var tags: [info.field_names.len]u32 = undefined;

    for (info.field_names, 0..) |name, i| {
        tags[i] = @intCast(i);
        validateEndpoint(@field(endpoints, name));
    }

    const OperationType = @Enum(u32, .exhaustive, info.field_names, &tags);

    return struct {
        transport: *Api,
        base_uri: std.Uri,
        authorization: []const u8,

        const Self = @This();
        pub const Operation = OperationType;

        pub fn Args(comptime operation: Operation) type {
            return endpointArgs(@field(endpoints, @tagName(operation)));
        }

        pub fn Result(comptime operation: Operation) type {
            const T = endpointType(@field(endpoints, @tagName(operation)), "Response");

            return struct {
                status: std.http.Status,
                value: T,
                parsed: ?std.json.Parsed(T) = null,

                pub fn deinit(self: @This()) void {
                    if (self.parsed) |parsed|
                        parsed.deinit();
                }
            };
        }

        pub fn init(
            transport: *Api,
            base_uri: std.Uri,
            authorization: []const u8,
        ) Self {
            return .{
                .transport = transport,
                .base_uri = base_uri,
                .authorization = authorization,
            };
        }

        /// uri overrides the complete endpoint URI; declared Query fields are still appended.
        /// A void Response ignores the body; an optional Response accepts an empty body as null.
        pub fn call(
            self: Self,
            comptime operation: Operation,
            allocator: Allocator,
            args: Args(operation),
        ) !Result(operation) {
            const endpoint = comptime @field(endpoints, @tagName(operation));
            const T = comptime endpointType(endpoint, "Response");

            var scratch = std.heap.ArenaAllocator.init(self.transport.client.allocator);
            defer scratch.deinit();

            const a = scratch.allocator();

            var uri = args.uri orelse self.base_uri;
            if (args.uri == null) {
                var path: Io.Writer.Allocating = .init(a);
                // Keep encoded slashes and other reserved characters in the base URI.
                var encoded_base: Io.Writer.Allocating = .init(a);

                try uri.path.formatPath(&encoded_base.writer);
                try path.writer.writeAll(std.mem.trimEnd(u8, encoded_base.written(), "/"));
                try writeEndpointPath(endpoint, args, a, &path.writer);

                uri.path = .{ .percent_encoded = path.written() };
            }

            if (@hasField(@TypeOf(endpoint), "Query")) {
                var query: Io.Writer.Allocating = .init(a);

                if (uri.query) |existing|
                    try existing.formatQuery(&query.writer);

                inline for (@typeInfo(endpoint.Query).@"struct".field_names) |name| {
                    const value = @field(args.query, name);

                    if (try scalarText(a, value)) |text| {
                        if (query.written().len > 0)
                            try query.writer.writeByte('&');
                        try query.writer.print("{s}={s}", .{ try escape(a, name), try escape(a, text) });
                    }
                }

                uri.query = if (query.written().len > 0)
                    .{ .percent_encoded = query.written() }
                else
                    null;
            }

            var headers: std.ArrayList(std.http.Header) = .empty;
            try headers.appendSlice(a, &.{
                .{ .name = "Accept", .value = "application/json" },
                .{ .name = "Content-Type", .value = "application/json" },
            });

            if (@hasField(@TypeOf(endpoint), "Headers")) {
                inline for (@typeInfo(endpoint.Headers).@"struct".field_names) |name| {
                    if (try scalarText(a, @field(args.headers, name))) |text|
                        try putHeader(&headers, a, .{ .name = name, .value = text });
                }
            }

            for (args.extra_headers) |header|
                try putHeader(&headers, a, header);

            const body = if (@hasField(@TypeOf(endpoint), "Body"))
                try std.json.Stringify.valueAlloc(a, args.body, .{})
            else
                null;

            const response = try self.transport.request(endpoint.method, uri, self.authorization, body, headers.items);
            defer response.deinit();

            if (T == void)
                return .{ .status = response.status, .value = {} };
            if (@typeInfo(T) == .optional and response.body.len == 0)
                return .{ .status = response.status, .value = null };

            const parsed = try response.parse(T, allocator);

            return .{
                .status = response.status,
                .value = parsed.value,
                .parsed = parsed,
            };
        }
    };
}

const default_endpoint_uri: ?std.Uri = null;
const default_endpoint_headers: []const std.http.Header = &.{};

fn endpointType(
    comptime endpoint: anytype,
    comptime name: []const u8,
) type {
    return if (@hasField(@TypeOf(endpoint), name)) @field(endpoint, name) else void;
}

fn endpointArgs(comptime endpoint: anytype) type {
    const sections = .{ "Path", "Query", "Headers", "Body" };
    const fields = .{ "path", "query", "headers", "body" };

    const count = comptime count: {
        var count: usize = 2;
        for (sections) |name| if (@hasField(@TypeOf(endpoint), name)) {
            count += 1;
        };
        break :count count;
    };

    var names: [count][]const u8 = undefined;
    var types: [count]type = undefined;
    var attrs: [count]std.lang.Type.Struct.FieldAttributes = @splat(.{});

    names[0..2].* = .{ "uri", "extra_headers" };
    types[0..2].* = .{ ?std.Uri, []const std.http.Header };
    attrs[0].default_value_ptr = &default_endpoint_uri;
    attrs[1].default_value_ptr = @ptrCast(&default_endpoint_headers);

    var i: usize = 2;
    for (sections, fields) |section, field| {
        if (@hasField(@TypeOf(endpoint), section)) {
            names[i] = field;
            types[i] = @field(endpoint, section);
            i += 1;
        }
    }

    return @Struct(.auto, null, &names, &types, &attrs);
}

fn validateEndpoint(comptime endpoint: anytype) void {
    for (@typeInfo(@TypeOf(endpoint)).@"struct".field_names) |name| {
        const known = for (.{ "method", "path", "Path", "Query", "Headers", "Body", "Response" }) |allowed| {
            if (std.mem.eql(u8, name, allowed)) break true;
        } else false;
        if (!known)
            @compileError("unknown endpoint description field: " ++ name);
    }

    const method: std.http.Method = endpoint.method;
    _ = method;

    const path: []const u8 = endpoint.path;
    if (path.len == 0 or path[0] != '/' or std.mem.indexOfAny(u8, path, "?#") != null)
        @compileError("endpoint path must start with / and contain no query or fragment");

    var cursor: usize = 0;
    var used: usize = 0;
    while (cursor < path.len) {
        if (path[cursor] == '}')
            @compileError("unmatched } in endpoint path");
        if (path[cursor] != '{') {
            cursor += 1;
            continue;
        }

        const end = std.mem.indexOfScalarPos(u8, path, cursor + 1, '}') orelse @compileError("unclosed endpoint placeholder");
        const name = path[cursor + 1 .. end];
        const Path = endpointType(endpoint, "Path");
        if (Path == void or !@hasField(Path, name))
            @compileError("endpoint placeholder has no Path field: " ++ name);
        if (@typeInfo(@FieldType(Path, name)) == .optional)
            @compileError("Path fields cannot be optional");

        used += 1;
        cursor = end + 1;
    }

    if (@hasField(@TypeOf(endpoint), "Path")) {
        for (@typeInfo(endpoint.Path).@"struct".field_names) |name| {
            if (std.mem.indexOf(u8, path, "{" ++ name ++ "}") == null)
                @compileError("Path field has no endpoint placeholder: " ++ name);
        }
    } else if (used != 0) unreachable;

    for (.{ "Path", "Query", "Headers" }) |section| {
        if (@hasField(@TypeOf(endpoint), section)) {
            for (@typeInfo(@field(endpoint, section)).@"struct".field_types) |T| validateScalar(T);
        }
    }
}

fn validateScalar(comptime T: type) void {
    switch (@typeInfo(T)) {
        .bool, .int, .@"enum" => {},
        .optional => |info| validateScalar(info.child),
        .pointer => |info| if (info.size != .slice or info.child != u8)
            @compileError("HTTP parameters must be strings, integers, booleans or enums"),
        else => @compileError("HTTP parameters must be strings, integers, booleans or enums"),
    }
}

fn scalarText(
    allocator: Allocator,
    value: anytype,
) !?[]const u8 {
    return switch (@typeInfo(@TypeOf(value))) {
        .optional => if (value) |v| scalarText(allocator, v) else null,
        .pointer => value,
        .bool => if (value) "true" else "false",
        .int => try std.fmt.allocPrint(allocator, "{d}", .{value}),
        .@"enum" => @tagName(value),
        else => unreachable,
    };
}

fn putHeader(
    headers: *std.ArrayList(std.http.Header),
    allocator: Allocator,
    header: std.http.Header,
) !void {
    if (std.mem.indexOfAny(u8, header.name, "\r\n:") != null or
        std.mem.indexOfAny(u8, header.value, "\r\n") != null)
        return error.InvalidHeader;

    for (headers.items) |*existing| {
        if (std.ascii.eqlIgnoreCase(existing.name, header.name)) {
            existing.* = header;
            return;
        }
    }

    try headers.append(allocator, header);
}

fn writeEndpointPath(
    comptime endpoint: anytype,
    args: anytype,
    allocator: Allocator,
    writer: *Io.Writer,
) !void {
    comptime var cursor: usize = 0;

    inline while (cursor < endpoint.path.len) {
        const start = comptime std.mem.indexOfScalarPos(u8, endpoint.path, cursor, '{') orelse endpoint.path.len;
        try writer.writeAll(endpoint.path[cursor..start]);
        if (comptime start == endpoint.path.len)
            break;

        const end = comptime std.mem.indexOfScalarPos(u8, endpoint.path, start + 1, '}').?;
        const name = comptime endpoint.path[start + 1 .. end];
        const text = (try scalarText(allocator, @field(args.path, name))).?;

        try writer.writeAll(try escape(allocator, text));

        cursor = end + 1;
    }
}

test "typed HTTP client encodes parameters, preserves base query and owns parsed responses" {
    const Client = HttpClient(.{
        .read = .{
            .method = .GET,
            .path = "/items/{owner}/{id}",
            .Path = struct { owner: []const u8, id: u64 },
            .Query = struct { search: []const u8, state: enum { waiting, running }, enabled: bool, limit: ?u32 = null },
            .Headers = struct { @"X-ScaleSetMaxCapacity": u32 },
            .Response = struct { name: []const u8, state: enum { waiting, running } },
        },
    });
    var server = try TestServer.init();
    defer server.deinit();
    const steps = [_]TestServer.Step{.{
        .method = "GET",
        .target = "/api/items/acme%2Fteam/42?existing=a%2Fb&search=hello%20%26%3F&state=waiting&enabled=true",
        .authorization = "Bearer secret",
        .capacity = "3",
        .reply = "{\"name\":\"owned response\",\"state\":\"running\",\"unknown\":true}",
    }};
    var peer = try std.testing.io.concurrent(TestServer.serve, .{ &server, &steps });
    defer peer.cancel(std.testing.io) catch {};
    var transport = Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    const base = try std.fmt.allocPrint(std.testing.allocator, "{s}/api/?existing=a%2Fb", .{server.url});
    defer std.testing.allocator.free(base);
    const client = Client.init(&transport, try std.Uri.parse(base), "Bearer secret");
    const result = try client.call(.read, std.testing.allocator, .{
        .path = .{ .owner = "acme/team", .id = 42 },
        .query = .{ .search = "hello &?", .state = .waiting, .enabled = true },
        .headers = .{ .@"X-ScaleSetMaxCapacity" = 1 },
        .extra_headers = &.{.{ .name = "x-scalesetmaxcapacity", .value = "3" }},
    });
    defer result.deinit();
    try peer.await(std.testing.io);
    try std.testing.expectEqualStrings("owned response", result.value.name);
    try std.testing.expectEqual(.running, result.value.state);
    try std.testing.expectEqual(.ok, result.status);
}

test "typed HTTP client serializes body and accepts empty optional and void responses" {
    const Client = HttpClient(.{
        .create = .{
            .method = .POST,
            .path = "/items",
            .Body = struct { name: []const u8, state: enum { waiting, running } },
            .Response = struct { id: u64 },
        },
        .poll = .{ .method = .GET, .path = "/unused", .Query = struct { last: u64 }, .Response = ?struct { id: u64 } },
        .remove = .{ .method = .DELETE, .path = "/items/{id}", .Path = struct { id: u64 } },
    });
    var server = try TestServer.init();
    defer server.deinit();
    const steps = [_]TestServer.Step{
        .{ .method = "POST", .target = "/items", .authorization = "token secret", .body = "{\"name\":\"quoted\\\"name\",\"state\":\"waiting\"}", .reply = "{\"id\":7}", .status = 201 },
        .{ .method = "GET", .target = "/queue?session=a%2Fb&last=7", .authorization = "token secret", .status = 202 },
        .{ .method = "DELETE", .target = "/items/7", .authorization = "token secret", .status = 204 },
    };
    var peer = try std.testing.io.concurrent(TestServer.serve, .{ &server, &steps });
    defer peer.cancel(std.testing.io) catch {};
    var transport = Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    const client = Client.init(&transport, try std.Uri.parse(server.url), "token secret");
    const created = try client.call(.create, std.testing.allocator, .{ .body = .{ .name = "quoted\"name", .state = .waiting } });
    defer created.deinit();
    try std.testing.expectEqual(@as(u64, 7), created.value.id);
    const queue = try std.fmt.allocPrint(std.testing.allocator, "{s}/queue?session=a%2Fb", .{server.url});
    defer std.testing.allocator.free(queue);
    const polled = try client.call(.poll, std.testing.allocator, .{ .uri = try std.Uri.parse(queue), .query = .{ .last = 7 } });
    defer polled.deinit();
    try std.testing.expectEqual(.accepted, polled.status);
    try std.testing.expectEqual(null, polled.value);
    const removed = try client.call(.remove, std.testing.allocator, .{ .path = .{ .id = 7 } });
    defer removed.deinit();
    try std.testing.expectEqual(.no_content, removed.status);
    try peer.await(std.testing.io);
}

test "typed HTTP client propagates parse and HTTP errors without retaining allocations" {
    const Client = HttpClient(.{
        .read = .{ .method = .GET, .path = "/item", .Response = struct { id: u64 } },
    });
    var server = try TestServer.init();
    defer server.deinit();
    const steps = [_]TestServer.Step{
        .{ .method = "GET", .target = "/item", .authorization = "Bearer secret", .reply = "{}" },
        .{ .method = "GET", .target = "/item", .authorization = "Bearer secret", .status = 404 },
    };
    var peer = try std.testing.io.concurrent(TestServer.serve, .{ &server, &steps });
    defer peer.cancel(std.testing.io) catch {};
    var transport = Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    const client = Client.init(&transport, try std.Uri.parse(server.url), "Bearer secret");
    try std.testing.expectError(error.InvalidHeader, client.call(.read, std.testing.allocator, .{
        .extra_headers = &.{.{ .name = "Accept", .value = "application/json\r\nInjected: yes" }},
    }));
    try std.testing.expectError(error.MissingField, client.call(.read, std.testing.allocator, .{}));
    try std.testing.expectError(error.NotFound, client.call(.read, std.testing.allocator, .{}));
    try peer.await(std.testing.io);
}

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

    pub fn serve(
        server: *TestServer,
        steps: []const Step,
    ) !void {
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
                if (std.mem.eql(u8, line, "\r\n"))
                    break;
                const colon = std.mem.indexOfScalar(u8, line, ':') orelse return error.InvalidHttpHeader;
                const value = std.mem.trim(u8, line[colon + 1 ..], " \r\n");
                if (std.ascii.eqlIgnoreCase(line[0..colon], "content-length")) length = try std.fmt.parseInt(usize, value, 10);
                if (std.ascii.eqlIgnoreCase(line[0..colon], "authorization")) authorization = try a.dupe(u8, value);
                if (std.ascii.eqlIgnoreCase(line[0..colon], "x-scalesetmaxcapacity")) capacity = try a.dupe(u8, value);
            }

            try std.testing.expectEqualStrings(step.authorization, authorization orelse "");
            if (step.capacity) |want|
                try std.testing.expectEqualStrings(want, capacity orelse "");

            const body = try reader.interface.readAllocAll(a, length);
            if (step.body) |want|
                try std.testing.expectEqualStrings(want, body);
            if (step.name_prefix != null or step.ephemeral != null or step.work_folder != null) {
                const parsed = try std.json.parseFromSlice(std.json.Value, a, body, .{});
                const object = parsed.value.object;
                if (step.name_prefix) |prefix|
                    try std.testing.expect(std.mem.startsWith(u8, object.get("name").?.string, prefix));
                if (step.ephemeral) |value|
                    try std.testing.expectEqual(value, object.get("ephemeral").?.bool);
                if (step.work_folder) |value|
                    try std.testing.expectEqualStrings(value, object.get("workFolder").?.string);
            }

            if (step.entered) |event|
                event.set(io);
            if (step.release) |event|
                try event.wait(io);

            const reply = try std.mem.replaceOwned(u8, a, step.reply, "$URL", server.url);
            var write_buffer: [8192]u8 = undefined;
            var writer = stream.writer(io, &write_buffer);
            try writer.interface.print("HTTP/1.1 {d} Test\r\nContent-Type: application/json\r\nContent-Length: {d}\r\nConnection: close\r\n\r\n{s}", .{ step.status, reply.len, reply });
            try writer.interface.flush();
        }
    }
};

test "GitHub repository queue preserves API base paths and encodes repository names" {
    const github = @import("github.zig");
    var server = try TestServer.init();
    defer server.deinit();
    const steps = [_]TestServer.Step{
        .{ .method = "POST", .target = "/api/v3/repos/acme/my%20repo/actions/runners/registration-token", .authorization = "Bearer pat", .reply = "{\"token\":\"registration\"}" },
        .{ .method = "POST", .target = "/api/v3/actions/runner-registration", .authorization = "RemoteAuth registration", .reply = "{\"url\":\"$URL/runtime\",\"token\":\"admin\"}" },
        .{ .method = "GET", .target = "/runtime/_apis/runtime/runnerscalesets?runnerGroupId=1&name=host-build&api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"value\":[{\"id\":7}]}" },
        .{ .method = "PATCH", .target = "/runtime/_apis/runtime/runnerscalesets/7?api-version=6.0-preview", .authorization = "Bearer admin" },
        .{ .method = "POST", .target = "/runtime/_apis/runtime/runnerscalesets/7/sessions?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"sessionId\":\"session\",\"messageQueueUrl\":\"$URL/queue\",\"messageQueueAccessToken\":\"queue\",\"statistics\":{\"totalAssignedJobs\":1}}" },
        .{ .method = "POST", .target = "/runtime/_apis/runtime/runnerscalesets/7/generatejitconfig?api-version=6.0-preview", .authorization = "Bearer admin", .reply = "{\"runner\":{\"id\":123},\"encodedJITConfig\":\"jit\"}" },
        .{ .method = "DELETE", .target = "/runtime/_apis/distributedtask/pools/0/agents/123?api-version=6.0-preview", .authorization = "Bearer admin", .status = 404 },
        .{ .method = "DELETE", .target = "/runtime/_apis/runtime/runnerscalesets/7/sessions/session?api-version=6.0-preview", .authorization = "Bearer admin", .status = 204 },
    };
    var peer = try Io.concurrent(std.testing.io, TestServer.serve, .{ &server, &steps });
    defer peer.cancel(std.testing.io) catch {};
    var transport = Api.init(std.testing.io, std.testing.allocator);
    defer transport.deinit();
    const base = try std.fmt.allocPrint(std.testing.allocator, "{s}/api/v3", .{server.url});
    defer std.testing.allocator.free(base);
    var queue = try github.init(&transport, std.testing.allocator, .{
        .version = "2.338.0",
        .runner_group_id = 1,
        .url = "https://github.com/acme/my%20repo",
        .api_url = base,
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
    const job = try queue.next(arena.allocator());
    try std.testing.expectEqualStrings("jit", job.jit_config);
    try queue.finish(job, false);
    alive = false;
    queue.deinit();
    try peer.await(std.testing.io);
}

test "Forgejo typed requests work in every scope and return owned credentials" {
    const forgejo = @import("forgejo.zig");
    const configs = [_]forgejo.Config{
        .{ .version = "13.0.0-zsf", .scope = .user },
        .{ .version = "13.0.0-zsf", .scope = .admin },
        .{ .version = "13.0.0-zsf", .scope = .{ .org = "team name" } },
        .{ .version = "13.0.0-zsf", .scope = .{ .repo = .{ .owner = "team name", .name = "my/repo" } } },
    };
    const scopes = [_][]const u8{ "user", "admin", "orgs/team%20name", "repos/team%20name/my%2Frepo" };
    for (configs, scopes) |settings, scope| {
        var server = try TestServer.init();
        defer server.deinit();
        var scratch = std.heap.ArenaAllocator.init(std.testing.allocator);
        defer scratch.deinit();
        const a = scratch.allocator();
        const base = try std.fmt.allocPrint(a, "/api/v1/{s}/actions/runners", .{scope});
        const steps = [_]TestServer.Step{
            .{ .method = "GET", .target = try std.fmt.allocPrint(a, "{s}/jobs?labels=linux", .{base}), .authorization = "token pat", .reply = "[{\"handle\":\"attempt\",\"status\":\"waiting\",\"runs_on\":[\"linux\"]}]" },
            .{ .method = "POST", .target = base, .authorization = "token pat", .reply = "{\"id\":7,\"uuid\":\"owned-uuid\",\"token\":\"owned-token\"}", .ephemeral = true },
            .{ .method = "DELETE", .target = try std.fmt.allocPrint(a, "{s}/7", .{base}), .authorization = "token pat", .status = 404 },
        };
        var peer = try Io.concurrent(std.testing.io, TestServer.serve, .{ &server, &steps });
        defer peer.cancel(std.testing.io) catch {};
        var transport = Api.init(std.testing.io, std.testing.allocator);
        defer transport.deinit();
        var cfg = settings;
        cfg.url = server.url;
        cfg.name = "build";
        cfg.labels = &.{"linux"};
        var queue = try forgejo.init(&transport, std.testing.allocator, cfg, "host", "pat");
        defer queue.deinit();
        const job = try queue.next(a);
        try std.testing.expectEqualStrings("owned-uuid", job.uuid);
        try std.testing.expectEqualStrings("owned-token", job.token);
        try queue.finish(job, true);
        try peer.await(std.testing.io);
    }
}
