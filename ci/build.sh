#!/bin/bash
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -euo pipefail

reuse lint
zig fmt --check build.zig build.zig.zon src
shellcheck -x -P SCRIPTDIR \
  ci/*.sh \
  pkg/control \
  pkg/scripts/*.sh \
  pkg/services/hrc.alpine

# Fetch each archive in a separate process to avoid reusing idle connections.
# --save-exact also prepares Zig's local package layout; use a temporary manifest.
fetch_dir=$(mktemp -d)
trap 'rm -rf "$fetch_dir"' EXIT
cp build.zig.zon "$fetch_dir/build.zig.zon"
touch "$fetch_dir/build.zig"
awk '
  /\.url =/ { split($0, fields, "\""); url = fields[2] }
  /\.hash =/ && url != "" {
    split($0, fields, "\""); print url, fields[2]; url = ""
  }
' build.zig.zon | while read -r url expected; do
  echo "Fetching $url"

  (cd "$fetch_dir" && zig fetch --save-exact=archive "$url")

  actual=$(awk '
    $1 == ".archive" { found = 1 }
    found && /\.hash =/ { split($0, fields, "\""); print fields[2]; exit }
  ' "$fetch_dir/build.zig.zon")

  if [ "$actual" != "$expected" ]; then
    echo "Package hash mismatch for $url: expected $expected, got $actual" >&2
    exit 1
  fi
done
rm -rf "$fetch_dir"
trap - EXIT

for target in \
  x86_64-linux-musl aarch64-linux-musl \
  x86_64-macos aarch64-macos \
  x86_64-windows aarch64-windows; do

  zig build release -Dtarget="$target" -Doptimize=ReleaseSafe -j2 --summary all
  case $target in
    x86_64-*|aarch64-macos)
      zig build test-build -Dtarget="$target" -Doptimize=ReleaseSafe -j2 --summary all
      ;;
  esac

done
echo "version=$(cat zig-out/release/VERSION)" >>"$GITHUB_OUTPUT"

(cd zig-out/release && sha256sum hrc-*.tar.gz >SHASUMS256.txt)
(cd zig-out/tests && sha256sum hrc-* >SHASUMS256.txt)
