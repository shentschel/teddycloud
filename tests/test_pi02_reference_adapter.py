#!/usr/bin/env python3
"""Fixture-level PI-02 reference-adapter tests, not server/wire conformance."""

from __future__ import annotations

import copy
import json
import sys
import tempfile
import unittest
from pathlib import Path
from urllib.parse import parse_qsl


ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "tests" / "fixtures" / "compatibility"
sys.path.insert(0, str(ROOT / "scripts"))

from pi02_reference_adapter import (  # noqa: E402
    apply_assignment_form,
    atomic_json_patch,
    normalize_cf03,
)


def fixture_cases(name: str) -> dict[str, dict]:
    document = json.loads((FIXTURES / name).read_text(encoding="utf-8"))
    return {case["id"]: case for case in document["cases"]}


class Pi02ReferenceAdapterTests(unittest.TestCase):
    def test_cf01_atomic_metadata_semantic_diff(self) -> None:
        case = fixture_cases("ct-01-03-negative-state.json")["A-CF01-disposable-get-writeback"]
        before_fixture = case["initial_state"]["metadata_semantic_before"]
        expected_after = case["final_state"]["metadata_semantic_after_if_save_succeeds"]
        updates = {
            key: value
            for key, value in expected_after.items()
            if before_fixture.get(key) != value
        }
        original = copy.deepcopy(before_fixture)
        original["unrelated_fixture_field"] = {"preserved": [1, 2, 3]}

        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            relative = Path(case["initial_state"]["metadata_path_relative"])
            metadata_path = root / relative
            metadata_path.parent.mkdir(parents=True)
            metadata_path.write_text(json.dumps(original) + "\n", encoding="utf-8")

            before, after, difference = atomic_json_patch(metadata_path, updates)

            self.assertEqual(before, original)
            self.assertEqual(
                difference,
                {
                    "live": {"before": False, "after": True},
                    "nocloud": {"before": False, "after": True},
                },
            )
            self.assertEqual(
                {key: after[key] for key in expected_after},
                expected_after,
            )
            self.assertEqual(after["unrelated_fixture_field"], original["unrelated_fixture_field"])
            self.assertEqual(list(metadata_path.parent.glob("*.tmp")), [])

    def test_ct02_optional_form_fields_and_readback(self) -> None:
        case = fixture_cases("ct-01-02-08-10-catalog.json")["A-CT02-form-readback"]
        initial = copy.deepcopy(case["initial_state"]["content_json"])
        initial["unrelated_fixture_field"] = "preserve-me"

        updated, supplied = apply_assignment_form(initial, case["request"]["body"])
        fixture_supplied = frozenset(
            field for field, _ in parse_qsl(case["request"]["body"], keep_blank_values=True)
        )

        self.assertEqual(supplied, fixture_supplied)
        self.assertEqual(supplied, {"tonie_model", "nocloud"})
        self.assertEqual(
            {key: updated[key] for key in case["final_state"]["content_json"]},
            case["final_state"]["content_json"],
        )
        self.assertEqual(
            {key: updated[key] for key in case["readback"]["body"]},
            case["readback"]["body"],
        )
        self.assertEqual(updated["source"], initial["source"])
        self.assertEqual(updated["unrelated_fixture_field"], "preserve-me")

    def test_cf03_models_auth_and_ownership_stay_distinct(self) -> None:
        case = fixture_cases("ct-01-03-negative-state.json")[
            "A-CF03-assigned-source-auth-disagreement"
        ]
        tag_info = case["response"]["body_source_expected_subset"]["tagInfo"]
        derived = case["initial_state"]["derived_if_symbolic_auth_precondition_is_injected"]
        normalized = normalize_cf03(
            tag_info,
            raw_has_cloud_auth=derived["_has_cloud_auth"],
            cloud_override=derived["cloud_override"],
        )

        self.assertEqual(normalized["assigned_model"], "synthetic-assigned-model")
        self.assertEqual(normalized["source_model"], "synthetic-content-model")
        self.assertNotEqual(normalized["assigned_model"], normalized["source_model"])
        self.assertIs(normalized["raw_has_cloud_auth"], True)
        self.assertIs(normalized["cloud_override"], True)
        self.assertIs(normalized["effective_has_cloud_auth"], False)
        self.assertIsNone(normalized["physical_card_ownership"])
        self.assertEqual(
            normalized["physical_card_ownership_basis"],
            "unsupported by fixture contract",
        )


if __name__ == "__main__":
    unittest.main()
