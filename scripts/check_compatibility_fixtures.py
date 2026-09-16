#!/usr/bin/env python3
"""Validate the PI-02 compatibility fixture corpus without network access."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import re
import struct
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_FIXTURES = ROOT / "tests" / "fixtures" / "compatibility"
JSON_FILES = (
    "ct-01-02-08-10-catalog.json",
    "ct-01-03-negative-state.json",
    "ct-15-freshness-protobuf.json",
    "ct-negative-status.json",
)
REVISION_RE = re.compile(r"^[0-9a-f]{7,40}$")


class Validator:
    def __init__(self, fixtures_dir: Path) -> None:
        self.fixtures_dir = fixtures_dir
        self.errors: list[str] = []
        self.documents: dict[str, dict[str, Any]] = {}
        self.cases: dict[str, dict[str, Any]] = {}

    def error(self, code: str, location: str, message: str) -> None:
        self.errors.append(f"[{code}] {location}: {message}")

    def load_json(self, name: str) -> dict[str, Any] | None:
        path = self.fixtures_dir / name
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            self.error("corpus.missing", name, "required fixture file is absent")
            return None
        except json.JSONDecodeError as exc:
            self.error("json.syntax", name, f"{exc.msg} at line {exc.lineno}")
            return None
        if not isinstance(value, dict):
            self.error("json.root", name, "root must be an object")
            return None
        self.documents[name] = value
        return value

    def register_cases(self, name: str, document: dict[str, Any]) -> None:
        raw_cases = document.get("cases", [document] if "id" in document else [])
        if not isinstance(raw_cases, list) or not raw_cases:
            self.error("case.collection", name, "must contain at least one case")
            return
        for index, case in enumerate(raw_cases):
            location = f"{name}:case[{index}]"
            if not isinstance(case, dict):
                self.error("case.type", location, "case must be an object")
                continue
            case_id = case.get("id")
            if not isinstance(case_id, str) or not case_id:
                self.error("case.id", location, "stable non-empty id is required")
                continue
            if case_id in self.cases:
                self.error("case.duplicate", case_id, "case id must be unique")
                continue
            self.cases[case_id] = case

            for key in (
                "ct",
                "producer",
                "consumer",
                "request",
                "response",
                "initial_state",
                "final_state",
                "side_effects",
                "assertions",
                "unknowns",
            ):
                if key not in case:
                    self.error("case.field", case_id, f"missing {key}")
            if not (case.get("source") or case.get("source_evidence")):
                self.error("case.source", case_id, "source evidence is required")
            origin = case.get("origin", document.get("origin"))
            if not isinstance(origin, str) or not origin:
                self.error("case.provenance", case_id, "origin/provenance is required")

    def validate_document_headers(self) -> None:
        for name, document in self.documents.items():
            revision = document.get("source_revision")
            if not isinstance(revision, str) or not REVISION_RE.fullmatch(revision):
                self.error("provenance.revision", name, "invalid source_revision")
            origin = document.get("origin")
            if not isinstance(origin, str) or not origin:
                self.error("provenance.origin", name, "origin is required")

    def validate_identity(self, identity: Any, location: str) -> None:
        if not isinstance(identity, dict):
            self.error("identity.type", location, "identity must be an object")
            return
        uid = identity.get("uid_compact")
        ruid = identity.get("ruid")
        if not isinstance(uid, str) or not isinstance(ruid, str):
            self.error("identity.fields", location, "uid_compact and ruid are required")
            return
        try:
            uid_bytes = bytes.fromhex(uid)
            ruid_bytes = bytes.fromhex(ruid)
        except ValueError:
            self.error("identity.hex", location, "UID values must be hexadecimal")
            return
        if len(uid_bytes) != 8 or len(ruid_bytes) != 8:
            self.error("identity.length", location, "UID values must contain eight bytes")
            return
        if ruid_bytes != uid_bytes[::-1]:
            self.error(
                "identity.byte_order",
                location,
                "ruid must reverse the eight uid_compact bytes",
            )
        display = identity.get("uid_display")
        if display is not None and display.replace(":", "").lower() != uid.lower():
            self.error("identity.display", location, "uid_display does not match uid_compact")

    def validate_identities(self) -> None:
        catalog = self.documents.get("ct-01-02-08-10-catalog.json", {})
        if "identities" in catalog:
            self.validate_identity(catalog["identities"], "catalog.identities")
        for case_id, case in self.cases.items():
            if "synthetic_identity" in case:
                self.validate_identity(case["synthetic_identity"], case_id)
            identities = case.get("synthetic_identities", [])
            if identities is not None and not isinstance(identities, list):
                self.error("identity.type", case_id, "synthetic_identities must be a list")
            elif isinstance(identities, list):
                for index, identity in enumerate(identities):
                    self.validate_identity(identity, f"{case_id}.synthetic_identities[{index}]")

    def require_case(self, case_id: str) -> dict[str, Any] | None:
        case = self.cases.get(case_id)
        if case is None:
            self.error("case.required", case_id, "required compatibility case is missing")
        return case

    def validate_wrappers(self) -> None:
        info = self.require_case("A-CT01-info-writeback")
        if info is not None:
            body = info.get("response", {}).get("body")
            if not isinstance(body, dict) or not isinstance(body.get("tagInfo"), dict):
                self.error("wrapper.tagInfo", info["id"], "response body must wrap an object in tagInfo")

        index = self.require_case("A-CT01-index-wrapper")
        if index is not None:
            body = index.get("response", {}).get("body")
            if not isinstance(body, dict) or not isinstance(body.get("tags"), list):
                self.error("wrapper.tags", index["id"], "response body must wrap a list in tags")

        plugins = self.require_case("A-CT10-bare-array")
        if plugins is not None:
            body = plugins.get("response", {}).get("body")
            if not isinstance(body, list):
                self.error(
                    "wrapper.plugin_array",
                    plugins["id"],
                    "plugin discovery response body must be a bare array",
                )

    def validate_cf01_state_capture(self) -> None:
        case = self.require_case("A-CF01-disposable-get-writeback")
        if case is None:
            return
        capture = case.get("file_state_capture")
        if not isinstance(capture, dict):
            self.error(
                "state.cf01_capture",
                case["id"],
                "CF-01 write-back fixture must declare file_state_capture",
            )
            return
        required = ("paths_relative_to_fixture_root", "before", "after", "expected_semantic_diff_if_save_succeeds")
        if any(not capture.get(key) for key in required):
            self.error(
                "state.cf01_capture",
                case["id"],
                "file_state_capture must declare paths, before, after and semantic diff",
            )
        if capture.get("exact_serialized_bytes", "missing") is not None:
            self.error(
                "state.cf01_unknown_bytes",
                case["id"],
                "source fixture must preserve unknown exact serialized bytes as null",
            )
        effects = " ".join(
            str(item.get("effect", "")) for item in case.get("side_effects", []) if isinstance(item, dict)
        ).lower()
        if "rewrite" not in effects:
            self.error("state.cf01_effect", case["id"], "metadata rewrite side effect must be declared")

    def validate_binary_blob(self, record: Any, location: str) -> bytes | None:
        if not isinstance(record, dict):
            self.error("protobuf.record", location, "binary record must be an object")
            return None
        body_hex = record.get("body_hex")
        digest = record.get("body_sha256")
        content_length = record.get("headers", {}).get("Content-Length")
        try:
            body = bytes.fromhex(body_hex)
        except (TypeError, ValueError):
            self.error("protobuf.hex", location, "body_hex must contain complete bytes")
            return None
        try:
            expected_length = int(content_length)
        except (TypeError, ValueError):
            self.error("protobuf.length", location, "Content-Length must be an integer string")
            return body
        if len(body) != expected_length:
            self.error(
                "protobuf.length",
                location,
                f"Content-Length {expected_length} does not match {len(body)} body bytes",
            )
        actual_digest = hashlib.sha256(body).hexdigest()
        if digest != actual_digest:
            self.error("protobuf.digest", location, "body_sha256 does not match body_hex")
        return body

    def validate_positive_protobuf(self) -> None:
        case = self.require_case("A-CT15-freshness-raw-protobuf")
        if case is None:
            return
        request = case.get("request")
        response = case.get("response")
        request_body = self.validate_binary_blob(request, f"{case['id']}.request")
        self.validate_binary_blob(response, f"{case['id']}.response")
        if not isinstance(request, dict) or not isinstance(response, dict):
            return
        expected_framing = "v1-freshness-check/proto2-unprefixed-http-body"
        if request.get("framing_version") != expected_framing or response.get("framing_version") != expected_framing:
            self.error("protobuf.framing_version", case["id"], "request/response framing_version is not the accepted version")
        if request_body is None:
            return
        if not request_body or request_body[0] != 0x0A:
            self.error(
                "protobuf.framing",
                case["id"],
                "HTTP body must start with protobuf field 1, not a length prefix",
            )
            return
        if len(request_body) < 2 or request_body[1] != len(request_body) - 2:
            self.error("protobuf.framing", case["id"], "outer message length does not match HTTP body")
            return
        nested = request_body[2:]
        if len(nested) != 14 or nested[0] != 0x09 or nested[9] != 0x15:
            self.error("protobuf.framing", case["id"], "request does not contain fixed64 UID followed by fixed32 audio ID")
            return
        decoded = request.get("decoded", {}).get("tonie_infos", [{}])[0]
        try:
            expected_uid = int(decoded.get("uid"), 16)
            expected_audio = int(decoded.get("audio_id"))
        except (TypeError, ValueError):
            self.error("protobuf.decoded", case["id"], "decoded UID/audio_id is invalid")
            return
        if struct.unpack("<Q", nested[1:9])[0] != expected_uid:
            self.error("protobuf.byte_order", case["id"], "fixed64 UID is not little-endian decoded value")
        if struct.unpack("<I", nested[10:14])[0] != expected_audio:
            self.error("protobuf.byte_order", case["id"], "fixed32 audio_id is not little-endian decoded value")

    def validate_other_binary_records(self) -> None:
        negative = self.require_case("A-CT15-truncated-protobuf-message")
        if negative is not None:
            self.validate_binary_blob(negative.get("request"), f"{negative['id']}.request")

    def validate_collision(self) -> None:
        case = self.require_case("A-CF03-shared-audio-distinct-hash-collision")
        if case is None:
            return
        entries = case.get("initial_state", {}).get("catalog_entries")
        if not isinstance(entries, list) or len(entries) != 2:
            self.error("collision.entries", case["id"], "exactly two synthetic catalog entries are required")
            return
        audio_ids = {entry.get("audio_id") for entry in entries if isinstance(entry, dict)}
        hashes = {entry.get("hash_hex") for entry in entries if isinstance(entry, dict)}
        if len(audio_ids) != 1:
            self.error("collision.audio_id", case["id"], "entries must share one audio ID")
        try:
            hash_bytes = [bytes.fromhex(value) for value in hashes]
        except (TypeError, ValueError):
            self.error("collision.hash", case["id"], "hashes must be hexadecimal")
            return
        if len(hashes) != 2 or any(len(value) != 20 for value in hash_bytes):
            self.error("collision.hash", case["id"], "entries need distinct 20-byte hashes")

    def validate_tsv(self, name: str, expected_rows: int, required_fields: tuple[str, ...]) -> None:
        path = self.fixtures_dir / name
        try:
            with path.open(encoding="utf-8", newline="") as handle:
                reader = csv.DictReader(handle, delimiter="\t")
                rows = list(reader)
                fields = tuple(reader.fieldnames or ())
        except FileNotFoundError:
            self.error("corpus.missing", name, "required manifest is absent")
            return
        missing = [field for field in required_fields if field not in fields]
        if missing:
            self.error("manifest.columns", name, f"missing columns: {', '.join(missing)}")
            return
        if len(rows) != expected_rows:
            self.error("manifest.rows", name, f"expected {expected_rows} rows, found {len(rows)}")
        for expected, row in enumerate(rows, 1):
            if row.get("order") != str(expected):
                self.error("manifest.order", name, f"row {expected} has order {row.get('order')!r}")
            if not REVISION_RE.fullmatch(row.get("source_revision", "")):
                self.error("manifest.revision", name, f"row {expected} has invalid source_revision")
            if not row.get("disposition_reason"):
                self.error("manifest.reason", name, f"row {expected} lacks disposition_reason")

    def validate_manifests(self) -> None:
        self.validate_tsv(
            "router-manifest.tsv",
            69,
            (
                "order",
                "source_line",
                "source_revision",
                "method",
                "prefix",
                "server_type",
                "handler",
                "contract",
                "coverage",
                "owner",
                "disposition_reason",
            ),
        )
        self.validate_tsv(
            "router-fallback-manifest.tsv",
            4,
            (
                "order",
                "source_revision",
                "source_line",
                "kind",
                "condition",
                "result",
                "coverage",
                "owner",
                "disposition_reason",
            ),
        )

    def run(self) -> list[str]:
        for name in JSON_FILES:
            document = self.load_json(name)
            if document is not None:
                self.register_cases(name, document)
        self.validate_document_headers()
        self.validate_identities()
        self.validate_wrappers()
        self.validate_cf01_state_capture()
        self.validate_positive_protobuf()
        self.validate_other_binary_records()
        self.validate_collision()
        self.validate_manifests()
        return self.errors


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--fixtures-dir",
        type=Path,
        default=DEFAULT_FIXTURES,
        help="fixture directory to validate (defaults to repository corpus)",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    validator = Validator(args.fixtures_dir.resolve())
    errors = validator.run()
    if errors:
        print("\n".join(errors), file=sys.stderr)
        print(f"Compatibility fixture validation failed with {len(errors)} error(s).", file=sys.stderr)
        return 1
    print(
        f"Validated {len(validator.documents)} JSON catalogs, "
        f"{len(validator.cases)} cases and 2 route manifests offline."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
