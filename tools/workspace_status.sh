#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

# Resolve the root VERSION and Git metadata into stable Bazel stamp values.
set -eu

ROOT=$(CDPATH= cd -P "$(dirname "$0")/.." && pwd)

fail() {
    printf '%s\n' "$*" >&2
    exit 1
}

parse_version() (
    value=$1
    # grep matches lines; reject embedded newlines before validating SemVer.
    case "$value" in
        *'
'*) fail "invalid semantic version: $value" ;;
    esac
    pattern='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
    printf '%s\n' "$value" | LC_ALL=C grep -Eq "$pattern" || fail "invalid semantic version: $value"
    core=${value%%[-+]*}
    prerelease=${value%%+*}
    case "$prerelease" in
        *-*) prerelease=${prerelease#*-} ;;
        *) prerelease= ;;
    esac
    if printf '%s\n' "$prerelease" | LC_ALL=C grep -Eq '(^|\.)0[0-9]+(\.|$)'; then
        fail "invalid semantic version: $value"
    fi
    printf '%s' "$core"
)

git_value() (
    if value=$(git -C "$ROOT" "$@" 2>/dev/null); then
        printf '%s' "$value"
    fi
)

resolve_mode() (
    base=$1
    ref=${GITHUB_REF:-}
    case "$ref" in
        refs/tags/release-*)
            [ "${ref#refs/tags/release-}" = "$base" ] || fail "release tag $ref does not match version $base"
            printf '%s' release
            ;;
        refs/heads/main) printf '%s' dev ;;
        *) printf '%s' snapshot ;;
    esac
)

resolve_version() (
    base=$1 mode=$2 commit=$3
    core=$(parse_version "$base") || return
    case "$mode" in
        release) printf '%s' "$base"; return ;;
        dev|snapshot) ;;
        *) fail 'HRC_BUILD_MODE must be release, dev or snapshot' ;;
    esac

    ref=HEAD
    if [ "$mode" = snapshot ]; then
        ref=$(git_value merge-base HEAD origin/main)
        ref=${ref:-HEAD}
    fi
    tag=$(git_value describe --tags --abbrev=0 --match 'release-[0-9]*' "$ref")
    cnt=$(git_value rev-list --count --first-parent "${tag:+$tag..}$ref")
    cnt=${cnt:-65536}
    case "$cnt" in
        *[!0-9]*) fail "invalid Git commit count: $cnt" ;;
    esac
    # Match Python's int(cnt), without shell-specific decimal arithmetic.
    while [ "${cnt#0}" != "$cnt" ]; do cnt=${cnt#0}; done
    pre="dev.${cnt:-0}"
    [ "$mode" != snapshot ] || pre="${pre}.snapshot"
    printf '%s-%s+%s' "$core" "$pre" "$commit"
)

main() (
    base=${HRC_VERSION:-}
    if [ -z "$base" ]; then
        base=$(cat "$ROOT/VERSION")
        base="${base#"${base%%[![:space:]]*}"}"
        base="${base%"${base##*[![:space:]]}"}"
    fi
    mode=${HRC_BUILD_MODE:-$(resolve_mode "$base")}
    commit=${HRC_COMMIT:-$(git_value rev-parse --short=12 HEAD)}
    commit=${commit:-unknown}
    version=$(resolve_version "$base" "$mode" "$commit")
    printf 'STABLE_HRC_VERSION %s\n' "$version"
    full_commit=$(git_value rev-parse HEAD)
    printf 'STABLE_HRC_COMMIT %s\n' "${full_commit:-unknown}"
)

main
