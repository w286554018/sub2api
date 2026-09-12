#!/bin/sh
# Capture validation is portable; binary execution requires Linux user/net namespaces.
set -eu

fail() { printf '%s\n' "$*" >&2; exit 2; }

check_binary() {
  python3 - "$1" <<'PY'
import platform
import struct
import sys
try:
    with open(sys.argv[1], "rb") as stream:
        header = stream.read(64)
    if (len(header) != 64 or header[:7] != b"\x7fELF\x02\x01\x01"
            or header[7] not in (0, 3) or struct.unpack_from("<H", header, 16)[0] not in (2, 3)):
        raise ValueError("expected a 64-bit little-endian Linux ELF executable, not WSL interop")
    expected = {"x86_64": 62, "amd64": 62, "aarch64": 183, "arm64": 183}.get(platform.machine().lower())
    if expected is None or struct.unpack_from("<H", header, 18)[0] != expected:
        raise ValueError("Linux ELF architecture does not match the runner")
except (OSError, ValueError) as error:
    print("binary rejected: " + str(error), file=sys.stderr)
    sys.exit(2)
print("Linux ELF header/architecture validated (static check only)", flush=True)
PY
}

case "${1:-}" in
  --check-binary)
    [ "$#" -eq 2 ] || fail 'usage: run-codex-fingerprint-offline.sh --check-binary BINARY'
    check_binary "$2"
    exit 0
    ;;
  --selftest|--strict|--suite|--help)
    exec python3 "$(dirname "$0")/fp_probe.py" "$@"
    ;;
  --inside)
    [ "$#" -eq 5 ] || fail 'internal namespace invocation requires five arguments'
    [ "$(uname -s)" = Linux ] || fail 'binary execution requires Linux'
    original_namespace=$2
    original_user_namespace=$3
    binary=$4
    pattern=$5
    current_namespace=$(readlink /proc/self/ns/net)
    current_user_namespace=$(readlink /proc/self/ns/user)
    [ "$current_namespace" != "$original_namespace" ] || fail 'network namespace was not changed'
    [ "$current_user_namespace" != "$original_user_namespace" ] ||
      fail 'a private user namespace is required'
    ip link set lo up
    links=$(ip -o link show)
    [ "$(printf '%s\n' "$links" | wc -l)" -eq 1 ] || fail 'network namespace is not loopback-only'
    ipv4_routes=$(ip route show)
    ipv6_routes=$(ip -6 route show)
    [ -z "$ipv4_routes" ] || fail 'unexpected IPv4 route'
    [ -z "$ipv6_routes" ] || fail 'unexpected IPv6 route'
    check_binary "$binary"
    python3 - <<'PY'
import errno
import socket
for family, address in [(socket.AF_INET, ("198.51.100.1", 443)),
                        (socket.AF_INET6, ("2001:db8::1", 443))]:
    with socket.socket(family, socket.SOCK_STREAM) as sock:
        sock.settimeout(1)
        try:
            sock.connect(address)
        except OSError as error:
            if error.errno != errno.ENETUNREACH:
                raise
        else:
            raise SystemExit("network isolation self-check failed")
print("Isolation verified: loopback only; IPv4/IPv6 egress unreachable", flush=True)
PY
    # Package init and -test.list also execute only after dropping capabilities/environment.
    exec setpriv --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs \
      env -i PATH=/usr/bin:/bin HOME=/tmp GIN_MODE=test \
      sh -c '
        listed=$("$1" -test.list "$2") || exit "$?"
        printf "%s\n" "$listed"
        printf "%s\n" "$listed" | grep -Eq "^Test[^[:space:]]+$" || {
          echo "No matching tests; refusing an empty successful run" >&2
          exit 1
        }
        exec "$1" -test.run "$2" -test.count=1 -test.timeout=55s -test.v
      ' sh "$binary" "$pattern"
    ;;
esac

if [ "$#" -eq 1 ]; then
  exec python3 "$(dirname "$0")/fp_probe.py" "$1"
fi
[ "$#" -eq 2 ] || fail 'usage: run-codex-fingerprint-offline.sh [--strict] CAPTURE_JSONL | BINARY TEST_PATTERN'
[ "$(uname -s)" = Linux ] || fail 'binary execution requires Linux (or WSL2); captures can be checked on any OS'
[ "$(id -u)" -ne 0 ] || fail 'run as an unprivileged Linux user; this runner never uses sudo'
[ -n "$2" ] || fail 'TEST_PATTERN must not be empty'
command -v timeout >/dev/null || fail 'GNU timeout is required; nothing was installed'
command -v unshare >/dev/null || fail 'util-linux unshare is required; nothing was installed'
original_namespace=$(readlink /proc/self/ns/net)
original_user_namespace=$(readlink /proc/self/ns/user)
umask 077
stage=$(mktemp -d /tmp/sub2api-fingerprint-offline.XXXXXX)
trap 'rm -rf -- "$stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
cp -- "$1" "$stage/fingerprint.test"
cp -- "$0" "$stage/runner.sh"
chmod 700 "$stage/fingerprint.test"
status=0
# The outer wall-clock bound includes namespace setup, package init and test enumeration.
timeout --signal=TERM --kill-after=2s 58s unshare --user --map-root-user --net \
  sh "$stage/runner.sh" --inside "$original_namespace" "$original_user_namespace" "$stage/fingerprint.test" "$2" || status=$?
exit "$status"
