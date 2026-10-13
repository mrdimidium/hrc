#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

# HRC_ROOT stages a filesystem without changing host accounts or services.
# shellcheck disable=SC2034 # Variables are consumed by scripts sourcing this library.
# The destination must already exist; HRC_OS and HRC_INIT select staged targets.
hrc_root=""
if [ -n "${HRC_ROOT:-}" ]; then
    case $HRC_ROOT in
        /*) ;;
        *) echo 'hrc: HRC_ROOT must be an absolute directory' >&2; exit 1 ;;
    esac
    hrc_root=$(CDPATH='' cd -P "$HRC_ROOT" && pwd)
    [ "$hrc_root" != / ] || hrc_root=""
fi

hrc_staging() { [ -n "$hrc_root" ]; }
hrc_path() { printf '%s%s\n' "$hrc_root" "$1"; }
hrc_require_root() {
    if ! hrc_staging && [ "$(id -u)" != 0 ]; then
        echo 'hrc: run this command as root' >&2
        exit 1
    fi
}

hrc_os=$(uname -s)
if hrc_staging; then hrc_os=${HRC_OS:-$hrc_os}; fi
hrc_prefix=/usr/local
hrc_admin_group=wheel
case $hrc_os in
    Linux)
        hrc_target_os=linux; hrc_admin_group=root
        hrc_config_dir=/etc/hrc; hrc_state_dir=/var/lib/hrc; hrc_log_dir=/var/log/hrc
        ;;
    Darwin)
        hrc_target_os=macos
        hrc_config_dir=/usr/local/etc/hrc
        hrc_state_dir='/Library/Application Support/hrc'; hrc_log_dir=/Library/Logs/hrc
        ;;
    *) echo "hrc: unsupported OS: $hrc_os" >&2; exit 1 ;;
esac
hrc_binary="$hrc_prefix/bin/hrc"
hrc_payload="$hrc_prefix/share/hrc"
hrc_config="$hrc_config_dir/hrc.zon"

hrc_arch() {
    machine=$(uname -m)
    case $machine in
        amd64|x86_64) printf '%s\n' x86_64 ;;
        arm64|aarch64) printf '%s\n' aarch64 ;;
        s390x|loongarch64|powerpc64le) printf '%s\n' "$machine" ;;
        ppc64le) printf '%s\n' powerpc64le ;;
        riscv64) printf '%s\n' riscv64 ;;
        *) echo "hrc: unsupported architecture: $machine" >&2; return 1 ;;
    esac
}

hrc_install_dir() {
    mode=$1; owner=$2; group=$3; destination=$(hrc_path "$4")
    if hrc_staging; then
        install -d -m "$mode" "$destination"
    else
        install -d -o "$owner" -g "$group" -m "$mode" "$destination"
    fi
}

hrc_install_file() {
    mode=$1; source=$2; destination=$(hrc_path "$3")
    if hrc_staging; then
        install -m "$mode" "$source" "$destination"
    else
        install -o root -g "$hrc_admin_group" -m "$mode" "$source" "$destination"
    fi
}

hrc_prepare_dirs() {
    hrc_install_dir 0750 root hrc "$hrc_config_dir"
    hrc_install_dir 0750 root hrc "$hrc_config_dir/secrets"
    hrc_install_dir 0700 hrc hrc "$hrc_state_dir"
    hrc_install_dir 0700 hrc hrc "$hrc_log_dir"
}

hrc_select_init() {
    hrc_init=${HRC_INIT:-}
    if [ -z "$hrc_init" ]; then
        case $hrc_os in
            Linux)
                if hrc_staging || { command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; }; then
                    hrc_init=systemd
                elif command -v openrc-run >/dev/null 2>&1; then
                    hrc_init=openrc
                else
                    echo 'hrc: Linux installation requires systemd or OpenRC' >&2
                    exit 1
                fi
                ;;
            Darwin) hrc_init=launchd ;;
        esac
    fi
    case "$hrc_os:$hrc_init" in
        Linux:systemd) hrc_service_source=hrc.systemd; hrc_service=/etc/systemd/system/hrc.service; hrc_service_mode=0644 ;;
        Linux:openrc) hrc_service_source=hrc.alpine; hrc_service=/etc/init.d/hrc; hrc_service_mode=0755 ;;
        Darwin:launchd) hrc_service_source=org.hrc.plist; hrc_service=/Library/LaunchDaemons/org.hrc.plist; hrc_service_mode=0644 ;;
        *) echo "hrc: unsupported init system $hrc_init on $hrc_os" >&2; exit 1 ;;
    esac
}
