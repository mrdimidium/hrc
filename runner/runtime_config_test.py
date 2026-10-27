# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Check the packaged Worker's runtime settings without changing its layout."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


WORKER = Path(sys.argv.pop(1)).resolve()


class RuntimeConfigTest(unittest.TestCase):
    def test_environment_settings_ignore_legacy_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary_dir = root / "bin"
            shutil.copytree(WORKER.parent, binary_dir)
            legacy = (".runner", ".credentials", ".credentials_rsaparams", ".runner_migrated")
            for name in legacy:
                (root / name).write_text("invalid legacy configuration", encoding="utf-8")
            environment = dict(os.environ)
            environment.pop("RUNNER_CONFIG", None)
            environment.pop("RUNNER_DIAG", None)
            executable = binary_dir / WORKER.name
            missing = subprocess.run([str(executable)], cwd=binary_dir, env=environment,
                                     capture_output=True, text=True, timeout=30)
            self.assertNotEqual(missing.returncode, 0)
            self.assertIn("RUNNER_CONFIG", missing.stdout + missing.stderr)
            environment["RUNNER_CONFIG"] = json.dumps({
                "agentId": 7, "agentName": "runtime-config-test", "poolId": 3,
                "workFolder": "_work",
            })
            result = subprocess.run([str(executable)], cwd=binary_dir, env=environment,
                                    capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            # With valid settings startup reaches argument validation, rather
            # than trying to parse any of the invalid registration files.
            self.assertIn("Expected '3'. Actual '0'", result.stdout)
            self.assertTrue(list((root / "_diag").glob("Worker_*.log")))
            for name in legacy:
                self.assertEqual((root / name).read_text(encoding="utf-8"),
                                 "invalid legacy configuration")

    def test_separate_diagnostics_per_invocation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary_dir = root / "bin"
            shutil.copytree(WORKER.parent, binary_dir)
            for job in ("first", "second"):
                diag = root / "logs" / job
                environment = dict(os.environ,
                    RUNNER_CONFIG=json.dumps({"agentId": 7, "agentName": job,
                                              "poolId": 3, "workFolder": "_work"}),
                    RUNNER_DIAG=str(diag))
                result = subprocess.run([str(binary_dir / WORKER.name)], cwd=binary_dir,
                                        env=environment, capture_output=True,
                                        text=True, timeout=30)
                self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
                self.assertIn("Expected '3'. Actual '0'", result.stdout)
                self.assertEqual(len(list(diag.glob("Worker_*.log"))), 1)
                self.assertFalse((root / "_diag").exists())
            self.assertEqual(len(list((root / "logs").glob("*/Worker_*.log"))), 2)


if __name__ == "__main__":
    unittest.main()
