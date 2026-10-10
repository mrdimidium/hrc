// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

const api = @import("api.zig");

pub const Connection = struct { url: []const u8, token: []const u8 };
pub const Statistics = struct { totalAssignedJobs: u32 = 0 };
pub const Session = struct {
    sessionId: []const u8,
    messageQueueUrl: []const u8,
    messageQueueAccessToken: []const u8,
    statistics: ?Statistics = null,
};
pub const Label = struct { name: []const u8, type: enum { System } = .System };
pub const Event = struct { messageType: []const u8, runnerRequestId: u64 = 0, runnerId: u64 = 0 };
const ScaleSet = struct { id: u64 };
const Settings = struct {
    name: []const u8,
    runnerGroupId: u64,
    labels: []const Label,
    RunnerSetting: struct { disableUpdate: bool },
};
const Version = struct { @"api-version": []const u8 = "6.0-preview" };
const ScaleSetPath = struct { scaleSetId: u64 };
const SessionPath = struct { scaleSetId: u64, sessionId: []const u8 };

/// Only HTTP schemas; authorization refresh and message handling belong to the provider.
pub const Client = api.HttpClient(.{
    .org_registration_token = .{
        .method = .POST,
        .path = "/orgs/{org}/actions/runners/registration-token",
        .Path = struct { org: []const u8 },
        .Response = struct { token: []const u8 },
    },
    .repo_registration_token = .{
        .method = .POST,
        .path = "/repos/{owner}/{repo}/actions/runners/registration-token",
        .Path = struct { owner: []const u8, repo: []const u8 },
        .Response = struct { token: []const u8 },
    },
    .register = .{
        .method = .POST,
        .path = "/actions/runner-registration",
        .Body = struct { url: []const u8, runner_event: enum { register } },
        .Response = Connection,
    },
    .list_scale_sets = .{
        .method = .GET,
        .path = "/_apis/runtime/runnerscalesets",
        .Query = struct { runnerGroupId: u64, name: []const u8, @"api-version": []const u8 = "6.0-preview" },
        .Response = struct { value: []const ScaleSet },
    },
    .create_scale_set = .{
        .method = .POST,
        .path = "/_apis/runtime/runnerscalesets",
        .Query = Version,
        .Body = Settings,
        .Response = ScaleSet,
    },
    .update_scale_set = .{
        .method = .PATCH,
        .path = "/_apis/runtime/runnerscalesets/{scaleSetId}",
        .Path = ScaleSetPath,
        .Query = Version,
        .Body = Settings,
    },
    .create_session = .{
        .method = .POST,
        .path = "/_apis/runtime/runnerscalesets/{scaleSetId}/sessions",
        .Path = ScaleSetPath,
        .Query = Version,
        .Body = struct { ownerName: []const u8 },
        .Response = Session,
    },
    .refresh_session = .{
        .method = .PATCH,
        .path = "/_apis/runtime/runnerscalesets/{scaleSetId}/sessions/{sessionId}",
        .Path = SessionPath,
        .Query = Version,
        .Response = Session,
    },
    .delete_session = .{
        .method = .DELETE,
        .path = "/_apis/runtime/runnerscalesets/{scaleSetId}/sessions/{sessionId}",
        .Path = SessionPath,
        .Query = Version,
    },
    .generate_jit_config = .{
        .method = .POST,
        .path = "/_apis/runtime/runnerscalesets/{scaleSetId}/generatejitconfig",
        .Path = ScaleSetPath,
        .Query = Version,
        .Body = struct { name: []const u8, workFolder: []const u8 },
        .Response = struct { runner: ScaleSet, encodedJITConfig: []const u8 },
    },
    // The server supplies the complete queue URI, including its query parameters.
    .poll_messages = .{
        .method = .GET,
        .path = "/",
        .Query = struct { lastMessageId: u64 },
        .Headers = struct { @"X-ScaleSetMaxCapacity": u32 },
        .Response = ?struct { messageId: u64, body: []const u8 = "", statistics: Statistics },
    },
    .acknowledge_message = .{
        .method = .DELETE,
        .path = "/{messageId}",
        .Path = struct { messageId: u64 },
    },
    .acquire_jobs = .{
        .method = .POST,
        .path = "/_apis/runtime/runnerscalesets/{scaleSetId}/acquirejobs",
        .Path = ScaleSetPath,
        .Query = Version,
        .Body = []const u64,
        .Response = struct { value: []const u64 },
    },
    .delete_runner = .{
        .method = .DELETE,
        .path = "/_apis/distributedtask/pools/0/agents/{runnerId}",
        .Path = struct { runnerId: u64 },
        .Query = Version,
    },
});
