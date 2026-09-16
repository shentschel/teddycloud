#!/usr/bin/env python3
"""Mutation tests for the offline PI-02 compatibility fixture validator."""

from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "check_compatibility_fixtures.py"
SOURCE_FIXTURES = ROOT / "tests" / "fixtures" / "compatibility"


class CompatibilityFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.fixtures = Path(self.temporary.name) / "compatibility"
        shutil.copytree(SOURCE_FIXTURES, self.fixtures)

    def run_checker(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SCRIPT), "--fixtures-dir", str(self.fixtures)],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
            timeout=10,
        )

    def load(self, name: str) -> dict:
        return json.loads((self.fixtures / name).read_text(encoding="utf-8"))

    def save(self, name: str, value: dict) -> None:
        (self.fixtures / name).write_text(
            json.dumps(value, indent=2, sort_keys=True) + "\n",
            encoding="utf-8",
        )

    def assert_rejected_for(self, code: str) -> None:
        result = self.run_checker()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(f"[{code}]", result.stderr)

    def test_accepted_fixture_corpus_passes(self) -> None:
        result = self.run_checker()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Validated 4 JSON catalogs, 12 cases and 2 route manifests offline.", result.stdout)

    def test_swapped_uid_ruid_relation_is_rejected(self) -> None:
        name = "ct-01-02-08-10-catalog.json"
        value = self.load(name)
        value["identities"]["ruid"] = value["identities"]["uid_compact"]
        self.save(name, value)
        self.assert_rejected_for("identity.byte_order")

    def test_plugin_object_wrapper_is_rejected(self) -> None:
        name = "ct-01-02-08-10-catalog.json"
        value = self.load(name)
        case = next(case for case in value["cases"] if case["id"] == "A-CT10-bare-array")
        case["response"]["body"] = {"plugins": case["response"]["body"]}
        self.save(name, value)
        self.assert_rejected_for("wrapper.plugin_array")

    def test_omitted_cf01_state_capture_is_rejected(self) -> None:
        name = "ct-01-03-negative-state.json"
        value = self.load(name)
        case = next(case for case in value["cases"] if case["id"] == "A-CF01-disposable-get-writeback")
        del case["file_state_capture"]
        self.save(name, value)
        self.assert_rejected_for("state.cf01_capture")

    def test_length_prefixed_ct15_protobuf_is_rejected(self) -> None:
        name = "ct-15-freshness-protobuf.json"
        value = self.load(name)
        request = value["request"]
        original = bytes.fromhex(request["body_hex"])
        mutated = len(original).to_bytes(4, "big") + original
        request["body_hex"] = mutated.hex()
        request["headers"]["Content-Length"] = str(len(mutated))
        request["body_sha256"] = hashlib.sha256(mutated).hexdigest()
        self.save(name, value)
        self.assert_rejected_for("protobuf.framing")


if __name__ == "__main__":
    unittest.main()
