from __future__ import annotations

import copy
import json
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools"))

import artifact_manifest  # noqa: E402
import check_licenses  # noqa: E402
import check_workspace_boundaries  # noqa: E402
import generate_sdk  # noqa: E402
import validate_contract  # noqa: E402


class QualityGateNegativeTests(unittest.TestCase):
    def test_contract_rejects_missing_success_response(self) -> None:
        document = json.loads((ROOT / "contracts" / "openapi.json").read_text(encoding="utf-8"))
        broken = copy.deepcopy(document)
        del broken["paths"]["/health"]["get"]["responses"]["200"]
        self.assertIn("GET /health: missing 200 response", validate_contract.validate(broken))

    def test_license_gate_rejects_unapproved_license(self) -> None:
        report = {"GPL-3.0-only": [{"name": "unexpected-package"}]}
        self.assertEqual(
            check_licenses.validate(report),
            ["unapproved license GPL-3.0-only: unexpected-package"],
        )

    def test_artifact_gate_rejects_mutated_file(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            payload = root / "payload.txt"
            payload.write_text("accepted\n", encoding="utf-8")
            artifact_manifest.write_manifest(root)
            payload.write_text("mutated\n", encoding="utf-8")
            self.assertEqual(artifact_manifest.verify_manifest(root), ["digest mismatch: payload.txt"])

    def test_workspace_gate_rejects_direct_fetch(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "web" / "src").mkdir(parents=True)
            (root / "web" / "package.json").write_text(
                json.dumps({"dependencies": {"@teddycloud-next/sdk": "workspace:*"}}),
                encoding="utf-8",
            )
            (root / "web" / "src" / "bad.ts").write_text(
                'import "@teddycloud-next/sdk"; fetch("/api");\n',
                encoding="utf-8",
            )
            self.assertIn("web source bypasses the SDK with direct fetch", check_workspace_boundaries.validate(root))

    def test_generator_gate_rejects_stale_output(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "generated.ts"
            output.write_text("stale\n", encoding="utf-8")
            self.assertNotEqual(output.read_text(encoding="utf-8"), generate_sdk.render())


if __name__ == "__main__":
    unittest.main()
