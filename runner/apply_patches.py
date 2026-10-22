# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Copy declared runner sources and apply patches with patch-ng."""

import logging
from pathlib import Path
import sys

import patch_ng


def main(output_root, *arguments):
    separator = arguments.index("--")
    patches = [Path(path).resolve() for path in arguments[:separator]]
    output_root = Path(output_root).resolve()
    for source in arguments[separator + 1:]:
        destination = output_root / Path(source).as_posix().split("runner/runner/", 1)[1]
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(Path(source).read_text(encoding="utf-8-sig"), encoding="utf-8", newline="\n")
    for path in patches:
        patch = patch_ng.fromfile(str(path))
        if not patch or not patch.apply(strip=1, root=str(output_root)):
            raise RuntimeError(f"Failed to apply {path}")


if __name__ == "__main__":
    logging.basicConfig(level=logging.WARNING)
    main(*sys.argv[1:])
