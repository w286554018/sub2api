import base64
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from fingerprint_fixtures import body, capture, fixture_body, headers, metadata, packed, raw_zstd, set_body


TOOLS = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("fp_probe", TOOLS / "fp_probe.py")
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)

BODY_INDEXES = (0, 1, 2, 3, 4, 5, 7, 8)
INVALID_JSON_ENCODINGS = (
    ("utf-16-le", b""), ("utf-16-be", b""),
    ("utf-32-le", b""), ("utf-32-be", b""),
    ("utf-16-le", b"\xff\xfe"), ("utf-16-be", b"\xfe\xff"),
    ("utf-32-le", b"\xff\xfe\x00\x00"), ("utf-32-be", b"\x00\x00\xfe\xff"),
    ("utf-8", b"\xef\xbb\xbf"),
)


def wire_capture(index, raw):
    rows = capture()
    row = rows[index]
    wire = raw_zstd(raw) if row.get("headers", {}).get("content-encoding") == "zstd" else raw
    row["body_wire_b64"] = base64.b64encode(wire).decode("ascii")
    return rows


class LegacyTests(unittest.TestCase):
    def run_capture(self, text, *args):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "capture.jsonl"
            path.write_bytes(text.encode("utf-8") if isinstance(text, str) else text)
            return subprocess.run([sys.executable, str(TOOLS / "fp_probe.py"), *args, str(path)],
                                  text=True, capture_output=True)

    def test_legacy_valid_capture_still_works(self):
        row = {"headers": headers(), "body": body()}
        self.assertEqual(self.run_capture(packed(row)).returncode, 0)

    def test_empty_capture_fails_closed(self):
        for text in ("", "\n \n"):
            with self.subTest(text=text):
                result = self.run_capture(text)
                self.assertEqual(result.returncode, 1)
                self.assertIn("empty", result.stderr)

    def test_malformed_capture_has_diagnostic_not_traceback(self):
        for value in ("{", "null", "[]", '{"headers":[],"body":false}',
                      '{"headers":{},"body":{"client_metadata":[]}}',
                      '{"headers":{"x-codex-turn-metadata":"["},"body":{}}',
                      '{"headers":{},"body":{},"body":{}}', '{"headers":{"a":NaN}}'):
            with self.subTest(value=value):
                result = self.run_capture(value)
                self.assertEqual(result.returncode, 1)
                self.assertNotIn("Traceback", result.stderr)

    def test_strict_cli_accepts_complete_fixture(self):
        result = self.run_capture("\n".join(map(packed, capture())), "--strict")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_cli_rejects_non_utf8_or_bom_capture_lines(self):
        for args, rows in (((), [{"headers": headers(), "body": body()}]),
                           (("--strict",), capture())):
            text = "\n".join(map(packed, rows))
            for encoding, bom in INVALID_JSON_ENCODINGS:
                with self.subTest(args=args, encoding=encoding, bom=bool(bom)):
                    result = self.run_capture(bom + text.encode(encoding), *args)
                    self.assertEqual(result.returncode, 1, result.stdout)
                    self.assertIn("capture error:", result.stderr)
                    self.assertNotIn("Traceback", result.stderr)

    def test_strict_cli_rejects_utf16_utf32_or_bom_wire_bodies(self):
        for index in BODY_INDEXES:
            text = packed(fixture_body(capture()[index]))
            for encoding, bom in INVALID_JSON_ENCODINGS:
                with self.subTest(index=index, encoding=encoding, bom=bool(bom)):
                    rows = wire_capture(index, bom + text.encode(encoding))
                    result = self.run_capture("\n".join(map(packed, rows)), "--strict")
                    self.assertEqual(result.returncode, 1, result.stdout)
                    self.assertIn("malformed", result.stderr)
                    self.assertNotIn("Traceback", result.stderr)
                    self.assertTrue(probe.check_capture(rows))

    def test_strict_cli_rejects_malformed_utf8_wire_bodies(self):
        for index in BODY_INDEXES:
            value = fixture_body(capture()[index])
            value["query" if index == 5 else "input"] = "wire-marker"
            for invalid in (b"\x80", b"\xc0\xaf", b"\xc3", b"\xed\xa0\x80", b"\xf4\x90\x80\x80"):
                with self.subTest(index=index, invalid=invalid):
                    raw = packed(value).encode("utf-8").replace(b"wire-marker", invalid)
                    rows = wire_capture(index, raw)
                    result = self.run_capture("\n".join(map(packed, rows)), "--strict")
                    self.assertEqual(result.returncode, 1, result.stdout)
                    self.assertIn("malformed", result.stderr)
                    self.assertNotIn("Traceback", result.stderr)
                    self.assertTrue(probe.check_capture(rows))

    def test_strict_cli_accepts_utf8_and_escaped_unicode_wire_bodies(self):
        sample = "caf\u00e9 \u65e5\u672c \U0001f4a1 \ufeff"
        for index in BODY_INDEXES:
            value = fixture_body(capture()[index])
            value["query" if index == 5 else "input"] = sample
            for escaped in (False, True):
                with self.subTest(index=index, escaped=escaped):
                    raw = json.dumps(value, ensure_ascii=escaped, separators=(",", ":")).encode("utf-8")
                    rows = wire_capture(index, raw)
                    self.assertEqual(probe.check_capture(rows), [])
                    result = self.run_capture("\n".join(map(packed, rows)), "--strict")
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("strict all check: 9 records passed", result.stdout)


class StrictTests(unittest.TestCase):
    def check(self, rows, suite="all"):
        self.assertTrue(hasattr(probe, "check_capture"), "strict capture validator is missing")
        return probe.check_capture(rows, suite=suite)

    def test_complete_http_ws_and_explicit_suites(self):
        rows = capture()
        self.assertEqual(self.check(rows), [])
        self.assertEqual(self.check(rows[:6], "http"), [])
        self.assertEqual(self.check(rows[6:], "ws"), [])

    def test_missing_duplicate_extra_reordered_capture_rejected(self):
        rows = capture()
        for bad in ([], rows[:-1], rows[1:], rows[:6], rows[6:], rows + [rows[-1]],
                    [rows[1], rows[0], *rows[2:]], [*rows[:2], rows[0], *rows[3:]]):
            with self.subTest(count=len(bad)):
                self.assertTrue(self.check(bad))

    def test_bad_shapes_fail_without_exceptions(self):
        for bad in (None, {}, [None], [False], [1], ["x"], [[]]):
            self.assertTrue(self.check(bad))
        for key, value in (("headers", None), ("headers", [["session-id"]]),
                           ("headers", {"session-id": []}), ("kind", []),
                           ("request_rc", False), ("request_rc", "0"), ("request_rc", 7),
                           ("method", "GET"), ("path", "/wrong"), ("case", "unknown"),
                           ("body_wire_b64", None), ("body_wire_b64", "***")):
            rows = capture()
            rows[0][key] = value
            with self.subTest(key=key, value=value):
                self.assertTrue(self.check(rows))

    def test_required_headers_and_body_carriers_cannot_be_omitted(self):
        for index in range(7):
            for key in tuple(capture()[index]["headers"]):
                if key == "content-encoding":
                    continue
                rows = capture()
                del rows[index]["headers"][key]
                with self.subTest(index=index, key=key):
                    self.assertTrue(self.check(rows))
        for key in body()["client_metadata"]:
            rows = capture()
            value = body()
            del value["client_metadata"][key]
            set_body(rows[0], value)
            self.assertTrue(self.check(rows), key)

    def test_duplicate_headers_metadata_and_json_rejected(self):
        for value in ('{"model":"a","model":"b"}', '{"a":NaN}', "[]", "null", "{"):
            rows = capture()
            set_body(rows[0], value)
            self.assertTrue(self.check(rows))
        rows = capture()
        rows[0]["headers"] = list(headers().items()) + [("Session-ID", "foreign")]
        self.assertTrue(self.check(rows))
        rows = capture()
        rows[0]["headers"]["x-codex-turn-metadata"] = '{"session_id":"A","session_id":"B"}'
        self.assertTrue(self.check(rows))

    def test_each_embedded_metadata_relation_is_required(self):
        for key in metadata():
            for index in (0, 7, 8):
                for mode in ("drop", "change"):
                    rows = capture()
                    value = fixture_body(rows[index])
                    meta = metadata(4 if index == 8 else 3)
                    if mode == "drop":
                        del meta[key]
                    else:
                        meta[key] = "wrong"
                    value["client_metadata"]["x-codex-turn-metadata"] = packed(
                        dict(meta, tool_namespaces_info=["shell", "apply_patch"]))
                    set_body(rows[index], value)
                    with self.subTest(key=key, index=index, mode=mode):
                        self.assertTrue(self.check(rows))

    def test_header_metadata_tools_removed_body_tools_retained(self):
        rows = capture()
        rows[0]["headers"]["x-codex-turn-metadata"] = packed(dict(metadata(), tool_namespaces_info=[]))
        self.assertTrue(self.check(rows))
        rows = capture()
        value = body()
        value["client_metadata"]["x-codex-turn-metadata"] = packed(metadata())
        set_body(rows[0], value)
        self.assertTrue(self.check(rows))

    def test_actual_zstd_bytes_not_collector_claims(self):
        for wire in (b"", b"\x28\xb5\x2f\xfd\x00\x58", b"{}", raw_zstd(b"{}")[:-1],
                     raw_zstd(packed(body()).encode()) + b"trailing"):
            rows = capture()
            rows[0]["body_wire_b64"] = base64.b64encode(wire).decode()
            rows[0].update(body=body(), body_raw_head_hex="28b52ffd0058", zstd_rc=0)
            self.assertTrue(self.check(rows))
        good = raw_zstd(packed(body()).encode())
        for byte in (4, 5):
            rows = capture()
            bad = bytearray(good)
            bad[byte] ^= 1
            rows[0]["body_wire_b64"] = base64.b64encode(bad).decode()
            self.assertTrue(self.check(rows))

    def test_order_compression_and_encoding_matrix(self):
        for index in (0, 3, 4, 7, 8):
            rows = capture()
            value = fixture_body(rows[index])
            set_body(rows[index], dict(reversed(list(value.items()))))
            self.assertTrue(self.check(rows))
        for index in (3, 5):
            rows = capture()
            rows[index]["headers"]["content-encoding"] = "zstd"
            self.assertTrue(self.check(rows))
        rows = capture()
        rows[0]["headers"]["content-encoding"] = "gzip"
        self.assertTrue(self.check(rows))

    def test_identity_ua_inference_and_search_contracts(self):
        for index, key, value in (
            (0, "session-id", "different"), (0, "user-agent", "codex-tui/0.153.4 (Linux)"),
            (0, "version", "wrong"), (0, "originator", "wrong"),
            (0, "x-codex-inference-call-id", "bbd9bf7b-cb3d-48e7-bdcb-1c4bba7ee0a1"),
            (0, "openai-beta", "responses=experimental"), (3, "x-client-request-id", "leak"),
            (4, "x-codex-inference-call-id", "leak"), (5, "session-id", "leak"),
            (6, "x-codex-turn-state", "leak"), (6, "openai-beta", "not_responses_websockets")):
            rows = capture()
            rows[index]["headers"][key] = value
            self.assertTrue(self.check(rows), (index, key))
        for key in ("installation_id", "window_number", "parent_turn_id", "tool_namespaces_info"):
            rows = capture()
            meta = json.loads(rows[5]["headers"]["x-codex-turn-metadata"])
            meta[key] = "leak"
            rows[5]["headers"]["x-codex-turn-metadata"] = packed(meta)
            self.assertTrue(self.check(rows))
        rows = capture()
        meta = json.loads(rows[5]["headers"]["x-codex-turn-metadata"])
        meta["model"] = "wrong-model"
        rows[5]["headers"]["x-codex-turn-metadata"] = packed(meta)
        self.assertTrue(self.check(rows))

    def test_ws_frame_evidence_and_both_turns(self):
        for index in (7, 8):
            for key, bad in (("opcode", 2), ("opcode", True), ("fin", False),
                             ("compressed", True), ("connection", "other"), ("turn", 3),
                             ("sent_at_ms", None)):
                rows = capture()
                rows[index][key] = bad
                self.assertTrue(self.check(rows), (index, key))
            for field, bad in (("session_id", "other"), ("x-codex-turn-state", "wrong"),
                               ("x-codex-ws-stream-request-start-ms", "1700000000123"),
                               ("x-codex-ws-stream-request-start-ms", "1800000000001x"),
                               ("x-codex-ws-stream-request-start-ms", 1800000000001)):
                rows = capture()
                value = json.loads(base64.b64decode(rows[index]["body_wire_b64"]))
                value["client_metadata"][field] = bad
                set_body(rows[index], value)
                self.assertTrue(self.check(rows), (index, field))

    def test_cross_endpoint_installation_must_match(self):
        rows = capture()
        rows[3]["headers"]["x-codex-installation-id"] = "device-B"
        meta = metadata()
        meta["installation_id"] = "device-B"
        rows[3]["headers"]["x-codex-turn-metadata"] = packed(meta)
        self.assertTrue(self.check(rows))


if __name__ == "__main__":
    unittest.main()
