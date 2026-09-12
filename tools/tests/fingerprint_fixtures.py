"""Synthetic, secret-free echo captures. Expected wire order is hand authored."""
import base64
import copy
import json


def packed(value):
    return json.dumps(value, separators=(",", ":"))


def raw_zstd(data):
    # A valid streaming frame with raw blocks, including the final-block bit.
    blocks = [data[i:i + 65536] for i in range(0, len(data), 65536)] or [b""]
    return b"\x28\xb5\x2f\xfd\x00\x58" + b"".join(
        ((len(block) << 3) | int(i == len(blocks) - 1)).to_bytes(3, "little") + block
        for i, block in enumerate(blocks)
    )


def set_body(row, body, compress=None):
    if compress is None:
        compress = row.get("headers", {}).get("content-encoding") == "zstd"
    raw = body.encode() if isinstance(body, str) else packed(body).encode()
    row["body_wire_b64"] = base64.b64encode(raw_zstd(raw) if compress else raw).decode()
    return row


def fixture_body(row):
    raw = base64.b64decode(row["body_wire_b64"])
    if raw.startswith(b"\x28\xb5\x2f\xfd\x00\x58"):
        # These fixtures use one raw block; do not use this to parse real captures.
        raw = raw[9:]
    return json.loads(raw)


def metadata(window=3):
    return {
        "installation_id": "device-A", "session_id": "session-A",
        "thread_id": "session-A", "turn_id": "turn-A", "root_turn_id": "turn-A",
        "window_id": f"session-A:{window}", "window_number": window,
        "context_window_id": "context-A",
    }


def headers(window=3):
    return {
        "originator": "codex-tui",
        "user-agent": "codex-tui/0.153.4 (Linux; x86_64) terminal (codex-tui; 0.153.4)",
        "version": "0.153.4", "session-id": "session-A", "thread-id": "session-A",
        "x-client-request-id": "session-A", "x-codex-window-id": f"session-A:{window}",
        "x-codex-turn-metadata": packed(metadata(window)),
        "x-openai-memgen-request": "true",
        "x-responsesapi-include-timing-metrics": "true",
    }


def body(window=3):
    return {
        "model": "fixture-model", "input": [], "stream": True,
        "prompt_cache_key": "session-A",
        "client_metadata": {
            "session_id": "session-A", "thread_id": "session-A",
            "x-codex-installation-id": "device-A",
            "x-codex-window-id": f"session-A:{window}",
            "x-codex-turn-metadata": packed(dict(metadata(window), tool_namespaces_info=["shell", "apply_patch"])),
        },
    }


def capture():
    rows = []
    for name in ("responses-direct", "responses-relayed", "responses-nonstream", "compact", "images", "search"):
        row = {"kind": "http", "case": name, "method": "POST",
               "path": "/backend-api/codex/responses", "request_rc": 0, "headers": headers()}
        value = body()
        if name == "compact":
            row["path"] += "/compact"
            row["headers"].pop("x-client-request-id")
            row["headers"]["x-codex-installation-id"] = "device-A"
            value = {"model": "fixture-model", "input": [], "prompt_cache_key": "session-A"}
        elif name == "search":
            row["path"] = "/backend-api/codex/alpha/search"
            row["headers"] = {k: v for k, v in headers().items() if k in ("originator", "user-agent", "version")}
            row["headers"]["x-codex-turn-metadata"] = packed({
                "session_id": "session-A", "thread_id": "session-A", "turn_id": "turn-A",
                "codex_version": "0.153.4", "model": "fixture-model",
            })
            value = {"id": "session-A", "model": "fixture-model", "query": "fixture"}
        else:
            row["headers"]["content-encoding"] = "zstd"
            if name.startswith("responses"):
                row["headers"]["x-codex-inference-call-id"] = "2f4f2132-8719-4b24-a1f5-0e3e3c5294de"
                value["stream"] = name != "responses-nonstream"
            else:
                value = {"model": "fixture-model", "input": [], "client_metadata": {"x-codex-installation-id": "device-A"}}
        rows.append(set_body(row, value))
    rows.append({
        "kind": "ws_handshake", "case": "ws", "connection": "connection-A",
        "method": "GET", "path": "/backend-api/codex/responses",
        "request_rc": 0, "headers": dict(headers(), **{"openai-beta": "responses_websockets=2026-02-06"}),
    })
    for turn in (1, 2):
        value = {"type": "response.create", **body(turn + 2)}
        value["client_metadata"]["x-codex-turn-state"] = ("ts-handshake", "ts-own")[turn - 1]
        value["client_metadata"]["x-codex-ws-stream-request-start-ms"] = str(1800000000000 + turn)
        rows.append(set_body({
            "kind": "ws_message", "case": "ws", "connection": "connection-A", "turn": turn,
            "opcode": 1, "fin": True, "compressed": False, "sent_at_ms": 1800000000000 + turn,
        }, value))
    return copy.deepcopy(rows)
