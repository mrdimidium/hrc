# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Check Worker runtime settings with its executable outside the workspace."""
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
            program, work = root / "programs", root / "work"
            program.mkdir()
            work.mkdir()
            executable = program / WORKER.name
            shutil.copy2(WORKER, executable)
            legacy = (".runner", ".credentials", ".credentials_rsaparams", ".runner_migrated")
            for name in legacy:
                (work / name).write_text("invalid legacy configuration", encoding="utf-8")
            environment = dict(os.environ)
            environment.pop("RUNNER_CONFIG", None)
            environment.pop("RUNNER_DIAG", None)
            missing = subprocess.run([str(executable)], cwd=work, env=environment,
                                     capture_output=True, text=True, timeout=30)
            self.assertNotEqual(missing.returncode, 0)
            self.assertIn("RUNNER_CONFIG", missing.stdout + missing.stderr)
            environment["RUNNER_CONFIG"] = json.dumps({
                "agentId": 7, "agentName": "runtime-config-test", "poolId": 3,
                "workFolder": ".",
            })
            result = subprocess.run([str(executable)], cwd=work, env=environment,
                                    capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertIn("Expected '3'. Actual '0'", result.stdout)
            self.assertTrue(list((work / "_diag").glob("Worker_*.log")))
            for name in legacy:
                self.assertEqual((work / name).read_text(encoding="utf-8"),
                                 "invalid legacy configuration")

    def test_separate_diagnostics_per_invocation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for job in ("first", "second"):
                work = root / "work" / job
                work.mkdir(parents=True)
                diag = root / "logs" / job
                environment = dict(os.environ,
                    RUNNER_CONFIG=json.dumps({"agentId": 7, "agentName": job,
                                              "poolId": 3, "workFolder": "."}),
                    RUNNER_DIAG=str(diag))
                result = subprocess.run([str(WORKER)], cwd=work, env=environment,
                                        capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
                self.assertIn("Expected '3'. Actual '0'", result.stdout)
                self.assertEqual(len(list(diag.glob("Worker_*.log"))), 1)
                self.assertFalse((work / "_diag").exists())
            self.assertEqual(len(list((root / "logs").glob("*/Worker_*.log"))), 2)


if __name__ == "__main__":
    unittest.main()
