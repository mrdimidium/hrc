# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest


FIXTURES = {}
HOOKS = {"preinstall.sh", "postinstall.sh", "preremove.sh", "postremove.sh"}


class ScriptsTest(unittest.TestCase):
    def test_platform_selection_and_substitutions(self):
        self.assertEqual(set(FIXTURES), {"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"})
        for target, scripts in FIXTURES.items():
            system, arch = target.split("-")
            prefix = "/opt/fixture's prefix" if arch == "amd64" else ""
            config_dir = prefix + "/etc/fixture" if system == "darwin" else "/etc/fixture"
            paths = {
                "config_dir": config_dir,
                "config": config_dir + "/fixture.json",
                "state_dir": prefix + "/var/fixture" if system == "darwin" else "/var/lib/fixture",
                "log_dir": "/Library/Logs/fixture" if system == "darwin" else "/var/log/fixture",
            }
            self.assertEqual(set(scripts), HOOKS if system == "linux" else HOOKS - {"postremove.sh"})
            for name, path in scripts.items():
                with self.subTest(target=target, hook=name):
                    text = path.read_text(encoding="utf-8")
                    self.assertEqual(path.parent.name, "scripts")
                    self.assertIn(f"# Target: {arch}-{system}", text)
                    self.assertNotRegex(text, r"@[A-Z_]+@")
                    self.assertNotIn("uname", text)
                    self.assertNotIn("arch()", text)
                    self.assertNotIn("common.sh", text)
                    self.assertNotIn("select_init", text)
                    self.assertNotRegex(text, r"(?m)^(?:config|config_dir|state_dir|log_dir)=")
                    self.assertNotRegex(text, r"\$(?:\{)?(?:config|config_dir|state_dir|log_dir)\b")
                    if name == "preinstall.sh":
                        directories = {
                            shlex.split(line)[-1]
                            for line in text.splitlines()
                            if line.startswith("install -d ")
                        }
                        self.assertEqual(directories, {paths["config_dir"], paths["config_dir"] + "/secrets", paths["state_dir"], paths["log_dir"]})
                    elif name == "postinstall.sh":
                        condition = re.search(r"^if \[ -f (.*) \]; then$", text, re.MULTILINE)
                        self.assertIsNotNone(condition)
                        self.assertEqual(shlex.split(condition.group(1)), [paths["config"]])
                    forbidden = ("dscl", "launchctl") if system == "linux" else ("systemctl", "rc-service", "getent", "useradd")
                    for command in forbidden:
                        self.assertNotIn(command, text)
                    if os.name != "nt":
                        self.assertTrue(path.stat().st_mode & 0o111)

    @unittest.skipIf(shutil.which("sh") is None, "POSIX shell is unavailable")
    def test_shell_syntax(self):
        for target, scripts in FIXTURES.items():
            for name, path in scripts.items():
                with self.subTest(target=target, hook=name):
                    result = subprocess.run([shutil.which("sh"), "-n", str(path)], capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)

    @unittest.skipIf(shutil.which("sh") is None, "POSIX shell is unavailable")
    def test_unknown_init_skips_integration(self):
        shell = shutil.which("sh")
        with tempfile.TemporaryDirectory() as empty_path:
            for target, scripts in FIXTURES.items():
                if not target.startswith("linux-"):
                    continue
                text = scripts["preremove.sh"].read_text(encoding="utf-8")
                with self.subTest(target=target):
                    # Mock only the root check. An empty PATH prevents init
                    # detection and any real service-manager invocation.
                    result = subprocess.run(
                        [shell],
                        input="id() { printf '0\\n'; }\n" + text,
                        env={**os.environ, "PATH": empty_path},
                        capture_output=True,
                        text=True,
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("skipping service integration", result.stderr)


if __name__ == "__main__":
    for argument in sys.argv[1:]:
        if "=" in argument:
            target, argument = argument.split("=", 1)
            FIXTURES[target] = {}
        for path in argument.split():
            FIXTURES[target][Path(path).name] = Path(path)
    unittest.main(argv=[sys.argv[0]])
