#!/usr/bin/env bash
# =====================================================================
# PITR 恢复演练 v2  scripts/backup/pitr-drill.sh
#
# 与 v1（sha ca3c990b…，三轮真实 FAIL 证据保留在 sprint7/dr/pitr-20260912T21*.{md,json,logs}）的差别：
#   1. base 目录权限：容器内 uid 0 预建**唯一** base 目录并 chown postgres（不 chmod 777、
#      不改生产 bind mount 根权限、不动已有目录）→ 修复 "could not create directory: Permission denied"。
#   2. 恢复一律基于 **T_fail 时刻冻结的归档集** 的**独立 clone**；绝不在原 base 上启动。
#   3. durability RPO 不再用 "archive_timeout + 估算延迟"：记录 T_fail → 冻结归档清单
#      （逐段 sha256 + 截止段/LSN）→ **真的用该冻结集恢复** → 得到
#        (a) 库中最新**可见且 hash 一致**的业务标记 occurred_at（保守业务边界，基准为事件时间）；
#        (b) 恢复库 pg_last_xact_replay_timestamp()（重放到的最后一次事务提交时间，真实 commit 时间）
#      → RPO = T_fail − T_durable（两者都算，取保守者；不得为负；≤900s）。
#      两个时间来源与误差方向必须在 JSON/报告中明示，不得把 occurred_at 冒充 commit 时间。
#   4. target reached 由 **恢复日志 + 目标前后两个已提交业务标记（request_id 唯一绑定、hash 非空）**
#      共同证明，不是 pg_is_in_recovery()=f。
#   5. 缺段负向：在**私有副本**上隐藏必需段后**真的启动恢复**，必须无法到达目标；
#      生产归档全程只读，绝不移动/改写（__moved 永不出现）。
#   6. RTO 从 T_fail（灾难时刻）起算，含 base clone、恢复启动、WAL 重放、就绪业务验证（≤14400s）。
#   7. 报告(.md) / JSON / 退出码三者由**同一个 verdict** 生成；任一维度未闭合只能是 PARTIAL。
#   8. 冻结运行：脚本先复制到唯一 run 目录并以冻结副本执行（runhash），运行中绝不原地热改。
#
# 退出码：0=PASS；1=FAIL；2=前置错误；3=PARTIAL/INCOMPLETE。
# 不自动清理任何容器、目录、归档副本与证据。
# =====================================================================
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/dr}"

if [ -z "${PITR_FROZEN_RUN:-}" ]; then
  _TS="$(date -u +%Y%m%dT%H%M%SZ)"
  _RUN="$EV/pitr-$_TS.logs"
  mkdir -p "$_RUN/frozen" || { echo "ABORT: 无法创建 $_RUN" >&2; exit 2; }
  _SELF="${BASH_SOURCE[0]}"
  cp -p "$_SELF" "$_RUN/frozen/pitr-drill.sh" || { echo "ABORT: 冻结副本失败" >&2; exit 2; }
  ( cd "$_RUN/frozen" && sha256sum pitr-drill.sh > pitr-drill.sh.sha256 )
  export PROJECT EV PITR_FROZEN_RUN="$_RUN" PITR_TS="$_TS"
  echo "冻结运行：$_RUN/frozen/pitr-drill.sh"
  echo "runhash：$(cut -d' ' -f1 < "$_RUN/frozen/pitr-drill.sh.sha256")"
  exec bash "$_RUN/frozen/pitr-drill.sh" "@@"
fi

RUN="$PITR_FROZEN_RUN"
TS="$PITR_TS"
SRC="${PITR_SRC_CONTAINER:-deploy-postgres-1}"
PGUSER="${PITR_PG_USER:-blockchain}"
PGDB="${PITR_PG_DB:-blockchain_platform}"
PGVOL_HOST="${PITR_PGVOL_HOST:-/data/blockchain-platform/postgres}"
ARCHIVE_CTR="/var/lib/postgresql/wal-archive"
IMG="${PITR_IMAGE:-postgres:18}"
API_BASE="${PITR_API_BASE:-http://127.0.0.1:8088}"
RPO_LIMIT_S="${PITR_RPO_LIMIT_S:-900}"
RTO_LIMIT_S="${PITR_RTO_LIMIT_S:-14400}"
BASE_NAME="pitr-base-$TS"
BASE_CTR="/var/lib/postgresql/$BASE_NAME"
BASE_HOST="$PGVOL_HOST/$BASE_NAME"
REPORT="$EV/pitr-$TS.md"
RESULT="$EV/pitr-$TS.json"
FAILURES="$RUN/FAILURES.txt"
NOT_VERIFIED="$RUN/NOT_VERIFIED.txt"
FACTS="$RUN/facts.tsv"
# 端口必须与历史运行不同（215751 的 5544/5545/5546/5547 实例仍在运行且保留不清理）
RA_NAME="pitr-restore-$TS";  RA_PORT="${PITR_PORT_A:-5554}"
RB1_NAME="pitr-rpo1-$TS";    RB1_PORT="${PITR_PORT_B1:-5555}"
RB2_NAME="pitr-rpo2-$TS";    RB2_PORT="${PITR_PORT_B2:-5556}"
RC_NAME="pitr-neg-$TS";      RC_PORT="${PITR_PORT_C:-5557}"

mkdir -p "$EV" "$RUN" || { echo "ABORT: 无法创建输出目录" >&2; exit 2; }
: > "$FAILURES"; : > "$NOT_VERIFIED"; : > "$FACTS"
FAILS=0; NVS=0
say()  { echo "$*" | tee -a "$REPORT"; }
fail_check()   { FAILS=$((FAILS+1)); echo "$1" >> "$FAILURES"; say "  FAIL  $1"; }
not_verified() { NVS=$((NVS+1)); echo "$1" >> "$NOT_VERIFIED"; say "  NOT_VERIFIED  $1"; }
fact() { printf '%s\t%s\n' "$1" "$2" >> "$FACTS"; }
# [Codex 05:49] 提前终止也必须有机器可读结果：报告/JSON/退出码一致，绝不留空 JSON 或 FAILS=0。
emit_result() { # $1=verdict $2=stage $3=reason
  printf 'verdict\t%s\nstage_failed\t%s\nstage_failed_reason\t%s\nfailures_count\t%s\nnot_verified_count\t%s\n' \
    "$1" "$2" "$3" "$FAILS" "$NVS" >> "$FACTS"
  python3 - "$FACTS" "$RESULT" "$REPORT" "$1" "$2" "$3" <<'PYEOF'
import json, sys
facts = {}
for ln in open(sys.argv[1], errors="replace"):
    if "\t" in ln:
        k, v = ln.rstrip("\n").split("\t", 1); facts[k] = v
verdict, stage, reason = sys.argv[4], sys.argv[5], sys.argv[6]
res = {
  "drill": "pitr", "ts": facts.get("run_ts"), "verdict": verdict,
  "verdict_note": "提前终止：未形成完整证据链时不得 PASS。",
  "stage_failed": stage, "stage_failed_reason": reason,
  "unclosed_reasons": [reason],
  "archive_mode": facts.get("archive_mode"),
  "reached_target": False,
  "rpo_seconds": None, "rpo_status": "NOT_MEASURED",
  "rto_seconds": None, "rto_status": "NOT_MEASURED",
  "base_backup_dir": facts.get("base_dir"),
  "basebackup_rc": facts.get("basebackup_rc"),
  "manifest_parse_ok": facts.get("manifest_parse_ok"),
  "manifest_start_lsn": facts.get("manifest_start_lsn"),
  "manifest_end_lsn": facts.get("manifest_end_lsn"),
  "manifest_timeline": facts.get("manifest_timeline"),
  "script_sha256": facts.get("script_sha256"),
  "failures_count": int(facts.get("failures_count") or 0),
  "not_verified_count": int(facts.get("not_verified_count") or 0),
  "evidence_dir": facts.get("run_dir"),
  "report": sys.argv[3],
  "cleanup": "失败证据与目录一律保留，不自动清理",
}
json.dump(res, open(sys.argv[2], "w"), indent=2, ensure_ascii=False)
print(verdict)
PYEOF
  echo "" >> "$REPORT"
  echo "  **判定：$1**（stage=$2）" >> "$REPORT"
  echo "  - $3" >> "$REPORT"
  echo "  JSON：$RESULT" >> "$REPORT"
  echo "PITR 演练：$1（stage=$2：$3）" >&2
}
abort_hard() { echo "ABORT: $*" >&2; emit_result "INCOMPLETE" "precondition" "$*"; exit 2; }
# [Codex 05:55] 任何未预期提前退出（含 set -u 触发的 unbound variable）都必须留下诚实 JSON，
# 不遮盖原 rc 与报告；绝不留下"报告说 FAIL 但机器读为空/0"的状态。
PITR_STAGE="preflight"
trap 'PITR_RC=$?; if [ ! -s "$RESULT" ]; then echo "PITR 演练：INCOMPLETE（未预期提前退出 rc=$PITR_RC，stage=$PITR_STAGE）" >&2; emit_result "INCOMPLETE" "$PITR_STAGE" "未预期提前退出 rc=$PITR_RC（stage=$PITR_STAGE）：未形成完整证据链"; fi' EXIT

[ -f "$PROJECT/deploy/.env" ] && { set -a; . "$PROJECT/deploy/.env"; set +a; }
PGPW="${POSTGRES_PASSWORD:-}"
[ -n "$PGPW" ] || abort_hard "缺少 POSTGRES_PASSWORD"

PS()  { docker exec -e "PGPASSWORD=$PGPW" "$SRC" psql -U "$PGUSER" -d "$PGDB" -qAt -c "$1" 2>/dev/null; }
PSP() { docker exec -e "PGPASSWORD=$PGPW" "$SRC" psql -U "$PGUSER" -d postgres -qAt -c "$1" 2>/dev/null; }
# [Codex 05:59] 恢复实例的 port 是 5544/5545/5546/5547，容器内 unix socket 也随之改名
# （/var/run/postgresql/.s.PGSQL.<port>）。psql 不带 -p 会去找 5432 → 观测为空/误判，
# 必须按容器绑定真实端口，绝不依赖默认端口。
rport() {
  case "$1" in
    "$RA_NAME")  printf '%s' "$RA_PORT" ;;
    "$RB1_NAME") printf '%s' "$RB1_PORT" ;;
    "$RB2_NAME") printf '%s' "$RB2_PORT" ;;
    "$RC_NAME")  printf '%s' "$RC_PORT" ;;
    *)           printf '' ;;
  esac
}
RPS() { docker exec "$1" psql -p "$(rport "$1")" -U "$PGUSER" -d postgres -qAt -c "$2" 2>/dev/null; }
RPSD(){ docker exec "$1" psql -p "$(rport "$1")" -U "$PGUSER" -d "$PGDB" -qAt -c "$2" 2>/dev/null; }

say "==== PITR 恢复演练 v2  $TS ===="
say "证据目录：$RUN"
say "报告：$REPORT"
say "runhash（冻结副本）：$(cut -d' ' -f1 < "$RUN/frozen/pitr-drill.sh.sha256")"
say ""
fact run_ts "$TS"
fact run_dir "$RUN"
fact script_sha256 "$(cut -d' ' -f1 < "$RUN/frozen/pitr-drill.sh.sha256")"
fact started_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# ===================== 1) 前置条件 =====================
say "== 1) 前置条件 =="
docker ps --format '{{.Names}}' | grep -qx "$SRC" || abort_hard "源容器 $SRC 未运行"
for n in "$RA_NAME" "$RB1_NAME" "$RB2_NAME" "$RC_NAME"; do
  if docker ps -a --format '{{.Names}}' | grep -qx "$n"; then abort_hard "容器名已存在：$n"; fi
done
for p in "$RA_PORT" "$RB1_PORT" "$RB2_PORT" "$RC_PORT"; do
  if ss -ltn 2>/dev/null | grep -q ":$p "; then abort_hard "端口 $p 已被占用"; fi
done
[ -d "$PGVOL_HOST" ] || abort_hard "生产 PG 卷宿主路径不存在：$PGVOL_HOST"

AM="$(PSP "show archive_mode")"
AC="$(PSP "show archive_command")"
WL="$(PSP "show wal_level")"
PGV="$(PSP "show server_version")"
BBV="$(docker exec -u postgres "$SRC" pg_basebackup --version 2>/dev/null)"
[ "$AM" = "on" ] || abort_hard "archive_mode != on（PITR 前提不成立）"
[ -n "$AC" ] || abort_hard "archive_command 为空"
say "  archive_mode=$AM wal_level=$WL pg=$PGV"
say "  $BBV"
fact archive_mode "$AM"
fact archive_command "$AC"
fact wal_level "$WL"
fact pg_version "$PGV"
fact basebackup_version "$BBV"

AT_ROW="$(PSP "select setting||'|'||coalesce(unit,'') from pg_settings where name='archive_timeout'")"
AT_SET="$(printf '%s' "$AT_ROW" | cut -d'|' -f1)"
AT_UNIT="$(printf '%s' "$AT_ROW" | cut -d'|' -f2)"
AT_S=""
if [ -n "$AT_SET" ]; then
  case "$AT_UNIT" in
    ""|s|sec|secs) AT_S="$AT_SET" ;;
    min|mins)      AT_S="$(awk -v v="$AT_SET" 'BEGIN{printf "%d", v*60}')" ;;
    h|hour|hours)  AT_S="$(awk -v v="$AT_SET" 'BEGIN{printf "%d", v*3600}')" ;;
    ms)            AT_S="$(awk -v v="$AT_SET" 'BEGIN{printf "%.3f", v/1000}')" ;;
    *)             AT_S="" ;;
  esac
  case "$AT_SET" in ''|*[!0-9]*) AT_S="" ;; esac
fi
say "  archive_timeout: setting=$AT_SET unit=${AT_UNIT:-（无）} → ${AT_S:-无法换算}s"
if [ -z "$AT_S" ]; then
  not_verified "archive_timeout 无法从 pg_settings(setting+unit) 换算为秒（setting=$AT_SET unit=$AT_UNIT）—— 不得当作 0s RPO"
  fact archive_timeout_seconds ""
elif [ "$AT_S" = "0" ]; then
  not_verified "archive_timeout=0（禁用超时归档）：时效上界无法由配置界定 —— 不得当作 0s RPO"
  fact archive_timeout_seconds "0"
else
  fact archive_timeout_seconds "$AT_S"
fi
fact archive_timeout_setting "$AT_SET"
fact archive_timeout_unit "$AT_UNIT"

PROD_MIG="$(PS "select count(*) from schema_migrations")"
PROD_MAXSEQ="$(PS "select coalesce(max(seq),0) from audit_events_v2")"
DBSIZE="$(PS "select pg_size_pretty(pg_database_size('$PGDB'))")"
PGDATA_SIZE="$(docker exec -u postgres "$SRC" sh -c "du -sh /var/lib/postgresql/18/docker 2>/dev/null | cut -f1" | tr -d ' ')"
PATHS_FACT="$(docker exec -u postgres "$SRC" sh -c "stat -c '%U:%G %a %n' /var/lib/postgresql /var/lib/postgresql/18/docker /var/lib/postgresql/wal-archive 2>/dev/null" | tr '\n' ';')"
say "  生产库：migrations=$PROD_MIG max_seq=$PROD_MAXSEQ db_size=$DBSIZE PGDATA=$PGDATA_SIZE"
say "  路径属主/权限：$PATHS_FACT"
fact prod_migrations "$PROD_MIG"
fact prod_max_seq "$PROD_MAXSEQ"
fact db_size "$DBSIZE"
fact pgdata_size "$PGDATA_SIZE"
fact path_facts "$PATHS_FACT"
AVL_KB="$(df -Pk "$PGVOL_HOST" | awk 'NR==2{print $4}')"
say "  PG 卷可用空间：$((AVL_KB/1024)) MiB（basebackup 为整个 cluster，不是单库大小）"
fact volume_free_mib "$((AVL_KB/1024))"
[ "$AVL_KB" -gt 20971520 ] || fail_check "PG 卷可用空间不足 20GiB（当前 $((AVL_KB/1024)) MiB）"

# ===================== 2) pg_basebackup =====================
say ""
PITR_STAGE="basebackup"
say "== 2) pg_basebackup（-X stream -c fast）=="
MKDIR_RC=0
docker exec -u 0 "$SRC" sh -c "mkdir -p '$BASE_CTR' && chown postgres:postgres '$BASE_CTR' && chmod 700 '$BASE_CTR'" > "$RUN/base-dir.txt" 2>&1 || MKDIR_RC=$?
say "  预建 base 目录 rc=$MKDIR_RC：$BASE_CTR（容器内 uid0 创建 + chown postgres + 0700）"
[ "$MKDIR_RC" -eq 0 ] || fail_check "无法在容器内预建 base 目录（rc=$MKDIR_RC）"
docker exec -u postgres "$SRC" sh -c "test -w '$BASE_CTR'" || fail_check "base 目录对 postgres 不可写"

BB_START="$(date -u +%s)"
docker exec -u postgres "$SRC" pg_basebackup -h /var/run/postgresql -U "$PGUSER" -D "$BASE_CTR" -X stream -c fast -P > "$RUN/basebackup.log" 2>&1
BB_RC=$?
BB_END="$(date -u +%s)"
BB_SEC=$((BB_END-BB_START))
say "  pg_basebackup rc=$BB_RC 耗时=${BB_SEC}s"
tail -4 "$RUN/basebackup.log" | sed 's/^/    /' >> "$REPORT"
fact basebackup_rc "$BB_RC"
fact basebackup_seconds "$BB_SEC"
fact base_dir "$BASE_HOST"

MAN_SIZE="$(docker exec -u postgres "$SRC" sh -c "wc -c < '$BASE_CTR/backup_manifest' 2>/dev/null || echo 0" | tr -d ' ')"
say "  backup_manifest: ${MAN_SIZE:-0} 字节"
fact manifest_bytes "${MAN_SIZE:-0}"
MAN_JSON="$RUN/backup_manifest.json"
docker exec -u postgres "$SRC" cat "$BASE_CTR/backup_manifest" > "$MAN_JSON" 2>/dev/null
# [FIX] backup_manifest v2 的真实 schema：Start-LSN/End-LSN/Timeline 都在 WAL-Ranges[0] 内，
# 顶层没有 Start-LSN。这里统一解析并落自检文件，缺任一字段即视为 manifest 解析失败（不是 basebackup 失败）。
python3 - "$MAN_JSON" "$RUN/manifest-parse.txt" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
w = (d.get("WAL-Ranges") or [{}])[0]
out = {
  "manifest_version": d.get("PostgreSQL-Backup-Manifest-Version"),
  "system_identifier": d.get("System-Identifier"),
  "files": len(d.get("Files") or []),
  "timeline": w.get("Timeline"),
  "start_lsn": w.get("Start-LSN"),
  "end_lsn": w.get("End-LSN"),
  "manifest_checksum": d.get("Manifest-Checksum"),
  "wal_ranges_count": len(d.get("WAL-Ranges") or []),
}
ok = bool(out["start_lsn"]) and bool(out["end_lsn"]) and (out["timeline"] is not None) and (out["files"] > 0) and (out["manifest_version"] is not None)
out["parse_ok"] = ok
json.dump(out, open(sys.argv[2], "w"), indent=1, ensure_ascii=False)
print("parse_ok=%s version=%s timeline=%s files=%s start=%s end=%s" % (ok, out["manifest_version"], out["timeline"], out["files"], out["start_lsn"], out["end_lsn"]))
PYEOF
MAN_PARSE_LINE="$(tail -1 "$RUN/manifest-parse.txt" 2>/dev/null)"
if [ -f "$RUN/manifest-parse.txt" ] && head -1 "$RUN/manifest-parse.txt" | grep -q '^{'; then
  MAN_PARSE_LINE="$(python3 -c "
import json
d=json.load(open('$RUN/manifest-parse.txt'))
print('parse_ok=%s version=%s timeline=%s files=%s start=%s end=%s' % (d.get('parse_ok'), d.get('manifest_version'), d.get('timeline'), d.get('files'), d.get('start_lsn'), d.get('end_lsn')))
" 2>/dev/null)"
fi
MAN_PARSE_OK=0
[ -f "$RUN/manifest-parse.txt" ] && MAN_PARSE_OK="$(python3 -c "import json;print(1 if json.load(open('$RUN/manifest-parse.txt')).get('parse_ok') else 0)" 2>/dev/null)"
BB_START_LSN="$(python3 -c "import json;print(json.load(open('$RUN/manifest-parse.txt')).get('start_lsn') or '')" 2>/dev/null)"
BB_END_LSN="$(python3 -c "import json;print(json.load(open('$RUN/manifest-parse.txt')).get('end_lsn') or '')" 2>/dev/null)"
MAN_TLI="$(python3 -c "import json;print(json.load(open('$RUN/manifest-parse.txt')).get('timeline'))" 2>/dev/null)"
MAN_FILES="$(python3 -c "import json;print(json.load(open('$RUN/manifest-parse.txt')).get('files'))" 2>/dev/null)"
MAN_VER="$(python3 -c "import json;print(json.load(open('$RUN/manifest-parse.txt')).get('manifest_version'))" 2>/dev/null)"
say "  manifest 自检：${MAN_PARSE_LINE:-（解析失败）}"
say "  manifest: version=$MAN_VER timeline=$MAN_TLI files=$MAN_FILES Start-LSN=${BB_START_LSN:-?} End-LSN=${BB_END_LSN:-?}"
fact manifest_parse_ok "$MAN_PARSE_OK"
fact manifest_start_lsn "$BB_START_LSN"
fact manifest_end_lsn "$BB_END_LSN"
fact manifest_timeline "$MAN_TLI"
fact manifest_files "$MAN_FILES"
fact manifest_version "$MAN_VER"

if [ "$BB_RC" -ne 0 ] || [ "${MAN_SIZE:-0}" -le 0 ] || [ "$MAN_PARSE_OK" != "1" ] || [ -z "${BB_START_LSN:-}" ] || [ -z "${BB_END_LSN:-}" ]; then
  say ""
  say "== 提前终止：基础备份或 manifest 不可用，不进入恢复阶段 =="
  FAIL_STAGE="pg_basebackup"
  FAIL_REASON="pg_basebackup rc=$BB_RC（见 $RUN/basebackup.log）"
  if [ "$BB_RC" -eq 0 ]; then
    FAIL_STAGE="manifest-parse"
    FAIL_REASON="basebackup 本体成功（rc=0，${BB_SEC}s，manifest ${MAN_SIZE:-0} 字节），但 backup_manifest 解析/自检未通过：parse_ok=${MAN_PARSE_OK} version=${MAN_VER:-?} timeline=${MAN_TLI:-?} files=${MAN_FILES:-?} start=${BB_START_LSN:-空} end=${BB_END_LSN:-空}"
  fi
  fail_check "$FAIL_REASON"
  say "  **判定：FAIL**（stage=$FAIL_STAGE，fail-fast；基础备份目录与证据保留）"
  emit_result "FAIL" "$FAIL_STAGE" "$FAIL_REASON"
  exit 1
fi

docker exec -u postgres "$SRC" pg_verifybackup "$BASE_CTR" > "$RUN/pg_verifybackup.log" 2>&1
VERIFY_RC=$?
say "  pg_verifybackup rc=$VERIFY_RC"
tail -4 "$RUN/pg_verifybackup.log" | sed 's/^/    /' >> "$REPORT"
fact verifybackup_rc "$VERIFY_RC"
[ "$VERIFY_RC" -eq 0 ] || fail_check "pg_verifybackup 未通过：不得用未校验的基础备份做恢复"
docker exec -u postgres "$SRC" sh -c "cd '$BASE_CTR' && find . -type f -print0 | sort -z | xargs -0 -r sha256sum" > "$RUN/base-hashes.txt" 2>/dev/null
BASE_FILES="$(wc -l < "$RUN/base-hashes.txt")"
say "  原 base 冻结文件清单：$BASE_FILES 个文件（sha256）"
fact base_files "$BASE_FILES"
[ "$BASE_FILES" -gt 100 ] || fail_check "base 冻结清单文件数异常（$BASE_FILES）"
docker exec -u postgres "$SRC" sh -c "test ! -f '$BASE_CTR/postmaster.pid'" || fail_check "base 目录含 postmaster.pid（不是干净的基础备份）"

# ===================== 3) 业务标记（真实 API + request_id 唯一绑定） =====================
say ""
PITR_STAGE="markers"
say "== 3) 业务标记：真实 API 产生已提交审计事件，并以 request_id 唯一绑定 =="
MARKER_UA_TAG="PITR-$TS"
make_marker() { # $1=tag → "tag|http|<seq|ts|epoch|hash|prev|http_status|request_id|trace_id>"
  local tag="$1"
  local rid ua code row i
  rid="pitr-$TS-$tag"
  ua="$MARKER_UA_TAG-$tag"
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -A "$ua" -H "X-Request-Id: $rid" "$API_BASE/api/v2/cases" || echo 000)"
  printf '%s\n' "$code" > "$RUN/marker-$tag.http"
  row=""
  for i in $(seq 1 20); do
    row="$(PS "select seq::text||'|'||to_char(occurred_at at time zone 'UTC','YYYY-MM-DD HH24:MI:SS.US')||'|'||extract(epoch from occurred_at)::text||'|'||event_hash||'|'||coalesce(prev_hash,'')||'|'||coalesce(http_status::text,'')||'|'||coalesce(request_id,'')||'|'||coalesce(trace_id::text,'') from audit_events_v2 where request_id='$rid' order by seq desc limit 1")"
    [ -n "$row" ] && break
    sleep 1
  done
  if [ -n "$row" ]; then
    printf '%s\n' "$row" > "$RUN/marker-$tag.row.txt"
    PSP "select pg_current_wal_insert_lsn()" > "$RUN/marker-$tag.lsn.txt" 2>/dev/null
  fi
  printf '%s|%s|%s\n' "$tag" "$code" "$row"
}
mfield() { printf '%s' "$2" | cut -d'|' -f"$1"; }
validate_marker() { # $1=tag $2=line
  local tag="$1" line="$2" seq ts ep hash prev http rid trace nf
  seq="$(mfield 1 "$line")"; ts="$(mfield 2 "$line")"; ep="$(mfield 3 "$line")"
  hash="$(mfield 4 "$line")"; prev="$(mfield 5 "$line")"; http="$(mfield 6 "$line")"
  rid="$(mfield 7 "$line")"; trace="$(mfield 8 "$line")"
  nf="$(printf '%s' "$line" | awk -F'|' '{print NF}')"
  [ -n "$line" ] || { say "    [$tag] 未取回审计行（request_id 未落库）"; return 1; }
  [ "$nf" -eq 8 ] || { say "    [$tag] 字段数=$nf（应为 8）"; return 1; }
  case "$seq" in ''|*[!0-9]*) say "    [$tag] seq 非数字：$seq"; return 1 ;; esac
  case "$ep" in ''|*[!0-9.]*) say "    [$tag] epoch 非数字：$ep"; return 1 ;; esac
  [ -n "$ts" ] || { say "    [$tag] occurred_at 为空"; return 1; }
  printf '%s' "$hash" | grep -qE '^[0-9a-f]{64}$' || { say "    [$tag] event_hash 非 64 位小写 hex：$hash"; return 1; }
  printf '%s' "$prev" | grep -qE '^[0-9a-f]{64}$' || { say "    [$tag] prev_hash 非 64 位小写 hex（链非空）：$prev"; return 1; }
  [ "$http" = "401" ] || { say "    [$tag] http_status=$http（期望 401）"; return 1; }
  [ "$rid" = "pitr-$TS-$tag" ] || { say "    [$tag] request_id 未按探针回写：$rid"; return 1; }
  [ -n "$trace" ] || { say "    [$tag] trace_id 为空"; return 1; }
  return 0
}
mk_record() { # $1=tag $2=line $3=http
  local tag="$1" line="$2" code="$3"
  fact "marker_${tag}_seq" "$(mfield 1 "$line")"
  fact "marker_${tag}_ts" "$(mfield 2 "$line")"
  fact "marker_${tag}_epoch" "$(mfield 3 "$line")"
  fact "marker_${tag}_hash" "$(mfield 4 "$line")"
  fact "marker_${tag}_prev_hash" "$(mfield 5 "$line")"
  fact "marker_${tag}_request_id" "$(mfield 7 "$line")"
  fact "marker_${tag}_trace_id" "$(mfield 8 "$line")"
  fact "marker_${tag}_http" "$code"
}

CURL_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$API_BASE/api/v2/cases" || echo 000)"
say "  API 可达性探针 $API_BASE/api/v2/cases → HTTP $CURL_CODE"
fact api_probe_http "$CURL_CODE"
[ "$CURL_CODE" != "000" ] || abort_hard "API 入口不可达，无法产生真实业务标记"

MK_LINE="$(make_marker before)"
MK_HTTP="$(mfield 2 "$MK_LINE")"
MK_BODY="$(printf '%s' "$MK_LINE" | cut -d'|' -f3-)"
if validate_marker before "$MK_BODY"; then
  MK_BEFORE="$MK_BODY"; mk_record before "$MK_BODY" "$MK_HTTP"
  say "  marker_before ✓ seq=$(mfield 1 "$MK_BODY") occurred_at=$(mfield 2 "$MK_BODY") request_id=$(mfield 7 "$MK_BODY")"
else
  MK_BEFORE=""
  fail_check "marker_before 校验失败（HTTP=$MK_HTTP，见 $RUN/marker-before.*）"
  fact marker_before_seq ""; fact marker_before_ts ""; fact marker_before_epoch ""; fact marker_before_hash ""
fi
E_BEFORE="$(mfield 3 "$MK_BEFORE")"

LSN_AFTER_BEFORE="$(PSP "select pg_current_wal_lsn()")"
PSP "select pg_switch_wal()" >/dev/null 2>&1 || true
SEG_NEED_B="$(PSP "select pg_walfile_name('$LSN_AFTER_BEFORE')")"
say "  已请求 WAL 切换，等待段 $SEG_NEED_B 归档…"
ARCH_OK=0; HAVESEG=""
for i in $(seq 1 90); do
  HAVESEG="$(PSP "select coalesce(last_archived_wal,'') from pg_stat_archiver")"
  if [ -n "$HAVESEG" ] && [ "$(printf '%s\n%s\n' "$SEG_NEED_B" "$HAVESEG" | sort | tail -1)" = "$HAVESEG" ]; then ARCH_OK=1; break; fi
  sleep 1
done
if [ "$ARCH_OK" = "1" ]; then say "  已归档到 $HAVESEG（覆盖 $SEG_NEED_B）"; else fail_check "等待段 $SEG_NEED_B 归档超时（last_archived_wal=${HAVESEG:-空}）"; fi
fact switch1_archived_seg "$HAVESEG"

sleep 4
T_TARGET="$(PSP "select to_char(now() at time zone 'UTC','YYYY-MM-DD HH24:MI:SS.US')")"
T_TARGET_EP="$(PSP "select extract(epoch from now())::text")"
T_TARGET_ARG="${T_TARGET}+00"
sleep 3
MKF_LINE="$(make_marker filler)"; MKF_HTTP="$(mfield 2 "$MKF_LINE")"; MK_FILL="$(printf '%s' "$MKF_LINE" | cut -d'|' -f3-)"
MKA_LINE="$(make_marker after)";  MKA_HTTP="$(mfield 2 "$MKA_LINE")"; MK_AFTER="$(printf '%s' "$MKA_LINE" | cut -d'|' -f3-)"
if validate_marker filler "$MK_FILL"; then mk_record filler "$MK_FILL" "$MKF_HTTP"; say "  marker_filler ✓ seq=$(mfield 1 "$MK_FILL") ts=$(mfield 2 "$MK_FILL")"; else MK_FILL=""; fail_check "marker_filler 校验失败（HTTP=$MKF_HTTP）"; fact marker_filler_seq ""; fi
if validate_marker after "$MK_AFTER"; then mk_record after "$MK_AFTER" "$MKA_HTTP"; say "  marker_after ✓ seq=$(mfield 1 "$MK_AFTER") ts=$(mfield 2 "$MK_AFTER")"; else MK_AFTER=""; fail_check "marker_after 校验失败（HTTP=$MKA_HTTP）"; fact marker_after_seq ""; fact marker_after_hash ""; fi
S_BEFORE="$(mfield 1 "$MK_BEFORE")"; H_BEFORE="$(mfield 4 "$MK_BEFORE")"
S_FILL="$(mfield 1 "$MK_FILL")"; S_AFTER="$(mfield 1 "$MK_AFTER")"; H_AFTER="$(mfield 4 "$MK_AFTER")"
E_FILL="$(mfield 3 "$MK_FILL")"; E_AFTER="$(mfield 3 "$MK_AFTER")"
say "  目标时刻 T_target=$T_TARGET"
fact target_time "$T_TARGET"
fact target_time_epoch "$T_TARGET_EP"
if [ -n "$E_BEFORE" ] && [ -n "$T_TARGET_EP" ] && [ -n "$E_FILL" ] && [ -n "$E_AFTER" ]; then
  if awk -v a="$E_BEFORE" -v b="$T_TARGET_EP" -v c="$E_FILL" -v d="$E_AFTER" 'BEGIN{ if(!(a<b && b<c && c<d)) exit 1 }'; then
    say "  时间序成立（事件时间基准）：before < T_target < filler < after ✓"
    fact marker_order_ok True
  else
    fail_check "标记时间序不成立：$E_BEFORE / $T_TARGET_EP / $E_FILL / $E_AFTER"
    fact marker_order_ok False
  fi
else
  fail_check "标记时间序无法计算（有标记缺失）"
  fact marker_order_ok False
fi
# [Codex 05:52] 目标恢复必须让归档覆盖"目标之后已提交的 filler/after"所在的 WAL 段，
# 否则恢复会在到达 recovery_target_time 之前耗尽 WAL 而 FATAL（本轮 215037 的真实失败原因）。
# 在 after 标记之后再切一次 WAL 并等待归档完成：冻结集因此确定覆盖到 after 的提交，
# 恢复会在 filler 的提交处 stop，marker_after 仍应不可见（证明 recovery_target_time 生效）。
MKA_LSN="$(cat "$RUN/marker-after.lsn.txt" 2>/dev/null)"
[ -n "$MKA_LSN" ] || MKA_LSN="$(PSP "select pg_current_wal_insert_lsn()")"
SEG_COVER="$(PSP "select pg_walfile_name('$MKA_LSN')")"
PSP "select pg_switch_wal()" >/dev/null 2>&1 || true
COVER_OK=0; HAVESEG2=""
for i in $(seq 1 120); do
  HAVESEG2="$(PSP "select coalesce(last_archived_wal,'') from pg_stat_archiver")"
  if [ -n "$HAVESEG2" ] && [ "$(printf '%s\n%s\n' "$SEG_COVER" "$HAVESEG2" | sort | tail -1)" = "$HAVESEG2" ]; then COVER_OK=1; break; fi
  sleep 1
done
if [ "$COVER_OK" = "1" ]; then
  say "  目标覆盖段 $SEG_COVER 已归档（last_archived_wal=$HAVESEG2）✓"
else
  say "  目标覆盖段 $SEG_COVER 未归档（last_archived_wal=${HAVESEG2:-空}）"
  fail_check "目标覆盖段 $SEG_COVER 未在冻结前完成归档：无法证明归档可恢复到目标时刻"
fi
say "  marker_after WAL 位置=$MKA_LSN 段=$SEG_COVER"
fact marker_after_lsn "$MKA_LSN"
fact target_cover_segment "$SEG_COVER"
fact target_cover_last_archived "$HAVESEG2"
fact target_cover_ok "$([ "$COVER_OK" = "1" ] && echo True || echo False)"

SEG_RANGE_START="$(PSP "select pg_walfile_name('$BB_END_LSN')")"
say "  恢复所需起始段（服务器按 manifest End-LSN 计算）：${SEG_RANGE_START:-?}"
fact range_start_segment "$SEG_RANGE_START"

# ===================== 4) 冻结归档集 =====================
show() { if [ -n "$1" ]; then printf '%s' "$1"; else printf '?'; fi; }
fw()   { printf 'freeze_%s_%s' "$1" "$2"; }
rw()   { printf 'rpo_%s_%s' "$1" "$2"; }
fget() { awk -F'\t' -v k="$1" '$1==k{print $2}' "$FACTS"; }
PGVOL_CTR="/var/lib/postgresql"
# 本轮 range_start 段是否已在基础备份自带的 pg_wal 里（决定是否需要从归档取）
RANGE_START_IN_BASE="no"
docker exec -u postgres "$SRC" sh -c "test -f '$BASE_CTR/pg_wal/$SEG_RANGE_START'" && RANGE_START_IN_BASE="yes"
say "  起始段 $SEG_RANGE_START 在基础备份 pg_wal 中：$RANGE_START_IN_BASE"
fact range_start_in_base "$RANGE_START_IN_BASE"

say ""
say "== 4) 冻结归档集（T_fail 时刻 pg_stat_archiver 已确认完成的段 + 逐段 sha256）=="
freeze_round() { # $1=label
  local label="$1"
  PITR_STAGE="freeze:$label"
  local out="$RUN/freeze-$label"
  local ctrdir="$PGVOL_CTR/pitr-frozen-$TS-$label"
  local hostdir="$PGVOL_HOST/pitr-frozen-$TS-$label"
  local meta t_fail t_fail_ep l_fail last_arch n_incl pnow list_at
  mkdir -p "$out" || return 1
  # [Codex 05:43] 截止边界取 pg_stat_archiver.last_archived_wal（服务端确认已完成的归档），
  # 不用目录 mtime 推断（cp 保留源 mtime，可能早于完成时刻）。
  meta="$(PSP "select to_char(now() at time zone 'UTC','YYYY-MM-DD HH24:MI:SS.US')||'|'||extract(epoch from now())::text||'|'||pg_current_wal_lsn()||'|'||coalesce(last_archived_wal,'')||'|'||coalesce((round(extract(epoch from (now()-last_archived_time))))::text,'') from pg_stat_archiver")"
  t_fail="$(printf '%s' "$meta" | cut -d'|' -f1)"
  t_fail_ep="$(printf '%s' "$meta" | cut -d'|' -f2)"
  l_fail="$(printf '%s' "$meta" | cut -d'|' -f3)"
  last_arch="$(printf '%s' "$meta" | cut -d'|' -f4)"
  pnow="$(printf '%s' "$meta" | cut -d'|' -f5)"
  list_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf '%s|%s|%s|%s|%s\n' "$label" "$t_fail" "$t_fail_ep" "$l_fail" "$last_arch" > "$out/freeze-meta.txt"
  printf 'listing_taken_at=%s\n' "$list_at" >> "$out/freeze-meta.txt"
  docker exec -u postgres "$SRC" sh -c "cd $ARCHIVE_CTR && ls -l --time-style=+%s" > "$out/prod-listing.txt" 2>"$out/prod-listing.err"
  LIST_RC=$?
  [ "$LIST_RC" -eq 0 ] || fail_check "[$label] 无法列出生产归档（rc=$LIST_RC）"
  python3 - "$out/prod-listing.txt" "$out/segments.txt" <<'PYEOF'
import sys
names = []
for ln in open(sys.argv[1], errors="replace"):
    p = ln.split()
    if len(p) >= 7 and p[0].startswith("-"): names.append(p[-1])
open(sys.argv[2], "w").write("\n".join(names) + ("\n" if names else ""))
PYEOF
  NSEG="$(wc -l < "$out/segments.txt")"
  say "  生产归档段数（冻结时观测）：$NSEG；T_fail 时 last_archived_wal=$(show "$last_arch")"
  fact "$(fw "$label" observed_segments)" "$NSEG"
  # 逐段 sha256（只对清单内段计算；必须 -i 才有 stdin）
  python3 -c "
import sys
print(' '.join(open(sys.argv[1]).read().split()))
" "$out/segments.txt" > "$out/segs.arg"
  docker exec -i -u postgres "$SRC" sh -c "cd $ARCHIVE_CTR && sha256sum \$(cat)" < "$out/segs.arg" > "$out/sha256.txt" 2>"$out/sha256.err"
  SHA_RC=$?
  WAL_SEGS="$(grep -cE '^[0-9A-F]{24}$' "$out/segments.txt" || true)"
  SHA_WAL="$(grep -cE '^[0-9a-f]{64}  [0-9A-F]{24}$' "$out/sha256.txt" || true)"
  SHA_TOTAL="$(grep -cE '^[0-9a-f]{64}  ' "$out/sha256.txt" || true)"
  say "  归档口径：清单总数=$NSEG（其中 24 位 WAL 段=$WAL_SEGS，其余为 .backup/.history）；sha256 行：WAL=$SHA_WAL 合计=$SHA_TOTAL"
  fact "$(fw "$label" sha_lines)" "$SHA_WAL"
  fact "$(fw "$label" sha_total)" "$SHA_TOTAL"
  fact "$(fw "$label" wal_segments)" "$WAL_SEGS"
  if [ "$SHA_RC" -ne 0 ] || [ "$SHA_WAL" != "$WAL_SEGS" ] || [ "$SHA_TOTAL" != "$NSEG" ]; then
    fail_check "[$label] 生产归档 sha256 清单不完整（rc=$SHA_RC WAL=$SHA_WAL/$WAL_SEGS total=$SHA_TOTAL/$NSEG）"
  fi
  python3 - "$out/prod-listing.txt" "$SEG_RANGE_START" "$last_arch" "$RANGE_START_IN_BASE" "$out/plan.json" <<'PYEOF'
import json, sys
listing, start_seg, cutoff, in_base, outp = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]
def parse(n):
    n = (n or "").strip()
    if len(n) != 24: return None
    try: return (int(n[0:8],16), int(n[8:16],16), int(n[16:24],16))
    except ValueError: return None
def name_of(t): return "%08X%08X%08X" % t
def nxt(t):
    tl, log, seg = t
    seg += 1
    if seg == 256: seg = 0; log += 1
    return (tl, log, seg)
have = {}
for ln in open(listing, errors="replace"):
    p = ln.split()
    if len(p) >= 7 and p[0].startswith("-"): have[p[-1]] = p[-1]
s = parse(start_seg); c = parse(cutoff)
plan = {"start_segment": start_seg, "cutoff_last_archived": cutoff, "range_start_in_base": in_base,
        "included": [], "skipped_in_base": [], "missing": [], "boundary_last": None,
        "next_after_boundary": None, "error": None}
if s is None:
    plan["error"] = "起始段名无法解析"
elif c is None:
    plan["error"] = "T_fail 时刻没有已完成的归档段（last_archived_wal 为空）"
else:
    cur = s
    if start_seg not in have and in_base == "yes":
        plan["skipped_in_base"].append(start_seg); cur = nxt(cur)
    for _ in range(4096):
        if cur > c:
            plan["next_after_boundary"] = name_of(cur); break
        nm = name_of(cur)
        if nm not in have:
            plan["missing"].append(nm); plan["next_after_boundary"] = nm; break
        plan["included"].append(nm); plan["boundary_last"] = nm
        cur = nxt(cur)
        if len(plan["included"]) > 4096: plan["error"] = "段枚举过多"; break
json.dump(plan, open(outp, "w"), indent=1, ensure_ascii=False)
PYEOF
  n_incl="$(python3 -c "import json;print(len(json.load(open('$out/plan.json')).get('included') or []))")"
  say "  [$label] T_fail=$t_fail lsn=$l_fail 未归档窗口=$(show "$pnow")s 已确认完成且连续可用的段=$n_incl"
  fact "$(fw "$label" t_fail)" "$t_fail"
  fact "$(fw "$label" t_fail_epoch)" "$t_fail_ep"
  fact "$(fw "$label" lsn)" "$l_fail"
  fact "$(fw "$label" segments)" "$n_incl"
  fact "$(fw "$label" last_archived)" "$last_arch"
  fact "$(fw "$label" window_age)" "$pnow"
  fact "$(fw "$label" listing_taken_at)" "$list_at"
  printf '%s\n%s\n' "$hostdir" "$ctrdir" > "$out/frozen-dirs.txt"
  if [ "$n_incl" -eq 0 ]; then
    fail_check "[$label] T_fail 时刻没有连续可用的已归档段（无法构成可恢复集合）"
    return 1
  fi
  docker exec -u 0 "$SRC" sh -c "mkdir -p '$ctrdir' && chown postgres:postgres '$ctrdir' && chmod 700 '$ctrdir'" >/dev/null 2>&1
  docker exec -u postgres "$SRC" sh -c "test -w '$ctrdir'" || fail_check "[$label] 冻结目录对 postgres 不可写"
  python3 -c "import json;print('\n'.join(json.load(open('$out/plan.json'))['included']))" > "$out/included.lst"
  docker exec -i -u postgres "$SRC" sh -c "cd $ARCHIVE_CTR && cp -p \$(cat) '$ctrdir/'" < "$out/included.lst" > "$out/copy.log" 2>&1
  CP_RC=$?
  docker exec -u postgres "$SRC" sh -c "cd '$ctrdir' && sha256sum *" > "$out/frozen-sha256.txt" 2>&1
  [ "$CP_RC" -eq 0 ] || fail_check "[$label] 冻结副本复制命令 rc=$CP_RC（见 copy.log）"
  # [Codex 05:43] 严格按 included 名单逐一比对 expected hash；missing/diff/extra/数量不符/命令错误全部 FAIL
  python3 - "$out/included.lst" "$out/sha256.txt" "$out/frozen-sha256.txt" "$out/verify-frozen.txt" <<'PYEOF'
import sys
def load(p):
    d = {}
    for ln in open(p, errors="replace"):
        q = ln.split()
        if len(q) == 2: d[q[1].lstrip("./")] = q[0]
    return d
inc = [x.strip() for x in open(sys.argv[1]) if x.strip()]
prod = load(sys.argv[2]); froz = load(sys.argv[3])
ok = 0; bad = []; miss = []; extra = sorted(set(froz) - set(inc))
for n in inc:
    if n not in froz: miss.append(n)
    elif n not in prod: bad.append("%s NO_PROD_HASH" % n)
    elif froz[n] != prod[n]: bad.append("%s DIFF" % n)
    else: ok += 1
verdict = "OK" if (ok == len(inc) and not bad and not miss and not extra) else "FAIL"
with open(sys.argv[4], "w") as fh:
    fh.write("included=%d ok=%d missing=%d diff=%d extra=%d verdict=%s\n" % (len(inc), ok, len(miss), len(bad), len(extra), verdict))
    for x in miss[:8]: fh.write("MISSING %s\n" % x)
    for x in bad[:8]: fh.write("DIFF %s\n" % x)
    for x in extra[:8]: fh.write("EXTRA %s\n" % x)
print("%s %d %d %d %d %d" % (verdict, len(inc), ok, len(miss), len(bad), len(extra)))
PYEOF
  VF="$(python3 -c "
import re
t = open('$out/verify-frozen.txt').read()
def g(k):
    m = re.search(k + r'=(\d+)', t)
    return None if m is None else m.group(1)
v = re.search(r'verdict=(\w+)', t)
inc, ok = g('included'), g('ok')
bad = (v is None) or (v.group(1) != 'OK') or (inc is None) or (ok is None) or (inc != ok) or (g('missing') not in (None, '0')) or (g('diff') not in (None, '0')) or (g('extra') not in (None, '0'))
print('FAIL' if bad else 'OK')
" 2>/dev/null)"
  say "  冻结副本严格校验：$(head -1 "$out/verify-frozen.txt")"
  if [ "$VF" = "OK" ]; then
    fact "$(fw "$label" frozen_verify)" "OK"
  else
    fact "$(fw "$label" frozen_verify)" "FAIL"
    fail_check "[$label] 冻结副本严格校验未通过（见 verify-frozen.txt）"
  fi
  return 0
}

# ===================== 自检模式（PITR_SELFCHECK=1）：调用本脚本**真实函数** =====================
# 用于在正式演练前证明 make_marker / validate_marker / freeze_round 三个真实函数可用，
# 而不是另写等价片段；自检使用独立子目录，不污染正式 run 的 facts/结果。
if [ "${PITR_SELFCHECK:-}" = "1" ]; then
  trap - EXIT
  say "== 自检模式：调用最终脚本真实 make_marker / validate_marker / freeze_round =="
  SC_FAIL=0
  SC_SAVE_RUN="$RUN"; SC_SAVE_FACTS="$FACTS"
  RUN="$SC_SAVE_RUN/selfcheck"; mkdir -p "$RUN"
  FACTS="$RUN/selfcheck-facts.tsv"; : > "$FACTS"
  SC_LINE="$(make_marker selfcheck)"
  SC_HTTP="$(mfield 2 "$SC_LINE")"
  SC_BODY="$(printf '%s' "$SC_LINE" | cut -d'|' -f3-)"
  if validate_marker selfcheck "$SC_BODY"; then
    say "  自检 1/2 PASS：真实 API 探针标记通过 validate_marker（http=$SC_HTTP seq=$(mfield 1 "$SC_BODY") hash=$(mfield 4 "$SC_BODY" | cut -c1-12)… request_id=$(mfield 7 "$SC_BODY")）"
  else
    say "  自检 1/2 FAIL：validate_marker 未通过（http=$SC_HTTP）"; SC_FAIL=1
  fi
  RP_OK=1
  for n in "$RA_NAME" "$RB1_NAME" "$RB2_NAME" "$RC_NAME"; do
    [ -n "$(rport "$n")" ] || RP_OK=0
  done
  SRC_PROBE="$(docker exec "$SRC" psql -p 5432 -U "$PGUSER" -d postgres -qAt -c "select 'SRC_5432_OK'" 2>/dev/null)"
  if [ "$RP_OK" = "1" ] && [ "$SRC_PROBE" = "SRC_5432_OK" ]; then
    say "  自检 rport/端口：$RA_NAME→$RA_PORT $RB1_NAME→$RB1_PORT $RB2_NAME→$RB2_PORT $RC_NAME→$RC_PORT；psql -p 探测（源 5432）=OK ✓"
  else
    say "  自检 rport/端口 FAIL：RP_OK=$RP_OK SRC_PROBE=$SRC_PROBE"; SC_FAIL=1
  fi
  printf 'LOG:  recovery stopping before commit of transaction 1, time 2026-01-01\n' > "$RUN/log-before.txt"
  printf 'LOG:  recovery stopping after commit of transaction 2\n' > "$RUN/log-after.txt"
  LT_B=0; LT_A=0
  grep -qiE 'recovery stopping (before|after)' "$RUN/log-before.txt" && LT_B=1
  grep -qiE 'recovery stopping (before|after)' "$RUN/log-after.txt" && LT_A=1
  if [ "$LT_B" = "1" ] && [ "$LT_A" = "1" ]; then
    say "  自检日志谓词：before/after 双模式均可识别为到达目标 ✓"
  else
    say "  自检日志谓词 FAIL：before=$LT_B after=$LT_A"; SC_FAIL=1
  fi
  freeze_round sc
  SC_VF="$(awk -F'\t' '$1=="freeze_sc_frozen_verify"{print $2}' "$FACTS")"
  if [ "$SC_VF" = "OK" ]; then
    say "  自检 2/2 PASS：freeze_round 产出冻结集并通过逐段严格校验"
  else
    say "  自检 2/2 FAIL：freeze_round 严格校验=${SC_VF:-空}"; SC_FAIL=1
  fi
  say "  自检产出目录：$RUN"
  RUN="$SC_SAVE_RUN"; FACTS="$SC_SAVE_FACTS"
  if [ "$SC_FAIL" = "0" ]; then echo "PITR 自检：PASS"; exit 0; fi
  echo "PITR 自检：FAIL" >&2; exit 1
fi

# 场景 1：target restore 的冻结集（覆盖到 marker_after 的提交；用于证明 recovery_target_time 生效）
freeze_round target
FRZ_T_REAL="$(sed -n 1p "$RUN/freeze-target/frozen-dirs.txt" 2>/dev/null)"
FRZ_T_CTR="$(sed -n 2p "$RUN/freeze-target/frozen-dirs.txt" 2>/dev/null)"
RTO_START_EP="$(fget "$(fw target t_fail_epoch)")"
[ -n "$RTO_START_EP" ] || RTO_START_EP="$(date -u +%s)"
# [Codex 05:55] T_fail 是带小数的 epoch；bash $(( )) 不接受小数 → 统一向下取整为整数秒
RTO_START_EP="$(awk -v v="$RTO_START_EP" 'BEGIN{printf "%d", v+0}')"
say "  target 冻结副本：HOST=$FRZ_T_REAL / 容器内=$FRZ_T_CTR"

# ===================== 5) clone + 恢复实例 A（目标时刻） =====================
say ""
say "== 5) 独立 clone 恢复实例 A（recovery_target_time；绝不启动原 base）=="
clone_base() { # $1=hostdir（-v 用宿主路径）
  local ctr="$PGVOL_CTR/$(basename "$1")"
  docker exec -u 0 "$SRC" sh -c "mkdir -p '$ctr' && chown postgres:postgres '$ctr' && chmod 700 '$ctr'" >/dev/null 2>&1 || return 1
  docker exec -u postgres "$SRC" sh -c "cp -a '$BASE_CTR/.' '$ctr/' && touch '$ctr/recovery.signal'" >/dev/null 2>&1 || return 1
  docker exec -u postgres "$SRC" sh -c "test -f '$ctr/recovery.signal' && test -f '$ctr/PG_VERSION' && test ! -f '$ctr/postmaster.pid'" || return 1
  return 0
}
PITR_STAGE="restoreA"
RA_HOST="$PGVOL_HOST/pitr-cloneA-$TS"
CLONE_A0="$(date -u +%s)"
if clone_base "$RA_HOST"; then say "  clone A 完成：$RA_HOST（原 base 保持冻结）"; else fail_check "clone A 失败"; fi
CLONE_A1="$(date -u +%s)"

docker run -d --name "$RA_NAME" --network host -e "PGDATA=/pgdata" \
  -v "$RA_HOST:/pgdata" -v "$FRZ_T_REAL:/frozen:ro" "$IMG" \
  -c "restore_command=cp /frozen/%f %p" \
  -c "recovery_target_time=$T_TARGET_ARG" \
  -c "recovery_target_action=promote" \
  -c "archive_mode=off" \
  -c "hot_standby=on" \
  -c "port=$RA_PORT" \
  > "$RUN/restoreA-run.txt" 2>&1
RA_RUN_RC=$?
say "  docker run rc=$RA_RUN_RC（容器 $RA_NAME，端口 $RA_PORT）"
[ "$RA_RUN_RC" -eq 0 ] || fail_check "无法启动恢复实例 A"

REC_START_EP="$(date -u +%s)"
RA_STATE=""; RA_INREC="t"; RA_ELAPSED=0
for i in $(seq 1 300); do
  RA_STATE="$(docker inspect -f '{{.State.Status}}' "$RA_NAME" 2>/dev/null)"
  if [ "$RA_STATE" = "exited" ]; then RA_ELAPSED=$(( $(date -u +%s) - REC_START_EP )); break; fi
  RA_INREC="$(RPS "$RA_NAME" "select pg_is_in_recovery()")"
  if [ "$RA_INREC" = "f" ]; then RA_ELAPSED=$(( $(date -u +%s) - REC_START_EP )); break; fi
  sleep 1
done
docker logs "$RA_NAME" > "$RUN/restoreA-container.log" 2>&1 || true
say "  恢复 A 状态=$(show "$RA_STATE") in_recovery=$(show "$RA_INREC") 耗时=$RA_ELAPSED s"
# [Codex 05:59] 观测隔离证明：不显式指定端口时，恢复容器内的 5432 socket 必须不存在，
# 否则观测可能误读其它实例（本轮 215751 的误判根因）。
RA_DEFAULT_PROBE="$(docker exec "$RA_NAME" psql -U "$PGUSER" -d postgres -qAt -c "select 'DEFAULT_PORT_REACHABLE'" 2>&1 | head -1)"
case "$RA_DEFAULT_PROBE" in
  *5432*) say "  观测隔离：恢复容器内默认端口 5432 不可达 ✓（观测必须走 -p $RA_PORT）"; fact restoreA_default_port_isolated True ;;
  *) say "  恢复容器默认端口探测结果：$RA_DEFAULT_PROBE"; fail_check "恢复容器内默认端口 5432 意外可用：观测可能误读其它实例"; fact restoreA_default_port_isolated False ;;
esac
fact restoreA_container "$RA_NAME"
fact restoreA_state "$RA_STATE"
fact restoreA_seconds "$RA_ELAPSED"

# [Codex 05:59] PG 到达 recovery_target_time 时可能打印 "recovery stopping before commit"（目标不是
# 精确事务时刻，默认停在首个 commit_time >= target 的事务**之前**）或 "recovery stopping after"。
# 两者都是合法到达；只看 after 会把真实成功误判为失败。同时解析停止事务/时间与 last completed。
LOG_TARGET_OK=0; LOG_CONSISTENT=0; LOG_TARGET_FAIL=0
LOG_STOP_BEFORE=0; LOG_STOP_AFTER=0
grep -qiE 'recovery stopping before' "$RUN/restoreA-container.log" && LOG_STOP_BEFORE=1
grep -qiE 'recovery stopping after'  "$RUN/restoreA-container.log" && LOG_STOP_AFTER=1
[ "$LOG_STOP_BEFORE" = "1" ] || [ "$LOG_STOP_AFTER" = "1" ] && LOG_TARGET_OK=1
grep -qiE 'consistent recovery state reached' "$RUN/restoreA-container.log" && LOG_CONSISTENT=1
grep -qiE 'recovery ended before configured recovery target' "$RUN/restoreA-container.log" && LOG_TARGET_FAIL=1
LOG_STOP_LINE="$(grep -iE 'recovery stopping (before|after)' "$RUN/restoreA-container.log" | tail -1 | sed 's/^.*LOG:  //')"
LOG_LASTCOMPLETE="$(grep -iE 'last completed transaction was at log time' "$RUN/restoreA-container.log" | tail -1 | sed 's/^.*log time //')"
say "  恢复日志：consistent=$LOG_CONSISTENT stopping(before=$LOG_STOP_BEFORE after=$LOG_STOP_AFTER)=$LOG_TARGET_OK target_failed=$LOG_TARGET_FAIL"
say "    停止行：@@{LOG_STOP_LINE:-（无）}"
say "    最后完成事务：@@{LOG_LASTCOMPLETE:-（无）}（target=$T_TARGET）"
fact log_stop_before "$LOG_STOP_BEFORE"
fact log_stop_after "$LOG_STOP_AFTER"
fact log_stop_line "$LOG_STOP_LINE"
fact log_last_completed "$LOG_LASTCOMPLETE"
grep -iE 'recovery stopping|consistent recovery|redo done|target' "$RUN/restoreA-container.log" | tail -4 | sed 's/^/    /' >> "$REPORT"
fact log_consistent "$LOG_CONSISTENT"
fact log_target_stopping "$LOG_TARGET_OK"
fact log_target_failed "$LOG_TARGET_FAIL"

TARGET_REACHED=0
if [ "$RA_STATE" = "running" ] && [ "$RA_INREC" = "f" ] && [ "$LOG_CONSISTENT" = "1" ] && [ "$LOG_TARGET_OK" = "1" ] && [ "$LOG_TARGET_FAIL" = "0" ]; then
  TARGET_REACHED=1
  say "  目标到达：pg_is_in_recovery()=f + 恢复日志一致性与 recovery stopping after 同时成立 ✓"
else
  fail_check "恢复实例 A 未证明到达目标（state=$(show "$RA_STATE") in_recovery=$(show "$RA_INREC") log_ok=$LOG_TARGET_OK log_fail=$LOG_TARGET_FAIL）"
fi
fact target_reached "$([ "$TARGET_REACHED" = "1" ] && echo True || echo False)"

R_A_MAX=""; R_A_CNT=""; R_A_HASH=""; R_A_AFTER=""; R_A_FILL=""; TLI_A=""; RPO_S=""; LAST_REPLAY=""
if [ "$TARGET_REACHED" = "1" ]; then
  R_A_MAX="$(RPSD "$RA_NAME" "select coalesce(max(seq),0) from audit_events_v2")"
  R_A_CNT="$(RPSD "$RA_NAME" "select count(*) from audit_events_v2")"
  R_A_HASH="$(RPSD "$RA_NAME" "select coalesce(event_hash,'') from audit_events_v2 where seq=$S_BEFORE")"
  R_A_AFTER="$(RPSD "$RA_NAME" "select count(*) from audit_events_v2 where seq=$S_AFTER")"
  R_A_FILL="$(RPSD "$RA_NAME" "select count(*) from audit_events_v2 where seq=$S_FILL")"
  say "  恢复库 max_seq=$(show "$R_A_MAX") count=$(show "$R_A_CNT")（生产 before=$(show "$S_BEFORE") filler=$(show "$S_FILL") after=$(show "$S_AFTER")）"
  if [ -z "$R_A_MAX" ] || [ -z "$R_A_CNT" ]; then
    not_verified "恢复库观测不可用（max_seq/count 为空）：目标边界无法核对"
    fact marker_before_present ""; fact marker_before_hash_match ""; fact marker_after_absent ""
  elif [ -n "$R_A_HASH" ] && [ "$R_A_HASH" = "$H_BEFORE" ]; then
    say "  marker_before 在恢复库中存在且 event_hash 完全一致 ✓"
    fact marker_before_present True; fact marker_before_hash_match True
  else
    fail_check "marker_before 缺失或 hash 不一致：恢复库=$(show "$R_A_HASH")"
    fact marker_before_present False; fact marker_before_hash_match False
  fi
  # [Codex 06:16] 缺失/null 与"真实 count>0"必须区分：读不到 = 未验证，不得写成"已出现"
  if [ "$R_A_AFTER" = "0" ]; then
    say "  marker_after（目标之后提交）在恢复库中 count=0，不存在（实测）✓"
    fact marker_after_absent True
  elif [ -z "$R_A_AFTER" ]; then
    not_verified "恢复库中 marker_after 存在性读取为空（count 缺失）：目标后边界未验证"
    fact marker_after_absent ""
  else
    fail_check "marker_after 出现在恢复库中（count=$R_A_AFTER，实测），未真正恢复到目标时刻"
    fact marker_after_absent False
  fi
  fact restored_seqs "filler_present=$R_A_FILL after_present=$R_A_AFTER max=$R_A_MAX count=$R_A_CNT"
  TLI_A="$(RPS "$RA_NAME" "select timeline_id from pg_control_checkpoint()")"
  LAST_REPLAY="$(RPS "$RA_NAME" "select coalesce(to_char(pg_last_xact_replay_timestamp() at time zone 'UTC','YYYY-MM-DD HH24:MI:SS.US'),'')")"
  REC_LSN="$(RPS "$RA_NAME" "select pg_last_wal_replay_lsn()")"
  say "  新 timeline=$(show "$TLI_A") replay_lsn=$(show "$REC_LSN") last_xact_replay=$(show "$LAST_REPLAY")"
  fact timeline_restored "$TLI_A"; fact replay_lsn "$REC_LSN"; fact last_xact_replay "$LAST_REPLAY"
  if [ -n "$TLI_A" ] && [ "$TLI_A" != "1" ]; then say "  已提升到新 timeline ✓"; else fail_check "恢复实例未产生新 timeline（timeline=$(show "$TLI_A")）"; fi
  if [ -n "$LAST_REPLAY" ] && [ -n "$T_TARGET" ]; then
    RPO_S="$(python3 -c "
import datetime,sys
def p(x): return datetime.datetime.strptime(x.strip()[:26],'%Y-%m-%d %H:%M:%S.%f')
try:
    d=(p(sys.argv[1])-p(sys.argv[2])).total_seconds()
    print('NEGATIVE:%.3f'%d if d<0 else '%.3f'%d)
except Exception: print('')
" "$T_TARGET" "$LAST_REPLAY")"
    case "$RPO_S" in
      NEGATIVE:*) fail_check "回放精度为负（$RPO_S）" ;;
      "") fail_check "无法计算回放精度" ;;
      *) say "  replay_precision（**不是 RPO**）=$RPO_S s"; fact replay_precision_seconds "$RPO_S" ;;
    esac
  fi
else
  fact marker_before_present False; fact marker_before_hash_match False; fact marker_after_absent False
fi

# ===================== 6) 就绪业务验证（RTO 终点） =====================
say ""
say "== 6) 恢复库就绪业务验证（RTO 终点）=="
BIZ_OK=1
biz_verify() { # $1=container $2=label
  local c="$1" label="$2" ir mig broken
  local out="$RUN/biz-$label.txt"
  : > "$out"
  ir="$(RPS "$c" "select pg_is_in_recovery()")"
  echo "pg_is_in_recovery=$ir" >> "$out"
  [ "$ir" = "f" ] || { echo "NOT_READY in_recovery=$ir" >> "$out"; return 1; }
  mig="$(RPSD "$c" "select count(*) from schema_migrations")"
  echo "schema_migrations=$mig" >> "$out"
  for t in audit_events_v2 cases investigations download_jobs exports identities organizations workspaces provider_usage; do
    printf '%s=%s\n' "$t" "$(RPSD "$c" "select count(*) from $t")" >> "$out"
  done
  broken="$(RPSD "$c" "with c as (select seq, event_hash, prev_hash, lag(event_hash) over (order by seq) lh, row_number() over (order by seq) rn from audit_events_v2) select count(*) from c where rn>1 and prev_hash is distinct from lh")"
  echo "audit_chain_broken=$broken" >> "$out"
  [ "$broken" = "0" ] || { echo "CHAIN_BROKEN=$broken" >> "$out"; return 1; }
  [ "$mig" = "$PROD_MIG" ] || { echo "MIGRATION_MISMATCH mig=$mig prod=$PROD_MIG" >> "$out"; return 1; }
  echo "BUSINESS_VERIFY=OK" >> "$out"
  return 0
}
if [ "$TARGET_REACHED" = "1" ]; then
  if biz_verify "$RA_NAME" "A"; then
    say "  恢复库 A 业务验证通过（迁移数=$PROD_MIG、核心表可读、审计链 0 断链）✓"
  else
    BIZ_OK=0; fail_check "恢复库 A 业务验证未通过（见 $RUN/biz-A.txt）"
  fi
  if [ -x "$PROJECT/backend/bin/auditverify" ]; then
    DATABASE_URL="postgres://$PGUSER:$PGPW@127.0.0.1:$RA_PORT/$PGDB?sslmode=disable" \
      timeout 180 "$PROJECT/backend/bin/auditverify" -timeout 150s > "$RUN/auditverify-A.json" 2>"$RUN/auditverify-A.err"
    AV_RC=$?
    say "  auditverify（独立哈希链校验器 → 恢复库）rc=$AV_RC"
    fact auditverify_rc "$AV_RC"
    if [ "$AV_RC" -eq 0 ]; then say "  auditverify 判定恢复库审计链自洽 ✓"
    elif [ "$AV_RC" -eq 2 ]; then BIZ_OK=0; not_verified "auditverify 无法连接恢复库（rc=2，见 auditverify-A.err）"
    else BIZ_OK=0; fail_check "auditverify 判定恢复库审计链不自洽（rc=$AV_RC）"; fi
    head -c 500 "$RUN/auditverify-A.json" >> "$REPORT" 2>/dev/null || true
  else
    BIZ_OK=0; not_verified "backend/bin/auditverify 不存在，无法用独立校验器核对恢复库"
  fi
else
  BIZ_OK=0; fail_check "恢复库 A 未到达目标，业务验证无法执行"
fi
fact biz_verify_ok "$([ "$BIZ_OK" = "1" ] && echo True || echo False)"
RTO_END_EP="$(date -u +%s)"
RTO_S=$((RTO_END_EP - RTO_START_EP))
fact rto_clone_seconds "$((CLONE_A1 - CLONE_A0))"
fact rto_recovery_seconds "$RA_ELAPSED"
fact elapsed_attempt_seconds "$RTO_S"
# [Codex 06:16] 只有"目标到达且就绪业务验证通过"才构成有效 RTO；否则只是观测耗时，
# rto_status 必须是 NOT_MEASURED 且 rto_seconds=null，不得据此宣称满足 4h。
if [ "$TARGET_REACHED" = "1" ] && [ "$BIZ_OK" = "1" ]; then
  say "  RTO（T_fail → 恢复实例就绪 + 业务验证完成）=$RTO_S s（上限 $RTO_LIMIT_S s；clone=$((CLONE_A1-CLONE_A0)) s）"
  fact rto_seconds "$RTO_S"
  fact rto_ready True
  [ "$RTO_S" -le "$RTO_LIMIT_S" ] || fail_check "RTO $RTO_S s 超过 $RTO_LIMIT_S s"
else
  say "  RTO 未达成（target_reached=$TARGET_REACHED biz_ok=$BIZ_OK）：elapsed_attempt=$RTO_S s 仅记录为观测耗时，rto_status=NOT_MEASURED，不得据此宣称满足 4h"
  fact rto_seconds ""
  fact rto_ready False
fi

# ===================== 7) durability RPO：用冻结集真实恢复 =====================
say ""
say "== 7) durability RPO：对冻结归档集**实际恢复**证明最后可恢复的已提交业务边界 =="
run_durability() { # $1=label $2=frozen_host $3=container $4=port
  local label="$1" frz="$2" name="$3" port="$4"
  PITR_STAGE="durability:$label"
  local host="$PGVOL_HOST/pitr-clone-$label-$TS" t_fail_ep t_fail
  t_fail_ep="$(fget "$(fw "$label" t_fail_epoch)")"
  t_fail="$(fget "$(fw "$label" t_fail)")"
  [ -n "$frz" ] || { fail_check "[$label] 冻结目录缺失，无法恢复"; return 1; }
  fact "$(rw "$label" container)" "$name"
  clone_base "$host" || { fail_check "[$label] clone 失败"; return 1; }
  docker run -d --name "$name" --network host -e "PGDATA=/pgdata" \
    -v "$host:/pgdata" -v "$frz:/frozen:ro" "$IMG" \
    -c "restore_command=cp /frozen/%f %p" \
    -c "archive_mode=off" \
    -c "hot_standby=on" \
    -c "port=$port" \
    > "$RUN/$name-run.txt" 2>&1
  local st="t" i
  for i in $(seq 1 300); do
    [ "$(docker inspect -f '{{.State.Status}}' "$name" 2>/dev/null)" = "exited" ] && break
    st="$(RPS "$name" "select pg_is_in_recovery()")"
    [ "$st" = "f" ] && break
    sleep 1
  done
  docker logs "$name" > "$RUN/$name-container.log" 2>&1 || true
  if [ "$st" != "f" ]; then
    fail_check "[$label] 冻结集恢复未完成（in_recovery=$(show "$st")）—— 无法测出 durability 边界"
    return 1
  fi
  local dmax dts dep dhash dlsn lrc lrc_ep rpo_m rpo_l
  dmax="$(RPSD "$name" "select coalesce(max(seq),0) from audit_events_v2")"
  dts="$(RPSD "$name" "select coalesce(to_char(occurred_at at time zone 'UTC','YYYY-MM-DD HH24:MI:SS.US'),'') from audit_events_v2 order by seq desc limit 1")"
  dep="$(RPSD "$name" "select coalesce(extract(epoch from occurred_at)::text,'') from audit_events_v2 order by seq desc limit 1")"
  dhash="$(RPSD "$name" "select coalesce(event_hash,'') from audit_events_v2 order by seq desc limit 1")"
  dlsn="$(RPS "$name" "select pg_last_wal_replay_lsn()")"
  lrc="$(RPS "$name" "select coalesce(to_char(pg_last_xact_replay_timestamp() at time zone 'UTC','YYYY-MM-DD HH24:MI:SS.US'),'')")"
  lrc_ep="$(RPS "$name" "select coalesce(extract(epoch from pg_last_xact_replay_timestamp())::text,'')")"
  say "  [$label] 冻结集恢复后最新可见业务标记：seq=$(show "$dmax") occurred_at=$(show "$dts") hash=$(printf '%s' "$dhash" | cut -c1-12) replay_lsn=$(show "$dlsn")"
  say "  [$label] 重放到的最后一次事务提交时间：$(show "$lrc")"
  if [ -z "$dep" ] || [ -z "$t_fail_ep" ]; then
    fail_check "[$label] 缺少耐久边界时刻，RPO 无法计算"
    return 1
  fi
  rpo_m="$(awk -v tf="$t_fail_ep" -v td="$dep" 'BEGIN{printf "%.3f", tf-td}')"
  rpo_l=""
  [ -n "$lrc_ep" ] && rpo_l="$(awk -v tf="$t_fail_ep" -v td="$lrc_ep" 'BEGIN{printf "%.3f", tf-td}')"
  say "  [$label] T_fail=$t_fail"
  say "  [$label] RPO_marker（T_fail − 最新可见业务标记 occurred_at；事件时间早于提交时间，故**保守偏大**）=$rpo_m s"
  if [ -n "$rpo_l" ]; then say "  [$label] RPO_last_replay（T_fail − pg_last_xact_replay_timestamp，真实提交时间）=$rpo_l s"; else say "  [$label] RPO_last_replay 不可得"; fi
  awk -v v="$rpo_m" 'BEGIN{exit !(v>=0)}' || fail_check "[$label] RPO_marker 为负（$rpo_m s）—— 可见标记晚于 T_fail，冻结集不可能包含它"
  if [ -n "$rpo_l" ]; then awk -v v="$rpo_l" 'BEGIN{exit !(v>=0)}' || fail_check "[$label] RPO_last_replay 为负（$rpo_l s）"; fi
  fact "$(rw "$label" marker_seconds)" "$rpo_m"
  [ -n "$rpo_l" ] && fact "$(rw "$label" last_replay_seconds)" "$rpo_l"
  fact "$(rw "$label" t_durable)" "$dts"
  fact "$(rw "$label" last_replay_ts)" "$lrc"
  fact "$(rw "$label" max_seq)" "$dmax"
  fact "$(rw "$label" max_hash)" "$dhash"
  fact "$(rw "$label" replay_lsn)" "$dlsn"
  return 0
}
# 场景 2：durability capture —— **不做任何 switch**，故障时刻落在自然的未归档窗口内
freeze_round rpo1
FRZ1_REAL="$(sed -n 1p "$RUN/freeze-rpo1/frozen-dirs.txt" 2>/dev/null)"
FRZ1_CTR="$(sed -n 2p "$RUN/freeze-rpo1/frozen-dirs.txt" 2>/dev/null)"
run_durability rpo1 "$FRZ1_REAL" "$RB1_NAME" "$RB1_PORT" || true

say ""
say "== 7b) 第二轮冻结：等到未归档窗口接近上界（采样稳态 RPO 上界）=="
WIN_TARGET=45
if [ -n "$AT_S" ] && [ "$AT_S" -gt 60 ]; then WIN_TARGET=$(( AT_S * 3 / 4 )); fi
say "  等待未归档窗口 >= $WIN_TARGET s（最多 180s），期间持续产生真实业务标记…"
AGE=""
for i in $(seq 1 18); do
  AGE="$(PSP "select coalesce(round(extract(epoch from (now()-last_archived_time)))::text,'') from pg_stat_archiver")"
  if [ -n "$AGE" ] && [ "$AGE" -ge "$WIN_TARGET" ]; then say "  窗口年龄=$AGE s >= $WIN_TARGET s"; break; fi
  make_marker "win$i" > "$RUN/marker-win$i.txt" 2>/dev/null || true
  sleep 10
done
fact window_wait_age "$AGE"
freeze_round rpo2
FRZ2_REAL="$(sed -n 1p "$RUN/freeze-rpo2/frozen-dirs.txt" 2>/dev/null)"
FRZ2_CTR="$(sed -n 2p "$RUN/freeze-rpo2/frozen-dirs.txt" 2>/dev/null)"
run_durability rpo2 "$FRZ2_REAL" "$RB2_NAME" "$RB2_PORT" || true

# ===================== 8) WAL 连续性 =====================
say ""
say "== 8) WAL 连续性（对 T_fail 冻结清单逐段核对）=="
python3 - "$RUN/freeze-rpo2/plan.json" "$RUN/freeze-rpo2/prod-listing.txt" "$RUN/wal-continuity.json" <<'PYEOF'
import json, sys
plan = json.load(open(sys.argv[1]))
names = set()
for ln in open(sys.argv[2], errors="replace"):
    p = ln.split()
    if len(p) >= 7 and p[0].startswith("-"): names.add(p[-1])
inc = plan.get("included") or []
missing = [n for n in inc if n not in names]
out = {"start_segment": plan.get("start_segment"), "boundary_last": plan.get("boundary_last"),
       "cutoff_last_archived": plan.get("cutoff_last_archived"),
       "checked": len(inc), "missing": missing}
json.dump(out, open(sys.argv[3], "w"), ensure_ascii=False, indent=1)
PYEOF
CONT_N="$(python3 -c "import json;print(json.load(open('$RUN/wal-continuity.json')).get('checked',0))")"
CONT_MISS="$(python3 -c "import json;print(len(json.load(open('$RUN/wal-continuity.json')).get('missing') or []))")"
BOUNDARY_LAST="$(python3 -c "import json;print(json.load(open('$RUN/wal-continuity.json')).get('boundary_last') or '')")"
say "  区间 $(show "$SEG_RANGE_START") → $(show "$BOUNDARY_LAST")：检查 $CONT_N 段，缺失 $CONT_MISS 段"
fact wal_continuity_checked "$CONT_N"
fact wal_continuity_missing "$CONT_MISS"
fact boundary_last_segment "$BOUNDARY_LAST"
[ "$CONT_N" -gt 0 ] || fail_check "WAL 连续性检查未覆盖任何段"
[ "$CONT_MISS" -eq 0 ] || fail_check "WAL 连续性发现 $CONT_MISS 个缺失段"

# ===================== 9) 缺段真实负向 =====================
say ""
PITR_STAGE="negative"
say "== 9) 缺段负向：私有副本隐藏必需段后**真实启动恢复**，必须无法到达目标 =="
NEG_HOST="$PGVOL_HOST/pitr-neg-$TS"
NEG_CTR="$PGVOL_CTR/pitr-neg-$TS"
NEG_SEG="$(python3 -c "
import json,sys
inc=json.load(open(sys.argv[1])).get('included') or []
print(inc[len(inc)//2] if len(inc)>=3 else (inc[0] if inc else ''))
" "$RUN/freeze-target/plan.json")"
NEG_OK=0
NEG_ST=""; NEG_IR=""; NEG_LOGMISS=0; NEG_LOGFAIL=0; NEG_STOPPING=0
if [ -z "$NEG_SEG" ] || [ -z "$FRZ1_CTR" ]; then
  not_verified "可恢复段不足或冻结目录缺失，缺段负向未执行"
else
  say "  选定必需段（区间中部）：$NEG_SEG"
  docker exec -u 0 "$SRC" sh -c "mkdir -p '$NEG_CTR' && chown postgres:postgres '$NEG_CTR' && chmod 700 '$NEG_CTR'" >/dev/null 2>&1
  docker exec -u postgres "$SRC" sh -c "cp -p '$FRZ_T_CTR/'* '$NEG_CTR/'" > "$RUN/neg-copy.log" 2>&1
  NEG_COPY_N="$(docker exec -u postgres "$SRC" sh -c "ls -1 '$NEG_CTR' | wc -l")"
  say "  私有副本段数=$NEG_COPY_N（源冻结集 $(python3 -c "import json;print(len(json.load(open('$RUN/freeze-target/plan.json'))['included']))")）"
  docker exec -u postgres "$SRC" sh -c "rm -f '$NEG_CTR/$NEG_SEG'" > "$RUN/neg-hide.log" 2>&1
  NEG_SHA_BEFORE="$(grep -E "^[0-9a-f]{64}  $NEG_SEG\$" "$RUN/freeze-target/sha256.txt" | awk '{print $1}')"
  if docker exec -u postgres "$SRC" sh -c "test -f '$NEG_CTR/$NEG_SEG'"; then fail_check "私有副本隐藏段失败"; else say "  已在私有副本隐藏 $NEG_SEG（生产归档未被触碰）"; fi
  NEG_CLONE="$PGVOL_HOST/pitr-clone-neg-$TS"
  clone_base "$NEG_CLONE" || fail_check "负向 clone 失败"
  docker run -d --name "$RC_NAME" --network host -e "PGDATA=/pgdata" \
    -v "$NEG_CLONE:/pgdata" -v "$NEG_HOST:/frozen:ro" "$IMG" \
    -c "restore_command=cp /frozen/%f %p" \
    -c "recovery_target_time=$T_TARGET_ARG" \
    -c "recovery_target_action=promote" \
    -c "archive_mode=off" \
    -c "port=$RC_PORT" \
    > "$RUN/restoreNeg-run.txt" 2>&1
  NEG_ST="t"; NEG_IR="t"
  for i in $(seq 1 180); do
    NEG_ST="$(docker inspect -f '{{.State.Status}}' "$RC_NAME" 2>/dev/null)"
    [ "$NEG_ST" = "exited" ] && break
    NEG_IR="$(RPS "$RC_NAME" "select pg_is_in_recovery()")"
    [ "$NEG_IR" = "f" ] && break
    sleep 1
  done
  docker logs "$RC_NAME" > "$RUN/restoreNeg-container.log" 2>&1 || true
  grep -qiE "No such file or directory|could not open file|could not restore file|failed to restore file" "$RUN/restoreNeg-container.log" && NEG_LOGMISS=1
  grep -qiE 'recovery ended before configured recovery target' "$RUN/restoreNeg-container.log" && NEG_LOGFAIL=1
  grep -qiE 'recovery stopping (before|after)' "$RUN/restoreNeg-container.log" && NEG_STOPPING=1
  say "  负向恢复：state=$(show "$NEG_ST") in_recovery=$(show "$NEG_IR") 缺段日志=$NEG_LOGMISS 目标失败日志=$NEG_LOGFAIL 到达目标=$NEG_STOPPING"
  NEG_LOG_LINE="$(grep -m1 -iE "No such file or directory|could not open file|could not restore file|failed to restore file" "$RUN/restoreNeg-container.log" | head -c 300)"
  fact neg_container "$RC_NAME"
  fact neg_missing_segment "$NEG_SEG"
  fact neg_log_evidence "$NEG_LOG_LINE"
  fact neg_target_stop_seen "$NEG_STOPPING"
  say "    缺段日志原文：$(show "$NEG_LOG_LINE")"
  if [ "$NEG_IR" != "f" ] && [ "$NEG_LOGMISS" = "1" ] && [ "$NEG_STOPPING" = "0" ]; then
    NEG_OK=1; say "  缺段负向成立：隐藏必需段后恢复**无法到达目标**（日志有缺段证据）✓"
  else
    fail_check "缺段负向不成立（state=$(show "$NEG_ST") in_recovery=$(show "$NEG_IR") logmiss=$NEG_LOGMISS stopping=$NEG_STOPPING）"
  fi
  NEG_SHA_AFTER="$(docker exec -u postgres "$SRC" sh -c "cd $ARCHIVE_CTR && sha256sum $NEG_SEG 2>/dev/null" | awk '{print $1}')"
  if [ -n "$NEG_SHA_BEFORE" ] && [ "$NEG_SHA_BEFORE" = "$NEG_SHA_AFTER" ]; then
    say "  生产归档段 $NEG_SEG 全程未被修改（sha256 前后一致）✓"; fact neg_prod_untouched True
  else
    fail_check "生产归档段 sha256 前后不一致（before=$(show "$NEG_SHA_BEFORE") after=$(show "$NEG_SHA_AFTER")）"; fact neg_prod_untouched False
  fi
  MOVED_EXISTS="$(docker exec -u postgres "$SRC" sh -c "test -e $ARCHIVE_CTR/__moved && echo yes || echo no")"
  say "  生产归档 __moved 存在性：$MOVED_EXISTS（必须为 no）"
  fact prod_moved_dir "$MOVED_EXISTS"
  [ "$MOVED_EXISTS" = "no" ] || fail_check "生产归档出现 __moved 目录 —— 必须立即人工介入"
fi
fact neg_ok "$([ "$NEG_OK" = "1" ] && echo True || echo False)"

# ===================== 10) 汇总与判定 =====================
say ""
PITR_STAGE="finalize"
say "== 10) 汇总 =="
say "  FAIL=$FAILS  NOT_VERIFIED=$NVS"
while IFS= read -r l; do say "    - $l"; done < "$FAILURES"
while IFS= read -r l; do say "    - NV: $l"; done < "$NOT_VERIFIED"
fact failures_count "$FAILS"
fact not_verified_count "$NVS"

python3 - "$FACTS" "$RESULT" <<'PYEOF'
import json, math, sys
facts = {}
for ln in open(sys.argv[1], errors="replace"):
    if "\t" in ln:
        k, v = ln.rstrip("\n").split("\t", 1); facts[k] = v
def num(k):
    v = facts.get(k, "")
    try: return float(v)
    except Exception: return None
def boolean(k): return facts.get(k) == "True"
def intnum(k):
    try: return int(float(facts.get(k, "")))
    except Exception: return None
fails = intnum("failures_count") or 0
nvs = intnum("not_verified_count") or 0
rpo_m1 = num("rpo_rpo1_marker_seconds"); rpo_m2 = num("rpo_rpo2_marker_seconds")
rpo_l1 = num("rpo_rpo1_last_replay_seconds"); rpo_l2 = num("rpo_rpo2_last_replay_seconds")
ms = [x for x in (rpo_m1, rpo_m2) if x is not None]
ls_ = [x for x in (rpo_l1, rpo_l2) if x is not None]
rpo_marker = max(ms) if ms else None
rpo_last = max(ls_) if ls_ else None
cands = [x for x in (rpo_marker, rpo_last) if x is not None]
rpo_head = max(cands) if cands else None
rto = num("rto_seconds")
at_s = facts.get("archive_timeout_seconds", "")
reasons = []
if fails > 0: reasons.append("存在 %d 条 FAIL" % fails)
if nvs > 0: reasons.append("存在 %d 条 NOT_VERIFIED" % nvs)
if not boolean("target_reached"): reasons.append("未证明恢复到目标时刻（恢复日志 + 目标边界）")
mbp = facts.get("marker_before_present"); mbh = facts.get("marker_before_hash_match"); maa = facts.get("marker_after_absent")
if mbp == "False": reasons.append("目标之前的业务标记未在恢复库中出现（实测缺失）")
elif mbp != "True": reasons.append("目标之前业务标记的存在性未验证（读取为空）")
if mbh == "False": reasons.append("目标之前业务标记的 event_hash 与生产不一致（实测）")
elif mbh != "True": reasons.append("目标之前业务标记的 hash 一致性未验证（读取为空）")
if maa == "False": reasons.append("目标之后提交的业务标记出现在恢复库中（实测 count>0）")
elif maa != "True": reasons.append("目标之后业务标记的存在性未验证（count 读取为空，未构成'已出现'）")
if not boolean("biz_verify_ok"): reasons.append("恢复库就绪业务验证未通过")
if not boolean("neg_ok"): reasons.append("缺段负向未成立（未真实证明缺段不可恢复）")
if intnum("wal_continuity_missing") != 0: reasons.append("WAL 连续性存在缺失段")
if not intnum("wal_continuity_checked"): reasons.append("WAL 连续性未覆盖任何段")
if rpo_marker is None: reasons.append("RPO_marker 未测出（无有效冻结集恢复结果）")
elif rpo_marker > 900: reasons.append("RPO_marker %.1fs 超过 900s" % rpo_marker)
if len(ms) < 2: reasons.append("两轮冻结集恢复未各产出一个 RPO_marker 采样（当前 %d 个）" % len(ms))
if not boolean("rto_ready"):
    reasons.append("RTO 未达成（目标到达/就绪业务验证未完成）→ rto_status=NOT_MEASURED；观测耗时 %.0fs 不计为满足 4h" % (num("elapsed_attempt_seconds") or 0))
elif rto is None: reasons.append("RTO 未计算")
elif rto > 14400: reasons.append("RTO %.0fs 超过 14400s" % rto)
if facts.get("archive_mode") != "on": reasons.append("archive_mode != on")
if intnum("basebackup_rc") != 0: reasons.append("pg_basebackup 未成功")
if intnum("verifybackup_rc") != 0: reasons.append("pg_verifybackup 未通过")
if facts.get("marker_order_ok") != "True": reasons.append("目标前后标记时间序不成立")
if facts.get("neg_prod_untouched") != "True": reasons.append("缺段负向期间生产归档段未被证明只读")
if facts.get("freeze_rpo1_frozen_verify") != "OK": reasons.append("rpo1 冻结副本严格校验未通过")
if facts.get("freeze_rpo2_frozen_verify") != "OK": reasons.append("rpo2 冻结副本严格校验未通过")
if not at_s: reasons.append("archive_timeout 未能从 pg_settings(setting+unit) 换算为秒（不得当 0s RPO）")
if fails > 0: verdict = "FAIL"
elif reasons: verdict = "PARTIAL"
else: verdict = "PASS"
def rr(n):
    p = "freeze_rpo" + n
    q = "rpo_rpo" + n
    return {"t_fail": facts.get(p + "_t_fail"),
            "t_fail_epoch": num(p + "_t_fail_epoch"),
            "cutoff_last_archived_wal": facts.get(p + "_last_archived"),
            "window_age_s": facts.get(p + "_window_age"),
            "segments": facts.get(p + "_segments"),
            "frozen_verify": facts.get(p + "_frozen_verify"),
            "listing_taken_at": facts.get(p + "_listing_taken_at"),
            "rpo_marker_seconds": num(q + "_marker_seconds"),
            "rpo_last_replay_seconds": num(q + "_last_replay_seconds"),
            "t_durable_occurred_at": facts.get(q + "_t_durable"),
            "last_replay_commit_ts": facts.get(q + "_last_replay_ts"),
            "restored_container": facts.get(q + "_container")}
res = {
  "drill": "pitr",
  "ts": facts.get("run_ts"),
  "verdict": verdict,
  "verdict_note": "PASS 需要：真实 basebackup + pg_verifybackup；基于 T_fail 时刻 pg_stat_archiver 已确认完成的连续归档段的冻结集；独立 clone 恢复；恢复日志 + 目标前后业务标记（request_id 唯一绑定）共同证明到达目标；**实测 durability RPO**（T_fail − T_durable，由对冻结集的实际恢复得出）；RTO（T_fail→业务验证完成）；WAL 无缺段；缺段真实负向成立。任一维度未闭合只能是 PARTIAL。",
  "unclosed_reasons": reasons,
  "archive_mode": facts.get("archive_mode"),
  "wal_level": facts.get("wal_level"),
  "pg_version": facts.get("pg_version"),
  "archive_timeout_setting": facts.get("archive_timeout_setting"),
  "archive_timeout_unit": facts.get("archive_timeout_unit"),
  "archive_timeout_seconds": (int(float(at_s)) if at_s else None),
  "reached_target": boolean("target_reached"),
  "target_time": facts.get("target_time"),
  "timeline_restored": facts.get("timeline_restored"),
  "replay_precision_seconds": num("replay_precision_seconds"),
  "replay_precision_note": "**不是 RPO**：目标时刻与恢复库最后重放事务时刻之差",
  # [Codex 06:30] 不得用 int() 截断（51.637 → 51 会被误读成"保守"）。取 ceil 才是保守上取整；
  # 精确 float 同时保留在 rpo_samples_seconds / rpo_rounds.*_seconds 中。
  "rpo_seconds": (math.ceil(rpo_head) if rpo_head is not None else None),
  "rpo_marker_seconds": (math.ceil(rpo_marker) if rpo_marker is not None else None),
  "rpo_last_replay_seconds": (math.ceil(rpo_last) if rpo_last is not None else None),
  "rpo_seconds_exact": rpo_head,
  "rpo_seconds_semantics": "ceil(精确秒)；精确样本见 rpo_samples_seconds 与 rpo_rounds",
  "rpo_limit_seconds_exact": 900.0,
  "rpo_samples_seconds": ms,
  "rpo_status": "MEASURED" if ms else "NOT_MEASURED",
  "rpo_method": "T_fail（模拟故障时刻）− T_durable；T_durable 取二者保守值：(a) 对 T_fail 冻结归档集实际恢复后库中最新**可见且 hash 一致**的业务标记 occurred_at（事件时间，早于提交时间 → RPO 保守偏大）；(b) 恢复库 pg_last_xact_replay_timestamp()（WAL 回放到的最后一次事务提交时间，真实 commit 时间）。两者均不得为负且 <=900s。",
  "rpo_time_sources": {
    "t_fail": "生产库 now()（与 pg_stat_archiver.last_archived_wal 截止边界同一次读取）",
    "t_durable_marker": "audit_events_v2.occurred_at（应用写入的事件时间；早于提交时刻 → RPO 保守偏大）",
    "t_durable_commit": "pg_last_xact_replay_timestamp()（WAL 回放到的最后一次事务提交时间）",
    "clock": "同一台主机、同一 PG 实例时钟，无跨机偏移"
  },
  "rpo_limit_seconds": 900,
  "rpo_rounds": {"rpo1": rr("1"), "rpo2": rr("2")},
  "rto_seconds": (int(rto) if (rto is not None and boolean("rto_ready")) else None),
  "rto_status": "MEASURED" if boolean("rto_ready") else "NOT_MEASURED",
  "rto_ready": boolean("rto_ready"),
  "elapsed_attempt_seconds": intnum("elapsed_attempt_seconds"),
  "rto_method": "T_fail（灾难时刻）→ base clone + 恢复实例启动 + WAL 重放 + 就绪业务验证完成；仅当 target 到达且业务验证通过才计为有效 RTO",
  "rto_breakdown_seconds": {"clone": intnum("rto_clone_seconds"), "recovery": intnum("rto_recovery_seconds")},
  "rto_limit_seconds": 14400,
  "base_backup_dir": facts.get("base_dir"),
  "basebackup_seconds": intnum("basebackup_seconds"),
  "pgdata_size": facts.get("pgdata_size"),
  "db_size": facts.get("db_size"),
  "path_facts": facts.get("path_facts"),
  "base_manifest_start_lsn": facts.get("manifest_start_lsn"),
  "base_manifest_end_lsn": facts.get("manifest_end_lsn"),
  "wal_continuity": {"checked": intnum("wal_continuity_checked"), "missing": intnum("wal_continuity_missing"),
                     "range_start": facts.get("range_start_segment"), "boundary_last": facts.get("boundary_last_segment"),
                     "range_start_in_base_pgwal": facts.get("range_start_in_base")},
  "markers": {
    "before": {"seq": facts.get("marker_before_seq"), "occurred_at": facts.get("marker_before_ts"),
               "hash": facts.get("marker_before_hash"), "request_id": facts.get("marker_before_request_id")},
    "filler": {"seq": facts.get("marker_filler_seq"), "occurred_at": facts.get("marker_filler_ts"),
               "request_id": facts.get("marker_filler_request_id")},
    "after": {"seq": facts.get("marker_after_seq"), "occurred_at": facts.get("marker_after_ts"),
              "hash": facts.get("marker_after_hash"), "request_id": facts.get("marker_after_request_id")},
    "restored": facts.get("restored_seqs"),
    "order_ok": facts.get("marker_order_ok")
  },
  "negative_missing_segment": {"ok": boolean("neg_ok"), "segment": facts.get("neg_missing_segment"),
                               "container": facts.get("neg_container"), "log_evidence": facts.get("neg_log_evidence"),
                               "target_stop_seen": facts.get("neg_target_stop_seen"),
                               "prod_archive_sha_unchanged": boolean("neg_prod_untouched")},
  "prod_archive_untouched": boolean("neg_prod_untouched"),
  "prod_moved_dir": facts.get("prod_moved_dir"),
  "biz_verify": {"ok": boolean("biz_verify_ok"), "auditverify_rc": intnum("auditverify_rc"),
                 "prod_migrations": facts.get("prod_migrations")},
  "script_sha256": facts.get("script_sha256"),
  "failures_count": fails,
  "not_verified_count": nvs,
  "evidence_dir": facts.get("run_dir"),
  "report": sys.argv[2].replace(".json", ".md"),
  "cleanup": "所有容器/基础备份/克隆/冻结副本与证据一律保留，不自动清理",
}
json.dump(res, open(sys.argv[2], "w"), indent=2, ensure_ascii=False)
print(verdict)
PYEOF
VERDICT="$(python3 -c "import json;print(json.load(open('$RESULT')).get('verdict','MISSING'))" 2>/dev/null)"
say ""
say "  **判定：$VERDICT**"
python3 -c "
import json
d=json.load(open('$RESULT'))
for r in d.get('unclosed_reasons') or []: print('    - %s' % r)
" >> "$REPORT" 2>/dev/null || true
say "  JSON：$RESULT"
say "  报告：$REPORT"
say "  - 恢复实例/基础备份/克隆/冻结副本**保留不清理**：$RA_NAME / $RB1_NAME / $RB2_NAME / $RC_NAME / $BASE_HOST"
say "  - 清理需人工确认后执行（脚本不代劳）"
case "$VERDICT" in
  PASS) echo "PITR 演练：PASS（base+verify、冻结集恢复达目标、实测 durability RPO、RTO、缺段负向全部成立）"; exit 0 ;;
  FAIL) echo "PITR 演练：FAIL（见 $RUN/FAILURES.txt）" >&2; exit 1 ;;
  *)    echo "PITR 演练：PARTIAL/INCOMPLETE（verdict=$VERDICT，未闭合项见 $RESULT）" >&2; exit 3 ;;
esac
