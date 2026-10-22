# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Exercise the packaged Worker's startup with a separate workspace and logs."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


WORKER = Path(sys.argv.pop(1)).resolve()


class LayoutTest(unittest.TestCase):
    def test_workspace_config_and_diagnostics(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            work, diag, resources = root / "work", root / "logs", root / "resources"
            work.mkdir()
            resources.mkdir()
            # Any attempt to parse old registration files must fail. The files
            # must also remain untouched when the Worker exits.
            for name in (".runner", ".credentials", ".credentials_rsaparams", ".runner_migrated"):
                (work / name).write_text("invalid legacy configuration", encoding="utf-8")
            environment = dict(os.environ,
                RUNNER_CONFIG=json.dumps({"agentId": 7, "agentName": "layout-test", "poolId": 3}),
                RUNNER_BIN=str(resources), RUNNER_DIAG=str(diag),
                RUNNER_EXTERNALS=str(root / "externals"))
            result = subprocess.run([str(WORKER)], cwd=work, env=environment,
                                    capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertIn("Expected '3'. Actual '0'", result.stdout)
            logs = list(diag.glob("Worker_*.log"))
            self.assertEqual(len(logs), 1)
            log = logs[0].read_text(encoding="utf-8-sig")
            self.assertIn(str(resources), log)
            self.assertIn(str(work), log)
            self.assertFalse((work / "_diag").exists())
            for path in work.iterdir():
                self.assertEqual(path.read_text(encoding="utf-8"), "invalid legacy configuration")


if __name__ == "__main__":
    unittest.main()
