#!/usr/bin/env bash
set -euo pipefail
BINARY=${1:?必须显式传入F盘最终仓储测试二进制路径}
SOURCE=/mnt/f/sub2api/.task-cache/ranxi-20261013/source
EXPECTED_SHA=969cb234b79de0d67b27b2242cd71cca124deca3da7011b239491f5b7c091d5d
[[ "$BINARY" == /mnt/f/sub2api/.task-cache/ranxi-20261013/bin/* ]] || exit 2
printf '%s  %s\n' "$EXPECTED_SHA" "$BINARY" | sha256sum -c -
# 复用本任务专用 daemon 的数据，避免重复下载；仅运行 F 盘已编译的测试。
TASK_ROOT=/var/tmp/ranxi-isolated-docker-v8
BRIDGE=ranxi8
mkdir -p "$TASK_ROOT"
export DOCKER_HOST="unix://$TASK_ROOT/docker.sock"
if [ -e "$TASK_ROOT/dockerd.pid" ] || ip link show "$BRIDGE" >/dev/null 2>&1; then
  echo 'Existing isolated daemon or bridge state needs inspection'; exit 3
fi
ip link add "$BRIDGE" type bridge
ip addr add 192.168.248.1/24 dev "$BRIDGE"
ip link set "$BRIDGE" up
/usr/bin/dockerd --host="$DOCKER_HOST" --data-root="$TASK_ROOT/data" --exec-root="$TASK_ROOT/exec" --pidfile="$TASK_ROOT/dockerd.pid" --bridge="$BRIDGE" --fixed-cidr=192.168.248.0/24 --iptables=false --ip-masq=false --userland-proxy=true --storage-driver=vfs >"$TASK_ROOT/daemon-final-v9.log" 2>&1 &
daemon_pid=$!
trap 'kill "$daemon_pid" 2>/dev/null || true; wait "$daemon_pid" 2>/dev/null || true; ip link delete "$BRIDGE" 2>/dev/null || true' EXIT
printf 'ISOLATED_DOCKER_PID=%s\n' "$daemon_pid"
ready=0
for attempt in $(seq 1 45); do
  if ! kill -0 "$daemon_pid" 2>/dev/null; then
    tail -50 "$TASK_ROOT/daemon-final-v9.log"; exit 4
  fi
  if timeout -s KILL 3 /usr/bin/docker info --format '{{.ServerVersion}}' >"$TASK_ROOT/version-final-v9.log" 2>/dev/null; then
    ready=1; break
  fi
  sleep 1
done
if [ "$ready" -ne 1 ]; then
  tail -50 "$TASK_ROOT/daemon-final-v9.log"; exit 5
fi
printf 'ISOLATED_DOCKER_VERSION='; cat "$TASK_ROOT/version-final-v9.log"
ROOT=/var/tmp/ranxi-repository-integration-final-v9
mkdir -p "$ROOT/bin" "$ROOT/tmp"
cp "$BINARY" "$ROOT/bin/repository.test"
chmod +x "$ROOT/bin/repository.test"
printf 'INTEGRATION_BINARY_SHA256='
sha256sum "$ROOT/bin/repository.test"
export TMPDIR="$ROOT/tmp" TMP="$ROOT/tmp" TEMP="$ROOT/tmp"
export GOMAXPROCS=2 GOMEMLIMIT=1GiB GOGC=25 CI=1 TESTCONTAINERS_RYUK_DISABLED=false
cd "$SOURCE/backend/internal/repository"
printf 'INTEGRATION_SOURCE=%s\nINTEGRATION_START\n' "$PWD"
"$ROOT/bin/repository.test" -test.v -test.timeout=30m -test.count=1
printf 'INTEGRATION_EXIT=0\n'
