// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const std = @import("std");
const Sha256 = std.crypto.hash.sha2.Sha256;

const Hash = [Sha256.digest_length]u8;

/// Downloads to <path>.tmp, verifies SHA-256, then renames to path.
/// SHA is a 64-character hexadecimal SHA-256 digest.
/// Existing destination and temporary files are preserved; failures remove our temporary file.
pub fn download(
    io: std.Io,
    allocator: std.mem.Allocator,
    uri: std.Uri,
    sha: []const u8,
    path: []const u8,
) !void {
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

    var client = std.http.Client{ .allocator = allocator, .io = io };
    defer client.deinit();

    var request = try client.request(.GET, uri, .{
        .redirect_behavior = @fromBackingInt(@intCast(5)),
        .headers = .{ .accept_encoding = .{ .override = "identity" } },
    });
    defer request.deinit();

    try request.sendBodiless();

    var redirect_buffer: [16 * 1024]u8 = undefined;
    var transfer_buffer: [16 * 1024]u8 = undefined;

    var response = try request.receiveHead(&redirect_buffer);
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

fn parseSha(hex: []const u8) !Hash {
    if (hex.len != Sha256.digest_length * 2)
        return error.InvalidSha;

    var digest: Hash = undefined;
    _ = std.fmt.hexToBytes(&digest, hex) catch return error.InvalidSha;

    return digest;
}

test "invalid input and existing temporary file never start a download" {
    const io = std.testing.io;
    const allocator = std.testing.allocator;

    var tmp = std.testing.tmpDir(.{});
    defer tmp.cleanup();

    const uri = try std.Uri.parse("https://example.com/archive.tar.gz");

    try std.testing.expectError(error.InvalidSha, download(io, allocator, uri, "", "unused"));
    try std.testing.expectError(error.InvalidSha, download(io, allocator, uri, "bad", "unused"));

    try tmp.dir.writeFile(io, .{ .sub_path = "existing.tmp", .data = "keep" });

    var path_buffer: [std.Io.Dir.max_path_bytes]u8 = undefined;
    const root = path_buffer[0..try tmp.dir.realPath(io, &path_buffer)];

    const folder = try std.fs.path.join(allocator, &.{ root, "existing" });
    defer allocator.free(folder);

    const hex = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";
    try std.testing.expectError(error.PathAlreadyExists, download(io, allocator, uri, hex, folder));

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
