#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

set -eu

[ "$#" = 2 ] || [ "$#" = 3 ] || {
  echo 'Usage: test.sh arch os [vm-release]' >&2
  exit 1
}

arch=$1
os=$2

case $os in
linux | darwin) extension= ;;
windows) extension=.exe ;;
*)
  echo "Unsupported test OS: $os" >&2
  exit 1
  ;;
esac

tests="zig-out/tests/hrc-$arch-$os$extension"
name=${tests##*/}
expected=$(awk -v name="$name" 'substr($0, 67) == name { print substr($0, 1, 64); count++ } END { if (count != 1) exit 1 }' zig-out/tests/SHASUMS256.txt)

case $os in
linux | windows) actual=$(sha256sum "$tests") ;;
darwin) actual=$(shasum -a 256 "$tests") ;;
esac

actual=${actual%% *}
[ "$actual" = "$expected" ] || {
  echo "SHA256 mismatch: $tests" >&2
  exit 1
}

chmod 0755 "$tests"

"./$tests"
