# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Assemble an Ansible collection from caller-supplied files and templates."""

load("@bazel_skylib//rules:common_settings.bzl", "BuildSettingInfo")

def _mapped_files(mapping):
    result = {}
    for target, destination in mapping.items():
        files = target[DefaultInfo].files.to_list()
        if len(files) != 1:
            fail("Expected one file from %s" % target.label)
        if destination in result:
            fail("Duplicate collection destination: " + destination)
        result[destination] = files[0]
    return result

def _collection_impl(ctx):
    templates = _mapped_files(ctx.attr.templates)
    files = _mapped_files(ctx.attr.files)
    template_files = templates.values()
    prefix = ctx.label.package + "/" if ctx.label.package else ""
    for file in ctx.files.srcs:
        if file in template_files:
            continue
        if not file.short_path.startswith(prefix):
            fail("Use files to specify a destination for " + file.short_path)
        destination = file.short_path.removeprefix(prefix)
        if destination in files or destination in templates:
            fail("Duplicate collection destination: " + destination)
        files[destination] = file
    if any([destination in templates for destination in files]):
        fail("Files and templates must have distinct destinations")
    substitutions = dict(ctx.attr.substitutions)
    for target, token in ctx.attr.settings.items():
        value = target[BuildSettingInfo].value
        if value == "":
            fail("Set --%s to supply %s" % (target.label, token))
        substitutions[token] = str(value)
    substitution_files = _mapped_files(ctx.attr.substitution_files)
    spec = ctx.actions.declare_file(ctx.label.name + ".inputs.json")
    ctx.actions.write(spec, json.encode({
        "files": {destination: file.path for destination, file in files.items()},
        "stamp_substitutions": ctx.attr.stamp_substitutions,
        "status_file": ctx.info_file.path,
        "substitution_files": {token: file.path for token, file in substitution_files.items()},
        "substitutions": substitutions,
        "templates": {destination: file.path for destination, file in templates.items()},
        "validation": ctx.attr.validation,
    }))
    archive = ctx.actions.declare_file(ctx.label.name + ".tar.gz")
    inputs = [spec] + files.values() + templates.values() + substitution_files.values()
    if ctx.attr.stamp_substitutions:
        inputs.append(ctx.info_file)
    ctx.actions.run(
        executable = ctx.attr._builder[DefaultInfo].files_to_run,
        arguments = ["--spec", spec.path, "--output", archive.path],
        inputs = inputs,
        outputs = [archive],
        mnemonic = "AnsibleCollection",
        progress_message = "Building Ansible collection %s" % ctx.label,
    )
    return [DefaultInfo(files = depset([archive]))]

collection = rule(
    implementation = _collection_impl,
    attrs = {
        "files": attr.label_keyed_string_dict(allow_files = True, doc = "Extra files mapped to collection-relative destinations."),
        "settings": attr.label_keyed_string_dict(doc = "Build settings mapped to template tokens."),
        "srcs": attr.label_list(allow_files = True, doc = "Files retaining their caller-package-relative paths."),
        "stamp_substitutions": attr.string_dict(doc = "Template tokens mapped to stable workspace-status keys."),
        "substitution_files": attr.label_keyed_string_dict(allow_files = True, doc = "Text files mapped to template tokens; surrounding whitespace is stripped."),
        "substitutions": attr.string_dict(),
        "templates": attr.label_keyed_string_dict(allow_files = True, doc = "Templates mapped to collection-relative destinations."),
        "validation": attr.string_dict(doc = "Optional full-match regexes for template tokens."),
        "_builder": attr.label(default = Label("//tools/rules_ansible:collection_builder"), executable = True, cfg = "exec"),
    },
)
