# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

import hashlib
from html.parser import HTMLParser
import json
from pathlib import Path
import posixpath
import struct
import subprocess
import sys
import tarfile
import unittest
import xml.etree.ElementTree as ET
from urllib.parse import unquote, urlsplit

from python.runfiles import runfiles


class PageParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.resources = []
        self.scripts = []
        self.tabs = []
        self.tab_panels = []
        self.links = []
        self.icon_links = []
        self.viewports = []
        self.ids = set()
        self.in_footer = False
        self.footer_text = []
        self.in_header = False
        self.header_links = []
        self.header_text = []
        self.in_title = False
        self.title_text = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "meta" and attrs.get("name") == "viewport":
            self.viewports.append(attrs.get("content"))
        if tag == "footer":
            self.in_footer = True
        if tag == "header":
            self.in_header = True
        if tag == "title":
            self.in_title = True
        if tag == "a" and self.in_header:
            self.header_links.append(attrs.get("href"))
        if "id" in attrs:
            self.ids.add(attrs["id"])
        if tag in ("a", "link") and "href" in attrs:
            self.links.append(attrs["href"])
        if tag == "script":
            self.scripts.append(attrs)
            if "src" in attrs:
                self.resources.append(attrs["src"])
        if "data-tabs-group" in attrs:
            self.tabs.append(attrs)
        if "tabs__panel" in attrs.get("class", "").split():
            self.tab_panels.append(attrs)
        if tag == "link" and attrs.get("rel") == "stylesheet":
            self.resources.append(attrs["href"])
        if tag == "link" and attrs.get("rel") in ("icon", "apple-touch-icon", "manifest"):
            self.icon_links.append(attrs)

    def handle_endtag(self, tag):
        if tag == "footer":
            self.in_footer = False
        if tag == "header":
            self.in_header = False
        if tag == "title":
            self.in_title = False

    def handle_data(self, data):
        if self.in_footer:
            self.footer_text.append(data)
        if self.in_header:
            self.header_text.append(data)
        if self.in_title:
            self.title_text.append(data)


class SiteTest(unittest.TestCase):
    def test_site_archive(self):
        directory = Path(runfiles.Create().Rlocation("_main/docs"))
        archives = list(directory.glob("hrc-docs-*.tar.gz"))
        self.assertEqual(len(archives), 1)
        version = archives[0].name.removeprefix("hrc-docs-").removesuffix(".tar.gz")
        with tarfile.open(archives[0]) as site:
            files = {
                posixpath.normpath(member.name): member
                for member in site.getmembers() if member.isfile()
            }
            for docname in ("index", "manual/index", "manual/host-preparation",
                            "manual/github-auth", "manual/installation", "manual/workflows",
                            "manual/operation", "manual/upgrading", "manual/removal", "configuration"):
                for suffix in (".html", ".txt"):
                    name = docname + suffix
                    with self.subTest(file=name):
                        self.assertIn(name, files)
                        self.assertTrue(site.extractfile(files[name]).read().strip())

            stylesheets = [name for name in files if name.endswith(".css")]
            self.assertEqual(len(stylesheets), 1)
            stylesheet = stylesheets[0]
            self.assertRegex(stylesheet, r"^_static/site\.[0-9a-f]{16}\.css$")
            css = site.extractfile(files[stylesheet]).read()
            self.assertEqual(
                stylesheet, f"_static/site.{hashlib.sha256(css).hexdigest()[:16]}.css",
            )
            self.assertIn(b"@layer config,palette,typography,syntax,components;", css)
            self.assertIn(b"@layer syntax{", css)
            self.assertNotIn(b"components.document", css)
            self.assertIn(b".document-content", css)
            self.assertIn(b".highlight", css)
            self.assertIn(b"data:image/svg+xml", css)
            self.assertNotIn(b"@import", css)
            self.assertEqual([name for name in files if name.endswith(".svg")], ["favicon.svg"])
            self.assertIn(b"SPDX-License-Identifier", css)
            self.assertFalse(any(name.endswith((".html", ".css")) and "components/" in name for name in files))
            self.assertNotIn("_static/basic.css", files)
            self.assertNotIn("search.html", files)
            self.assertNotIn("usage.html", files)
            self.assertNotIn("getting-started.html", files)
            scripts = [name for name in files if name.endswith(".js")]
            self.assertEqual(len(scripts), 1)
            script = scripts[0]
            self.assertRegex(script, r"^_static/site\.[0-9a-f]{16}\.js$")
            js = site.extractfile(files[script]).read()
            self.assertEqual(script, f"_static/site.{hashlib.sha256(js).hexdigest()[:16]}.js")
            self.assertNotIn(b"sourceMappingURL", js)
            runtime = subprocess.run(
                [sys.argv[1], sys.argv[2]], input=js, capture_output=True, timeout=30,
            )
            self.assertEqual(runtime.returncode, 0, runtime.stdout.decode() + runtime.stderr.decode())
            self.assertFalse(any(name.endswith((".ts", ".py", ".cjs")) for name in files))
            self.assertIn(b"@media print", css)
            favicon = ET.fromstring(site.extractfile(files["favicon.svg"]).read())
            self.assertEqual(favicon.attrib["fill"], "#00afaf")
            self.assertEqual(favicon.attrib["viewBox"], "0 0 500 500")
            for element in favicon.iter():
                self.assertIn(element.tag, ("{http://www.w3.org/2000/svg}svg",
                                            "{http://www.w3.org/2000/svg}path"))
                self.assertFalse(any(key.lower().startswith("on") for key in element.attrib))

            png_sizes = {
                "apple-touch-icon.png": 180,
                "icon-192.png": 192,
                "icon-512.png": 512,
                "icon-mask.png": 512,
            }
            for name, size in png_sizes.items():
                with self.subTest(icon=name):
                    data = site.extractfile(files[name]).read()
                    self.assertEqual(data[:8], b"\x89PNG\r\n\x1a\n")
                    self.assertEqual(data[12:16], b"IHDR")
                    self.assertEqual(struct.unpack(">II", data[16:24]), (size, size))

            ico = site.extractfile(files["favicon.ico"]).read()
            self.assertEqual(struct.unpack("<HHH", ico[:6]), (0, 1, 3))
            for index, size in enumerate((16, 32, 48)):
                entry = struct.unpack_from("<BBBBHHII", ico, 6 + 16 * index)
                self.assertEqual(entry[:2], (size, size))
                self.assertEqual(entry[5], 32)
                self.assertGreater(entry[6], 0)
                self.assertLessEqual(entry[7] + entry[6], len(ico))

            manifest = json.loads(site.extractfile(files["manifest.webmanifest"]).read())
            self.assertEqual(manifest["name"], "Hosted Runner Controller")
            self.assertEqual(manifest["icons"], [
                {"src": "icon-192.png", "type": "image/png", "sizes": "192x192"},
                {"src": "icon-512.png", "type": "image/png", "sizes": "512x512"},
                {"src": "icon-mask.png", "type": "image/png", "sizes": "512x512", "purpose": "maskable"},
            ])
            for icon in manifest["icons"]:
                self.assertIn(icon["src"], files)

            pages = {}
            for name in files:
                if not name.endswith(".html"):
                    continue
                with self.subTest(page=name):
                    html = site.extractfile(files[name]).read().decode("utf-8")
                    page = PageParser()
                    page.feed(html)
                    pages[name] = page
                    self.assertEqual(page.viewports, ["width=device-width, initial-scale=1"])
                    footer = " ".join("".join(page.footer_text).split())
                    header = " ".join("".join(page.header_text).split())
                    self.assertIn(f"Version {version}", header)
                    self.assertNotIn(f"Version {version}", footer)
                    self.assertIn("Documentation: CC BY 4.0", footer)
                    self.assertIn("main-content", page.ids)
                    self.assertTrue("".join(page.title_text).endswith(" · Hosted Runner Controller"))
                    self.assertIn("https://github.com/mrdimidium/hrc", page.header_links)
                    self.assertEqual(len(page.header_links), 2)
                    self.assertNotIn("site-sidebar__title", html)
                    self.assertEqual({
                        (link["rel"], posixpath.normpath(posixpath.join(
                            posixpath.dirname(name), link["href"],
                        ))) for link in page.icon_links
                    }, {
                        ("icon", "favicon.ico"),
                        ("icon", "favicon.svg"),
                        ("apple-touch-icon", "apple-touch-icon.png"),
                        ("manifest", "manifest.webmanifest"),
                    })
                    self.assertEqual(len(page.icon_links), 4)
                    self.assertEqual(len(page.resources), 2 if page.tabs else 1)
                    self.assertTrue(page.resources[0].endswith(stylesheet))
                    self.assertEqual(len(page.scripts), 1 if page.tabs else 0)
                    if page.tabs:
                        self.assertTrue(page.scripts[0]["src"].endswith(script))
                        self.assertIn("defer", page.scripts[0])
                        self.assertTrue(page.tab_panels)
                        for tabs in page.tabs:
                            self.assertIn(tabs["data-tabs-group"], (
                                "host-platform", "command-shell", "operating-system",
                            ))
                            self.assertIn("data-tabs-label", tabs)
                            self.assertNotIn("data-enhanced", tabs)
                        for panel in page.tab_panels:
                            self.assertIn("id", panel)
                            self.assertNotIn("hidden", panel)
                            self.assertNotIn("role", panel)
                        text = site.extractfile(files[name.removesuffix(".html") + ".txt"]).read().decode("utf-8")
                        for label in ("Windows", "macOS", "Linux / Systemd", "Linux / OpenRC"):
                            self.assertIn(label, html)
                            self.assertIn(label, text)
                        if name == "manual/installation.html":
                            self.assertNotIn("Git Bash", text)
                            self.assertNotIn("MINGW", text)
                            self.assertIn('archive="hrc-$arch-darwin-$version.tar.gz"', text)
                            self.assertIn('archive="hrc-$arch-linux-$version.tar.gz"', text)
                    self.assertNotIn("basic.css", html)
                    self.assertNotIn('role="search"', html)
                    self.assertNotIn("search.html", html)
                    for resource in page.resources:
                        url = urlsplit(resource)
                        if url.scheme or url.netloc:
                            continue
                        target = posixpath.normpath(posixpath.join(
                            posixpath.dirname(name), unquote(url.path),
                        ))
                        self.assertIn(target, files, resource)

            self.assertFalse(any("/_includes/" in name for name in files))
            for name, page in pages.items():
                for link in page.links:
                    url = urlsplit(link)
                    if url.scheme or url.netloc:
                        continue
                    target = name if not url.path else posixpath.normpath(posixpath.join(
                        posixpath.dirname(name), unquote(url.path),
                    ))
                    with self.subTest(page=name, link=link):
                        self.assertIn(target, files)
                        if url.fragment and target in pages:
                            self.assertIn(unquote(url.fragment), pages[target].ids)


if __name__ == "__main__":
    unittest.main(argv=[sys.argv[0]])
