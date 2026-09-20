#!/usr/bin/env python3
"""Validate deterministic structural requirements of the minimal OpenAPI file."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def validate(document: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    if document.get("openapi") != "3.1.0":
        errors.append("openapi must be 3.1.0")

    schemas = document.get("components", {}).get("schemas", {})
    operation_ids: set[str] = set()
    paths = document.get("paths")
    if not isinstance(paths, dict) or not paths:
        return errors + ["paths must be a non-empty object"]

    for path, path_item in paths.items():
        if not isinstance(path_item, dict):
            errors.append(f"{path}: path item must be an object")
            continue
        for method, operation in path_item.items():
            if method not in {"get", "post", "put", "patch", "delete"}:
                continue
            if not isinstance(operation, dict):
                errors.append(f"{method.upper()} {path}: operation must be an object")
                continue
            operation_id = operation.get("operationId")
            if not isinstance(operation_id, str) or not operation_id:
                errors.append(f"{method.upper()} {path}: missing operationId")
            elif operation_id in operation_ids:
                errors.append(f"duplicate operationId: {operation_id}")
            else:
                operation_ids.add(operation_id)
            responses = operation.get("responses", {})
            if "200" not in responses:
                errors.append(f"{method.upper()} {path}: missing 200 response")

    def check_refs(value: Any) -> None:
        if isinstance(value, dict):
            reference = value.get("$ref")
            if isinstance(reference, str):
                prefix = "#/components/schemas/"
                if not reference.startswith(prefix) or reference[len(prefix) :] not in schemas:
                    errors.append(f"unresolved schema reference: {reference}")
            for child in value.values():
                check_refs(child)
        elif isinstance(value, list):
            for child in value:
                check_refs(child)

    check_refs(document)
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("contract", type=Path)
    args = parser.parse_args()
    errors = validate(json.loads(args.contract.read_text(encoding="utf-8")))
    if errors:
        print("\n".join(errors))
        return 1
    print(f"validated OpenAPI contract: {args.contract}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
