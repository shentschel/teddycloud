#!/usr/bin/env python3
"""Reject dependency licenses outside the reviewed PI-03/B allowlist."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


ALLOWED_LICENSES = {
    "Apache-2.0",
    "BSD-2-Clause",
    "BSD-3-Clause",
    "ISC",
    "MIT",
    "MPL-2.0",
}


def validate(report: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    for license_name, packages in sorted(report.items()):
        if license_name not in ALLOWED_LICENSES:
            names = ", ".join(sorted(str(item.get("name", "unknown")) for item in packages))
            errors.append(f"unapproved license {license_name}: {names}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("report", type=Path)
    args = parser.parse_args()
    errors = validate(json.loads(args.report.read_text(encoding="utf-8")))
    if errors:
        print("\n".join(errors))
        return 1
    print(f"dependency licenses match allowlist: {args.report}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
