# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Declare the repository's rules_lint aspects."""

load("@aspect_rules_lint//lint:buildifier.bzl", "lint_buildifier_aspect")
load(":tools.bzl", "BUILDIFIER_ARGS")

buildifier = lint_buildifier_aspect(
    args = BUILDIFIER_ARGS,
    binary = Label("//tools:buildifier_lint"),
    warnings = "all",
)
