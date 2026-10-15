# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Runner build contract: reject foreign OSes and never execute target code."""

from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import build


class RunnerBuildTest(unittest.TestCase):
    def test_foreign_os_fails_before_checkout(self):
        with patch.object(build.sys, "platform", "linux"), patch.object(build.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "requires a win build host"):
                build.build("missing/dev.sh", "output", "win-arm64")
            run.assert_not_called()

    def test_unsupported_runtime_fails_before_checkout(self):
        with patch.object(build.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "Unsupported runner runtime"):
                build.build("missing/dev.sh", "output", "linux-riscv64")
            run.assert_not_called()

    def test_cross_arch_layout_uses_target_rid(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "upstream" / "src"
            source.mkdir(parents=True)
            (source / "dev.sh").write_text("# fixture\n")
            (source / "runnerversion").write_text("1.2.3\n")
            (source.parent / "LICENSE").write_text("runner license\n")
            output = root / "output"
            calls = []

            def execute(argv, **kwargs):
                calls.append(argv)
                if argv[:2] == ["git", "clone"]:
                    Path(argv[-1]).mkdir()
                elif argv[1:3] == ["dev.sh", "layout"]:
                    layout = Path(kwargs["cwd"]).parent / "_layout"
                    layout.mkdir()
                    (layout / "target-binary").write_bytes(b"arm64 payload")
                elif argv != ["git", "read-tree", "HEAD"]:
                    self.fail(f"Unexpected execution (possibly target binary): {argv}")

            with patch.object(build.sys, "platform", "linux"), patch.object(build.subprocess, "run", side_effect=execute):
                build.build(source / "dev.sh", output, "linux-arm64")

            self.assertEqual(calls[-1][1:], ["dev.sh", "layout", "Release", "linux-arm64"])
            self.assertEqual((output / "target-binary").read_bytes(), b"arm64 payload")
            self.assertEqual((output / "VERSION").read_text(), "1.2.3\n")
            self.assertEqual((output / "LICENSE").read_text(), "runner license\n")


if __name__ == "__main__":
    unittest.main()
