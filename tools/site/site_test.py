# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

from pathlib import Path
import posixpath
import tarfile
import unittest

from python.runfiles import runfiles


class SiteTest(unittest.TestCase):
    def test_site_archive(self):
        directory = Path(runfiles.Create().Rlocation("_main/docs"))
        archives = list(directory.glob("hrc-docs-*.tar.gz"))
        self.assertEqual(len(archives), 1)
        with tarfile.open(archives[0]) as site:
            files = {
                posixpath.normpath(member.name): member
                for member in site.getmembers() if member.isfile()
            }
            for name in ("index.html", "index.txt", "_static/theme.css", "searchindex.js"):
                with self.subTest(file=name):
                    self.assertIn(name, files)
                    self.assertTrue(site.extractfile(files[name]).read().strip())


if __name__ == "__main__":
    unittest.main()
