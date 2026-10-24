#!/usr/bin/env bash
# Full acceptance: browser clicks, real suppliers and deterministic fault cases.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ "${E2E_LIVE:-0}" != 1 ]]; then
  export E2E_ACCEPTANCE_DIR="$ROOT/.e2e/runs/$(date -u +%Y%m%dT%H%M%SZ)-$$"
  mkdir -p "$E2E_ACCEPTANCE_DIR"
  ln -sfn "$E2E_ACCEPTANCE_DIR" "$ROOT/.e2e/latest"
  preflight_status=0
  python3 "$ROOT/scripts/with-live-vendors.py" bash "$0" "$@" 2>&1 | tee "$E2E_ACCEPTANCE_DIR/preflight.log" || preflight_status=$?
  if [[ ! -f "$E2E_ACCEPTANCE_DIR/summary.json" ]]; then
    export E2E_RUN_DIR="$E2E_ACCEPTANCE_DIR/browser"
    export E2E_BROWSER_STATUS=1 E2E_BACKEND_STATUS=1
    python3 "$ROOT/scripts/e2e-summary.py" || true
  fi
  exit "$preflight_status"
fi
mkdir -p "$ROOT/.e2e"
LOCK="$ROOT/.e2e/acceptance.lock"
if ! mkdir "$LOCK" 2>/dev/null; then
  echo "Another full regression is running ($LOCK)." >&2
  exit 1
fi
REDIS_NAME="xhub-e2e-redis-$$"
export E2E_ACCEPTANCE_DIR="${E2E_ACCEPTANCE_DIR:-$ROOT/.e2e/runs/$(date -u +%Y%m%dT%H%M%SZ)-$$}"
export E2E_EVIDENCE_DIR="$E2E_ACCEPTANCE_DIR/evidence"
export E2E_RUN_DIR="$E2E_ACCEPTANCE_DIR/browser"
export E2E_REVISION="$(git -C "$ROOT" rev-parse HEAD)"
export E2E_BROWSER_STATUS=1 E2E_BACKEND_STATUS=1
export E2E_FULL_COVERAGE=0
if [[ $# == 0 ]]; then export E2E_FULL_COVERAGE=1; fi
mkdir -p "$E2E_ACCEPTANCE_DIR/browser" "$E2E_EVIDENCE_DIR" "$E2E_RUN_DIR"
ln -sfn "$E2E_ACCEPTANCE_DIR" "$ROOT/.e2e/latest"
rm -f "$E2E_RUN_DIR/results.json" "$E2E_RUN_DIR/junit.xml"
# cleanup 由 EXIT 调用，无参数；先清理专属 Redis/锁，再生成最终报告。
# 清理失败不得被阶段成功掩盖；已有测试/启动失败保留原退出码，日志留供定位。
cleanup() {
  local code=$? cleanup_status=0
  docker rm -f "$REDIS_NAME" >"$E2E_ACCEPTANCE_DIR/cleanup.log" 2>&1 || cleanup_status=1
  rmdir "$LOCK" || cleanup_status=1
  export E2E_CLEANUP_STATUS="$cleanup_status"
  if [[ "$cleanup_status" != 0 && "$code" == 0 ]]; then code=1; fi
  E2E_RUN_DIR="$E2E_ACCEPTANCE_DIR/browser" python3 "$ROOT/scripts/e2e-summary.py" || { if [[ "$code" == 0 ]]; then code=1; fi; }
  echo "Regression finished: exit=$code; report: $E2E_ACCEPTANCE_DIR/report.html"
  return "$code"
}
trap cleanup EXIT
echo "Run directory: $E2E_ACCEPTANCE_DIR"
echo "[1/4] Checking environment and starting isolated Redis; suppliers: $E2E_LIVE_VENDORS"
# Dedicated Redis with no persistent volume and a random local host port.
docker run --rm -d --name "$REDIS_NAME" -p 127.0.0.1::6379 redis:7-alpine >/dev/null
REDIS_PORT="$(docker port "$REDIS_NAME" 6379/tcp | cut -d: -f2)"
export XHUB_REGRESSION_REDIS_URL="redis://127.0.0.1:$REDIS_PORT/0"
for attempt in {1..30}; do
  if docker exec "$REDIS_NAME" redis-cli ping >/dev/null 2>&1; then break; fi
  sleep 0.2
done
status=0
# run_stage 接收阶段名、日志路径及命令，返回命令/tee 状态；心跳覆盖编译和长请求。
# 阶段结束同时回收心跳与 sleep 子进程，避免日志管道保持打开；不改写产品数据。
run_stage() {
  local label=$1 logfile=$2
  shift 2
  (set -o pipefail; "$@" 2>&1 | tee "$logfile") &
  local stage_pid=$!
  (timer_pid=''
  # TERM 必须同时停止定时子进程，否则 sleep 仍持有日志管道，入口结束后迟迟没有 EOF。
  trap 'if [[ -n "$timer_pid" ]]; then kill "$timer_pid" 2>/dev/null || true; wait "$timer_pid" 2>/dev/null || true; fi; exit 0' TERM INT
  while kill -0 "$stage_pid" 2>/dev/null; do
    sleep 30 &
    timer_pid=$!
    wait "$timer_pid" || true
    timer_pid=''
    kill -0 "$stage_pid" 2>/dev/null && echo "[$(date +%H:%M:%S)] $label still running; log: $logfile"
  done) &
  local heartbeat_pid=$! stage_status=0
  wait "$stage_pid" || stage_status=$?
  kill "$heartbeat_pid" 2>/dev/null || true
  wait "$heartbeat_pid" 2>/dev/null || true
  return "$stage_status"
}
echo "[2/4] Building console and running browser clicks (live output)"
if run_stage Browser "$E2E_ACCEPTANCE_DIR/browser.log" bash "$ROOT/scripts/e2e.sh" "$@"; then
  browser_status=0
else
  browser_status=1
  status=1
fi
export E2E_RUN_DIR="$E2E_ACCEPTANCE_DIR/browser"
echo "[3/4] Running backend business regression and real suppliers"
if run_stage Backend "$E2E_ACCEPTANCE_DIR/backend.log" bash "$ROOT/scripts/regression.sh" --live -v; then
  backend_status=0
else
  backend_status=1
  status=1
fi
echo "[4/4] Summarizing results: browser=$browser_status backend=$backend_status"
export E2E_BROWSER_STATUS="$browser_status" E2E_BACKEND_STATUS="$backend_status"
exit "$status"
