#!/usr/bin/env python3
"""Exercise CT-15 fixtures with the repository-bundled protobuf-c parser."""

from __future__ import annotations

import json
import os
import shlex
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
HARNESS = ROOT / "tests" / "ct15_protobuf_parser_harness.c"
FIXTURES = ROOT / "tests" / "fixtures" / "compatibility"
PROTO_ROOT = ROOT / "src" / "proto"
REQUEST_SOURCE = PROTO_ROOT / "proto" / "toniebox.pb.freshness-check.fc-request.pb-c.c"
RESPONSE_SOURCE = PROTO_ROOT / "proto" / "toniebox.pb.freshness-check.fc-response.pb-c.c"
RUNTIME_SOURCE = PROTO_ROOT / "protobuf-c.c"


def system_compiler() -> list[str] | None:
    candidates = [os.environ.get("CC"), "cc", "gcc", "clang"]
    for candidate in candidates:
        if not candidate:
            continue
        command = shlex.split(candidate)
        if command and shutil.which(command[0]):
            return command
    return None


class Ct15ProtobufParserTests(unittest.TestCase):
    def test_positive_request_response_and_truncated_rejection(self) -> None:
        compiler = system_compiler()
        if compiler is None:
            self.skipTest("no system C compiler found (checked CC, cc, gcc and clang)")

        positive = json.loads(
            (FIXTURES / "ct-15-freshness-protobuf.json").read_text(encoding="utf-8")
        )
        negatives = json.loads(
            (FIXTURES / "ct-negative-status.json").read_text(encoding="utf-8")
        )
        truncated = next(
            case for case in negatives["cases"] if case["id"] == "A-CT15-truncated-protobuf-message"
        )
        request_decoded = positive["request"]["decoded"]["tonie_infos"][0]
        response_decoded = positive["response"]["decoded"]

        with tempfile.TemporaryDirectory() as temporary:
            executable = Path(temporary) / "ct15-protobuf-parser-harness"
            compile_command = [
                *compiler,
                "-std=c11",
                "-Wall",
                "-Wextra",
                "-I",
                str(ROOT / "include"),
                "-I",
                str(ROOT / "include" / "protobuf-c"),
                "-I",
                str(PROTO_ROOT),
                str(HARNESS),
                str(RUNTIME_SOURCE),
                str(REQUEST_SOURCE),
                str(RESPONSE_SOURCE),
                "-o",
                str(executable),
            ]
            compiled = subprocess.run(
                compile_command,
                cwd=ROOT,
                text=True,
                capture_output=True,
                check=False,
                timeout=30,
            )
            self.assertEqual(
                compiled.returncode,
                0,
                f"compile command: {shlex.join(compile_command)}\n{compiled.stdout}{compiled.stderr}",
            )

            harness_command = [
                str(executable),
                positive["request"]["body_hex"],
                truncated["request"]["body_hex"],
                positive["response"]["body_hex"],
                request_decoded["uid"],
                str(request_decoded["audio_id"]),
                str(response_decoded["field2"]),
                str(response_decoded["max_vol_spk"]),
                str(response_decoded["slap_en"]),
                str(response_decoded["slap_dir"]),
                str(response_decoded["field6"]),
                str(response_decoded["max_vol_hdp"]),
                str(response_decoded["led"]),
            ]
            executed = subprocess.run(
                harness_command,
                cwd=ROOT,
                text=True,
                capture_output=True,
                check=False,
                timeout=10,
            )
            self.assertEqual(executed.returncode, 0, executed.stdout + executed.stderr)
            self.assertEqual(
                executed.stdout,
                "accepted positive request; rejected 3-byte truncation; packed 14-byte response\n",
            )


if __name__ == "__main__":
    unittest.main()
