# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Declare the repository's rules_lint aspects."""

load("@aspect_rules_lint//lint:buildifier.bzl", "lint_buildifier_aspect")

buildifier = lint_buildifier_aspect(
    binary = Label("//tools:buildifier"),
    warnings = "all",
)
