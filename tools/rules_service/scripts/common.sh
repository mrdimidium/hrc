#!/bin/sh
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

# Shared source inlined into the installer and each package hook at build time.
# shellcheck disable=SC2034 # Variables are consumed by the including script.
require_root() {
    if [ "$(id -u)" != 0 ]; then
        echo '@NAME@: run this command as root' >&2
        exit 1
    fi
}

os=$(uname -s)
prefix=@SHELL_PREFIX@
admin_group=wheel
case $os in
    Linux)
        target_os=linux; admin_group=root
        config_dir=/etc/@NAME@; state_dir=/var/lib/@NAME@; log_dir=/var/log/@NAME@
        ;;
    Darwin)
        target_os=darwin
        config_dir="$prefix/etc/@NAME@"
        # Not /Library/Application Support: the GitHub runner passes script
        # paths to the shell unquoted, so the state must not contain spaces.
        state_dir="$prefix/var/@NAME@"; log_dir=/Library/Logs/@NAME@
        ;;
    *) echo "@NAME@: unsupported OS: $os" >&2; exit 1 ;;
esac
binary="$prefix/bin/@EXECUTABLE@"
payload="$prefix/share/@NAME@"
config="$config_dir/@NAME@.json"

arch() {
    machine=$(uname -m)
    case $machine in
        amd64|x86_64) printf '%s\n' amd64 ;;
        arm64|aarch64) printf '%s\n' arm64 ;;
        *) echo "@NAME@: unsupported architecture: $machine" >&2; return 1 ;;
    esac
}

install_dir() {
    install -d -m "$1" -o "$2" -g "$3" "$4"
}

install_file() {
    install -o root -g "$admin_group" -m "$1" "$2" "$3"
}

prepare_dirs() {
    # Missing parents, such as $prefix/etc and $prefix/var on macOS, are created
    # world-readable; under the installer's umask 0077 they would lock the
    # account out. Existing directories are left as they are.
    (umask 022 && mkdir -p "$(dirname "$config_dir")" "$(dirname "$state_dir")" "$(dirname "$log_dir")")
    install_dir 0750 root @GROUP@ "$config_dir"
    install_dir 0750 root @GROUP@ "$config_dir/secrets"
    install_dir 0700 @USER@ @GROUP@ "$state_dir"
    install_dir 0700 @USER@ @GROUP@ "$log_dir"
}

select_init() {
    init=${SERVICE_INIT:-}
    if [ -z "$init" ]; then
        case $os in
            Linux)
                if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
                    init=systemd
                elif command -v openrc-run >/dev/null 2>&1; then
                    init=openrc
                else
                    echo '@NAME@: Linux installation requires systemd or OpenRC' >&2
                    exit 1
                fi
                ;;
            Darwin) init=launchd ;;
        esac
    fi
    case "$os:$init" in
        Linux:systemd) service_source=@NAME@.systemd; service=/etc/systemd/system/@NAME@.service; service_mode=0644 ;;
        Linux:openrc) service_source=@NAME@.alpine; service=/etc/init.d/@NAME@; service_mode=0755 ;;
        Darwin:launchd) service_source=org.@NAME@.plist; service=/Library/LaunchDaemons/org.@NAME@.plist; service_mode=0644 ;;
        *) echo "@NAME@: unsupported init system $init on $os" >&2; exit 1 ;;
    esac
}
