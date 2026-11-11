# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

import os
from pathlib import Path
import re
from urllib.parse import unquote, urlsplit, urlunsplit

project = "Hosted Runner Controller"
author = "Dimidium Labs"
copyright = "2026, Nikolay Govorov"
language = "en"
root_doc = "index"
exclude_patterns = ["internal/**", "readme.rst"]
nitpicky = True

html_theme = "theme"
html_theme_path = [str(Path(__file__).resolve().parent)]
html_css_files = ["theme.css"]

html_title = project
release = os.environ.get("HRC_DOCS_VERSION", "")
html_context = {"build_commit": os.environ.get("HRC_DOCS_COMMIT", "")}
# Keep the archive relocatable: no deployment URL or absolute canonical links.
html_show_sourcelink = False
html_copy_source = False
html_show_sphinx = True
html_use_index = False


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


def setup(app):
    app.connect("source-read", prepare_source)
    app.connect("include-read", prepare_include)
    return {"parallel_read_safe": True, "parallel_write_safe": True}
