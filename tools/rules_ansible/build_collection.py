# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Package caller-supplied files and templates with ansible-galaxy."""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path, PurePosixPath


def build_collection(spec: dict, output: Path) -> Path:
    substitutions = dict(spec["substitutions"])
    for token, filename in spec["substitution_files"].items():
        substitutions[token] = Path(filename).read_text().strip()
    if spec["stamp_substitutions"]:
        status = dict(line.split(" ", 1) for line in Path(spec["status_file"]).read_text().splitlines() if " " in line)
        for token, key in spec["stamp_substitutions"].items():
            substitutions[token] = status[key]
    for token, pattern in spec["validation"].items():
        if not re.fullmatch(pattern, substitutions[token]):
            raise ValueError(f"Invalid template substitution: {token}")

    with tempfile.TemporaryDirectory(prefix="ansible-collection-") as directory:
        root = Path(directory)
        stage = root / "source"
        stage.mkdir()
        for kind in ("files", "templates"):
            for destination, source in spec[kind].items():
                relative = PurePosixPath(destination)
                if relative.is_absolute() or ".." in relative.parts or "\\" in destination:
                    raise ValueError(f"Invalid collection destination: {destination}")
                target = stage / destination
                target.parent.mkdir(parents=True, exist_ok=True)
                if kind == "templates":
                    content = Path(source).read_text()
                    for token, value in substitutions.items():
                        content = content.replace(token, value)
                    target.write_text(content)
                else:
                    shutil.copy2(source, target)
        build_output = root / "output"
        build_output.mkdir()
        config = root / "ansible.cfg"
        config.write_text("[defaults]\n")
        env = dict(os.environ, PYTHONPATH=os.pathsep.join(sys.path),
                   ANSIBLE_CONFIG=str(config), ANSIBLE_HOME=str(root / "home"))
        subprocess.run([
            sys.executable, "-m", "ansible.cli.galaxy", "collection", "build",
            str(stage), "--output-path", str(build_output),
        ], check=True, env=env)
        archives = list(build_output.glob("*.tar.gz"))
        if len(archives) != 1:
            raise ValueError("Expected exactly one collection archive from ansible-galaxy")
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(archives[0], output)
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--spec", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    build_collection(json.loads(args.spec.read_text()), args.output)


if __name__ == "__main__":
    main()
