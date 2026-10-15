#!/bin/bash
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu
buildifier="${RUNFILES_DIR:-$0.runfiles}/@BUILDIFIER_RUNFILE@"

# Aspect's explicit fix action operates on its own temporary copies.
for argument in "$@"; do
    case "$argument" in
        --lint=fix|-lint=fix) exec "$buildifier" "$@" ;;
    esac
done

flags=()
files=()
for argument in "$@"; do
    case "$argument" in
        -*) flags+=("$argument") ;;
        *) files+=("$argument") ;;
    esac
done

status=0
for file in "${files[@]}"; do
    # With stdin, fix mode prints formatted code instead of writing the input.
    # Discard that code, retaining lint diagnostics and their exit status.
    if "$buildifier" "${flags[@]}" --mode=fix --path="$file" < "$file" > /dev/null; then
        :
    else
        status=$?
    fi
done
exit "$status"
