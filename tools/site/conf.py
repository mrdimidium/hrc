# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

import hashlib
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit, urlunsplit

project = "Hosted Runner Controller"
author = "Dimidium Labs"
release = os.environ["HRC_DOCS_VERSION"]
copyright = "2026, Nikolay Govorov"
language = "en"
root_doc = "index"
exclude_patterns = ["internal/**", "**/_includes/**", "readme.rst"]
nitpicky = True

html_theme = "theme"
html_theme_path = [str(Path(__file__).resolve().parent)]
templates_path = ["static"]
# Extension imports must not create files in the watched workspace sources.
sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent / "static/components/tabs"))
extensions = ["tabs"]
# Publish the favicon set at the archive root; nested pages use relative URLs.
html_extra_path = ["assets"]

html_title = project
html_context = {"build_commit": os.environ.get("HRC_DOCS_COMMIT", "")}
# Keep the archive relocatable: no deployment URL or absolute canonical links.
html_show_sourcelink = False
html_copy_source = False
html_show_sphinx = True
html_use_index = False
# Accessible link text; the document component renders the visual link icon.
html_permalinks_icon = "Permalink"


def prepare_source(app, docname, source, included_docname=None):
    """Map repository-local RST links to their HTML or plain-text pages."""

    repository = Path(__file__).resolve().parents[2]
    document_directory = (repository / "docs" / (included_docname or docname)).parent
    output_directory = (Path(app.srcdir) / docname).parent
    suffix = ".txt" if app.builder.name == "text" else ".html"

    def rewrite_link(match):
        url = urlsplit(match[1])
        if url.scheme or url.netloc or not url.path or url.path.startswith("/"):
            return match[0]

        # Normalize URL paths lexically: a license may be a symlink, but its
        # published page still uses the link's name rather than its target.
        target = Path(os.path.abspath(document_directory / unquote(url.path)))
        if target.suffix == ".rst" and target.is_relative_to(repository / "docs"):
            relative = target.relative_to(repository / "docs")
        elif target.suffix == ".txt" and target.is_relative_to(repository / "LICENSES"):
            relative = target.relative_to(repository)
        else:
            return match[0]
        output = Path(app.srcdir) / relative.with_suffix(suffix)
        path = Path(os.path.relpath(output, output_directory)).as_posix()
        fragment = "" if app.builder.name == "text" else url.fragment

        return "<" + urlunsplit(("", "", path, url.query, fragment)) + ">`__"

    source[0] = re.sub(r"<([^<>\s]+)>`__", rewrite_link, source[0])


def prepare_include(app, relative_path, parent_docname, content):
    docname = Path(relative_path).with_suffix("").as_posix()
    prepare_source(app, parent_docname, content, included_docname=docname)


def prepare_assets(app):
    if app.builder.format != "html":
        return
    # Sphinx exposes search on the builder, not as an HTML config option.
    app.builder.search = False
    sources = Path(__file__).resolve().parent / "static"
    # Only the layer prelude has a fixed position. File order is not cascade order.
    css = ["@layer config, palette, typography, syntax, components;"]
    # Let esbuild resolve each file's URLs relative to that file.
    css.extend(f'@import "./{path.relative_to(sources).as_posix()}";'
               for path in sorted(sources.rglob("*.css")))
    css.append("@layer syntax { .document-content {\n"
               + app.builder.highlighter.get_stylesheet() + "\n} }")
    result = subprocess.run(
        [os.environ["HRC_ESBUILD"], "--loader=css", "--bundle", "--minify",
         "--loader:.svg=dataurl", "--supported:nesting=false",
         "--legal-comments=inline", "--charset=utf8"],
        input="\n".join(css).encode("utf-8"), capture_output=True, cwd=sources,
    )
    if result.returncode:
        raise RuntimeError("esbuild failed:\n" + result.stderr.decode("utf-8"))
    digest = hashlib.sha256(result.stdout).hexdigest()[:16]
    filename = f"site.{digest}.css"
    destination = Path(app.outdir) / "_static"
    destination.mkdir(parents=True, exist_ok=True)
    (destination / filename).write_bytes(result.stdout)
    app.config.html_context["site_css"] = filename

    scripts = "\n".join(
        f'import "./{path.relative_to(sources).as_posix()}";'
        for path in sorted(sources.rglob("*.ts"))
    )
    result = subprocess.run(
        [os.environ["HRC_ESBUILD"], "--loader=ts", "--bundle", "--minify",
         "--format=iife", "--target=es2020", "--legal-comments=inline", "--charset=utf8"],
        input=scripts.encode("utf-8"), capture_output=True, cwd=sources,
    )
    if result.returncode:
        raise RuntimeError("esbuild failed:\n" + result.stderr.decode("utf-8"))
    digest = hashlib.sha256(result.stdout).hexdigest()[:16]
    filename = f"site.{digest}.js"
    (destination / filename).write_bytes(result.stdout)
    app.config.html_context["site_js"] = filename


def clean_assets(app, exception):
    if exception is not None or app.builder.format != "html":
        return
    # Sphinx always emits Pygments CSS. Its rules are already in our bundle.
    destination = Path(app.outdir) / "_static"
    (destination / "pygments.css").unlink(missing_ok=True)
    for suffix in ("css", "js"):
        for path in destination.glob(f"site.*.{suffix}"):
            if path.name != app.config.html_context[f"site_{suffix}"]:
                path.unlink()


def setup(app):
    app.connect("builder-inited", prepare_assets)
    app.connect("build-finished", clean_assets)
    app.connect("source-read", prepare_source)
    app.connect("include-read", prepare_include)
    return {"parallel_read_safe": True, "parallel_write_safe": True}
