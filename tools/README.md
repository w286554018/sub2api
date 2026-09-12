# Codex Maintenance Tools

These tools inspect local captures or public source code. They do not run a
gateway, send inference requests, install software, deploy, publish, or modify
Git history. Selftests use synthetic fixtures, not real account credentials.

## Capture Checker

```sh
python3 tools/fp_probe.py capture.jsonl
python3 tools/fp_probe.py --strict capture.jsonl
python3 tools/fp_probe.py --strict --suite http http-capture.jsonl
python3 tools/fp_probe.py --strict --suite ws ws-capture.jsonl
python3 tools/fp_probe.py --selftest
```

The original one-file invocation remains a **legacy carrier check**. Records
have `headers` and a decoded object `body`; it checks the session/thread/request,
cache key and embedded session relationship. It does not establish endpoint,
compression, WS or full profile acceptance. Empty captures now fail.

Strict mode is a controlled **device + convergence** rehearsal contract adapted
from KlN-4096/sub2api at `b0092e436df4485ffc2e16f14b77f0631fa58501`.
It is not a validator for arbitrary production traffic or disabled/legacy modes.
Default `--suite all` requires exactly these records, in order:

| Kind | Case | Method / Upstream Path |
| --- | --- | --- |
| `http` | `responses-direct` | POST `/backend-api/codex/responses` |
| `http` | `responses-relayed` | POST `/backend-api/codex/responses` |
| `http` | `responses-nonstream` | POST `/backend-api/codex/responses` |
| `http` | `compact` | POST `/backend-api/codex/responses/compact` |
| `http` | `images` | POST `/backend-api/codex/responses` |
| `http` | `search` | POST `/backend-api/codex/alpha/search` |
| `ws_handshake` | `ws` | GET `/backend-api/codex/responses` |
| `ws_message` | `ws` | First Text frame |
| `ws_message` | `ws` | Second Text frame |

HTTP and handshake records require an integer `request_rc: 0` from the
rehearsal sender, plus `method`, `path`, `headers`, `kind`, and `case`.
Headers may be a string-value object or a list of name/value pairs. Duplicate
headers, including case aliases, are rejected. Never retain Authorization,
cookies, actual turn-state tokens or other secrets in fixtures.

Every HTTP/frame body must have `body_wire_b64`, the base64 of the **actual
outbound bytes**, not a reserialized JSON object. For HTTP Responses/images,
these bytes must be one complete zstd frame with prefix `28b52ffd0058`;
`content-encoding` must be `zstd`. An installed `libzstd` is required to check
the complete frame and decompress it. Missing libraries fail closed; the tool
does not install one. Compact/search and WS bodies are uncompressed UTF-8 JSON.
`body`, `body_raw_head_hex` and `zstd_rc` collector claims cannot substitute for
wire bytes. Decoded bodies are limited to 16 MiB, captures to 64 MiB.

The handshake has a nonempty `connection` identifier. Each frame repeats that
identifier and includes integer `turn: 1` or `2`, integer `opcode: 1`,
`fin: true`, `compressed: false`, and integer `sent_at_ms` recorded by the
collector at send time. Both frames must be raw `response.create` Text JSON
without a serializer-added newline or HTML escaping.

Controlled input recipe (the gateway must produce consistent scoped values):

- Use the same credential/device across paths. Each request's session, thread,
  cache key and window carriers must agree; different HTTP cases may use
  different sessions. Responses direct/relayed are streaming, nonstream is false.
- Supply window number **3** in HTTP/handshake and first WS frame; increment it
  to **4** in the second frame. Keep turn/root/context identity fixed within WS.
- Supply `tool_namespaces_info: ["shell","apply_patch"]` in turn metadata.
  The compatibility header must omit it; embedded body metadata must retain it.
- Supply `x-openai-memgen-request: true` and
  `x-responsesapi-include-timing-metrics: true` on non-search inputs.
- Supply HTTP inference ID `bbd9bf7b-cb3d-48e7-bdcb-1c4bba7ee0a1`.
  Responses must replace it with a UUIDv4; compact/images/search/WS must omit it.
- First WS input uses handshake state `ts-handshake` with no first-frame state.
  Second input explicitly uses frame state `ts-own`; it must not be overwritten.
  Second input timestamp `1700000000123` must be replaced. Both outbound
  timestamps must be decimal millisecond strings within 5 seconds of the
  collector's send timestamp and nondecreasing.

Strict mode checks raw top-level field order, required metadata relationships,
per-endpoint device carriers, MCP search projection, cross-endpoint installation
identity, and UA/originator/version/trailer agreement. Unknown/duplicate JSON
members in the controlled ordered profile fail acceptance. This conservative
test policy does not change the gateway's duplicate-preservation contracts.
See `tests/fingerprint_fixtures.py` for complete synthetic capture examples.

Exit codes: **0** accepted; **1** invalid, empty, incomplete or unreadable
capture / missing decompressor; **2** invalid invocation. An explicit subset
does not prove the omitted suite. Synthetic selftest success does not prove
that a gateway actually forwarded correctly, or that upstream accepts its TLS.

## Offline Linux Runner

```sh
sh tools/run-codex-fingerprint-offline.sh --selftest
sh tools/run-codex-fingerprint-offline.sh --strict capture.jsonl
sh tools/run-codex-fingerprint-offline.sh --check-binary /tmp/fingerprint.test
sh tools/run-codex-fingerprint-offline.sh /tmp/fingerprint.test '^TestCodex'
```

One-file capture validation is portable. Two-argument binary mode requires
Linux or WSL2, an unprivileged caller, user/network namespace support, GNU
`timeout`, util-linux `unshare`/`setpriv`, `ip`, Python 3 and standard shell
utilities. It never calls sudo or installs missing dependencies. A restricted
kernel/WSL environment fails rather than falling back to host execution.

Build a trusted test binary outside the worktree with installed dependencies:

```sh
cd backend
env CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off \
  go test -c -mod=readonly -tags=unit -buildvcs=false \
  -o /tmp/fingerprint.test ./internal/service
```

The runner stages its own copies in a private temporary directory, creates new
user/network namespaces, verifies loopback-only links, empty IPv4/IPv6 routes
and unreachable external documentation addresses, then drops capabilities and
clears the environment before even package initialization/test enumeration.
Only a matching 64-bit Linux ELF is executed; Windows interop and wrong
architectures fail. A pattern matching no tests fails.

The outer timeout is 58 seconds with a 2-second kill grace; Go's test timeout is
55 seconds. Failures from timeout, namespace setup, privilege dropping,
enumeration and test execution are nonzero and are propagated. The private
staging directory is cleaned up. This is network isolation for trusted tests,
not a general sandbox for hostile binaries or arbitrary host filesystem access.
`--check-binary` is only a static header/architecture check.

### Windows / WSL

```powershell
.\tools\test-codex-fingerprint-offline.ps1 -Distribution Ubuntu -TestPattern '^TestCodex'
```

Use an already installed Go toolchain and an existing WSL2 distribution whose
default user is unprivileged. The launcher discovers WSL architecture, compiles
the unit-tag service test binary for Linux with downloads disabled and
`-mod=readonly`, translates paths via `wslpath`, then invokes the same namespace
runner. It supports `-GoExecutable`, `-OutputDirectory`, `-TestPackage`
(`./internal/service` or `./internal/handler`), and `-TestPattern`.
Output stays outside the worktree; environment overrides are restored. Native
WSL/compiler/test failures retain their exit code. Local validation errors
return 1. Neither WSL nor dependencies are installed automatically.

## Source Drift Gate

```sh
python3 tools/check_codex_source_drift.py --selftest
python3 tools/check_codex_source_drift.py --repo /path/to/existing/openai-codex
python3 tools/check_codex_source_drift.py --repo /path/to/existing/openai-codex --candidate COMMIT
# Optional, explicit, anonymous read-only public HTTPS requests:
python3 tools/check_codex_source_drift.py --network
```

`codex-source-references.json` pins the audited reference to the full commit
`e7637306bc9246a3e42e407cb94f96b7ed345e3e`, preserving the six narrow identity
paths from the target audit. This is a drift signal, not a complete upstream
compatibility audit. Reference changes require human source/contract review.

Local mode only resolves commits and reads tree objects. It never fetches
missing objects, updates refs or changes the working tree. Network mode uses
anonymous HTTPS GETs against the fixed public `openai/codex` API, with no token,
netrc, credential helper, inherited proxy or redirects. The moving candidate is
resolved once to an immutable commit/tree before comparison. Missing/truncated
API data, rate limits and missing baseline paths are errors, never "unchanged".

Exit codes: **0** unchanged watched files; **1** source/mode/deletion drift,
manual review required; **2** unavailable evidence or invalid invocation.
Unrelated source changes do not fail the gate.

The independent `codex-source-drift.yml` workflow has only `contents: read`,
disables checkout credential persistence, and runs offline fixtures on relevant
PRs. Scheduled/manual runs explicitly opt into the public comparison. It cannot
rebase, merge, force-push, create tags, dispatch releases or publish artifacts.

## Local Verification

```sh
python3 tools/fp_probe.py --selftest
python3 tools/check_codex_source_drift.py --selftest
python3 -m unittest discover -s tools/tests -v
sh -n tools/run-codex-fingerprint-offline.sh
bash -n deploy/install.sh
```

Configuration tests use Ruby's YAML parser and the installed Go linker; the
release fixture intentionally has no VERSION artifact. Script-boundary runner
tests use explicit fake native commands to check exit propagation, not kernel
isolation. PowerShell parsing is conditional on installed `pwsh`; a skipped
parser test is not a WSL run. Deployment tests requiring Bash 4 and native
Linux/systemd or a real GoReleaser release must be recorded separately.
