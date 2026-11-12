# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""An ordinary sequence of sections, optionally enhanced into tabs by the browser."""

from docutils import nodes
from docutils.parsers.rst import directives
from sphinx.util.docutils import SphinxDirective


class Tabs(nodes.container):
    pass


class TabsDirective(SphinxDirective):
    has_content = True
    option_spec = {
        "group": directives.unchanged_required,
        "label": directives.unchanged_required,
    }

    def run(self):
        self.assert_has_content()
        panels = self.parse_content_to_nodes(allow_section_headings=True)
        if len(panels) < 2 or any(not isinstance(panel, nodes.section) for panel in panels):
            raise self.error("tabs requires at least two sections, each with a heading")
        result = Tabs(
            classes=["tabs"], group=self.options.get("group", ""),
            label=self.options.get("label", "Platform"),
        )
        for panel in panels:
            panel["classes"].append("tabs__panel")
            panel[0]["classes"].append("tabs__title")
            result += panel
        return [result]


def visit_html(translator, node):
    translator.body.append(translator.starttag(
        node, "div", **{"data-tabs-group": node["group"], "data-tabs-label": node["label"]},
    ))


def depart_html(translator, node):
    translator.body.append("</div>\n")


def visit_text(translator, node):
    pass


def page_context(app, pagename, templatename, context, doctree):
    context["has_tabs"] = doctree is not None and any(doctree.findall(Tabs))


def setup(app):
    app.add_node(Tabs, html=(visit_html, depart_html), text=(visit_text, visit_text))
    app.add_directive("tabs", TabsDirective)
    app.connect("html-page-context", page_context)
    return {"parallel_read_safe": True, "parallel_write_safe": True}
