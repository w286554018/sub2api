#!/usr/bin/env bash
set -euo pipefail
BINARY=/mnt/f/sub2api/.task-cache/ranxi-20261013/bin/ranxi-mihomo-final-v2.test
printf '%s  %s\n' 487de020541aba290267ea6b52dee352e17696f9d18baac67144abedf484c093 "$BINARY" | sha256sum -c -
ROOT=/var/tmp/ranxi-mihomo-final-v3
mkdir -p "$ROOT/bin" "$ROOT/tmp"
cp "$BINARY" "$ROOT/bin/mihomo.test"
chmod +x "$ROOT/bin/mihomo.test"
export TMPDIR="$ROOT/tmp" TMP="$ROOT/tmp" TEMP="$ROOT/tmp"
export GOMAXPROCS=2 GOMEMLIMIT=1GiB GOGC=25
cd /mnt/f/sub2api/.task-cache/ranxi-20261013/source/backend/internal/mihomo
printf 'MIHOMO_BINARY_SHA256='
sha256sum "$ROOT/bin/mihomo.test"
printf 'MIHOMO_SOURCE=%s\n' "$PWD"
"$ROOT/bin/mihomo.test" -test.v -test.timeout=15m -test.count=1
printf 'MIHOMO_EXIT=0\n'
