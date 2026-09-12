#!/usr/bin/env python3
"""Read-only, offline Codex capture validation. See tools/README.md for schemas."""
import argparse
import base64
import binascii
import ctypes
import ctypes.util
import json
from pathlib import Path
import re
import sys
import unittest
import uuid


REQUIRED_HEADERS = (
    "session-id", "thread-id", "x-client-request-id", "x-codex-window-id",
    "x-codex-turn-metadata",
)
RESPONSES_ORDER = (
    "model instructions input tools tool_choice parallel_tool_calls reasoning store stream "
    "stream_options include service_tier prompt_cache_key text client_metadata access_programs"
).split()
COMPACT_ORDER = (
    "model input instructions tools parallel_tool_calls reasoning service_tier "
    "prompt_cache_key text access_programs"
).split()
WS_ORDER = (
    "type model instructions previous_response_id input tools tool_choice parallel_tool_calls "
    "reasoning store stream stream_options include service_tier prompt_cache_key text "
    "generate client_metadata access_programs"
).split()
HTTP_CASES = (
    "responses-direct", "responses-relayed", "responses-nonstream", "compact", "images", "search",
)
SEARCH_FORBIDDEN = (
    "installation_id window_id window_number context_window_id agent_name parent_turn_id "
    "root_turn_id request_kind compaction history_ingest_requested forked_from_ordinal_exclusive "
    "tool_namespaces_info"
).split()
CONDITIONAL_HEADERS = ("x-openai-memgen-request", "x-responsesapi-include-timing-metrics")
INBOUND_INFERENCE_ID = "bbd9bf7b-cb3d-48e7-bdcb-1c4bba7ee0a1"
MAX_BODY = 16 * 1024 * 1024
MAX_CAPTURE = 64 * 1024 * 1024


class CaptureError(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise CaptureError(message)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON member")
        result[key] = value
    return result


def reject_constant(_value):
    raise CaptureError("non-finite JSON number")


def json_object(raw):
    require(isinstance(raw, (str, bytes)), "expected raw JSON text")
    try:
        # Decode first so JSON cannot auto-detect encodings or strip a byte BOM.
        if isinstance(raw, bytes):
            raw = raw.decode("utf-8", errors="strict")
        value = json.loads(raw, object_pairs_hook=unique_object, parse_constant=reject_constant)
    except (ValueError, UnicodeError, RecursionError) as exc:
        raise CaptureError("malformed or duplicate JSON object") from exc
    require(isinstance(value, dict) and bool(value), "expected nonempty JSON object")
    return value


def text(value):
    return isinstance(value, str) and bool(value.strip())


def field(obj, key):
    value = obj.get(key)
    require(text(value), f"missing or invalid field: {key}")
    return value


def equal(actual, expected, label):
    require(actual is not None and expected is not None
            and type(actual) is type(expected) and actual == expected, f"identity mismatch: {label}")


def get_headers(record):
    raw = record.get("headers")
    require(isinstance(raw, (dict, list)), "headers must be an object or list of pairs")
    pairs = list(raw.items()) if isinstance(raw, dict) else raw
    result = {}
    for pair in pairs:
        require(isinstance(pair, (list, tuple)) and len(pair) == 2, "invalid header pair")
        key, value = pair
        require(isinstance(key, str) and re.fullmatch(r"[!#$%&'*+.^_`|~0-9A-Za-z-]+", key),
                "invalid header name")
        require(isinstance(value, str) and not any(ch in value for ch in "\r\n\0"), "invalid header value")
        key = key.lower()
        require(key not in result, "duplicate header")
        result[key] = value
    return result


def client_metadata(body):
    value = body.get("client_metadata")
    require(isinstance(value, dict) and bool(value), "missing or invalid client_metadata")
    return value


def check(record):
    """Legacy per-record carrier check; not a complete device-profile acceptance."""
    try:
        require(isinstance(record, dict), "record must be an object")
        headers = get_headers(record)
        for name in REQUIRED_HEADERS:
            field(headers, name)
        body = record.get("body")
        require(isinstance(body, dict), "body must be an object")
        cm = client_metadata(body)
        meta = json_object(headers["x-codex-turn-metadata"])
        equal(headers["session-id"], headers["thread-id"], "session/thread")
        equal(headers["thread-id"], headers["x-client-request-id"], "thread/request")
        for obj, key in ((body, "prompt_cache_key"), (cm, "session_id"), (meta, "session_id")):
            equal(field(obj, key), headers["session-id"], key)
    except CaptureError as exc:
        return [str(exc)]
    return []


def decompress_zstd(wire):
    """Use installed libzstd, with a bounded output and exactly one complete frame."""
    require(wire[:6] == b"\x28\xb5\x2f\xfd\x00\x58", "invalid zstd streaming frame header")
    library = ctypes.util.find_library("zstd")
    require(library is not None, "strict zstd validation requires installed libzstd")
    try:
        zstd = ctypes.CDLL(library)
        zstd.ZSTD_isError.argtypes = [ctypes.c_size_t]
        zstd.ZSTD_isError.restype = ctypes.c_uint
        zstd.ZSTD_findFrameCompressedSize.argtypes = [ctypes.c_void_p, ctypes.c_size_t]
        zstd.ZSTD_findFrameCompressedSize.restype = ctypes.c_size_t
        zstd.ZSTD_decompress.argtypes = [ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p, ctypes.c_size_t]
        zstd.ZSTD_decompress.restype = ctypes.c_size_t
        source = ctypes.create_string_buffer(wire)
        size = zstd.ZSTD_findFrameCompressedSize(source, len(wire))
        require(not zstd.ZSTD_isError(size) and size == len(wire), "incomplete zstd frame or trailing bytes")
        output = ctypes.create_string_buffer(MAX_BODY)
        size = zstd.ZSTD_decompress(output, MAX_BODY, source, len(wire))
        require(not zstd.ZSTD_isError(size), "zstd decompression failed or body exceeds limit")
        return output.raw[:size]
    except (OSError, AttributeError) as exc:
        raise CaptureError("installed libzstd is unavailable or incompatible") from exc


def wire_body(row, compressed=False):
    encoded = field(row, "body_wire_b64")
    require(len(encoded) <= (MAX_BODY + 1024) * 4 // 3 + 4, "wire body exceeds limit")
    try:
        wire = base64.b64decode(encoded, validate=True)
    except (ValueError, binascii.Error) as exc:
        raise CaptureError("invalid base64 wire body") from exc
    raw = decompress_zstd(wire) if compressed else wire
    require(len(raw) <= MAX_BODY, "decoded body exceeds limit")
    return json_object(raw), raw


def check_order(body, order):
    indexes = []
    for key in body:
        require(key in order, "unknown top-level field in strict profile")
        indexes.append(order.index(key))
    require(indexes == sorted(indexes), "top-level field order mismatch")


def no_fields(obj, names, label):
    for name in names:
        require(name not in obj, f"forbidden {label}: {name}")


def common_headers(row, kind):
    headers = get_headers(row)
    ua = field(headers, "user-agent")
    origin = field(headers, "originator")
    version = field(headers, "version")
    require(ua.startswith(f"{origin}/{version} ") and ua.endswith(f"({origin}; {version})"),
            "UA/originator/version or client trailer mismatch")
    meta = json_object(field(headers, "x-codex-turn-metadata"))
    no_fields(headers, ("session_id", "conversation_id"), "header")
    no_fields(meta, ("tool_namespaces_info",), "header metadata")
    require("responses=experimental" not in headers.get("openai-beta", "").lower(), "legacy beta header")
    if kind == "search":
        no_fields(headers, (*REQUIRED_HEADERS[:-1], "x-codex-installation-id",
                            "x-codex-inference-call-id", "openai-beta"), "header")
        no_fields(meta, SEARCH_FORBIDDEN, "search metadata")
        for key in ("session_id", "thread_id", "turn_id", "codex_version", "model"):
            field(meta, key)
        equal(meta["codex_version"], version, "search version")
        return headers, meta
    for name in ("session-id", "thread-id", "x-codex-window-id"):
        field(headers, name)
    for name in CONDITIONAL_HEADERS:
        equal(field(headers, name), "true", name)
    equal(headers["session-id"], headers["thread-id"], "header session/thread")
    if kind == "compact":
        no_fields(headers, ("x-client-request-id",), "header")
        equal(field(headers, "x-codex-installation-id"), field(meta, "installation_id"), "compact device")
    else:
        equal(field(headers, "x-client-request-id"), headers["thread-id"], "request/thread")
        no_fields(headers, ("x-codex-installation-id",), "header")
    if kind != "responses":
        no_fields(headers, ("x-codex-inference-call-id",), "header")
    else:
        inference = field(headers, "x-codex-inference-call-id")
        try:
            parsed = uuid.UUID(inference)
        except ValueError as exc:
            raise CaptureError("invalid inference-call-id") from exc
        require(parsed.version == 4 and inference != INBOUND_INFERENCE_ID, "inference-call-id not regenerated")
    for key in ("installation_id", "session_id", "thread_id", "turn_id", "root_turn_id",
                "window_id", "context_window_id"):
        field(meta, key)
    equal(meta.get("window_number"), 3, "header window number")
    equal(headers["x-codex-window-id"], headers["thread-id"] + ":3", "header window")
    equal(meta["window_id"], headers["x-codex-window-id"], "metadata window")
    equal(meta["session_id"], headers["session-id"], "metadata session")
    equal(meta["thread_id"], headers["thread-id"], "metadata thread")
    equal(meta["turn_id"], meta["root_turn_id"], "metadata root/turn")
    if kind == "ws":
        no_fields(headers, ("x-codex-turn-state", "content-encoding", "sec-websocket-extensions"), "header")
        require(re.fullmatch(r"responses_websockets=\d{4}-\d{2}-\d{2}", headers.get("openai-beta", "")),
                "invalid WS beta negotiation")
    return headers, meta


def body_identity(body, headers, meta, window):
    cm = client_metadata(body)
    bm = json_object(field(cm, "x-codex-turn-metadata"))
    equal(field(body, "prompt_cache_key"), headers["session-id"], "body cache key")
    for key, header in (("session_id", "session-id"), ("thread_id", "thread-id")):
        equal(field(cm, key), headers[header], "body " + key)
        equal(field(bm, key), cm[key], "embedded " + key)
    equal(field(cm, "x-codex-installation-id"), meta["installation_id"], "body device")
    equal(field(bm, "installation_id"), meta["installation_id"], "embedded device")
    expected_window = headers["thread-id"] + ":" + str(window)
    equal(field(cm, "x-codex-window-id"), expected_window, "body window")
    equal(field(bm, "window_id"), expected_window, "embedded window")
    equal(bm.get("window_number"), window, "embedded window number")
    for key in ("turn_id", "root_turn_id", "context_window_id"):
        equal(field(bm, key), meta[key], "embedded " + key)
    equal(bm.get("tool_namespaces_info"), ["shell", "apply_patch"], "body tool list")
    return cm


def request_evidence(row, method, path):
    equal(row.get("method"), method, "request method")
    equal(row.get("path"), path, "upstream path")
    equal(row.get("request_rc"), 0, "request exit code")


def check_http(row, case):
    kind = "responses" if case.startswith("responses-") else case
    path = "/backend-api/codex/" + {"compact": "responses/compact", "search": "alpha/search"}.get(kind, "responses")
    request_evidence(row, "POST", path)
    headers, meta = common_headers(row, kind)
    compressed = kind in ("responses", "images")
    equal(headers.get("content-encoding", ""), "zstd" if compressed else "", "content encoding")
    body, _ = wire_body(row, compressed)
    field(body, "model")
    if kind == "search":
        equal(field(body, "id"), meta["session_id"], "search id")
        equal(body["model"], meta["model"], "search model")
        return None
    check_order(body, COMPACT_ORDER if kind == "compact" else RESPONSES_ORDER)
    if kind == "responses":
        body_identity(body, headers, meta, 3)
        equal(body.get("stream"), case != "responses-nonstream", "stream mode")
    elif kind == "compact":
        no_fields(body, ("client_metadata",), "compact body")
        equal(field(body, "prompt_cache_key"), headers["session-id"], "compact cache key")
    else:
        cm = client_metadata(body)
        equal(field(cm, "x-codex-installation-id"), meta["installation_id"], "image device")
    return meta["installation_id"]


def check_ws(rows):
    hs, *frames = rows
    request_evidence(hs, "GET", "/backend-api/codex/responses")
    connection = field(hs, "connection")
    headers, meta = common_headers(hs, "ws")
    previous_start = 0
    for turn, row in enumerate(frames, 1):
        equal(row.get("connection"), connection, "WS connection")
        equal(row.get("turn"), turn, "WS turn")
        equal(row.get("opcode"), 1, "WS Text opcode")
        equal(row.get("fin"), True, "WS final frame")
        equal(row.get("compressed"), False, "WS compression")
        value, raw = wire_body(row)
        require(not raw.endswith(b"\n") and not any(token in raw.lower() for token in
                (b"\\u003c", b"\\u003e", b"\\u0026")), "WS serializer newline or HTML escaping")
        check_order(value, WS_ORDER)
        equal(value.get("type"), "response.create", "WS event type")
        cm = body_identity(value, headers, meta, turn + 2)
        equal(field(cm, "x-codex-turn-state"), ("ts-handshake", "ts-own")[turn - 1], "WS turn state")
        start = field(cm, "x-codex-ws-stream-request-start-ms")
        require(re.fullmatch(r"[0-9]{13}", start), "WS start must be decimal milliseconds")
        sent = row.get("sent_at_ms")
        require(type(sent) is int and sent > 0, "missing WS send timestamp evidence")
        require(abs(int(start) - sent) <= 5000 and int(start) >= previous_start
                and start != "1700000000123", "WS start timestamp not freshly stamped")
        previous_start = int(start)
    return meta["installation_id"]


def check_capture(rows, suite="all"):
    """Strict controlled-rehearsal matrix, not an arbitrary production-log validator."""
    problems = []
    try:
        require(suite in ("all", "http", "ws"), "unknown strict suite")
        require(isinstance(rows, list) and bool(rows), "empty or invalid capture")
        expected = [("http", case) for case in HTTP_CASES] if suite != "ws" else []
        if suite != "http":
            expected += [("ws_handshake", "ws"), ("ws_message", "ws"), ("ws_message", "ws")]
        require(len(rows) == len(expected), "incomplete or extra capture records")
        for row, (kind, case) in zip(rows, expected):
            require(isinstance(row, dict), "record must be an object")
            equal(row.get("kind"), kind, "capture kind/order")
            equal(row.get("case"), case, "capture case/order")
    except CaptureError as exc:
        return [str(exc)]
    devices = []
    for index, case in enumerate(HTTP_CASES if suite != "ws" else ()):
        try:
            device = check_http(rows[index], case)
            if device:
                devices.append(device)
        except CaptureError as exc:
            problems.append(f"{case}: {exc}")
    if suite != "http":
        try:
            devices.append(check_ws(rows[-3:]))
        except CaptureError as exc:
            problems.append(f"ws: {exc}")
    if len(set(devices)) > 1:
        problems.append("cross-endpoint installation identity mismatch")
    return problems


def load_capture(path):
    with open(path, "rb") as stream:
        raw = stream.read(MAX_CAPTURE + 1)
    require(len(raw) <= MAX_CAPTURE, "capture exceeds size limit")
    rows = []
    for line_no, line in enumerate(raw.splitlines(), 1):
        if line.strip():
            try:
                rows.append(json_object(line))
            except CaptureError as exc:
                raise CaptureError(f"record {line_no}: {exc}") from exc
    require(bool(rows), "empty capture")
    return rows


def selftest():
    tests = unittest.defaultTestLoader.discover(str(Path(__file__).with_name("tests")), pattern="test_fp_probe.py")
    return 0 if unittest.TextTestRunner(verbosity=2).run(tests).wasSuccessful() else 1


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("capture", nargs="?")
    parser.add_argument("--selftest", action="store_true")
    parser.add_argument("--strict", action="store_true", help="require complete raw-wire device-profile evidence")
    parser.add_argument("--suite", choices=("all", "http", "ws"), default="all",
                        help="explicit subset for strict validation (default: all)")
    args = parser.parse_args()
    if args.selftest:
        if args.capture:
            parser.error("--selftest does not consume a capture")
        return selftest()
    if not args.capture:
        parser.error("capture is required unless --selftest is used")
    if args.suite != "all" and not args.strict:
        parser.error("--suite requires --strict")
    try:
        rows = load_capture(args.capture)
        errors = check_capture(rows, args.suite) if args.strict else [
            f"record {index}: {error}" for index, row in enumerate(rows, 1) for error in check(row)
        ]
    except (CaptureError, OSError) as exc:
        print(f"capture error: {exc}", file=sys.stderr)
        return 1
    for error in errors:
        print(error, file=sys.stderr)
    if not errors:
        print(f"{'strict ' + args.suite if args.strict else 'legacy carrier'} check: {len(rows)} records passed")
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
