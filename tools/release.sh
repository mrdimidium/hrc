#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -euo pipefail
# Do not expose signing secrets to Bazel, gh or other child processes.
signing_private_key=${GPG_PRIVATE_KEY:-}
signing_passphrase=${GPG_PASSPHRASE:-}
unset GPG_PRIVATE_KEY GPG_PASSPHRASE
storage_access_key=${S3_ACCESS_KEY_ID:-}
storage_secret_key=${S3_SECRET_ACCESS_KEY:-}
unset S3_ACCESS_KEY_ID S3_SECRET_ACCESS_KEY
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

fail() {
    printf '%s\n' "$*" >&2
    exit 1
}

publish() {
    S3_ACCESS_KEY_ID=$storage_access_key S3_SECRET_ACCESS_KEY=$storage_secret_key \
        bazel run //tools:publish -- --input "$PWD/$directory" --channel "$channel" "$@"
}

notes() {
    local section=$1 required=$2 line previous= changes= generated commits= in_section=false
    [[ -r docs/changelog.rst ]] || fail 'Could not read docs/changelog.rst'
    while IFS= read -r line || [[ -n "$line" ]]; do
        if [[ "$in_section" == false ]]; then
            if [[ "$line" == "\`$section\`_"* ]]; then
                in_section=true
            fi
        elif [[ "$line" == \`*\`_* || "$line" == '.. _'* ]]; then
            break
        elif [[ "$line" =~ ^-+$ ]]; then
            continue
        elif [[ "$line" =~ ^~+$ ]]; then
            changes="${changes%"$previous"$'\n'}### $previous"$'\n'
        else
            changes+="$line"$'\n'
        fi
        previous=$line
    done < docs/changelog.rst
    # GitHub release notes are Markdown, unlike the source changelog.
    changes=${changes//\`\`/\`}
    changes="${changes#"${changes%%[![:space:]]*}"}"
    changes="${changes%"${changes##*[![:space:]]}"}"
    if [[ "$required" == true && -z "$changes" ]]; then
        fail "docs/changelog.rst has no notes under $section"
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
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+[-+A-Za-z0-9.]*$ ]] || fail 'Release version is invalid'
[[ "$recorded_version" == "$version" ]] || fail "VERSION does not match release version: $recorded_version != $version"

names=()

: > "$directory/SHASUMS256"
for target in amd64-linux arm64-linux amd64-darwin arm64-darwin amd64-windows arm64-windows; do
    manifest="SHASUMS256-$target.txt"
    [[ $(wc -l < "$directory/$manifest") -eq 1 ]] || fail "Expected one checksum in $manifest"

    read -r digest archive < "$directory/$manifest"
    [[ "$archive" == "hrc-$target-$version.tar.gz" ]] || fail "Unexpected archive in $manifest: $archive"

    (cd "$directory" && sha256sum --check --strict "$manifest")
    printf '%s  %s\n' "$digest" "$archive" >> "$directory/SHASUMS256"
    names+=("$archive")
done

: "${S3_PUBLIC_URL:?S3_PUBLIC_URL is required as the public mirror base URL}"
mirror="${S3_PUBLIC_URL%/}/hrc/builds/$version"
HRC_ARTIFACT_SOURCE=$mirror
HRC_VERSION=$version HRC_BUILD_MODE=release \
    bazel build //ansible:collection //:docs "--//ansible:artifact_source=$HRC_ARTIFACT_SOURCE"
collection="ansible-collection-$version.tar.gz"
cp bazel-bin/ansible/collection.tar.gz "$directory/$collection"
(cd "$directory" && sha256sum "$collection") >> "$directory/SHASUMS256"
names+=("$collection")

documentation="hrc-docs-$version.tar.gz"
cp "bazel-bin/docs/$documentation" "$directory/"
(cd "$directory" && sha256sum "$documentation") >> "$directory/SHASUMS256"
names+=("$documentation")

revision=${GITHUB_SHA:-$(git rev-parse HEAD)}
[[ "$revision" =~ ^[0-9a-fA-F]{40}$ ]] || fail 'Release commit must be a full Git SHA'
artifacts=()
for name in "${names[@]}"; do
    digest=$(sha256sum "$directory/$name")
    size=$(wc -c < "$directory/$name")
    os= arch= kind=archive
    case "$name" in
        hrc-docs-*) kind=documentation ;;
        hrc-*)
            target=${name#hrc-}
            arch=${target%%-*}
            target=${target#*-}
            os=${target%%-*}
            ;;
        ansible-collection-*) kind=ansible-collection ;;
    esac
    artifacts+=("$(jq -n \
        --arg filename "$name" --arg sha256 "${digest%% *}" --argjson size "$size" \
        --arg kind "$kind" --arg os "$os" --arg arch "$arch" \
        '{filename: $filename, sha256: $sha256, size: $size, kind: $kind}
         + if $os == "" then {} else {os: $os, arch: $arch} end')")
done
printf '%s\n' "${artifacts[@]}" | jq -s \
    --arg version "$version" --arg commit "$revision" \
    '{schemaVersion: 1, product: "hrc", version: $version, commit: $commit,
      artifacts: sort_by(.filename)}' > "$directory/manifest.json"
(cd "$directory" && sha256sum manifest.json) >> "$directory/SHASUMS256"
LC_ALL=C sort -k 2 -o "$directory/SHASUMS256" "$directory/SHASUMS256"
if [[ "$dry_run" == true ]]; then
    printf 'Validated release %s: %s\n' "$version" "${names[*]}"
    exit 0
fi

: "${GH_REPO:?GH_REPO is required for publishing}"
: "${GITHUB_SHA:?GITHUB_SHA is required for publishing}"
: "${GPG_KEY_ID:?GPG_KEY_ID is required for signing}"
: "${signing_private_key:?GPG_PRIVATE_KEY is required for signing}"
: "${signing_passphrase:?GPG_PASSPHRASE is required for signing}"
: "${S3_BUCKET:?S3_BUCKET is required for publishing}"
: "${S3_ENDPOINT:?S3_ENDPOINT is required for publishing}"
: "${storage_access_key:?S3_ACCESS_KEY_ID is required for publishing}"
: "${storage_secret_key:?S3_SECRET_ACCESS_KEY is required for publishing}"
case ${GITHUB_REF:-} in
    refs/heads/main) channel=dev ;;
    "refs/tags/release-$version") channel=stable ;;
    *) fail 'Ref is not publishable' ;;
esac

# Keep signing isolated from the user's keyring and never put secrets in argv.
(
    signing_home=$(mktemp -d)
    chmod 0700 "$signing_home"
    cleanup_signing() {
        gpgconf --homedir "$signing_home" --kill gpg-agent || true
        rm -rf -- "$signing_home"
    }
    trap cleanup_signing EXIT
    private_key=$signing_private_key
    passphrase=$signing_passphrase
    unset signing_private_key signing_passphrase GH_TOKEN
    printf '%s\n' "$private_key" | gpg --homedir "$signing_home" --batch --import
    unset private_key
    gpg --homedir "$signing_home" --batch --yes --pinentry-mode loopback \
        --passphrase-fd 3 --local-user "$GPG_KEY_ID" --armor --detach-sign \
        --output "$signing_home/SHASUMS256.asc" "$directory/SHASUMS256" \
        3<<< "$passphrase"
    unset passphrase
    gpg --homedir "$signing_home" --batch --verify \
        "$signing_home/SHASUMS256.asc" "$directory/SHASUMS256"
    mv "$signing_home/SHASUMS256.asc" "$directory/SHASUMS256.asc"
    # Upload before changing GitHub. The publisher retains an existing valid
    # signature on retries, so both mirrors expose exactly the same files.
    publish --keyring "$signing_home"
)
unset signing_private_key signing_passphrase

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
    body+=$'\n\n'"**Public mirror:** [Build manifest]($mirror/manifest.json)"
    if gh release view "$tag" --json isDraft,assets > /dev/null 2>&1; then
        gh release edit "$tag" --title "$tag" --notes "$body" --prerelease --latest=false
    else
        gh release create "$tag" --verify-tag --title "$tag" --notes "$body" --prerelease --latest=false
    fi
else
    [[ "$ref" == "refs/tags/release-$version" ]] || fail "Release ref does not match version: $ref"
    tag=${ref#refs/tags/}
    body=$(notes "$version" true)
    body+=$'\n\n'"**Public mirror:** [Build manifest]($mirror/manifest.json)"
    if existing=$(gh release view "$tag" --json isDraft 2>/dev/null); then
        if ! jq -e '.isDraft == true' <<< "$existing" > /dev/null; then
            # Recover if GitHub publication succeeded but channel promotion failed.
            # Never overwrite an already published stable release.
            (
                verification=$(mktemp -d)
                trap 'rm -rf -- "$verification"' EXIT
                gh release download "$tag" --dir "$verification" \
                    --pattern SHASUMS256 --pattern SHASUMS256.asc --pattern manifest.json
                for name in SHASUMS256 SHASUMS256.asc manifest.json; do
                    cmp "$directory/$name" "$verification/$name" || fail "Published release $tag differs: $name"
                done
            )
            publish --promote
            exit 0
        fi
    else
        gh release create "$tag" --verify-tag --draft --title "$version" --notes "$body"
    fi
fi

published=("${names[@]}" SHASUMS256 SHASUMS256.asc manifest.json)
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

# Only completed publications become the current channel build.
publish --promote
unset storage_access_key storage_secret_key
