#!/usr/bin/env python3
"""Create and validate the non-release TeddyCloud Next artifact layout."""

from __future__ import annotations

import argparse
import hashlib
import shutil
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
ARTIFACT = DIST / "artifact"
MANIFEST = "SHA256SUMS"


def digest(path: Path) -> str:
    hasher = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def write_manifest(root: Path) -> None:
    files = sorted(path for path in root.rglob("*") if path.is_file() and path.name != MANIFEST)
    lines = [f"{digest(path)}  {path.relative_to(root).as_posix()}" for path in files]
    (root / MANIFEST).write_text("\n".join(lines) + "\n", encoding="utf-8", newline="\n")


def verify_manifest(root: Path) -> list[str]:
    manifest = root / MANIFEST
    if not manifest.is_file():
        return ["missing SHA256SUMS"]
    expected: dict[str, str] = {}
    errors: list[str] = []
    for line in manifest.read_text(encoding="utf-8").splitlines():
        try:
            checksum, relative = line.split("  ", 1)
        except ValueError:
            errors.append(f"malformed manifest line: {line}")
            continue
        if relative in expected:
            errors.append(f"duplicate manifest path: {relative}")
        expected[relative] = checksum

    actual_paths = {
        path.relative_to(root).as_posix()
        for path in root.rglob("*")
        if path.is_file() and path.name != MANIFEST
    }
    if set(expected) != actual_paths:
        errors.append("manifest file set does not match artifact file set")
    for relative, checksum in expected.items():
        path = root / relative
        if path.is_file() and digest(path) != checksum:
            errors.append(f"digest mismatch: {relative}")
    return errors


def create() -> None:
    if ARTIFACT.exists():
        shutil.rmtree(ARTIFACT)
    (ARTIFACT / "application" / "bin").mkdir(parents=True)
    (ARTIFACT / "application" / "contracts").mkdir(parents=True)
    (ARTIFACT / "sdk").mkdir(parents=True)
    shutil.copy2(DIST / "backend" / "teddycloud-next", ARTIFACT / "application" / "bin")
    shutil.copytree(DIST / "web", ARTIFACT / "application" / "web")
    shutil.copy2(DIST / "contracts" / "openapi.json", ARTIFACT / "application" / "contracts")
    packages = sorted((DIST / "sdk-package").glob("*.tgz"))
    if len(packages) != 1:
        raise RuntimeError(f"expected one SDK tarball, found {len(packages)}")
    shutil.copy2(packages[0], ARTIFACT / "sdk")
    write_manifest(ARTIFACT)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("create", "verify"))
    args = parser.parse_args()
    if args.action == "create":
        create()
        print(f"created artifact layout: {ARTIFACT}")
        return 0
    errors = verify_manifest(ARTIFACT)
    if errors:
        print("\n".join(errors))
        return 1
    print(f"validated artifact manifest: {ARTIFACT / MANIFEST}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
