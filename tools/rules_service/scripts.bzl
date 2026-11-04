# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Inline the shared shell library so installed scripts have no sibling dependency."""

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

def inline_script(ctx, source, output, common, substitutions):
    """Replace the source's @COMMON@ marker with its platform's shared template."""
    combined = ctx.actions.declare_file(output.short_path.removeprefix(ctx.label.package + "/") + ".in")
    ctx.actions.run_shell(
        inputs = [source, common],
        outputs = [combined],
        arguments = [common.path, source.path, combined.path],
        command = """set -eu
while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
        '# @COMMON@') cat "$1" ;;
        *) printf '%s\n' "$line" ;;
    esac
done < "$2" > "$3"
""",
        mnemonic = "ServiceScript",
        progress_message = "Rendering standalone script %{output}",
    )
    ctx.actions.expand_template(
        template = combined,
        output = output,
        substitutions = substitutions,
        is_executable = True,
    )
