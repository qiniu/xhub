#!/usr/bin/env bash
# Runs the end-to-end regression suite against a real gateway.
#
#   ./scripts/regression.sh              # deterministic, uses the fake provider
#   ./scripts/regression.sh --live        # also calls the real vendors (costs money)
#   ./scripts/regression.sh -v            # show test status without gateway logs
# Default output is a short result; full diagnostics are saved under .e2e/runs/.
#
# The suite needs PostgreSQL on 5433 (docker compose up -d postgres). It creates
# a throwaway schema per test and drops it afterwards, so it is safe to run
# against a database a gateway is also using.
#
# Every test runs against its own gateway on a random port with a fake provider
# behind it. Nothing is faked at the gateway boundary: HTTP routes, the identity
# store, spend recording, budgets and guardrails are all the real ones.
#
# Live mode also needs at least one vendor, configured without storing keys in the repository:
#
#   XHUB_REGRESSION_<ID>_KEY, _BASE, _MODELS

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LIVE=0
VERBOSE=0
PATTERN=""
for arg in "$@"; do
  case "$arg" in
    --live) LIVE=1 ;;
    -v|--verbose) VERBOSE=1 ;;
    --help|-h)
      sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) PATTERN="$arg" ;;
  esac
done

# 必需数据库不可达必须失败；入口预检快速报错，Go 夹具也独立强制连接和清理。
if command -v psql >/dev/null 2>&1; then
  if [[ -n "${XHUB_TEST_DATABASE_URL:-}" ]]; then
    if ! psql "$XHUB_TEST_DATABASE_URL" -X -qAt -c 'select 1' >/dev/null 2>&1; then
      echo "the configured XHUB_TEST_DATABASE_URL is not reachable." >&2
      exit 1
    fi
  elif ! psql 'postgres://xhub:xhub_dev_password@127.0.0.1:5433/xhub?sslmode=disable' -X -qAt -c 'select 1' >/dev/null 2>&1; then
    echo "no PostgreSQL reachable at the regression default (127.0.0.1:5433)." >&2
    echo "start it with: docker compose up -d postgres" >&2
    exit 1
  fi
elif [[ -z "${XHUB_TEST_DATABASE_URL:-}" ]]; then
  if ! docker exec xhub-postgres psql -U xhub -d xhub -c 'select 1' >/dev/null 2>&1; then
    echo "no PostgreSQL reachable." >&2
    echo "start it with: docker compose up -d postgres" >&2
    exit 1
  fi
fi

ARGS=(-count=1 -timeout="${XHUB_REGRESSION_TIMEOUT:-1800s}")
# 指定报告文件时对全部生产模块插桩，保留后台覆盖证据供 internal-coverage.py 审计。
if [[ -n "${XHUB_REGRESSION_COVERPROFILE:-}" ]]; then
  mkdir -p "$(dirname "$XHUB_REGRESSION_COVERPROFILE")"
  ARGS+=(-coverpkg=./internal/... -coverprofile="$XHUB_REGRESSION_COVERPROFILE")
fi
export XHUB_REGRESSION_STRICT=1
if [[ "$VERBOSE" == 1 ]]; then
  ARGS+=(-v)
fi
if [[ -n "$PATTERN" ]]; then
  ARGS+=(-run "$PATTERN")
fi

if [[ "$LIVE" == 1 ]]; then
  export XHUB_REGRESSION_LIVE=1
  echo "running with live vendor calls: this spends real money"
else
  unset XHUB_REGRESSION_LIVE
  echo "running the deterministic suite (fake provider); --live to call real vendors"
fi

# 双流完整落盘；每轮独立目录防止覆盖证据，创建失败必须中止。
mkdir -p "$ROOT/.e2e/runs"
LOG_DIR="$(mktemp -d "$ROOT/.e2e/runs/regression-XXXXXXXX")"
LOG_FILE="$LOG_DIR/regression.log"
echo "full regression log: $LOG_FILE"
# -json 提供真实执行事件；不能靠 -list 或日志里的 PASS 判断子用例是否执行。
CHECK_ARGS=(--log "$LOG_FILE" --summary "$LOG_DIR/summary.json" --pattern "$PATTERN")
if [[ "$VERBOSE" == 1 ]]; then CHECK_ARGS+=(--verbose); fi
set +e
go test ./cmd/regression/ "${ARGS[@]}" -json 2>&1 | tee "$LOG_DIR/events.jsonl" | python3 "$ROOT/scripts/check-regression-results.py" "${CHECK_ARGS[@]}"
STAGE_STATUS=("${PIPESTATUS[@]}")
set -e
# 优先保留 Go 的失败码；Go 成功也必须检查证据写入及用例匹配门禁。
STATUS=0
for stage in "${STAGE_STATUS[@]}"; do
  if [[ "$stage" != 0 ]]; then STATUS="$stage"; break; fi
done
if [[ "$STATUS" == 0 ]]; then
  echo "backend regression passed"
else
  echo "backend regression failed (exit $STATUS); see $LOG_FILE" >&2
  exit "$STATUS"
fi
