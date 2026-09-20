#!/usr/bin/env python3
"""Check that browser API access crosses the generated SDK boundary."""

from __future__ import annotations

import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def validate(root: Path) -> list[str]:
    errors: list[str] = []
    package = json.loads((root / "web" / "package.json").read_text(encoding="utf-8"))
    if package.get("dependencies", {}).get("@teddycloud-next/sdk") != "workspace:*":
        errors.append("web must consume @teddycloud-next/sdk as workspace:*")

    sources = sorted((root / "web" / "src").rglob("*.ts")) + sorted(
        (root / "web" / "src").rglob("*.tsx")
    )
    combined = "\n".join(path.read_text(encoding="utf-8") for path in sources)
    if "@teddycloud-next/sdk" not in combined:
        errors.append("web source does not import the SDK workspace package")
    if re.search(r"\bfetch\s*\(", combined):
        errors.append("web source bypasses the SDK with direct fetch")
    if "next/backend" in combined or "internal/" in combined:
        errors.append("web source imports backend internals")
    return errors


def main() -> int:
    errors = validate(ROOT)
    if errors:
        print("\n".join(errors))
        return 1
    print("validated Web-to-SDK workspace boundary")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
