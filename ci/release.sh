#!/bin/bash
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -euo pipefail

(cd dist/release && sha256sum --check SHASUMS256.txt)

# All targets are built for coverage; publish only supported bundled-runner targets.
archives=(dist/release/hrc-{amd64,arm64}-{linux,darwin,windows}-"$HRC_VERSION".tar.gz)
(cd dist/release && sha256sum "${archives[@]##*/}" > SHASUMS256.txt)

if [ "$GITHUB_REF" = refs/heads/main ]; then
  tag=dev
  # Move the development tag to the commit used by this build.
  if gh api "repos/$GH_REPO/git/ref/tags/$tag" > /dev/null; then
    gh api --method PATCH "repos/$GH_REPO/git/refs/tags/$tag" \
      -f sha="$GITHUB_SHA" -F force=true
  else
    gh api --method POST "repos/$GH_REPO/git/refs" \
      -f ref="refs/tags/$tag" -f sha="$GITHUB_SHA"
  fi

  notes="Development build $HRC_VERSION from commit $GITHUB_SHA."
  if gh release view "$tag" > /dev/null; then
    gh release edit "$tag" --title "$tag" --notes "$notes" --prerelease --latest=false
  else
    gh release create "$tag" --verify-tag --title "$tag" --notes "$notes" \
      --prerelease --latest=false
  fi
else
  tag="${GITHUB_REF#refs/tags/}"
  # Published release contents are immutable; a failed draft can be retried.
  if gh release view "$tag" --json isDraft > "$RUNNER_TEMP/hrc-release.json"; then
    if ! jq -e '.isDraft == true' "$RUNNER_TEMP/hrc-release.json" > /dev/null; then
      echo "Release $tag is already published; refusing to overwrite it." >&2
      exit 1
    fi
  else
    gh release create "$tag" --verify-tag --draft --title "$HRC_VERSION" --generate-notes
  fi
fi

gh release upload "$tag" "${archives[@]}" dist/release/SHASUMS256.txt --clobber

if [ "$tag" = dev ]; then
  # Versioned filenames change on every run; retain only this build's assets.
  gh release view "$tag" --json assets --jq '.assets[].name' > "$RUNNER_TEMP/hrc-assets.txt"
  while IFS= read -r asset; do
    keep=no
    for published in "${archives[@]}" dist/release/SHASUMS256.txt; do
      if [ "$asset" = "${published##*/}" ]; then keep=yes; break; fi
    done
    if [ "$keep" = no ]; then gh release delete-asset "$tag" "$asset" --yes; fi
  done < "$RUNNER_TEMP/hrc-assets.txt"
else
  gh release edit "$tag" --draft=false
fi
