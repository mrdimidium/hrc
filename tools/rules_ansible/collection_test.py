# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

import json
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

from build_collection import build_collection


class CollectionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.metadata = self.root / "galaxy.json"
        self.metadata.write_text(json.dumps({
            "namespace": "example", "name": "demo", "version": "@VERSION@",
            "readme": "README.md", "authors": ["Example"], "license": ["BSD-3-Clause"],
            "documentation": "https://example.test/docs/@REVISION@",
        }))
        self.readme = self.root / "README.md"
        self.readme.write_text("# @TITLE@\n")
        self.payload = self.root / "payload"
        self.payload.write_bytes(b"\x00\xffpayload")
        self.version = self.root / "VERSION"
        self.version.write_text("1.2.3\n")
        self.revision = "0123456789abcdef0123456789abcdef01234567"
        self.status = self.root / "status.txt"
        self.status.write_text(f"STABLE_REVISION {self.revision}\n")
        self.spec = {
            "files": {"files/payload": str(self.payload)},
            "templates": {"galaxy.yml": str(self.metadata), "README.md": str(self.readme)},
            "substitutions": {"@TITLE@": "Demo"},
            "substitution_files": {"@VERSION@": str(self.version)},
            "stamp_substitutions": {"@REVISION@": "STABLE_REVISION"},
            "status_file": str(self.status),
            "validation": {"@REVISION@": "[0-9a-f]{40}"},
        }

    def test_build_and_install_arbitrary_collection(self):
        output = self.root / "collection.tar.gz"
        self.assertEqual(build_collection(self.spec, output), output)
        with tarfile.open(output) as archive:
            metadata = json.load(archive.extractfile("MANIFEST.json"))["collection_info"]
            self.assertEqual(metadata["namespace"], "example")
            self.assertEqual(metadata["name"], "demo")
            self.assertEqual(metadata["version"], "1.2.3")
            self.assertEqual(metadata["documentation"], f"https://example.test/docs/{self.revision}")
            self.assertEqual(archive.extractfile("README.md").read(), b"# Demo\n")
            self.assertEqual(archive.extractfile("files/payload").read(), self.payload.read_bytes())
        env = dict(os.environ, PYTHONPATH=os.pathsep.join(sys.path),
                   ANSIBLE_HOME=str(self.root / "home"), ANSIBLE_NOCOLOR="1")
        result = subprocess.run([
            sys.executable, "-m", "ansible.cli.galaxy", "collection", "install", str(output),
            "--no-deps", "--offline", "-p", str(self.root / "collections"),
        ], env=env, text=True, capture_output=True, timeout=60)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.root / "collections/ansible_collections/example/demo/MANIFEST.json").is_file())

    def test_rejects_invalid_substitution(self):
        self.status.write_text("STABLE_REVISION unknown\n")
        with self.assertRaisesRegex(ValueError, "Invalid template substitution"):
            build_collection(self.spec, self.root / "collection.tar.gz")

    def test_rejects_missing_status_key(self):
        self.status.write_text("")
        with self.assertRaises(KeyError):
            build_collection(self.spec, self.root / "collection.tar.gz")

    def test_rejects_paths_outside_collection(self):
        for destination in ("../outside", "/outside", "..\\outside"):
            with self.subTest(destination=destination):
                self.spec["files"] = {destination: str(self.payload)}
                with self.assertRaisesRegex(ValueError, "Invalid collection destination"):
                    build_collection(self.spec, self.root / "collection.tar.gz")


if __name__ == "__main__":
    unittest.main()
