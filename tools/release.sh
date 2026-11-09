#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

fail() {
    printf '%s\n' "$*" >&2
    exit 1
}

notes() {
    local section=$1 required=$2 line changes= generated commits= in_section=false
    [[ -r docs/CHANGELOG.md ]] || fail 'Could not read docs/CHANGELOG.md'
    while IFS= read -r line || [[ -n "$line" ]]; do
        if [[ "$in_section" == false ]]; then
            if [[ "$line" == "## [$section]"* ]]; then
                in_section=true
            fi
        elif [[ "$line" == '## '* || "$line" == '['*']: '* ]]; then
            break
        else
            changes+="$line"$'\n'
        fi
    done < docs/CHANGELOG.md
    changes="${changes#"${changes%%[![:space:]]*}"}"
    changes="${changes%"${changes##*[![:space:]]}"}"
    if [[ "$required" == true && -z "$changes" ]]; then
        fail "docs/CHANGELOG.md has no notes under ## [$section]"
    fi
    generated=$(gh api --method POST "repos/$GH_REPO/releases/generate-notes" -f "tag_name=$tag" --jq '.body') || fail "Could not generate release notes for $tag"
    while IFS= read -r line; do
        if [[ "$line" == '**Full Changelog**'* ]]; then
            commits=$line
            break
        fi
    done <<< "$generated"
    printf '%s' "$changes"
    if [[ -n "$commits" ]]; then
        if [[ -n "$changes" ]]; then
            printf '\n\n'
        fi
        printf '%s' "$commits"
    fi
}

dry_run=false
if [[ $# -eq 1 && "$1" == --dry-run ]]; then
    dry_run=true
elif [[ $# -ne 0 ]]; then
    printf 'Usage: %s [--dry-run]\n' "$0" >&2
    exit 2
fi

directory=dist/release
recorded_version=$(< "$directory/VERSION")
version=${HRC_VERSION:-$recorded_version}
[[ -n "$version" ]] || fail 'Release version is empty'
[[ "$recorded_version" == "$version" ]] || fail "VERSION does not match release version: $recorded_version != $version"

names=()

: > "$directory/SHASUMS256.txt"
for target in amd64-linux arm64-linux amd64-darwin arm64-darwin amd64-windows arm64-windows; do
    manifest="SHASUMS256-$target.txt"
    [[ $(wc -l < "$directory/$manifest") -eq 1 ]] || fail "Expected one checksum in $manifest"

    read -r digest archive < "$directory/$manifest"
    [[ "$archive" == "hrc-$target-$version.tar.gz" ]] || fail "Unexpected archive in $manifest: $archive"

    (cd "$directory" && sha256sum --check --strict "$manifest")
    printf '%s  %s\n' "$digest" "$archive" >> "$directory/SHASUMS256.txt"
    names+=("$archive")
done

: "${HRC_ARTIFACT_SOURCE:?HRC_ARTIFACT_SOURCE is required as the default collection source}"
bazel build //ansible:collection "--//ansible:artifact_source=$HRC_ARTIFACT_SOURCE"
collection="ansible-collection-$version.tar.gz"
cp bazel-bin/ansible/collection.tar.gz "$directory/$collection"
(cd "$directory" && sha256sum "$collection") >> "$directory/SHASUMS256.txt"
names+=("$collection")

LC_ALL=C sort -k 2 -o "$directory/SHASUMS256.txt" "$directory/SHASUMS256.txt"
if [[ "$dry_run" == true ]]; then
    printf 'Validated release %s: %s\n' "$version" "${names[*]}"
    exit 0
fi

: "${GH_REPO:?GH_REPO is required for publishing}"
: "${GITHUB_SHA:?GITHUB_SHA is required for publishing}"

ref=${GITHUB_REF:-}
if [[ "$ref" == refs/heads/main ]]; then
    tag=dev
    if gh api "repos/$GH_REPO/git/ref/tags/$tag" > /dev/null 2>&1; then
        gh api --method PATCH "repos/$GH_REPO/git/refs/tags/$tag" -f "sha=$GITHUB_SHA" -F force=true
    else
        gh api --method POST "repos/$GH_REPO/git/refs" -f "ref=refs/tags/$tag" -f "sha=$GITHUB_SHA"
    fi
    body="Development build $version from commit $GITHUB_SHA."
    changes=$(notes Unreleased false)
    if [[ -n "$changes" ]]; then
        body+=$'\n\n'"$changes"
    fi
    if gh release view "$tag" --json isDraft,assets > /dev/null 2>&1; then
        gh release edit "$tag" --title "$tag" --notes "$body" --prerelease --latest=false
    else
        gh release create "$tag" --verify-tag --title "$tag" --notes "$body" --prerelease --latest=false
    fi
else
    [[ "$ref" == "refs/tags/release-$version" ]] || fail "Release ref does not match version: $ref"
    tag=${ref#refs/tags/}
    body=$(notes "$version" true)
    if existing=$(gh release view "$tag" --json isDraft 2>/dev/null); then
        jq -e '.isDraft == true' <<< "$existing" > /dev/null || fail "Release $tag is already published; refusing to overwrite it."
    else
        gh release create "$tag" --verify-tag --draft --title "$version" --notes "$body"
    fi
fi

published=("${names[@]}" SHASUMS256.txt VERSION)
paths=()
for name in "${published[@]}"; do
    paths+=("$directory/$name")
done
gh release upload "$tag" "${paths[@]}" --clobber
if [[ "$tag" == dev ]]; then
    assets=$(gh release view "$tag" --json assets --jq '.assets[].name')
    while IFS= read -r asset; do
        [[ -n "$asset" ]] || continue
        keep=false
        for name in "${published[@]}"; do
            if [[ "$asset" == "$name" ]]; then
                keep=true
                break
            fi
        done
        if [[ "$keep" == false ]]; then
            gh release delete-asset "$tag" "$asset" --yes
        fi
    done <<< "$assets"
else
    gh release edit "$tag" --draft=false --notes "$body"
fi
