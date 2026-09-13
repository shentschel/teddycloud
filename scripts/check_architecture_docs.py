#!/usr/bin/env python3
"""Check local architecture Markdown contracts without network access."""

from __future__ import annotations

import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ARCHITECTURE = ROOT / "docs" / "architecture"
MARKDOWN_LINK = re.compile(r"\[[^\]]*\]\(([^)]+)\)")


def check_file(path: Path) -> list[str]:
    errors: list[str] = []
    text = path.read_text(encoding="utf-8")

    if len(re.findall(r"^```", text, re.MULTILINE)) % 2:
        errors.append(f"{path.relative_to(ROOT)}: unclosed fenced code block")

    for line_number, line in enumerate(text.splitlines(), 1):
        if line.rstrip() != line:
            errors.append(
                f"{path.relative_to(ROOT)}:{line_number}: trailing whitespace"
            )

    for target in MARKDOWN_LINK.findall(text):
        if "://" in target or target.startswith(("#", "mailto:")):
            continue
        relative_target = target.split("#", 1)[0]
        if relative_target and not (path.parent / relative_target).exists():
            errors.append(
                f"{path.relative_to(ROOT)}: missing relative link {target}"
            )

    return errors


def main() -> int:
    files = sorted(ARCHITECTURE.rglob("*.md"))
    errors = [error for path in files for error in check_file(path)]
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"Checked {len(files)} architecture documents.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
