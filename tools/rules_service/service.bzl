# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Package a service binary, generated service definitions and explicit dependencies.

service() produces a tar.gz archive. Source dependencies keep their package-relative
paths; generated files and directory trees use their basenames. rules_pkg mappings
retain their explicit destinations and attributes. Only declared
outputs of deps are included, never their transitive build inputs or runfiles.

Unix archives contain four standalone package hooks with helpers inlined at build
time. Configurations and hooks remain available on the private <name>_files target
through systemd/openrc/launchd/winsw/scripts output groups.
"""

load("@rules_pkg//pkg:tar.bzl", "pkg_tar")
load(":archive.bzl", "service_contents")
load(":scripts.bzl", "inline_script", "script_substitutions")

def _xml(value):
    return value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;").replace("'", "&apos;")

def _shell(value):
    return "'" + value.replace("'", "'\"'\"'") + "'"

def _systemd(value):
    return '"' + value.replace("\\", "\\\\").replace('"', '\\"').replace("%", "%%").replace("$", "$$") + '"'

def _windows(value):
    # Quote argv using Windows CommandLineToArgvW backslash/quote rules.
    result = '"'
    backslashes = 0
    for char in value.elems():
        if char == "\\":
            backslashes += 1
        else:
            result += "\\" * (backslashes * 2 + 1) + char if char == '"' else "\\" * backslashes + char
            backslashes = 0
    return result + "\\" * (backslashes * 2) + '"'

def _identifier(value, field):
    if not value or value[0] not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789":
        fail(field + " must start with a letter or digit")
    for char in value.elems():
        if char not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-":
            fail(field + " may contain only letters, digits, underscores and hyphens")

def _service_files_impl(ctx):
    name = ctx.attr.service or ctx.label.name
    user = ctx.attr.user or name
    group = ctx.attr.group or user
    executable = ctx.attr.executable or name
    for field, value in [("service", name), ("user", user), ("group", group), ("executable", executable)]:
        _identifier(value, field)
    for value in ctx.attr.args + [ctx.attr.description, ctx.attr.prefix]:
        if "\n" in value or "\r" in value or "\000" in value:
            fail("service settings must not contain newlines or NUL")
    if not ctx.attr.prefix.startswith("/"):
        fail("prefix must be an absolute Unix path")
    prefix = ctx.attr.prefix.rstrip("/")
    binary = prefix + "/bin/" + executable
    outputs = {}
    for kind, filename in [("systemd", name + ".systemd"), ("openrc", name + ".alpine"), ("launchd", "org." + name + ".plist"), ("winsw", name + ".winsw.xml")]:
        macos = kind == "launchd"
        config = (prefix + "/etc/" if macos else "/etc/") + name + "/" + name + ".json"

        # The GitHub runner passes script paths to the shell unquoted, so job
        # directories, and with them the state, must not contain spaces.
        state = (prefix + "/var/" if macos else "/var/lib/") + name
        log = ("/Library/Logs/" if macos else "/var/log/") + name
        if kind == "winsw":
            # The independent installer resolves this placeholder at installation time.
            config = "%SERVICE_CONFIG%"
        args = [arg.replace("{config}", config) for arg in ctx.attr.args]
        if kind == "systemd":
            substitutions = {
                "@COMMAND@": " ".join([_systemd(arg) for arg in [binary] + args]),
                "@DESCRIPTION@": ctx.attr.description.replace("%", "%%"),
                "@GROUP@": group,
                "@STATE@": state.replace("%", "%%"),
                "@USER@": user,
            }
        elif kind == "openrc":
            substitutions = {
                "@ARGUMENTS@": _shell(" ".join([_shell(arg) for arg in args])),
                "@DESCRIPTION@": _shell(ctx.attr.description),
                "@EXECUTABLE@": _shell(binary),
                "@LOG@": _shell(log),
                "@OWNER@": _shell(user + ":" + group),
                "@REQUIRED_FILES@": "required_files=" + _shell(config) if any(["{config}" in arg for arg in ctx.attr.args]) else "",
                "@STATE@": _shell(state),
                "@STDERR@": _shell(log + "/stderr.log"),
                "@STDOUT@": _shell(log + "/stdout.log"),
            }
        elif kind == "launchd":
            substitutions = {
                "@ARGUMENTS@": "\n".join(["    <string>" + _xml(arg) + "</string>" for arg in [binary] + args]),
                "@GROUP@": _xml(group),
                "@LABEL@": _xml("org." + name),
                "@LOG@": _xml(log),
                "@STATE@": _xml(state),
                "@USER@": _xml(user),
            }
        else:
            substitutions = {
                "@ARGUMENTS@": _xml(" ".join([_windows(arg) for arg in args])),
                "@DESCRIPTION@": _xml(ctx.attr.description),
                "@EXECUTABLE@": _xml(executable + ".exe"),
                "@NAME@": _xml(name),
            }
        output = ctx.actions.declare_file(ctx.label.name + "/" + filename)
        ctx.actions.expand_template(
            template = getattr(ctx.file, "_" + kind),
            output = output,
            substitutions = substitutions,
            is_executable = kind == "openrc",
        )
        outputs[kind] = depset([output])
    settings = script_substitutions(
        name = name,
        user = user,
        group = group,
        description = ctx.attr.description,
        prefix = prefix or "/",
        executable = executable,
    )
    scripts = []
    for source in ([] if ctx.attr.os == "windows" else ctx.files._scripts):
        output = ctx.actions.declare_file(ctx.label.name + "/scripts/" + source.basename)
        inline_script(ctx, source, output, ctx.file._common, settings)
        scripts.append(output)
    outputs["scripts"] = depset(scripts)
    return service_contents(ctx, outputs, settings, executable) + [OutputGroupInfo(**outputs)]

_service_files = rule(
    implementation = _service_files_impl,
    attrs = {
        "arch": attr.string(mandatory = True),
        "args": attr.string_list(doc = "Arguments; {config} expands to the OS-specific JSON configuration path."),
        "bin": attr.label(mandatory = True, allow_single_file = True),
        "config": attr.label(allow_single_file = True),
        "deps": attr.label_list(allow_files = True),
        "description": attr.string(mandatory = True, doc = "Human-readable service description."),
        "executable": attr.string(doc = "Binary basename, without .exe; defaults to service."),
        "group": attr.string(doc = "Unix service group; defaults to user."),
        "installer": attr.label(allow_single_file = True),
        "os": attr.string(mandatory = True, values = ["linux", "darwin", "windows"]),
        "prefix": attr.string(default = "/usr/local", doc = "Unix installation prefix for the binary and macOS configuration."),
        "service": attr.string(doc = "Service ID and base filename; defaults to the target name."),
        "user": attr.string(doc = "Unix service user; defaults to service. WinSW uses LocalService."),
        "_common": attr.label(default = Label("//tools/rules_service:scripts/common.sh"), allow_single_file = True),
        "_launchd": attr.label(default = Label("//tools/rules_service:templates/launchd.tpl"), allow_single_file = True),
        "_openrc": attr.label(default = Label("//tools/rules_service:templates/openrc.tpl"), allow_single_file = True),
        "_scripts": attr.label(default = Label("//tools/rules_service:script_templates"), allow_files = True),
        "_systemd": attr.label(default = Label("//tools/rules_service:templates/systemd.tpl"), allow_single_file = True),
        "_winsw": attr.label(default = Label("//tools/rules_service:templates/winsw.tpl"), allow_single_file = True),
    },
    doc = "Describe the service archive contents and generate its platform integration files.",
)

def service(name, bin, os, arch, deps = [], config = None, installer = None, tags = [], visibility = None, **kwargs):
    """Build a service archive from a binary, optional config/installer and deps.

    The archive includes TARGET and the selected OS's service definitions. Unix
    archives also include four standalone lifecycle hooks. Source deps keep their
    package-relative paths, generated outputs use basenames, and directory trees
    keep their contents under their basename. Executables and trees use mode 0755;
    other dependency files use 0644. Duplicate destinations are errors.

    Args:
        name: Archive target name, also the default service ID and binary basename.
        bin: Target producing a single main executable; no runfiles are included.
        os: Target OS: linux, darwin or windows.
        arch: Target architecture, written with os into the archive's TARGET file.
        deps: Explicit additional targets or files to include in the archive.
        config: Optional configuration file, placed at the archive root.
        installer: Optional installer script, placed at the archive root. On Unix
            its common.sh import is inlined with the service's settings.
        tags: Tags applied to the archive and its private contents target.
        visibility: Visibility of the archive target.
        **kwargs: Service description settings: required description and optional
            service, executable, args, prefix, user and group.
    """
    _service_files(
        name = name + "_files",
        bin = bin,
        os = os,
        arch = arch,
        deps = deps,
        config = config,
        installer = installer,
        service = kwargs.pop("service", name),
        tags = tags,
        visibility = ["//visibility:private"],
        **kwargs
    )
    pkg_tar(
        name = name,
        srcs = [":" + name + "_files"],
        allow_duplicates_with_different_content = False,
        extension = "tar.gz",
        mode = "",
        package_dir = ".",
        stamp = 0,
        tags = tags,
        visibility = visibility,
    )
