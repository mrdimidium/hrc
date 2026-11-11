# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

import hashlib
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

import yaml
from build_collection import build_collection
from release_manifest import PLATFORMS, release_manifest, write_manifest

SOURCE = Path(sys.argv.pop(1)).resolve().parent
WINDOWS = Path(sys.argv.pop(1)).resolve()
VERSION = "0.4.0-dev.12+abc01"
ARTIFACT_SOURCE = "https://artifacts.example.test/release"


class CollectionTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.artifacts = self.root / "artifacts"
        self.artifacts.mkdir()
        (self.artifacts / "VERSION").write_text(VERSION)
        for platform in PLATFORMS:
            os_name, arch = platform.split("-")
            content = f"{arch}-{os_name}".encode()
            with tarfile.open(self.artifacts / f"hrc-{arch}-{os_name}-{VERSION}.tar.gz", "w:gz") as archive:
                member = tarfile.TarInfo("./TARGET")
                member.size = len(content)
                archive.addfile(member, io.BytesIO(content))
        self.env = dict(os.environ, PYTHONPATH=os.pathsep.join(sys.path),
                        ANSIBLE_HOME=str(self.root / "home"),
                        ANSIBLE_REMOTE_TEMP=str(self.root / "remote-tmp"),
                        ANSIBLE_LOCAL_TEMP=str(self.root / "tmp"),
                        ANSIBLE_COLLECTIONS_PATH=str(self.root / "collections"),
                        ANSIBLE_NOCOLOR="1")

    def cli(self, module, *args, expected=0):
        result = subprocess.run([sys.executable, "-m", module, *map(str, args)],
                                env=self.env, text=True, capture_output=True, timeout=60)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result.stdout + result.stderr

    def test_manifest(self):
        version, manifest = release_manifest(self.artifacts)
        self.assertEqual(version, VERSION)
        self.assertEqual(set(manifest), set(PLATFORMS))
        for platform, artifact in manifest.items():
            os_name, arch = platform.split("-")
            self.assertEqual(artifact["filename"], f"hrc-{arch}-{os_name}-{VERSION}.tar.gz")
            self.assertEqual(artifact["sha256"], hashlib.sha256((self.artifacts / artifact["filename"]).read_bytes()).hexdigest())
        next(self.artifacts.glob("*.tar.gz")).unlink()
        with self.assertRaises(FileNotFoundError):
            release_manifest(self.artifacts)
        (self.artifacts / "VERSION").write_text("../../unexpected")
        with self.assertRaisesRegex(ValueError, "semantic version"):
            release_manifest(self.artifacts)

    def test_collection(self):
        release = self.root / "release.yml"
        write_manifest(self.artifacts, release)
        stub = self.root / "stub"
        stub.write_text("test fixture")
        files = {file.relative_to(SOURCE).as_posix(): str(file)
                 for directory in ("roles", "meta", "vars")
                 for file in (SOURCE / directory).rglob("*") if file.is_file()}
        files.update({path: str(stub) for path in (
            "LICENSE", "roles/install/files/winsw.exe", "roles/install/files/WinSW-LICENSE.txt",
        )})
        archive = build_collection({
            "files": files,
            "templates": {"README.md": str(SOURCE / "README.md"),
                          "galaxy.yml": str(SOURCE / "galaxy.yml.tpl"),
                          "roles/install/vars/release.yml": str(release)},
            "substitutions": {"@VERSION@": VERSION, "@ARTIFACT_SOURCE@": ARTIFACT_SOURCE},
            "substitution_files": {}, "stamp_substitutions": {}, "validation": {},
        }, self.root / "collection.tar.gz")
        with tarfile.open(archive) as collection:
            metadata = json.load(collection.extractfile("MANIFEST.json"))["collection_info"]
            self.assertEqual(metadata["version"], VERSION)
            self.assertEqual(metadata["documentation"], f"https://dimidiumlabs.io/hrc/doc/{VERSION}/installation.html")
            self.assertIn(f"https://dimidiumlabs.io/hrc/doc/{VERSION}/installation.html#ansible-collection",
                          collection.extractfile("README.md").read().decode())
            self.assertEqual(json.load(collection.extractfile("roles/install/vars/release.yml")), {
                "_hrc_release_version": VERSION, "_hrc_release_source": ARTIFACT_SOURCE,
                "_hrc_release_artifacts": release_manifest(self.artifacts)[1],
            })
        for item in (archive, WINDOWS):
            self.cli("ansible.cli.galaxy", "collection", "install", item, "--no-deps", "--offline", "-p", self.root / "collections")

        # Static imports check modules in every OS branch, unlike dynamic includes.
        imports = [{"ansible.builtin.import_role": {"name": f"dimidiumlabs.hrc.{role}", "tasks_from": task.name}}
                   for role in ("install", "remove")
                   for task in sorted((SOURCE / "roles" / role / "tasks").glob("*.yml"))]
        play = self.root / "play.yml"
        play.write_text(yaml.safe_dump([{"hosts": "localhost", "gather_facts": False, "tasks": imports}]))
        self.cli("ansible.cli.playbook", "-i", "localhost,", "--syntax-check", play)

        check_root = self.root / "check"
        settings = {
            "ansible_python_interpreter": sys.executable,
            "ansible_facts": {"system": "Linux", "os_family": "Debian", "architecture": "x86_64", "service_mgr": "systemd"},
            "_hrc_paths": {"linux": {key: str(check_root / key) for key in ("config", "state", "logs", "bin", "runtime", "service")}},
            "hrc_config": {"id": "check", "groups": [{"name": "check"}]},
            "hrc_secrets": {"test-key": "must-not-appear-in-output"},
            "hrc_artifact_source": str(self.artifacts),
        }
        play.write_text(yaml.safe_dump([{"hosts": "localhost", "gather_facts": False, "roles": ["dimidiumlabs.hrc.install"]}]))
        args = ("-i", "localhost,", "-c", "local", "-e", json.dumps(settings), play)
        output = self.cli("ansible.cli.playbook", "--check", "--diff", *args)
        self.assertNotIn("must-not-appear-in-output", output)
        self.assertFalse(check_root.exists())

        # Reject tampering before extraction or any installation changes.
        with (self.artifacts / f"hrc-amd64-linux-{VERSION}.tar.gz").open("ab") as stream:
            stream.write(b"tampered")
        output = self.cli("ansible.cli.playbook", *args, expected=2)
        self.assertIn("hrc archive SHA-256 mismatch", output)
        self.assertNotIn("must-not-appear-in-output", output)
        self.assertFalse(check_root.exists())


if __name__ == "__main__":
    unittest.main()
