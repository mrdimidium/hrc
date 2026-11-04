# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Render shell-quoted substitutions for standalone package hook templates."""

def _shell(value):
    return "'" + value.replace("'", "'\"'\"'") + "'"

def script_substitutions(name, user, group, description, prefix, os, arch):
    """Render target metadata and shell-quoted paths for platform-specific hooks.

    Args:
        name: Service name and configuration basename.
        user: Unprivileged service account.
        group: Service account's primary group.
        description: Human-readable account description.
        prefix: Absolute Unix installation prefix.
        os: Target operating system: linux or darwin.
        arch: Target architecture: amd64 or arm64.

    Returns:
        Template substitutions with shell-quoted paths and description.
    """
    prefix = prefix.rstrip("/")
    config_dir = (prefix + "/etc/" if os == "darwin" else "/etc/") + name
    state_dir = (prefix + "/var/" if os == "darwin" else "/var/lib/") + name
    log_dir = ("/Library/Logs/" if os == "darwin" else "/var/log/") + name
    return {
        "@ARCH@": arch,
        "@GROUP@": group,
        "@NAME@": name,
        "@OS@": os,
        "@SHELL_CONFIG@": _shell(config_dir + "/" + name + ".json"),
        "@SHELL_CONFIG_DIR@": _shell(config_dir),
        "@SHELL_DESCRIPTION@": _shell(description),
        "@SHELL_LOG_DIR@": _shell(log_dir),
        "@SHELL_STATE_DIR@": _shell(state_dir),
        "@USER@": user,
    }
