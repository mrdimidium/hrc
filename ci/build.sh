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
  pkg/services/hrc.alpine \
  pkg/services/hrc.freebsd \
  pkg/services/hrc.netbsd \
  pkg/services/hrc.openbsd

for target in \
  x86_64-linux-musl aarch64-linux-musl riscv64-linux-musl s390x-linux loongarch64-linux powerpc64le-linux \
  x86_64-openbsd aarch64-openbsd \
  x86_64-freebsd aarch64-freebsd \
  x86_64-netbsd aarch64-netbsd \
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
