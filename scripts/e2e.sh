#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/frontend"
export E2E_UI_PORT="${E2E_UI_PORT:-3100}"
export E2E_GW_PORT="${E2E_GW_PORT:-4100}"
export E2E_UP_PORT="${E2E_UP_PORT:-4110}"
# 企业集成使用 AES-GCM 保存秘密；默认测试密钥必须满足 32 字符限制，调用方仍可显式覆盖。
export E2E_MASTER_KEY="${E2E_MASTER_KEY:-sk-e2e-enterprise-test-master-key-32}"
export E2E_GATEWAY="http://127.0.0.1:$E2E_GW_PORT"
export E2E_UPSTREAM="http://127.0.0.1:$E2E_UP_PORT"
# 每轮运行使用专属证据目录，迟到的清理进程不能读取下一轮的 schema 文件。
export E2E_RUN_DIR="${E2E_RUN_DIR:-$ROOT/.e2e/run-$$-$RANDOM}"
# 调用方可指定独立构建和数据库；名称先校验，防止清理指向错误目标。
export E2E_BUILD_DIR="${E2E_BUILD_DIR:-.next-e2e}"
export E2E_DATABASE="${E2E_DATABASE:-xhub}"
if [[ ! "$E2E_DATABASE" =~ ^[a-z][a-z0-9_]*$ ]]; then
  echo "Invalid E2E_DATABASE" >&2
  exit 1
fi
# 完整浏览器套件包含 Responses 三轮续接场景，始终装载其本地离线模型夹具。
export E2E_RESPONSES_CONTINUATION=1
# HTTPS 身份与官方 Kodo SDK 协议夹具仅编入测试程序。
export E2E_ENTERPRISE_FIXTURE=1
export XHUB_GATEWAY_ORIGIN="$E2E_GATEWAY"
# 发布兼容模式与正式镜像一样不注入浏览器后台地址，实际验证 /gw 自举。
if [[ "${E2E_PORTABLE_CONSOLE:-0}" == 1 ]]; then
  unset NEXT_PUBLIC_BASE_URL
else
  export NEXT_PUBLIC_BASE_URL="$E2E_GATEWAY"
fi
LOCK="$ROOT/.e2e/browser.lock"
mkdir -p "$ROOT/.e2e"
source "$ROOT/scripts/browser-lock.sh"
acquire_browser_lock "$LOCK"
# cleanup 保留测试原始失败码；清理不可验证、删除或证据保存失败都会阻止成功。
# EXIT 调用，无参数；只操作当前运行目录和已校验的独立 schema，不删除占用端口的其他进程。
cleanup() {
  local exit_code=$?
  local cleanup_failed=0
  # Playwright may terminate its whole server process group before the gateway
  # shell runs its EXIT trap. Recover its private schema once ports are free.
  if python3 - "$E2E_GW_PORT" "$E2E_UP_PORT" <<'PYCLEAN'
import socket, sys
for value in sys.argv[1:]:
    with socket.socket() as probe:
        if probe.connect_ex(("127.0.0.1", int(value))) == 0:
            raise SystemExit(1)
PYCLEAN
  then
    rm -f "$E2E_RUN_DIR/c.yaml" || cleanup_failed=1
    if [[ -f "$E2E_RUN_DIR/schema.txt" ]]; then
      local schema
      schema="$(cat "$E2E_RUN_DIR/schema.txt")"
      if [[ "$schema" =~ ^e2e_[0-9]+_[0-9]+$ ]]; then
        docker exec xhub-postgres psql -X -U xhub -d "$E2E_DATABASE" -v ON_ERROR_STOP=1 -q -c "DROP SCHEMA IF EXISTS $schema CASCADE" >"$E2E_RUN_DIR/cleanup.log" 2>&1 || cleanup_failed=1
      else
        echo 'Invalid cleanup schema; refusing to delete it.' >&2
        cleanup_failed=1
      fi
    fi
  else
    echo 'E2E services still listening; schema cleanup is unverified.' >&2
    cleanup_failed=1
  fi
  # 在释放共享锁前保存调用方的专属证据，防止下一个排队运行清空或覆盖本轮结果。
  if [[ -n "${E2E_BROWSER_REPORT_DIR:-}" ]]; then
    mkdir -p "$E2E_BROWSER_REPORT_DIR" || cleanup_failed=1
    local artifact
    for artifact in results.json junit.xml scenario-coverage.json; do
      if [[ -f "$E2E_RUN_DIR/$artifact" ]]; then
        cp "$E2E_RUN_DIR/$artifact" "$E2E_BROWSER_REPORT_DIR/$artifact" || cleanup_failed=1
      fi
    done
  fi
  rmdir "$LOCK" || cleanup_failed=1
  # 浏览器断言报告与资源清理分开记录，不能把 results.json 的通过视为整个入口通过。
  printf '{"test_exit":%s,"cleanup_exit":%s}\n' "$exit_code" "$cleanup_failed" >"$E2E_RUN_DIR/cleanup-status.json" || cleanup_failed=1
  if [[ -n "${E2E_BROWSER_REPORT_DIR:-}" ]]; then
    cp "$E2E_RUN_DIR/cleanup-status.json" "$E2E_BROWSER_REPORT_DIR/cleanup-status.json" || cleanup_failed=1
  fi
  if [[ "$cleanup_failed" == 1 ]]; then
    echo "E2E cleanup failed; inspect $E2E_RUN_DIR before retrying." >&2
    if [[ "$exit_code" == 0 ]]; then exit_code=1; fi
  fi
  return "$exit_code"
}
trap cleanup EXIT
mkdir -p "$E2E_RUN_DIR"
rm -f "$E2E_RUN_DIR/results.json" "$E2E_RUN_DIR/junit.xml"
echo "Browser preflight: UI=$E2E_UI_PORT gateway=$E2E_GW_PORT upstream=$E2E_UP_PORT"
python3 - "$E2E_UI_PORT" "$E2E_GW_PORT" "$E2E_UP_PORT" <<'PY'
import socket, sys
ports = [int(value) for value in sys.argv[1:]]
if len(set(ports)) != 3: raise SystemExit("E2E ports must be distinct")
for port in ports:
    with socket.socket() as probe:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try: probe.bind(("127.0.0.1", port))
        except OSError: raise SystemExit(f"E2E port {port} is occupied; set E2E_UI_PORT / E2E_GW_PORT / E2E_UP_PORT")
PY
docker exec xhub-postgres psql -X -U xhub -d "$E2E_DATABASE" -qAt -c 'SELECT 1' >/dev/null
if [[ $# == 0 ]]; then export E2E_FULL_COVERAGE=1; fi
python3 - <<'PYCONFIG'
import json, os
from pathlib import Path
config = json.loads(Path("tsconfig.json").read_text())
config["include"] = [item for item in config["include"] if not item.startswith(".next")]
config["include"].append(os.environ["E2E_BUILD_DIR"] + "/types/**/*.ts")
config["exclude"] = [*config.get("exclude", []), ".next"]
Path("tsconfig.e2e.json").write_text(json.dumps(config, indent=2) + "\n")
PYCONFIG
echo "Building production console ($E2E_BUILD_DIR)..."
npm run build
echo "Building gateway and catalog sweep before the service readiness timeout..."
(cd "$ROOT" && go build -o "$E2E_RUN_DIR/xhub" ./cmd/gateway && go build -o "$E2E_RUN_DIR/livesweep" ./e2e/livesweep)
export E2E_PREBUILT=1
echo "Starting isolated gateway and console, then executing Playwright..."
npx playwright test "$@"
