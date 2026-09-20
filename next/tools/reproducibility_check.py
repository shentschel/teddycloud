#!/usr/bin/env python3
"""Build the Next artifact twice from clean source copies and compare bytes."""

from __future__ import annotations

import hashlib
import os
import shlex
import shutil
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
EXCLUDED = {".cache", "dist", "node_modules", "__pycache__"}


def copy_workspace(destination: Path) -> Path:
    target = destination / "next"
    shutil.copytree(
        ROOT,
        target,
        ignore=shutil.ignore_patterns(*EXCLUDED, "*.pyc"),
        copy_function=shutil.copy2,
    )
    return target


def build(workspace: Path) -> dict[str, str]:
    make = shlex.split(os.environ.get("MAKE_COMMAND", "make"))
    subprocess.run(
        [*make, "-C", str(workspace), "clean", "bootstrap", "artifact", "artifact-check"],
        check=True,
        env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
    )
    artifact = workspace / "dist" / "artifact"
    return {
        path.relative_to(artifact).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted(artifact.rglob("*"))
        if path.is_file()
    }


def differences(first: dict[str, str], second: dict[str, str]) -> list[str]:
    return [
        f"digest mismatch: {path}: {first.get(path)} != {second.get(path)}"
        for path in sorted(set(first) | set(second))
        if first.get(path) != second.get(path)
    ]


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="teddycloud-next-repro-") as directory:
        root = Path(directory)
        first = build(copy_workspace(root / "first"))
        second = build(copy_workspace(root / "second"))
    errors = differences(first, second)
    if errors:
        print("\n".join(errors))
        return 1
    print(f"reproducible artifact: {len(first)} files and byte-identical SHA-256 digests")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
