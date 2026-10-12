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

# Published GitHub Release SHA256 digests, pinned with the download URLs.
if [ "$#" = 3 ]; then
  case $os:$3 in
  alpine:3.23)
    image_url=https://github.com/anyvm-org/alpine-builder/releases/download/v2.0.3/alpine-3.23.qcow2.zst
    image_sha256=53a343e0963fad714d1e1989f68ce0c8db2710992d949284b769070149bfb953
    key_sha256=1aff1b5bf1caed280d32424615cd1250200a26133ccf376ba3e367668c7f6d8e
    ;;
  freebsd:14.3)
    image_url=https://github.com/anyvm-org/freebsd-builder/releases/download/v2.2.8/freebsd-14.3.qcow2.zst
    image_sha256=3d26c952026c660e1a081ce32e84d7cccb3707833d07231efa020071261aad4f
    key_sha256=ca74ee66322879bd25654034caeaca043d7714973d634adb9dfecbc420af945e
    ;;
  *) echo "Unpinned VM OS/release: $os $3" >&2; exit 1 ;;
  esac
fi

case $arch in
x86_64 | aarch64) ;;
*)
  echo "Unsupported test architecture: $arch" >&2
  exit 1
  ;;
esac

if [ "$#" = 3 ]; then
  # These pinned images and hardware settings are for the CI's x86_64 guests.
  [ "$arch" = x86_64 ] || { echo 'VM tests currently require x86_64' >&2; exit 1; }

  case $os in
  alpine | freebsd) net_card=virtio-net-pci ;;
  esac

  # Cache the compressed image; verify it before every extraction.
  cache_dir=${XDG_CACHE_HOME:-$HOME/.cache}/hrc-vm
  archive=$cache_dir/$image_sha256.qcow2.zst
  key=$cache_dir/$key_sha256.id_rsa
  mkdir -p "$cache_dir"
  work_dir=$(mktemp -d)
  vm_pid=

  # shellcheck disable=SC2329 # Called by the EXIT trap.
  cleanup() {
    status=$?
    trap - EXIT
    if [ -n "$vm_pid" ]; then
      kill "$vm_pid" 2>/dev/null || true
      wait "$vm_pid" 2>/dev/null || true
    fi
    if [ "$status" != 0 ]; then
      cat "$work_dir/qemu.log" "$work_dir/serial.log" "$work_dir/ssh.log" 2>/dev/null >&2 || true
    fi
    rm -rf "$work_dir"
    exit "$status"
  }
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  if [ ! -f "$archive" ]; then
    curl -fL --retry 3 "$image_url" -o "$work_dir/image.zst"
    printf '%s  %s\n' "$image_sha256" "$work_dir/image.zst" | sha256sum --check
    mv "$work_dir/image.zst" "$archive"
  fi
  printf '%s  %s\n' "$image_sha256" "$archive" | sha256sum --check
  image=$work_dir/image.qcow2
  zstd -d "$archive" -o "$image"
  if [ ! -f "$key" ]; then
    curl -fL --retry 3 "${image_url%.qcow2.zst}-host.id_rsa" -o "$work_dir/key"
    printf '%s  %s\n' "$key_sha256" "$work_dir/key" | sha256sum --check
    chmod 0600 "$work_dir/key"
    mv "$work_dir/key" "$key"
  fi
  printf '%s  %s\n' "$key_sha256" "$key" | sha256sum --check

  port=${HRC_VM_SSH_PORT:-2222}
  accel=tcg
  cpu=qemu64
  if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
    accel=kvm
  fi
  # A conservative CPU avoids guest boot failures on newer host CPUs.
  qemu-system-x86_64 -machine "pc,accel=$accel,hpet=off,smm=off" \
    -cpu "$cpu" -m 2048 -smp 2 -snapshot \
    -drive "file=$image,format=qcow2,if=virtio" \
    -netdev "user,id=net0,net=192.168.122.0/24,dhcpstart=192.168.122.254,ipv6=off,hostfwd=tcp:127.0.0.1:$port-192.168.122.254:22" \
    -device "$net_card,netdev=net0" \
    -object rng-builtin,id=rng0 -device virtio-rng-pci,rng=rng0 \
    -display none -monitor none -serial "file:$work_dir/serial.log" \
    >"$work_dir/qemu.log" 2>&1 &
  vm_pid=$!

  vm_ssh() {
    ssh -F /dev/null -i "$key" -p "$port" \
      -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 \
      -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
      root@127.0.0.1 "$@"
  }
  ready=false
  attempts=0
  while [ "$attempts" -lt 60 ]; do
    kill -0 "$vm_pid" 2>/dev/null || { echo 'QEMU exited before SSH was ready' >&2; exit 1; }
    if vm_ssh true </dev/null >"$work_dir/ssh.log" 2>&1; then
      ready=true
      break
    fi
    attempts=$((attempts + 1))
    sleep 5
  done
  [ "$ready" = true ] || { echo 'Timed out waiting for VM SSH' >&2; exit 1; }

  tar -cf "$work_dir/tests.tar" ci/test.sh zig-out/tests
  vm_ssh 'mkdir -p /hrc && cd /hrc && tar -xf -' <"$work_dir/tests.tar"
  vm_ssh "cd /hrc && sh ci/test.sh $arch $os"
  exit 0
fi

# Ubuntu and Alpine use the same Linux test binary.
case $os in
ubuntu | alpine) os=linux ;;
esac

case $os in
linux | macos | freebsd) extension= ;;
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
macos) actual=$(shasum -a 256 "$tests") ;;
freebsd) actual=$(sha256 -q "$tests") ;;
esac

actual=${actual%% *}
[ "$actual" = "$expected" ] || {
  echo "SHA256 mismatch: $tests" >&2
  exit 1
}

chmod 0755 "$tests"

"./$tests"
