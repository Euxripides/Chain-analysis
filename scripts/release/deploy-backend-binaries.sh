#!/usr/bin/env bash
# deploy-backend-binaries.sh — 受控原子部署 backend/bin/{api,worker}（导出性能整改轮）
#
# 沿用 scripts/release/deploy-api-write-mode-v3.sh 的既有规范：
#   R1 备份/staging 必须**不存在**，存在即 pre-abort；备份名带唯一 TS，旧备份永不被触碰。
#   R2 切换前记录将要被替换的二进制的 SHA256，切换后逐个校验目标文件与 staging 一致。
#   R3 readiness 用 python3 真正解析 JSON，确认 status=ready；失败即回滚。
#   R4 回滚是完整的：两个二进制一起回到备份版本并重启，不留半新半旧。
#
# 环境变量可覆盖（用于隔离校准；生产用默认）：
#   PROJECT SKIP_BUILD BACKUP_DIR STAGE_DIR API_UNIT WORKER_UNIT READY_URL LIVE_URL
#   READY_TRIES READY_SLEEP CALIB_SKIP_RESTART
set -uo pipefail
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
TS="${TS:-$(date -u +%Y%m%dT%H%M%SZ)}"
SKIP_BUILD="${SKIP_BUILD:-0}"
CALIB_SKIP_RESTART="${CALIB_SKIP_RESTART:-0}"

BIN_DIR="$PROJECT/backend/bin"
API_BIN="$BIN_DIR/api"
WORKER_BIN="$BIN_DIR/worker"
STAGE_DIR="${STAGE_DIR:-$BIN_DIR/.stage-$TS}"
BACKUP_DIR="${BACKUP_DIR:-$BIN_DIR/.bak-exportperf-$TS}"
API_UNIT="${API_UNIT:-blockchain-api.service}"
WORKER_UNIT="${WORKER_UNIT:-blockchain-worker.service}"
READY_URL="${READY_URL:-http://127.0.0.1:8080/health/ready}"
LIVE_URL="${LIVE_URL:-http://127.0.0.1:8080/health/live}"
READY_TRIES="${READY_TRIES:-30}"
READY_SLEEP="${READY_SLEEP:-2}"
export PATH="/usr/local/go/bin:$PATH"

fail() { echo "ABORT: $*" >&2; exit 1; }
note() { echo "[deploy] $*"; }

# ---- R1 前置：备份与 staging 必须不存在 ----
[ -e "$BACKUP_DIR" ] && fail "backup dir already exists: $BACKUP_DIR"
[ -e "$STAGE_DIR" ] && fail "stage dir already exists: $STAGE_DIR"
mkdir -p "$STAGE_DIR" || fail "mkdir stage"

# ---- 构建到 staging（绝不直接覆盖线上文件）----
if [ "$SKIP_BUILD" != "1" ]; then
  command -v go >/dev/null 2>&1 || fail "go not found in PATH"
  note "building api + worker into $STAGE_DIR"
  ( cd "$PROJECT/backend" && go build -o "$STAGE_DIR/api" ./cmd/api ) || fail "build api"
  ( cd "$PROJECT/backend" && go build -o "$STAGE_DIR/worker" ./cmd/worker ) || fail "build worker"
else
  note "SKIP_BUILD=1: using existing stage binaries"
fi
[ -x "$STAGE_DIR/api" ] || fail "stage api missing"
[ -x "$STAGE_DIR/worker" ] || fail "stage worker missing"

# ---- R1/R2 前置：备份在跑的二进制 ----
mkdir -p "$BACKUP_DIR" || fail "mkdir backup"
cp -p "$API_BIN" "$BACKUP_DIR/api" || fail "backup api"
cp -p "$WORKER_BIN" "$BACKUP_DIR/worker" || fail "backup worker"
SHA_API_OLD="$(sha256sum "$BACKUP_DIR/api" | cut -d' ' -f1)"
SHA_WORKER_OLD="$(sha256sum "$BACKUP_DIR/worker" | cut -d' ' -f1)"
SHA_API_NEW="$(sha256sum "$STAGE_DIR/api" | cut -d' ' -f1)"
SHA_WORKER_NEW="$(sha256sum "$STAGE_DIR/worker" | cut -d' ' -f1)"
note "backup: $BACKUP_DIR"
note "  api    $SHA_API_OLD -> $SHA_API_NEW"
note "  worker $SHA_WORKER_OLD -> $SHA_WORKER_NEW"

rollback() {
  echo "ROLLBACK: restoring binaries from $BACKUP_DIR" >&2
  cp -p "$BACKUP_DIR/api" "$API_BIN" || echo "rollback api copy FAILED" >&2
  cp -p "$BACKUP_DIR/worker" "$WORKER_BIN" || echo "rollback worker copy FAILED" >&2
  systemctl --user restart "$API_UNIT" "$WORKER_UNIT" >/dev/null 2>&1 || true
  sleep 3
  echo "ROLLBACK done (backup kept at $BACKUP_DIR)" >&2
  exit 1
}

# ---- 切换（同一目录内 mv，原子替换）----
mv -f "$STAGE_DIR/api" "$API_BIN" || rollback
mv -f "$STAGE_DIR/worker" "$WORKER_BIN" || rollback
[ "$(sha256sum "$API_BIN" | cut -d' ' -f1)" = "$SHA_API_NEW" ] || rollback
[ "$(sha256sum "$WORKER_BIN" | cut -d' ' -f1)" = "$SHA_WORKER_NEW" ] || rollback
note "binaries swapped, sha verified"

if [ "$CALIB_SKIP_RESTART" = "1" ]; then
  note "CALIB_SKIP_RESTART=1: not restarting units"
  exit 0
fi

# ---- 重启 ----
systemctl --user restart "$WORKER_UNIT" || rollback
systemctl --user restart "$API_UNIT" || rollback
note "units restarted"

# ---- R3 readiness：解析 JSON，四项全 ok 且 status=ready ----
ready_ok=0
for i in $(seq 1 "$READY_TRIES"); do
  body="$(curl -s -m 5 "$READY_URL" 2>/dev/null || true)"
  if [ -n "$body" ] && python3 - "$body" <<'PY' >/dev/null 2>&1
import json, sys
raw = sys.argv[1]
try:
    data = json.loads(raw)
except Exception:
    sys.exit(1)
if data.get("status") != "ready":
    sys.exit(1)
checks = data.get("checks")
# 实测形态：{"checks":{"clickhouse":"ok","minio":"ok","nats":"ok","postgres":"ok"},"status":"ready"}
if not isinstance(checks, dict) or not checks:
    sys.exit(1)
for name, value in checks.items():
    if value != "ok":
        sys.exit(1)
PY
  then
    ready_ok=1
    break
  fi
  sleep "$READY_SLEEP"
done
[ "$ready_ok" = "1" ] || { echo "readiness failed; last body: $body" >&2; rollback; }

live_code="$(curl -s -m 5 -o /dev/null -w '%{http_code}' "$LIVE_URL" 2>/dev/null || true)"
[ "$live_code" = "200" ] || { echo "liveness HTTP $live_code" >&2; rollback; }

note "PASS: api+worker deployed, readiness=ready, liveness=200"
note "rollback material: $BACKUP_DIR"
