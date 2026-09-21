#!/usr/bin/env bash
# deploy-api-dualread.sh v2 — 受控原子部署 backend/bin/api（F-R10-BUCKET 双读版）
#
# v2 修订（根 2026-09-13 21:0x 逐行审查后的强制修正）：
#   R1 绝不删除/覆盖备份或 staging：BACKUP_DIR / STAGE 必须**不存在**，存在即 pre-abort；
#      备份目录/文件名带唯一 TS，旧备份永不被触碰。
#   R2 切换前冻结**实际 EnvironmentFiles 指向的 env 文件**到同一备份目录，SHA 与源逐字节一致才允许
#      继续；只记录非敏感键（EXPORT_WRITE_BUCKET / EXPORT_READ_ORDER / EXPORT_BUCKET / RAW_BUCKET）
#      的存在性与取值，**绝不输出任何凭据内容**；并核对运行进程 environ 中未显式设置写桶（=默认 raw）。
#   R3 download_jobs 普查：pre/post 各写文件并校验命令 rc=0；post 必须 rc=0 且排序后与 pre 逐字一致，
#      否则回滚。口径使用应用运行角色（SET ROLE blockchain_app，最小权限），不用 owner 掩盖授权。
#   R4 readiness 用 python3 真正解析 JSON：拒绝重复键、拒绝缺字段/多余字段、四项必须全 ok、status 必须 ready。
#   R5 隔离负向校准点：backup/stage 已存在 → pre-abort；env 复制/SHA 失败 → pre-abort；
#      post 普查失败或变化 → 完整 rollback；伪/缺字段 ready JSON → 不通过；正常切换 → PASS。
#      CALIB_TAMPER_ENV_BACKUP=1 仅用于隔离校准，且被硬性互锁禁止与生产 unit 同时使用。
#
# 环境变量可覆盖（仅用于隔离校准；生产使用默认值）：
#   PROJECT API_BIN API_UNIT READY_URL STAGE BACKUP_DIR ENV_FILE EV EXPECT_SHA TARGET
#   READY_TRIES READY_SLEEP PG CENSUS_SQL CALIB_TAMPER_ENV_BACKUP EXPECT_WRITE_BUCKET
#
# v3 (2026-09-13, B4 export cutover): the R2/post-restart write-bucket gates were hard-coded to
# raw-only ("running process must NOT set EXPORT_WRITE_BUCKET"), which is correct for a raw round
# but makes the ADR-019 cutover impossible to deploy under a calibrated gate. v3 does NOT weaken
# the gate: it makes the expected mode an explicit, asserted input.
#   EXPECT_WRITE_BUCKET=raw    (default, identical to v2) env unset  + process environ unset
#   EXPECT_WRITE_BUCKET=export                           env=export + process environ=export
set -uo pipefail
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
TS="${TS:-$(date -u +%Y%m%dT%H%M%SZ)}"
EV="${EV:-}"
EXPECT_SHA="${EXPECT_SHA:-}"
EXPECT_WRITE_BUCKET="${EXPECT_WRITE_BUCKET:-raw}"
API_BIN="${API_BIN:-$PROJECT/backend/bin/api}"
API_UNIT="${API_UNIT:-blockchain-api.service}"
READY_URL="${READY_URL:-http://127.0.0.1:8080/health/ready}"
TARGET="${TARGET:-$EV/api.build}"
STAGE="${STAGE:-$API_BIN.stage-$TS}"
BACKUP_DIR="${BACKUP_DIR:-$PROJECT/backend/bin/.bak-dualread-$TS}"
ENV_FILE="${ENV_FILE:-}"
READY_TRIES="${READY_TRIES:-40}"
READY_SLEEP="${READY_SLEEP:-1}"
PG="${PG:-docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc}"
CENSUS_SQL="${CENSUS_SQL:-SET ROLE blockchain_app; select status||'='||count(*) from download_jobs group by status order by 1}"
CALIB_TAMPER_ENV_BACKUP="${CALIB_TAMPER_ENV_BACKUP:-0}"

if [ -z "$EV" ] || [ -z "$EXPECT_SHA" ]; then
  echo "usage: EV=<evidence-dir> EXPECT_SHA=<sha256> $0"; exit 2
fi
mkdir -p "$EV"
LOG="$EV/deploy-run.log"; SUM="$EV/deploy-summary.txt"
: > "$SUM"
log() { echo "$*" | tee -a "$LOG"; }
sum() { echo "$*" >> "$SUM"; }
pre_abort() { log "PRE-ABORT[$1]: $2"; sum "deploy_result=ABORT_PRE_SWITCH"; sum "abort_stage=$1"; sum "abort_reason=$2"; exit 2; }

# 校准互锁：篡改钩子只能用于隔离校准 unit，禁止对生产 unit 使用
if [ "$CALIB_TAMPER_ENV_BACKUP" = "1" ] && [ "$API_UNIT" = "blockchain-api.service" ]; then
  pre_abort "calib_interlock" "CALIB_TAMPER_ENV_BACKUP is calibration-only and must never be used with the production unit"
fi

sha_of()    { sha256sum "$1" 2>/dev/null | cut -d' ' -f1; }
pid_of()    { systemctl --user show "$API_UNIT" -p MainPID --value 2>/dev/null; }
active_of() { systemctl --user is-active "$API_UNIT" 2>/dev/null; }

ready_ok() {
  local code
  code=$(curl -s -m 10 -o "$EV/ready-last.json" -w '%{http_code}' "$READY_URL" 2>/dev/null)
  [ "$code" = "200" ] || return 1
  python3 - "$EV/ready-last.json" <<'PYEOF'
import json, sys
def nodup(pairs):
    seen = set()
    for k, v in pairs:
        if k in seen:
            raise ValueError("duplicate key: " + str(k))
        seen.add(k)
    return dict(pairs)
try:
    raw = open(sys.argv[1], "rb").read().decode("utf-8", "strict")
    d = json.loads(raw, object_pairs_hook=nodup)
except Exception as e:
    sys.stderr.write("ready-parse-error: %s\n" % e)
    sys.exit(1)
if not isinstance(d, dict):
    sys.exit(1)
if d.get("status") != "ready":
    sys.exit(1)
ch = d.get("checks")
if not isinstance(ch, dict):
    sys.exit(1)
if set(ch.keys()) != {"clickhouse", "minio", "nats", "postgres"}:
    sys.exit(1)
for k, v in ch.items():
    if v != "ok":
        sys.exit(1)
sys.exit(0)
PYEOF
}
wait_ready() {
  local i=0
  while [ "$i" -lt "$READY_TRIES" ]; do
    if ready_ok; then return 0; fi
    i=$((i+1)); sleep "$READY_SLEEP"
  done
  return 1
}
census() {
  local out="$1"
  $PG "$CENSUS_SQL" > "$out" 2> "$out.err"
  return $?
}
resolve_env_file() {
  [ -n "$ENV_FILE" ] && return 0
  local from_unit
  from_unit=$(systemctl --user show "$API_UNIT" -p EnvironmentFiles --value 2>/dev/null | awk '{print $1}')
  case "$from_unit" in ""|"[not-set]"|"n/a") ENV_FILE="$PROJECT/deploy/.env" ;; *) ENV_FILE="$from_unit" ;; esac
  return 0
}
env_key_value() { grep -m1 -E "^${1}=" "$ENV_FILE" 2>/dev/null | cut -d= -f2- | tr -d ' \r' || true; }

log "=== deploy-api-dualread v2 TS=$TS ==="
log "script_sha256=$(sha_of "$0")"
sum "ts_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
sum "script_sha256=$(sha_of "$0")"
sum "target=$TARGET"
sum "expect_sha=$EXPECT_SHA"
sum "api_bin=$API_BIN"
sum "api_unit=$API_UNIT"
sum "ready_url=$READY_URL"
sum "backup_dir=$BACKUP_DIR"
sum "stage_path=$STAGE"

# ---- R1 非破坏性前置：备份目录/ staging 绝不允许已存在 ----------------------
[ -e "$BACKUP_DIR" ] && pre_abort "backup_dir_exists" "backup dir already exists, refusing to reuse/overwrite: $BACKUP_DIR"
[ -e "$STAGE" ]      && pre_abort "stage_exists"      "staging path already exists, refusing to overwrite: $STAGE"
mkdir -p "$BACKUP_DIR" || pre_abort "backup_mkdir" "cannot create $BACKUP_DIR"
sum "backup_dir_created=fresh"

# ---- 0. 预状态 -------------------------------------------------------------
PRE_ACTIVE=$(active_of); PRE_PID=$(pid_of); PRE_BIN_SHA=$(sha_of "$API_BIN")
PRE_EXE_SHA=$(sha_of "/proc/$PRE_PID/exe")
PRE_STARTED=$(ps -o lstart= -p "$PRE_PID" 2>/dev/null | sed -e 's/^ *//')
sum "pre_active=$PRE_ACTIVE"; sum "pre_pid=$PRE_PID"; sum "pre_bin_sha=$PRE_BIN_SHA"
sum "pre_exe_sha=$PRE_EXE_SHA"; sum "pre_started=$PRE_STARTED"
log "pre: active=$PRE_ACTIVE pid=$PRE_PID bin_sha=$PRE_BIN_SHA exe_sha=$PRE_EXE_SHA"
[ "$PRE_ACTIVE" = "active" ] || pre_abort "pre_active" "service not active before deploy: $PRE_ACTIVE"
[ -n "$PRE_PID" ] && [ "$PRE_PID" != "0" ] || pre_abort "pre_pid" "no MainPID"
[ -f "$API_BIN" ] || pre_abort "pre_bin" "missing $API_BIN"
[ "$PRE_BIN_SHA" = "$PRE_EXE_SHA" ] || pre_abort "pre_identity" "on-disk bin sha != running exe sha ($PRE_BIN_SHA vs $PRE_EXE_SHA)"
ready_ok || pre_abort "pre_ready" "readiness not healthy before deploy (see $EV/ready-last.json)"
log "pre-ready: $(cat "$EV/ready-last.json")"
sum "pre_ready_json=$(cat "$EV/ready-last.json")"

# ---- R3 预普查（最小权限角色，rc 必须为 0） --------------------------------
census "$EV/census-pre.txt"; CENSUS_PRE_RC=$?
sum "census_pre_rc=$CENSUS_PRE_RC"
sum "census_pre_cmd=$CENSUS_SQL"
log "census-pre rc=$CENSUS_PRE_RC:"; sed -e 's/^/  /' "$EV/census-pre.txt" | tee -a "$LOG" >/dev/null
[ "$CENSUS_PRE_RC" -eq 0 ] || pre_abort "census_pre" "download_jobs census failed rc=$CENSUS_PRE_RC (see $EV/census-pre.txt.err)"
[ -s "$EV/census-pre.txt" ] || pre_abort "census_pre_empty" "download_jobs census pre output empty"

# ---- R2 冻结 env 文件（复制到同一备份目录，SHA 逐字节一致） ----------------
resolve_env_file
sum "env_file=$ENV_FILE"
[ -f "$ENV_FILE" ] || pre_abort "env_file_missing" "env file not found: $ENV_FILE"
ENV_SRC_SHA=$(sha_of "$ENV_FILE"); ENV_SRC_BYTES=$(stat -c%s "$ENV_FILE" 2>/dev/null || echo 0)
cp -p "$ENV_FILE" "$BACKUP_DIR/deploy.env" || pre_abort "env_copy" "cannot copy env file to backup dir"
chmod 600 "$BACKUP_DIR/deploy.env" 2>/dev/null || true
if [ "$CALIB_TAMPER_ENV_BACKUP" = "1" ]; then
  printf '\n# calibration tamper marker\n' >> "$BACKUP_DIR/deploy.env"
  log "CALIB: env backup tampered on purpose (isolated calibration only)"
  sum "calib_env_tamper=1"
fi
ENV_BAK_SHA=$(sha_of "$BACKUP_DIR/deploy.env")
sum "env_src_sha=$ENV_SRC_SHA"; sum "env_src_bytes=$ENV_SRC_BYTES"; sum "env_backup_sha=$ENV_BAK_SHA"
[ -n "$ENV_SRC_SHA" ] || pre_abort "env_sha_empty" "env source sha empty"
[ "$ENV_BAK_SHA" = "$ENV_SRC_SHA" ] || pre_abort "env_sha" "env backup sha mismatch: got $ENV_BAK_SHA want $ENV_SRC_SHA"
WB=$(env_key_value EXPORT_WRITE_BUCKET); RO=$(env_key_value EXPORT_READ_ORDER)
EB=$(env_key_value EXPORT_BUCKET);    RB=$(env_key_value RAW_BUCKET)
sum "env_export_write_bucket_explicit=${WB:-UNSET}"
sum "env_export_read_order_explicit=${RO:-UNSET}"
sum "env_export_bucket_explicit=${EB:-UNSET}"
sum "env_raw_bucket_explicit=${RB:-UNSET}"
sum "effective_export_write_bucket=${WB:-raw(default)}"
case "$EXPECT_WRITE_BUCKET" in
  raw)
    [ "${WB:-raw}" = "raw" ] || pre_abort "env_write_bucket" "env EXPORT_WRITE_BUCKET=$WB but this round expects raw"
    ;;
  export)
    [ "${WB:-}" = "export" ] || pre_abort "env_write_bucket" "env EXPORT_WRITE_BUCKET=${WB:-UNSET} but this round expects export"
    ;;
  *)
    pre_abort "expect_write_bucket_invalid" "EXPECT_WRITE_BUCKET must be raw or export (got $EXPECT_WRITE_BUCKET)"
    ;;
esac
sum "expect_write_bucket=$EXPECT_WRITE_BUCKET"
sum "effective_export_read_order=${RO:-export,raw(default)}"
log "env frozen: src_sha=$ENV_SRC_SHA bytes=$ENV_SRC_BYTES backup=$BACKUP_DIR/deploy.env"
log "env EXPORT_WRITE_BUCKET=${WB:-UNSET(default raw)} EXPORT_READ_ORDER=${RO:-UNSET(default export,raw)}"

# ---- 1. 产物 SHA 门 --------------------------------------------------------
[ -f "$TARGET" ] || pre_abort "target_missing" "missing $TARGET"
GOT=$(sha_of "$TARGET")
[ "$GOT" = "$EXPECT_SHA" ] || pre_abort "target_sha" "artifact sha mismatch: got $GOT want $EXPECT_SHA"
log "gate1-artifact OK: $GOT"

# ---- 2. staging（路径必须新建，使用 cp -p 不改写既有文件） ----------------
cp -p "$TARGET" "$STAGE" || pre_abort "stage_copy" "cp failed"
chmod 755 "$STAGE" || pre_abort "stage_chmod" "chmod failed"
STAGE_SHA=$(sha_of "$STAGE")
[ "$STAGE_SHA" = "$EXPECT_SHA" ] || pre_abort "stage_sha" "staged sha mismatch: got $STAGE_SHA"
log "gate2-stage OK: $STAGE_SHA"

# ---- 3. 旧字节备份（目标文件必须新建） -------------------------------------
cp -p "$API_BIN" "$BACKUP_DIR/api" || pre_abort "backup_copy" "cp failed"
BACKUP_SHA=$(sha_of "$BACKUP_DIR/api")
[ "$BACKUP_SHA" = "$PRE_BIN_SHA" ] || pre_abort "backup_sha" "backup sha mismatch: got $BACKUP_SHA want $PRE_BIN_SHA"
sha256sum "$BACKUP_DIR/api" "$BACKUP_DIR/deploy.env" > "$BACKUP_DIR/SHA256SUMS.txt"
log "gate3-backup OK: $BACKUP_SHA"
sum "backup_sha=$BACKUP_SHA"

# ---- 4. 原子切换 -----------------------------------------------------------
mv -f "$STAGE" "$API_BIN" || { log "SWITCH FAILED"; sum "deploy_result=FAIL"; sum "fail_stage=switch"; exit 3; }
SWITCHED_SHA=$(sha_of "$API_BIN"); sum "switched_sha=$SWITCHED_SHA"
log "switched: sha=$SWITCHED_SHA (prev=$PRE_BIN_SHA)"

rollback_incomplete() {
  log "ROLLBACK INCOMPLETE — MANUAL INTERVENTION REQUIRED: $1"
  sum "deploy_result=NOGO_ROLLBACK_INCOMPLETE"; sum "rollback_reason=$1"; exit 6
}
rollback() {
  local reason="$1"
  log "ROLLBACK TRIGGERED: $reason"; sum "rollback_triggered=$reason"
  local RB_TMP
  RB_TMP=$(mktemp "$API_BIN.tmp-rollback-XXXXXX") || rollback_incomplete "mktemp failed"
  cp -p "$BACKUP_DIR/api" "$RB_TMP" || rollback_incomplete "restore copy failed"
  chmod 755 "$RB_TMP" || rollback_incomplete "restore chmod failed"
  mv -f "$RB_TMP" "$API_BIN" || rollback_incomplete "restore move failed"
  RB_SHA=$(sha_of "$API_BIN"); sum "rollback_bin_sha=$RB_SHA"
  [ "$RB_SHA" = "$PRE_BIN_SHA" ] || rollback_incomplete "restored bytes sha=$RB_SHA want=$PRE_BIN_SHA"
  systemctl --user restart "$API_UNIT"; local rc_restart=$?
  sum "rollback_restart_rc=$rc_restart"
  local i=0
  while [ "$i" -lt 30 ]; do [ "$(active_of)" = "active" ] && break; i=$((i+1)); sleep 1; done
  RB_ACTIVE=$(active_of)
  wait_ready; local rc_ready=$?
  RB_PID=$(pid_of); RB_EXE_SHA=$(sha_of "/proc/$RB_PID/exe")
  sum "rollback_active=$RB_ACTIVE"; sum "rollback_ready_rc=$rc_ready"
  sum "rollback_pid=$RB_PID"; sum "rollback_exe_sha=$RB_EXE_SHA"
  log "rollback: active=$RB_ACTIVE ready_rc=$rc_ready pid=$RB_PID exe_sha=$RB_EXE_SHA"
  if [ "$rc_restart" -eq 0 ] && [ "$RB_ACTIVE" = "active" ] && [ "$rc_ready" -eq 0 ] && [ "$RB_EXE_SHA" = "$PRE_BIN_SHA" ]; then
    log "ROLLBACK COMPLETE — service restored to pre-deploy bytes and healthy"
    sum "deploy_result=FAIL_ROLLED_BACK"; exit 5
  fi
  rollback_incomplete "restart_rc=$rc_restart active=$RB_ACTIVE ready_rc=$rc_ready exe_sha=$RB_EXE_SHA want=$PRE_BIN_SHA"
}

[ "$SWITCHED_SHA" = "$EXPECT_SHA" ] || rollback "post-switch bin sha mismatch"

# ---- 5. 重启 + 运行身份验证 ------------------------------------------------
systemctl --user restart "$API_UNIT"; RC_RESTART=$?
sum "restart_rc=$RC_RESTART"; log "restart_rc=$RC_RESTART"
[ "$RC_RESTART" -eq 0 ] || rollback "systemctl restart failed rc=$RC_RESTART"

i=0
while [ "$i" -lt 30 ]; do [ "$(active_of)" = "active" ] && break; i=$((i+1)); sleep 1; done
POST_ACTIVE=$(active_of); sum "post_active=$POST_ACTIVE"; log "post_active=$POST_ACTIVE"
[ "$POST_ACTIVE" = "active" ] || rollback "service not active after restart: $POST_ACTIVE"

wait_ready || rollback "readiness not ok after restart (see $EV/ready-last.json)"
log "post-ready: $(cat "$EV/ready-last.json")"
sum "post_ready_json=$(cat "$EV/ready-last.json")"

POST_PID=$(pid_of); POST_EXE_SHA=$(sha_of "/proc/$POST_PID/exe")
sum "post_pid=$POST_PID"; sum "post_exe_sha=$POST_EXE_SHA"
log "post: pid=$POST_PID exe_sha=$POST_EXE_SHA"
[ -n "$POST_PID" ] && [ "$POST_PID" != "0" ] || rollback "no MainPID after restart"
[ "$POST_PID" != "$PRE_PID" ] || rollback "MainPID unchanged after restart ($POST_PID)"
[ "$POST_EXE_SHA" = "$EXPECT_SHA" ] || rollback "running exe sha=$POST_EXE_SHA want=$EXPECT_SHA"

# 运行进程 environ 中不得显式设置写桶（0 = 未设置 = 走默认 raw）；只记录计数，不打印任何值
if [ -r "/proc/$POST_PID/environ" ]; then
  PROC_WB=$(tr '\0' '\n' < "/proc/$POST_PID/environ" 2>/dev/null | grep -c '^EXPORT_WRITE_BUCKET=' || true)
  PROC_RO=$(tr '\0' '\n' < "/proc/$POST_PID/environ" 2>/dev/null | grep -c '^EXPORT_READ_ORDER=' || true)
else
  PROC_WB=UNREADABLE; PROC_RO=UNREADABLE
fi
sum "proc_export_write_bucket_set=$PROC_WB"
sum "proc_export_read_order_set=$PROC_RO"
log "proc environ: EXPORT_WRITE_BUCKET_set=$PROC_WB EXPORT_READ_ORDER_set=$PROC_RO"
case "$EXPECT_WRITE_BUCKET" in
  raw)
    [ "$PROC_WB" = "0" ] || rollback "running process explicitly sets EXPORT_WRITE_BUCKET (expected default raw for this round): count=$PROC_WB"
    ;;
  export)
    [ "$PROC_WB" = "1" ] || rollback "running process does not set EXPORT_WRITE_BUCKET (expected export cutover): count=$PROC_WB"
    PROC_WB_VAL=$(tr '\0' '\n' < "/proc/$POST_PID/environ" 2>/dev/null | sed -n 's/^EXPORT_WRITE_BUCKET=//p')
    sum "proc_export_write_bucket_value=$PROC_WB_VAL"
    [ "$PROC_WB_VAL" = "export" ] || rollback "running process EXPORT_WRITE_BUCKET=$PROC_WB_VAL want=export"
    ;;
  *)
    rollback "EXPECT_WRITE_BUCKET must be raw or export (got $EXPECT_WRITE_BUCKET)"
    ;;
esac

# ---- R3 后普查：rc=0 且排序后与 pre 逐字一致 -------------------------------
census "$EV/census-post.txt"; CENSUS_POST_RC=$?
sum "census_post_rc=$CENSUS_POST_RC"
[ "$CENSUS_POST_RC" -eq 0 ] || rollback "post deploy_jobs census failed rc=$CENSUS_POST_RC (see $EV/census-post.txt.err)"
if ! diff -q <(sort "$EV/census-pre.txt") <(sort "$EV/census-post.txt") >/dev/null 2>&1; then
  sum "census_diff=changed"
  log "census changed:"; diff <(sort "$EV/census-pre.txt") <(sort "$EV/census-post.txt") | tee -a "$LOG" >/dev/null
  rollback "download_jobs census changed across the deploy window"
fi
sum "census_diff=identical"

log "DEPLOY PASS: sha=$POST_EXE_SHA pid=$POST_PID (prev pid=$PRE_PID sha=$PRE_BIN_SHA)"
sum "deploy_result=PASS"
exit 0
