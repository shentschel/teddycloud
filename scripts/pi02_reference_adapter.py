#!/usr/bin/env python3
"""Offline PI-02 fixture adapter; not TeddyCloud handler or wire conformance."""

from __future__ import annotations

import copy
import json
import os
import tempfile
from pathlib import Path
from typing import Any
from urllib.parse import parse_qsl


BOOLEAN_ASSIGNMENT_FIELDS = frozenset({"live", "nocloud", "hide", "claimed"})
TEXT_ASSIGNMENT_FIELDS = frozenset({"source", "tonie_model"})
ASSIGNMENT_FIELDS = BOOLEAN_ASSIGNMENT_FIELDS | TEXT_ASSIGNMENT_FIELDS


def semantic_diff(before: dict[str, Any], after: dict[str, Any]) -> dict[str, dict[str, Any]]:
    """Return changed top-level values without making byte-serialization claims."""

    return {
        key: {"before": before.get(key), "after": after.get(key)}
        for key in sorted(before.keys() | after.keys())
        if before.get(key) != after.get(key) or (key in before) != (key in after)
    }


def atomic_json_patch(
    path: Path, updates: dict[str, Any]
) -> tuple[dict[str, Any], dict[str, Any], dict[str, dict[str, Any]]]:
    """Patch a JSON object through a same-directory temporary file and os.replace."""

    before = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(before, dict):
        raise ValueError("metadata JSON root must be an object")
    after = copy.deepcopy(before)
    after.update(updates)

    descriptor, temporary_name = tempfile.mkstemp(
        dir=path.parent,
        prefix=f".{path.name}.",
        suffix=".tmp",
    )
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(after, handle, indent=2, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)

    persisted = json.loads(path.read_text(encoding="utf-8"))
    if persisted != after:
        raise RuntimeError("atomic JSON replacement did not preserve the intended semantic state")
    return before, persisted, semantic_diff(before, persisted)


def _assignment_value(field: str, value: str) -> Any:
    if field in TEXT_ASSIGNMENT_FIELDS:
        return value
    normalized = value.casefold()
    if normalized == "true":
        return True
    if normalized == "false":
        return False
    raise ValueError(f"{field} requires true or false")


def apply_assignment_form(
    metadata: dict[str, Any], body: str
) -> tuple[dict[str, Any], frozenset[str]]:
    """Apply only supported URL-encoded fields and return an isolated readback value."""

    updated = copy.deepcopy(metadata)
    supplied: set[str] = set()
    for field, raw_value in parse_qsl(body, keep_blank_values=True, strict_parsing=True):
        if field not in ASSIGNMENT_FIELDS:
            continue
        updated[field] = _assignment_value(field, raw_value)
        supplied.add(field)
    return updated, frozenset(supplied)


def normalize_cf03(
    tag_info: dict[str, Any], *, raw_has_cloud_auth: bool, cloud_override: bool
) -> dict[str, Any]:
    """Keep assigned/source models and raw/effective auth separate; infer no ownership."""

    tonie_info = tag_info.get("tonieInfo") or {}
    source_info = tag_info.get("sourceInfo") or {}
    return {
        "assigned_model": tonie_info.get("model"),
        "source_model": source_info.get("model"),
        "raw_has_cloud_auth": bool(raw_has_cloud_auth),
        "cloud_override": bool(cloud_override),
        "effective_has_cloud_auth": bool(raw_has_cloud_auth) and not bool(cloud_override),
        "physical_card_ownership": None,
        "physical_card_ownership_basis": "unsupported by fixture contract",
    }
