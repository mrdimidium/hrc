# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Inline the shared shell library so installed scripts have no sibling dependency."""

def _shell(value):
    return "'" + value.replace("'", "'\"'\"'") + "'"

def script_substitutions(name, user, group, description, prefix, executable):
    """Settings shared by standalone package hooks and their installer."""
    return {
        "@EXECUTABLE@": executable,
        "@GROUP@": group,
        "@NAME@": name,
        "@SHELL_DESCRIPTION@": _shell(description),
        "@SHELL_PREFIX@": _shell(prefix),
        "@USER@": user,
    }

def inline_script(ctx, source, output, common, substitutions):
    """Render a standalone shell script from a source and the common library."""
    combined = ctx.actions.declare_file(output.short_path.removeprefix(ctx.label.package + "/") + ".in")
    ctx.actions.run_shell(
        inputs = [source, common],
        outputs = [combined],
        arguments = [common.path, source.path, combined.path],
        command = """set -eu
while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
        '. "$(dirname "$0")/common.sh"') cat "$1" ;;
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
