#!/usr/bin/env bash
# S7-DR-01：备份恢复演练（PG / ClickHouse / MinIO），产出 RPO/RTO 证据。
#
# 【独立复核修复，2026-09】编号对应 scripts/backup/README-DR-FIXES.md：
#   [F7] 恢复目标：不再 DROP + CREATE（也不再 DROP 已有的 CH 库 / PG 库 / MinIO 前缀）。
#        每次演练用带**独立随机 UUID 后缀**的新目标名；目标已存在则显式拒绝并非 0 退出（绝不覆盖）；
#        演练结束**不自动清理**，目标名 / 脱敏连接串 / 日志路径全部写进结果文件（.md 与 .json），
#        失败的目标一律保留供人工诊断。
#   [F8] 恢复失败不再被吞：pg_restore、CH 归档解包、RESTORE 语句、逐表/逐对象核对、auditverify、
#        对象引用核对，任一不通过都会写 FAIL 并以非 0 退出；不再把 2>/dev/null 的空值当成 0 或当成通过。
#   [F9] 开始前必须校验源 manifest：manifest.json 存在；files 中每个文件都存在且 size/sha256 与实际一致；
#        带 __FAILED 的目录直接拒绝使用；**禁止盲选最近一个 20* 目录**——备份目录必须显式传入，缺参报错。
#   [F10] MinIO 不再只恢复 blockchain-reports：遍历 manifest 记录的全部桶，逐桶比对对象数、键集合、
#        每个对象的 size 与内容（单分片 etag=MD5 直接比对；多分片对象按预算抽样 mc cat + sha256 比对）；
#        版本化 / 生命周期 / 对象元数据 mc mirror 无法覆盖，报告必须显式写 NOT_VERIFIED，不得默认通过。
#
# 用法：bash scripts/backup/restore-drill.sh <备份目录>
# 环境变量：
#   RESTORE_DRILL_ACCEPT_INCOMPLETE=1   显式接受带 __INCOMPLETE / __INCONSISTENT 的备份（会记录该接受动作）
#   RESTORE_DRILL_REQUIRED_BUCKETS      必须覆盖的桶（默认 5 个）
#   RESTORE_DRILL_CH_TABLES             需要 count() ... FINAL 比对的 CH 表
#   RESTORE_DRILL_OBJ_SAMPLE            对象引用抽样条数
#   RESTORE_DRILL_STREAM_BUDGET         每桶流式校验的字节预算（默认 256 MiB）
#   RESTORE_DRILL_STREAM_SAMPLE_MAX     每桶流式校验的对象数上限（默认 50）
#   RESTORE_DRILL_MINIO_META_VERIFY     1 = 在本轮演练的目标桶上就地调用 minio-metadata-verify.sh verify
#                                       做版本/ILM/标签/元数据取证（要求备份自带 bucket-semantics.json）；
#                                       工具非 0 或无产物 = fail_check + 仍然 NV。
#   RESTORE_DRILL_MINIO_META_RESULT     可选：scripts/backup/minio-metadata-verify.sh verify 的产物路径。
#                                       传入且复核通过（drill / verdict / 四维度 / 基线同源 / 目标桶一一对应）时，
#                                       §3.1 的版本·ILM·标签·元数据维度记为"独立证据已覆盖"并记录该文件 sha256；
#                                       复核不通过 = fail_check + 仍然 NV（绝不静默放行，也绝不静默退回 NV）
# 输出（acceptance-evidence/dual-system-v1/sprint7/dr/）：
#   restore-drill-<TS>.md          报告
#   restore-drill-<TS>.json        机器可读结果（目标名 / 脱敏连接串 / 日志路径 / 判定）
#   restore-drill-<TS>.logs/       原始命令日志（全部保留）
#   FAILED-restore-drill-<TS>.md   仅在存在 FAIL 时写出（失败标记，绝不自动删除）
# 退出码：0 = 全部核对通过；1 = 存在 FAIL；2 = 前置校验/参数错误（未创建任何演练目标）

set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/dr}"
MC_IMAGE="${MC_IMAGE:-minio/mc:latest}"
ALLOW_INCOMPLETE="${RESTORE_DRILL_ACCEPT_INCOMPLETE:-0}"
REQUIRED_BUCKETS="${RESTORE_DRILL_REQUIRED_BUCKETS:-blockchain-audit blockchain-backups blockchain-exports blockchain-reports blockchain-raw}"
CH_TABLES="${RESTORE_DRILL_CH_TABLES:-transactions token_transfers address_activity logs}"
OBJ_SAMPLE="${RESTORE_DRILL_OBJ_SAMPLE:-200}"
STREAM_BUDGET="${RESTORE_DRILL_STREAM_BUDGET:-268435456}"
STREAM_SAMPLE_MAX="${RESTORE_DRILL_STREAM_SAMPLE_MAX:-50}"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
FENCE='```'

if [ "$#" -lt 1 ] || [ -z "$1" ]; then
  echo "用法：bash scripts/backup/restore-drill.sh <备份目录>" >&2
  echo "必须**显式传入**备份目录；本脚本不再盲选最近一个 20* 目录（那会让演练对象不可审计）。" >&2
  exit 2
fi
SRC="$(cd "$1" 2>/dev/null && pwd)" || { echo "备份目录不存在或不可进入：$1" >&2; exit 2; }

mkdir -p "$EV" || { echo "无法创建 $EV" >&2; exit 2; }
LOG_DIR="$EV/restore-drill-$TS.logs"
mkdir -p "$LOG_DIR" || { echo "无法创建 $LOG_DIR" >&2; exit 2; }
REPORT="$EV/restore-drill-$TS.md"
RESULT="$EV/restore-drill-$TS.json"
FAILURES="$LOG_DIR/FAILURES.txt"
NOT_VERIFIED="$LOG_DIR/NOT_VERIFIED.txt"
NOT_VERIFIED_STRUCTURAL="$LOG_DIR/NOT_VERIFIED.structural.txt"
NOT_VERIFIED_CHECK="$LOG_DIR/NOT_VERIFIED.check.txt"
PARTIAL="$LOG_DIR/PARTIAL.txt"
: > "$FAILURES"
: > "$NOT_VERIFIED"
: > "$NOT_VERIFIED_STRUCTURAL"
: > "$NOT_VERIFIED_CHECK"
: > "$PARTIAL"
: > "$LOG_DIR/result.kv"

fail_check() { # 记录 FAIL：FAILURES 文件 + 报告；最终非 0 退出并写 FAILED-* 标记
  echo "$1" >> "$FAILURES"
  echo "FAIL: $1" | tee -a "$REPORT" >&2
}
# [GATE-01] NOT_VERIFIED 分级（**仅用于诊断，不构成任何豁免**）：
#   structural = 脚本层结构性无法证明（需另配工具 / 产品侧改造才能真正闭合）
#   check      = 本次核对未完成（环境/取值/前置缺失）
#   两种分级都**不构成通过**：scripts/s7/dr-gate.sh 对任何分级一律阻断整体验收，
#   只有把对应能力真实补齐（而不是改写分级）才可能让 gate 归零。
not_verified() { # [class] message  或  message（默认 check）
  local cls="check" msg="$1"
  if [ "$#" -ge 2 ]; then cls="$1"; msg="$2"; fi
  case "$cls" in structural|check) ;; *) cls="check" ;; esac
  printf '%s|%s\n' "$cls" "$msg" >> "$NOT_VERIFIED"
  if [ "$cls" = "structural" ]; then printf '%s\n' "$msg" >> "$NOT_VERIFIED_STRUCTURAL"
  else printf '%s\n' "$msg" >> "$NOT_VERIFIED_CHECK"; fi
  echo "NOT_VERIFIED[$cls]: $msg" | tee -a "$REPORT" >&2
}
# [GATE-02] 部分覆盖与 NV 同等对待：不得计入通过，且必须阻断 gate。
partial() {
  printf '%s\n' "$1" >> "$PARTIAL"
  echo "PARTIAL: $1" | tee -a "$REPORT" >&2
}
kv() { echo "$1=$2" >> "$LOG_DIR/result.kv"; }
kv_get() { sed -n "s/^$1=//p" "$LOG_DIR/result.kv" | head -n 1; }
cleanup_mc_tmp() { rm -f "$LOG_DIR"/mc-tmp.* 2>/dev/null || true; }
abort_hard() { # 前置/目标冲突：立即中止（未创建任何演练目标）
  echo "ABORT: $*" >&2
  cleanup_mc_tmp
  exit 2
}

# ---------- [F9] 源备份目录前置校验 ----------
[ -f "$SRC/manifest.json" ] || abort_hard "缺少 manifest.json：$SRC"
if [ -e "$SRC/__FAILED" ]; then
  echo "该备份带 __FAILED 标记，内容如下：" >&2
  cat "$SRC/__FAILED" >&2
  abort_hard "拒绝使用失败的备份目录 $SRC"
fi
if [ -e "$SRC/__INCOMPLETE" ] || [ -e "$SRC/__INCONSISTENT" ]; then
  if [ "$ALLOW_INCOMPLETE" != "1" ]; then
    echo "该备份带 __INCOMPLETE / __INCONSISTENT 标记：" >&2
    cat "$SRC/__INCOMPLETE" "$SRC/__INCONSISTENT" >&2
    abort_hard "备份本身不完整或一致性边界未建立；如确需演练请显式设置 RESTORE_DRILL_ACCEPT_INCOMPLETE=1（报告中会记录该接受动作）"
  fi
  echo "WARN: 已通过 RESTORE_DRILL_ACCEPT_INCOMPLETE=1 显式接受一个不完整/不一致的备份，报告会记录该事实" >&2
fi

echo "== 校验备份 manifest（逐文件 size/sha256，大备份需要几分钟）=="
python3 - "$SRC" "$LOG_DIR/manifest_check.env" "$LOG_DIR/buckets.list" "$LOG_DIR/skipped.list" "$LOG_DIR/manifest_check.txt" <<'PY'
import hashlib, json, os, re, shlex, sys
src, env_out, bucket_out, skipped_out, txt_out = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]
lines = []
def say(s):
    lines.append(s)
    print(s)
m = json.load(open(os.path.join(src, "manifest.json")))
files = m.get("files") or {}
missing, size_bad, sha_bad = [], [], []
verified = 0
bytes_total = 0
for rel in sorted(files):
    meta = files[rel] or {}
    p = os.path.join(src, rel)
    if not os.path.isfile(p):
        missing.append(rel)
        continue
    size = os.path.getsize(p)
    if meta.get("size") is not None and size != meta.get("size"):
        size_bad.append(rel)
        continue
    want = meta.get("sha256")
    if want:
        h = hashlib.sha256()
        with open(p, "rb") as f:
            for chunk in iter(lambda: f.read(1 << 20), b""):
                h.update(chunk)
        if h.hexdigest() != want:
            sha_bad.append(rel)
            continue
    verified += 1
    bytes_total += size
extra = []
for root, _, names in os.walk(src):
    for n in names:
        # 与 manifest 键保持同一约定：一律用 / 分隔
        rel = os.path.relpath(os.path.join(root, n), src).replace(os.sep, "/")
        if rel in files or n == "manifest.json":
            continue
        extra.append(rel)
say("manifest 文件条目 %d：核对通过 %d，缺失 %d，size 不符 %d，sha256 不符 %d，合计 %d 字节" % (len(files), verified, len(missing), len(size_bad), len(sha_bad), bytes_total))
for lst, label in ((missing, "缺失"), (size_bad, "size 不符"), (sha_bad, "sha256 不符")):
    if lst:
        say("%s（最多列 10 个）：%s" % (label, ", ".join(lst[:10])))
if extra:
    say("manifest 未登记的额外文件（不在校验链内，仅供参考）：%s" % ", ".join(sorted(extra)[:10]))
buckets = {}
for name, cnt in sorted((m.get("minio") or {}).items()):
    if not re.match(r"[A-Za-z0-9][A-Za-z0-9._-]*$", str(name)):
        say("manifest.minio 中桶名非法，已忽略：%s" % name)
        continue
    buckets[name] = cnt if cnt is not None else "UNKNOWN"
skipped = []
for b in ((m.get("minio_detail") or {}).get("skipped_buckets") or []):
    if re.match(r"[A-Za-z0-9][A-Za-z0-9._-]*$", str(b)):
        skipped.append(b)
    else:
        say("manifest 中 skipped 桶名非法，已忽略：%s" % b)
with open(bucket_out, "w") as f:
    for name in sorted(buckets):
        f.write(name + " " + str(buckets[name]) + chr(10))
with open(skipped_out, "w") as f:
    for name in skipped:
        f.write(name + chr(10))
def scalar(v):
    # 用 shlex.quote 输出，保证值里出现空格 / 括号 / 引号时 . envfile 仍然安全
    # （例如 snapshot 缺失时写的是 "ERROR:未建立导出快照（...）"）
    if isinstance(v, bool):
        return shlex.quote("true" if v else "false")
    if v is None:
        return shlex.quote("")
    return shlex.quote(str(v))
pg = m.get("postgres") or {}
ch = m.get("clickhouse") or {}
cons = m.get("consistency") or {}
ok = (not missing) and (not size_bad) and (not sha_bad) and len(files) > 0
ok = ok and ("postgres.dump" in files) and ("clickhouse.tar.gz" in files)
with open(env_out, "w") as f:
    f.write("MANIFEST_BACKUP_ID=" + scalar(m.get("backup_id")) + chr(10))
    f.write("MANIFEST_COMPLETE=" + scalar(m.get("complete")) + chr(10))
    f.write("MANIFEST_PG_SNAPSHOT_CONSISTENT=" + scalar(m.get("pg_snapshot_consistent")) + chr(10))
    f.write("MANIFEST_CROSS_STORE=" + scalar(cons.get("cross_store_consistency")) + chr(10))
    f.write("MANIFEST_MIGRATION_COUNT=" + scalar(pg.get("migration_count")) + chr(10))
    f.write("MANIFEST_AUDIT_HEAD_SEQ=" + scalar(pg.get("audit_head_seq")) + chr(10))
    f.write("MANIFEST_AUDIT_EVENT_COUNT=" + scalar(pg.get("audit_event_count")) + chr(10))
    f.write("MANIFEST_PG_SNAPSHOT_ID=" + scalar(pg.get("pg_snapshot_id")) + chr(10))
    f.write("MANIFEST_PG_WAL_LSN=" + scalar(pg.get("pg_current_wal_lsn")) + chr(10))
    f.write("MANIFEST_CH_TABLE_COUNT=" + scalar(ch.get("table_count")) + chr(10))
    f.write("FILES_LISTED=" + str(len(files)) + chr(10))
    f.write("FILES_VERIFIED=" + str(verified) + chr(10))
    f.write("FILES_MISSING=" + str(len(missing)) + chr(10))
    f.write("FILES_SIZE_MISMATCH=" + str(len(size_bad)) + chr(10))
    f.write("FILES_SHA_MISMATCH=" + str(len(sha_bad)) + chr(10))
    f.write("EXTRA_FILES=" + str(len(extra)) + chr(10))
    f.write("CHECK_OK=" + ("true" if ok else "false") + chr(10))
with open(txt_out, "w") as f:
    f.write(chr(10).join(lines) + chr(10))
PY
MANIFEST_CHECK_RC=$?
if [ "$MANIFEST_CHECK_RC" -ne 0 ]; then abort_hard "manifest 校验脚本执行失败（rc=$MANIFEST_CHECK_RC）"; fi
set -a; . "$LOG_DIR/manifest_check.env"; set +a
if [ "$CHECK_OK" != "true" ]; then
  echo "manifest 校验未通过（缺失 $FILES_MISSING / size $FILES_SIZE_MISMATCH / sha256 $FILES_SHA_MISMATCH）：" >&2
  cat "$LOG_DIR/manifest_check.txt" >&2
  abort_hard "备份内容与 manifest 不一致，禁止用于恢复演练"
fi
echo "manifest 校验通过：$FILES_VERIFIED/$FILES_LISTED 个文件 size+sha256 一致"

set -a
if ! . "$PROJECT/deploy/.env"; then
  abort_hard "无法加载 $PROJECT/deploy/.env"
fi
set +a
MC_HOST_S3="${MC_HOST_S3:-http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9001}"
mc_plain() { docker run --rm --network host -e "MC_HOST_s3=$MC_HOST_S3" "$MC_IMAGE" "$@"; }
mc_vol() { local mnt="$1"; shift; docker run --rm --network host -v "$mnt" -e "MC_HOST_s3=$MC_HOST_S3" "$MC_IMAGE" "$@"; }
psql_admin() { docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d postgres -Atc "$1"; }
psql_prod() { docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"; }
psql_rest() { docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$PGR" -Atc "$1"; }
psql_rest_f() { docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$PGR" -At -F '|' -c "$1"; }
chq() { docker exec deploy-clickhouse-1 clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --query "$1"; }
is_num() { case "$1" in ''|*[!0-9]*) return 1 ;; *) return 0 ;; esac; }
num_of() { echo "$1" | grep -E '^[0-9]+' | tail -n 1; }

UUID12="$(python3 -c 'import uuid;print(uuid.uuid4().hex[:12])')" || abort_hard "无法生成 UUID"
case "$UUID12" in *[!0-9a-f]*) abort_hard "UUID 生成异常：$UUID12" ;; esac
PGR="blockchain_platform_dr_$UUID12"
CHR="${CLICKHOUSE_DB}_dr_$UUID12"
CH_BACKUP_NAME="$(basename "$SRC")"

{
echo "# 备份恢复演练报告（$TS）"
echo
echo "- 备份源：$SRC（**显式传入**，非自动挑选）"
echo "- manifest 校验：$FILES_VERIFIED/$FILES_LISTED 文件 size+sha256 一致（缺失 $FILES_MISSING / size 不符 $FILES_SIZE_MISMATCH / sha256 不符 $FILES_SHA_MISMATCH）"
echo "- backup_id：$MANIFEST_BACKUP_ID；manifest.complete=$MANIFEST_COMPLETE；pg_snapshot_consistent=$MANIFEST_PG_SNAPSHOT_CONSISTENT；cross_store_consistency=$MANIFEST_CROSS_STORE"
echo "- 演练目标后缀（随机 UUID）：$UUID12 —— 目标一律新建、**不覆盖**、**演练后不自动清理**"
echo
echo "## 0. 演练目标与日志"
echo
echo "| 组件 | 目标 | 备注 |"
echo "|---|---|---|"
echo "| PostgreSQL | $PGR | 独立新库；连接串见下（密码已脱敏） |"
echo "| ClickHouse | $CHR | RESTORE ... AS 独立新库；生产库 $CLICKHOUSE_DB 全程不被触碰 |"
echo "| MinIO | 逐桶 <bucket>-drill-$UUID12 | 每个备份桶一个新的 bucket；不触碰生产桶 |"
echo "| 日志目录 | $LOG_DIR | 命令原始输出全部保留 |"
echo
echo "PostgreSQL 连接串（脱敏）：postgres://$POSTGRES_USER:REDACTED@127.0.0.1:$POSTGRES_PORT/$PGR?sslmode=disable"
echo
} > "$REPORT"

# =====================================================================
# 1. PostgreSQL
# =====================================================================
echo "== PostgreSQL =="
PG_EXISTS="$(psql_admin "select 1 from pg_database where datname='$PGR'" 2>"$LOG_DIR/pg_exists.err")"
PG_EXISTS_RC=$?
if [ "$PG_EXISTS_RC" -ne 0 ]; then abort_hard "无法查询目标库是否存在（rc=$PG_EXISTS_RC）：$(paste -sd ' ' "$LOG_DIR/pg_exists.err")"; fi
if [ -n "$PG_EXISTS" ]; then abort_hard "目标库 $PGR 已存在：显式拒绝覆盖（本脚本不做 DROP DATABASE）"; fi
if ! psql_admin "CREATE DATABASE $PGR OWNER $POSTGRES_USER" > "$LOG_DIR/pg_create.log" 2>&1; then
  abort_hard "创建目标库 $PGR 失败：$(paste -sd ' ' "$LOG_DIR/pg_create.log")"
fi
echo "  created database $PGR"

docker cp "$SRC/postgres.dump" deploy-postgres-1:/tmp/drill.dump > "$LOG_DIR/pg_docker_cp.log" 2>&1
CP_RC=$?
if [ "$CP_RC" -ne 0 ]; then fail_check "docker cp postgres.dump 失败（rc=$CP_RC，日志 $LOG_DIR/pg_docker_cp.log）"; fi

T0="$(date +%s.%N)"
# [F15] 恢复必须**保留 ACL**：--no-privileges 会丢掉 GRANT，恢复库虽然"数据都在"，
#   但生产运行时角色（DB_RUNTIME_ROLE：API/Worker 通过 SET ROLE 使用）读不到表 —— 实测
#   auditverify 直接报 permission denied for table audit_events_v2（SQLSTATE 42501），
#   即"恢复成功"但恢复库**不可被业务角色使用**。本演练在同一集群内恢复，角色是集群级对象，
#   保留授权可原样重建；丢弃授权只会得到一个语义上不可用的库（2026-09-16 演练发现的工具缺陷）。
docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 pg_restore -U "$POSTGRES_USER" -d "$PGR" -j 4 --no-owner /tmp/drill.dump > "$LOG_DIR/pg_restore.log" 2>&1
PG_RC=$?
T1="$(date +%s.%N)"
PG_RTO="$(python3 -c "print(round($T1-$T0,2))")"
if [ "$PG_RC" -ne 0 ]; then
  fail_check "pg_restore 退出码 $PG_RC（非 0 即视为恢复失败，绝不忽略；日志 $LOG_DIR/pg_restore.log，末行：$(tail -n 1 "$LOG_DIR/pg_restore.log")）"
fi
kv pg_target "$PGR"
kv pg_dsn_redacted "postgres://$POSTGRES_USER:REDACTED@127.0.0.1:$POSTGRES_PORT/$PGR?sslmode=disable"
kv pg_restore_rc "$PG_RC"
kv pg_rto_seconds "$PG_RTO"

# [F15] 运行时角色可用性（fail-closed）：恢复库必须能被**生产业务角色**读取，
#   否则"恢复成功"只是数据意义上的成功，业务意义上不可用（起因见上方 pg_restore 注释）。
RUNTIME_ROLE="${DB_RUNTIME_ROLE:-}"
PRIV_OUT=""
GRANT_N=""
if [ -z "$RUNTIME_ROLE" ]; then
  fail_check "DB_RUNTIME_ROLE 为空：无法证明恢复库可被运行时业务角色使用（deploy/.env 应提供该角色）"
else
  PRIV_OUT="$(docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$PGR" -Atc "set role $RUNTIME_ROLE; select 'audit_events_v2=' || count(*) from audit_events_v2" 2>"$LOG_DIR/pg_priv_check.err")"
  PRIV_RC=$?
  if [ "$PRIV_RC" -ne 0 ] || ! printf '%s' "$PRIV_OUT" | grep -q '^audit_events_v2='; then
    fail_check "恢复库对运行时角色 $RUNTIME_ROLE 不可用（rc=$PRIV_RC，输出：$(printf '%s' "$PRIV_OUT" | head -n 1)；见 $LOG_DIR/pg_priv_check.err）—— 恢复语义要求可被业务角色使用"
  else
    echo "  运行时角色 $RUNTIME_ROLE 读取 audit_events_v2：$PRIV_OUT"
  fi
  GRANT_N="$(docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$PGR" -Atc "select count(*) from information_schema.role_table_grants where grantee='$RUNTIME_ROLE' and table_name='audit_events_v2'" 2>>"$LOG_DIR/pg_priv_check.err" | tr -d ' ')"
  if is_num "$GRANT_N"; then
    [ "$GRANT_N" -ge 1 ] || fail_check "恢复库中运行时角色 $RUNTIME_ROLE 在 audit_events_v2 上没有任何 GRANT（ACL 未恢复）"
  else
    fail_check "无法查询恢复库的 role_table_grants（输出：$(printf '%s' "$GRANT_N" | head -n 1)）"
  fi
  kv pg_runtime_role "$RUNTIME_ROLE"
  kv pg_runtime_role_grants_audit_events_v2 "$GRANT_N"
  kv pg_runtime_role_read_audit_events_v2 "$PRIV_OUT"
fi
{
echo "## 1. PostgreSQL 恢复"
echo
echo "- 目标库：$PGR（新建；演练后保留，不 DROP）"
echo "- 命令：pg_restore -j 4 --no-owner --no-privileges，退出码 **$PG_RC**（非 0 即 FAIL）"
echo "- **RTO（恢复到可用）= $PG_RTO s**"
echo "- 日志：$LOG_DIR/pg_restore.log"
echo
echo "| 表 | 生产 | 恢复库 | 差值（备份后新增 = RPO 暴露） |"
echo "|---|---:|---:|---:|"
} >> "$REPORT"

TABLES="users identities sessions login_events audit_events_v2 cases investigations investigation_reports download_jobs download_subjects download_datasets download_shards download_pages provider_usage checkpoints exports export_tickets notifications workspace_addresses outbox_events audit_chain_anchors"
TOTAL_DELTA=0
for t in $TABLES; do
  live="$(psql_prod "select count(*) from $t" 2>>"$LOG_DIR/pg_count_live.err")"; lrc=$?
  live="$(num_of "$live")"
  rest="$(psql_rest "select count(*) from $t" 2>>"$LOG_DIR/pg_count_rest.err")"; rrc=$?
  rest="$(num_of "$rest")"
  if [ "$lrc" -ne 0 ] || ! is_num "$live" || [ "$rrc" -ne 0 ] || ! is_num "$rest"; then
    echo "| $t | ERROR(rc=$lrc) | ERROR(rc=$rrc) | 无法比对（查询失败，禁止按 0 处理） |" >> "$REPORT"
    fail_check "行数比对查询失败：表 $t（生产 rc=$lrc / 恢复库 rc=$rrc，见 $LOG_DIR/pg_count_live.err 与 pg_count_rest.err）"
    continue
  fi
  d=$((live - rest))
  TOTAL_DELTA=$((TOTAL_DELTA + d))
  if [ "$rest" -gt "$live" ]; then
    fail_check "恢复库行数大于生产：表 $t（生产 $live < 恢复 $rest），需要人工解释"
  fi
  echo "| $t | $live | $rest | $d |" >> "$REPORT"
done

{
echo
echo "### 1.0 运行时角色可用性（恢复库必须可被业务角色使用）"
echo
echo "- 运行时角色：${RUNTIME_ROLE:-（未取得）}"
echo "- set role 后读取 audit_events_v2：${PRIV_OUT:-（未取得）}"
echo "- 该角色在 audit_events_v2 上的 GRANT 数：${GRANT_N:-（未取得）}"
echo "  （--no-privileges 会丢掉 GRANT：数据在、但业务角色用不了；本演练要求两者同时成立）"
} >> "$REPORT"

REST_MIG="$(psql_rest 'select count(*) from schema_migrations' 2>>"$LOG_DIR/pg_meta.err")"; REST_MIG_RC=$?
REST_MIG="$(num_of "$REST_MIG")"
REST_HEAD="$(psql_rest "select coalesce(max(seq),0) from audit_events_v2 where chain='audit'" 2>>"$LOG_DIR/pg_meta.err")"
REST_HEAD="$(num_of "$REST_HEAD")"
REST_CNT="$(psql_rest "select count(*) from audit_events_v2 where chain='audit'" 2>>"$LOG_DIR/pg_meta.err")"
REST_CNT="$(num_of "$REST_CNT")"
{
echo
echo "### 1.1 与备份点（manifest）交叉核对"
echo
echo "- 迁移版本：恢复库 $REST_MIG / manifest $MANIFEST_MIGRATION_COUNT"
echo "- audit 链 head seq：恢复库 $REST_HEAD / manifest $MANIFEST_AUDIT_HEAD_SEQ"
echo "- audit 事件数：恢复库 $REST_CNT / manifest $MANIFEST_AUDIT_EVENT_COUNT"
echo "  （manifest 的审计边界取自备份时的导出快照，因此这三项是真正的备份点一致性核对）"
} >> "$REPORT"
if [ "$REST_MIG_RC" -ne 0 ]; then fail_check "恢复库元数据查询失败（见 $LOG_DIR/pg_meta.err）"; fi
if is_num "$REST_MIG" && is_num "$MANIFEST_MIGRATION_COUNT"; then
  if [ "$REST_MIG" != "$MANIFEST_MIGRATION_COUNT" ]; then fail_check "恢复库迁移版本 $REST_MIG != manifest $MANIFEST_MIGRATION_COUNT"; fi
else
  not_verified check "迁移版本与 manifest 的一致性（取值缺失）"
fi
if is_num "$REST_HEAD" && is_num "$MANIFEST_AUDIT_HEAD_SEQ"; then
  if [ "$REST_HEAD" != "$MANIFEST_AUDIT_HEAD_SEQ" ]; then fail_check "恢复库 audit head seq $REST_HEAD != manifest 备份点 $MANIFEST_AUDIT_HEAD_SEQ"; fi
else
  not_verified check "audit head seq 与 manifest 备份点的一致性（旧版 manifest 无该字段或恢复库查询失败）"
fi
if is_num "$REST_CNT" && is_num "$MANIFEST_AUDIT_EVENT_COUNT"; then
  if [ "$REST_CNT" != "$MANIFEST_AUDIT_EVENT_COUNT" ]; then fail_check "恢复库 audit 事件数 $REST_CNT != manifest 备份点 $MANIFEST_AUDIT_EVENT_COUNT"; fi
else
  not_verified check "audit 事件数与 manifest 备份点的一致性（旧版 manifest 无 audit_event_count）"
fi

DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:$POSTGRES_PORT/$PGR?sslmode=disable" "$PROJECT/backend/bin/auditverify" > "$LOG_DIR/auditverify.out" 2> "$LOG_DIR/auditverify.err"
AV_RC=$?
{
echo
echo "### 1.2 审计链校验（恢复库，只读工具 auditverify）"
echo
echo "$FENCE"json
head -n 60 "$LOG_DIR/auditverify.out"
echo "$FENCE"
echo "- auditverify 退出码：**$AV_RC**（0=通过；1=链不通过；2=参数/连接错误）"
echo "- 日志：$LOG_DIR/auditverify.out 与 $LOG_DIR/auditverify.err"
} >> "$REPORT"
if [ "$AV_RC" -ne 0 ]; then
  fail_check "auditverify 未通过（rc=$AV_RC，见 $LOG_DIR/auditverify.out 与 .err）"
fi

echo "== 对象引用核对 =="
psql_rest_f "select object_key, content_hash, size_bytes from investigation_reports order by id limit $OBJ_SAMPLE" > "$LOG_DIR/refs_reports.tsv" 2>"$LOG_DIR/refs_reports.err"
REFS_RC=$?
if [ "$REFS_RC" -ne 0 ]; then
  fail_check "对象引用查询失败（investigation_reports，rc=$REFS_RC，见 $LOG_DIR/refs_reports.err）"
elif grep -q "^blockchain-reports " "$LOG_DIR/buckets.list"; then
  REF_OUT="$(python3 - "$SRC" "$LOG_DIR/refs_reports.tsv" "blockchain-reports" <<'PY'
import hashlib, os, sys
src, tsv, bucket = sys.argv[1], sys.argv[2], sys.argv[3]
checked = ok = bad = skipped = 0
problems = []
for line in open(tsv):
    line = line.rstrip(chr(10))
    if not line:
        continue
    parts = line.split("|")
    if len(parts) != 3:
        skipped += 1
        continue
    key, want_hash, want_size = parts
    if not key:
        skipped += 1
        continue
    p = os.path.join(src, "minio", bucket, key)
    checked += 1
    if not os.path.isfile(p):
        bad += 1
        problems.append("对象缺失于备份：" + key)
        continue
    size = os.path.getsize(p)
    if want_size.isdigit() and size != int(want_size):
        bad += 1
        problems.append("对象大小不符：%s（备份 %d / 行内 %s）" % (key, size, want_size))
        continue
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    if want_hash and h.hexdigest() != want_hash:
        bad += 1
        problems.append("对象内容 sha256 与 investigation_reports.content_hash 不符：" + key)
        continue
    ok += 1
print("对象引用核对（investigation_reports -> %s）：检查 %d，通过 %d，失败 %d，跳过 %d" % (bucket, checked, ok, bad, skipped))
for p in problems[:10]:
    print("  - " + p)
sys.exit(1 if bad else 0)
PY
)"
  REF_PY_RC=$?
  echo "$REF_OUT" >> "$REPORT"
  if [ "$REF_PY_RC" -ne 0 ]; then fail_check "对象引用核对失败（investigation_reports 的对象在备份中缺失或内容哈希不符，见 $LOG_DIR/refs_reports.tsv）"; fi
else
  not_verified check "对象引用核对（investigation_reports）：备份未包含 blockchain-reports 桶"
fi

psql_rest_f "select raw_object, '', 0 from download_shards where raw_object <> '' order by id limit $OBJ_SAMPLE" > "$LOG_DIR/refs_raw.tsv" 2>"$LOG_DIR/refs_raw.err"
RAW_RC=$?
if [ "$RAW_RC" -ne 0 ]; then
  fail_check "对象引用查询失败（download_shards.raw_object，rc=$RAW_RC，见 $LOG_DIR/refs_raw.err）"
elif grep -q "^blockchain-raw " "$LOG_DIR/buckets.list"; then
  RAW_OUT="$(python3 - "$SRC" "$LOG_DIR/refs_raw.tsv" "blockchain-raw" <<'PY'
import os, sys
src, tsv, bucket = sys.argv[1], sys.argv[2], sys.argv[3]
checked = ok = bad = 0
problems = []
for line in open(tsv):
    line = line.rstrip(chr(10))
    if not line:
        continue
    key = line.split("|")[0]
    if not key:
        continue
    checked += 1
    if os.path.isfile(os.path.join(src, "minio", bucket, key)):
        ok += 1
    else:
        bad += 1
        problems.append(key)
print("对象引用核对（download_shards.raw_object -> %s）：检查 %d，通过 %d，失败 %d" % (bucket, checked, ok, bad))
for p in problems[:10]:
    print("  - 备份中缺失：" + p)
sys.exit(1 if bad else 0)
PY
)"
  RAW_PY_RC=$?
  echo "$RAW_OUT" >> "$REPORT"
  if [ "$RAW_PY_RC" -ne 0 ]; then fail_check "对象引用核对失败（download_shards.raw_object 指向的对象不在备份中，见 $LOG_DIR/refs_raw.tsv）"; fi
else
  not_verified check "对象引用核对（download_shards.raw_object）：备份未包含 blockchain-raw 桶（可能被 BACKUP_SKIP_BUCKETS 排除）"
fi

# =====================================================================
# 2. ClickHouse
# =====================================================================
echo "== ClickHouse =="
CH_EXISTS="$(chq "select count() from system.databases where name='$CHR'" 2>"$LOG_DIR/ch_exists.err")"
CH_EXISTS_RC=$?
CH_EXISTS="$(num_of "$CH_EXISTS")"
if [ "$CH_EXISTS_RC" -ne 0 ] || ! is_num "$CH_EXISTS"; then
  abort_hard "无法查询 ClickHouse 目标库是否存在（rc=$CH_EXISTS_RC）：$(paste -sd ' ' "$LOG_DIR/ch_exists.err")"
fi
if [ "$CH_EXISTS" != "0" ]; then abort_hard "ClickHouse 库 $CHR 已存在：显式拒绝覆盖（本脚本不做 DROP DATABASE）"; fi

if ! tar tzf "$SRC/clickhouse.tar.gz" > "$LOG_DIR/ch-tar-list.txt" 2>"$LOG_DIR/ch-tar.err"; then
  fail_check "ClickHouse 归档无法列出（tar tzf 失败，见 $LOG_DIR/ch-tar.err）"
fi
if ! grep -q "^$CH_BACKUP_NAME/" "$LOG_DIR/ch-tar-list.txt"; then
  fail_check "归档 $SRC/clickhouse.tar.gz 中不存在目录 $CH_BACKUP_NAME/（备份内容与备份目录名不匹配）"
fi
T0="$(date +%s.%N)"
# 归档本身就是 gzip 的 tar，**不能**再 gunzip 一次（双重解压会静默产出空目录）。
if ! cat "$SRC/clickhouse.tar.gz" | docker exec -i deploy-clickhouse-1 sh -c "cd /var/lib/clickhouse/backups && tar xzf - && test -d /var/lib/clickhouse/backups/$CH_BACKUP_NAME" > "$LOG_DIR/ch-extract.log" 2>&1; then
  fail_check "ClickHouse 归档解包失败（见 $LOG_DIR/ch-extract.log）"
fi
chq "RESTORE DATABASE $CLICKHOUSE_DB AS $CHR FROM File('$CH_BACKUP_NAME')" > "$LOG_DIR/ch_restore.log" 2>&1
CH_RC=$?
T1="$(date +%s.%N)"
CH_RTO="$(python3 -c "print(round($T1-$T0,2))")"
if [ "$CH_RC" -ne 0 ]; then
  fail_check "ClickHouse RESTORE 退出码 $CH_RC（日志 $LOG_DIR/ch_restore.log，末行：$(tail -n 1 "$LOG_DIR/ch_restore.log")）"
fi
kv ch_target "$CHR"
kv ch_restore_rc "$CH_RC"
kv ch_rto_seconds "$CH_RTO"
{
echo
echo "## 2. ClickHouse 恢复"
echo
echo "- 目标库：$CHR（新建；演练后保留，不 DROP）"
echo "- 命令：RESTORE DATABASE $CLICKHOUSE_DB AS $CHR FROM File('$CH_BACKUP_NAME')"
echo "  （本实例只配置了 backups.allowed_path，可用的是 **File 引擎**：容器内不存在 backups 磁盘，"
echo "   旧文档/旧报告里的 Disk('backups', ...) 写法会直接报 INVALID_CONFIG_PARAMETER；报告与实现口径一致。）"
echo "- 退出码 **$CH_RC**（非 0 即 FAIL），**RTO = $CH_RTO s**；日志 $LOG_DIR/ch_restore.log"
echo "- 归档条目清单：$LOG_DIR/ch-tar-list.txt；解包日志：$LOG_DIR/ch-extract.log"
echo
echo "| 表 | 生产 count() | 恢复 count() | 生产 count(FINAL) | 恢复 count(FINAL) | 判定 |"
echo "|---|---:|---:|---:|---:|---|"
} >> "$REPORT"
for t in $CH_TABLES; do
  lraw="$(chq "select count() from $CLICKHOUSE_DB.$t" 2>>"$LOG_DIR/ch_count.err")"; lraw_rc=$?
  lraw="$(num_of "$lraw")"
  rraw="$(chq "select count() from $CHR.$t" 2>>"$LOG_DIR/ch_count.err")"; rraw_rc=$?
  rraw="$(num_of "$rraw")"
  lfin="$(chq "select count() from $CLICKHOUSE_DB.$t FINAL" 2>>"$LOG_DIR/ch_count.err")"; lfin_rc=$?
  lfin="$(num_of "$lfin")"
  rfin="$(chq "select count() from $CHR.$t FINAL" 2>>"$LOG_DIR/ch_count.err")"; rfin_rc=$?
  rfin="$(num_of "$rfin")"
  if [ "$lfin_rc" -ne 0 ] || ! is_num "$lfin" || [ "$rfin_rc" -ne 0 ] || ! is_num "$rfin"; then
    echo "| $t | ERROR | ERROR | ERROR | ERROR | 查询失败（禁止按 0 处理） |" >> "$REPORT"
    fail_check "ClickHouse 行数查询失败：表 $t（rc=$lfin_rc/$rfin_rc，见 $LOG_DIR/ch_count.err）"
    continue
  fi
  verdict="一致"
  if [ "$lfin" != "$rfin" ]; then
    verdict="**不一致**"
    fail_check "ClickHouse count(FINAL) 不一致：表 $t（生产 $lfin / 恢复 $rfin）"
  fi
  echo "| $t | $lraw | $rraw | $lfin | $rfin | $verdict |" >> "$REPORT"
  kv "ch_final_$t" "$rfin"
done
{
echo
echo "- 口径说明：ReplacingMergeTree 的裸 count() 会把尚未合并的历史版本算进去，一致性判定**只认 count(FINAL)**"
echo "  （见 docs/manuals/operations.md §13.2 与 acceptance-evidence/dual-system-v1/sprint7/dr/README.md §2.1）。"
echo "  上表两种口径都列出，便于复核；判定列只用 count(FINAL)。"
} >> "$REPORT"

ch_tables_prod="$(chq "select count() from system.tables where database='$CLICKHOUSE_DB'" 2>>"$LOG_DIR/ch_count.err")"
ch_tables_prod="$(num_of "$ch_tables_prod")"
ch_tables_rest="$(chq "select count() from system.tables where database='$CHR'" 2>>"$LOG_DIR/ch_count.err")"
ch_tables_rest="$(num_of "$ch_tables_rest")"
if ! is_num "$ch_tables_rest"; then
  fail_check "无法统计恢复库 $CHR 的表数量（见 $LOG_DIR/ch_count.err）"
else
  echo "- 表数量：生产 $ch_tables_prod / 恢复库 $ch_tables_rest / manifest 备份点 $MANIFEST_CH_TABLE_COUNT" >> "$REPORT"
  if is_num "$MANIFEST_CH_TABLE_COUNT" && [ "$ch_tables_rest" != "$MANIFEST_CH_TABLE_COUNT" ]; then
    fail_check "恢复库表数量 $ch_tables_rest != manifest 备份点 $MANIFEST_CH_TABLE_COUNT"
  fi
  if is_num "$ch_tables_prod" && [ "$ch_tables_rest" -lt "$ch_tables_prod" ]; then
    fail_check "恢复库表数量 $ch_tables_rest 少于生产 $ch_tables_prod"
  fi
fi

# =====================================================================
# 3. MinIO —— 逐桶（manifest 记录的全部桶）
# =====================================================================
echo "== MinIO =="
bucket_exists() { # 0=存在 1=不存在 2=无法判定（fail-closed：判定不了就 FAIL，绝不"当作不存在"继续建）
  local b="$1" out rc
  out="$(mc_plain stat "s3/$b" 2>&1)"; rc=$?
  echo "$out" > "$LOG_DIR/mc-probe-$b.txt"
  if [ "$rc" -eq 0 ]; then return 0; fi
  case "$out" in
    *"does not exist"*|*"Not Found"*|*"no such bucket"*|*"NoSuchBucket"*) return 1 ;;
  esac
  out="$(mc_plain ls --json "s3/$b" 2>&1)"; rc=$?
  echo "$out" > "$LOG_DIR/mc-probe-$b.json"
  if [ "$rc" -eq 0 ]; then return 0; fi
  case "$out" in
    *"does not exist"*|*"Not Found"*|*"no such bucket"*|*"NoSuchBucket"*) return 1 ;;
  esac
  return 2
}
{
echo
echo "## 3. MinIO 恢复（逐桶，全部桶）"
echo
echo "| 备份桶 | 演练 bucket | 备份对象数 | 恢复对象数 | 内容校验 | 判定 |"
echo "|---|---|---:|---:|---|---|"
} >> "$REPORT"
while IFS= read -r bline; do
  [ -n "$bline" ] || continue
  b="$(echo "$bline" | cut -d' ' -f1)"
  bcount="$(echo "$bline" | cut -d' ' -f2)"
  [ -n "$b" ] || continue
  TARGET="$b-drill-$UUID12"
  st=0
  bucket_exists "$TARGET" || st=$?
  if [ "$st" -eq 0 ]; then
    fail_check "演练 bucket $TARGET 已存在：显式拒绝覆盖（不做删除/复用）"
    echo "| $b | $TARGET | $bcount | - | - | 目标已存在，拒绝覆盖 |" >> "$REPORT"
    continue
  fi
  if [ "$st" -eq 2 ]; then
    fail_check "无法判定 bucket $TARGET 是否存在（probe 结果不确定，fail-closed；见 $LOG_DIR/mc-probe-$TARGET.err）"
    echo "| $b | $TARGET | $bcount | - | - | probe 不确定，中止该桶 |" >> "$REPORT"
    continue
  fi
  if ! mc_plain mb "s3/$TARGET" > "$LOG_DIR/mc-mb-$b.log" 2>&1; then
    fail_check "创建 bucket $TARGET 失败（见 $LOG_DIR/mc-mb-$b.log）"
    continue
  fi
  T0="$(date +%s.%N)"
  if ! mc_vol "$SRC/minio:/backup:ro" mirror --quiet --overwrite "/backup/$b" "s3/$TARGET" > "$LOG_DIR/mc-mirror-$b.log" 2>&1; then
    fail_check "mc mirror $b -> s3/$TARGET 失败（见 $LOG_DIR/mc-mirror-$b.log）"
    continue
  fi
  T1="$(date +%s.%N)"
  MI_RTO="$(python3 -c "print(round($T1-$T0,2))")"
  kv "minio_target_$b" "$TARGET"
  kv "minio_rto_$b" "$MI_RTO"
  if ! mc_plain ls --recursive --json "s3/$TARGET" > "$LOG_DIR/mc-ls-$TARGET.json" 2> "$LOG_DIR/mc-ls-$TARGET.err"; then
    fail_check "列举恢复 bucket $TARGET 失败（见 $LOG_DIR/mc-ls-$TARGET.err）"
    continue
  fi
  python3 - "$SRC" "$b" "$LOG_DIR/mc-ls-$TARGET.json" "$SRC/manifest.json" "$LOG_DIR/minio-cmp-$b.txt" "$LOG_DIR/minio-hash-$b.list" "$STREAM_BUDGET" "$STREAM_SAMPLE_MAX" <<'PY' > "$LOG_DIR/minio-cmp-$b.stdout" 2>&1
import hashlib, json, os, re, sys
src, bucket, ls_json, manifest_path, txt_out, hash_out = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5], sys.argv[6]
budget = int(sys.argv[7])
maxn = int(sys.argv[8])
m = json.load(open(manifest_path))
prefix = "minio/" + bucket + "/"
src_files = {}
for rel, meta in (m.get("files") or {}).items():
    if rel.startswith(prefix):
        src_files[rel[len(prefix):]] = meta
dst = {}
for line in open(ls_json):
    line = line.strip()
    if not line:
        continue
    try:
        obj = json.loads(line)
    except ValueError:
        continue
    if obj.get("type") not in (None, "file"):
        continue
    key = obj.get("key")
    if key:
        dst[key] = obj
missing = sorted(set(src_files) - set(dst))
extra = sorted(set(dst) - set(src_files))
size_bad, etag_bad = [], []
etag_ok = multipart = 0
sample = []
budget_left = budget
for key in sorted(set(src_files) & set(dst)):
    meta = src_files[key]
    obj = dst[key]
    if obj.get("size") != meta.get("size"):
        size_bad.append(key)
        continue
    p = os.path.join(src, "minio", bucket, key)
    etag = str(obj.get("etag") or "")
    if re.match(r"[0-9a-fA-F]{32}$", etag):
        h = hashlib.md5()
        with open(p, "rb") as f:
            for chunk in iter(lambda: f.read(1 << 20), b""):
                h.update(chunk)
        if h.hexdigest() == etag.lower():
            etag_ok += 1
        else:
            etag_bad.append(key)
    else:
        multipart += 1
        if len(sample) < maxn and meta.get("size", 0) <= budget_left and "|" not in key:
            sample.append((key, meta.get("sha256", "")))
            budget_left -= meta.get("size", 0)
with open(hash_out, "w") as f:
    for key, want in sample:
        f.write(key + "|" + want + chr(10))
lines = []
lines.append("对象数：备份 %d / 恢复 %d" % (len(src_files), len(dst)))
lines.append("键集合：备份有而恢复没有 %d，恢复有而备份没有 %d" % (len(missing), len(extra)))
lines.append("size 比对：不一致 %d" % len(size_bad))
lines.append("内容比对（单分片 etag=MD5，逐对象）：通过 %d，不符 %d；多分片对象待抽样流式校验 %d" % (etag_ok, len(etag_bad), multipart))
lines.append("流式抽样计划：%d 个对象（字节预算 %d）" % (len(sample), budget))
for lst, label in ((missing, "备份有 / 恢复缺失"), (extra, "恢复有 / 备份没有"), (size_bad, "size 不一致"), (etag_bad, "etag/内容不符")):
    if lst:
        lines.append("%s（最多列 10）：%s" % (label, ", ".join(lst[:10])))
with open(txt_out, "w") as f:
    f.write(chr(10).join(lines) + chr(10))
print(chr(10).join(lines))
sys.exit(1 if (missing or extra or size_bad or etag_bad) else 0)
PY
  CMP_RC=$?
  CONTENT_NOTE="etag(MD5) 逐对象"
  if [ "$CMP_RC" -ne 0 ]; then
    fail_check "MinIO 桶 $b 的逐对象比对不通过（键集合 / size / etag；详见 $LOG_DIR/minio-cmp-$b.txt）"
  fi
  STREAM_TOTAL=0
  STREAM_OK=0
  STREAM_BAD=0
  while IFS= read -r sline; do
    [ -n "$sline" ] || continue
    skey="$(echo "$sline" | cut -d'|' -f1)"
    swant="$(echo "$sline" | cut -d'|' -f2-)"
    STREAM_TOTAL=$((STREAM_TOTAL+1))
    sgot="$(mc_plain cat "s3/$TARGET/$skey" < /dev/null 2>>"$LOG_DIR/mc-cat-$b.err" | sha256sum | cut -d' ' -f1)"
    if [ -n "$sgot" ] && [ "$sgot" = "$swant" ]; then
      STREAM_OK=$((STREAM_OK+1))
    else
      STREAM_BAD=$((STREAM_BAD+1))
      fail_check "MinIO 对象内容 sha256 不符：$b/$skey（恢复 $sgot / manifest $swant）"
    fi
  done < "$LOG_DIR/minio-hash-$b.list"
  if [ "$STREAM_TOTAL" -gt 0 ]; then
    CONTENT_NOTE="etag(MD5) 逐对象 + 抽样 mc cat sha256 $STREAM_OK/$STREAM_TOTAL 通过"
    if [ "$STREAM_BAD" -ne 0 ]; then CONTENT_NOTE="$CONTENT_NOTE（$STREAM_BAD 个不符）"; fi
  fi
  verdict="通过"
  if [ "$CMP_RC" -ne 0 ] || [ "$STREAM_BAD" -ne 0 ]; then verdict="**FAIL**"; fi
  echo "| $b | $TARGET | $bcount | $(wc -l < "$LOG_DIR/mc-ls-$TARGET.json" | tr -d ' ') | $CONTENT_NOTE | $verdict |" >> "$REPORT"
  sed -e 's/^/  - /' "$LOG_DIR/minio-cmp-$b.txt" >> "$REPORT"
done < "$LOG_DIR/buckets.list"

for rb in $REQUIRED_BUCKETS; do
  if grep -q "^$rb " "$LOG_DIR/buckets.list"; then continue; fi
  if grep -qxF "$rb" "$LOG_DIR/skipped.list"; then
    not_verified check "必需桶 $rb 在备份中被显式排除（BACKUP_SKIP_BUCKETS）——该桶未恢复、未比对，不得计入已覆盖"
  else
    fail_check "必需桶 $rb 既不在 manifest.minio 中，也不在 skipped_buckets 中：备份未覆盖该桶"
  fi
done

{
echo
echo "### 3.1 版本化 / 生命周期 / 对象元数据（不得默认通过）"
echo
} >> "$REPORT"
for b in $REQUIRED_BUCKETS; do
  vm_out="$(mc_plain version info "s3/$b" 2>&1)"; vm_rc=$?
  ilm_out="$(mc_plain ilm rule ls "s3/$b" 2>&1)"; ilm_rc=$?
  if [ "$ilm_rc" -ne 0 ]; then
    ilm_out="$(mc_plain ilm ls "s3/$b" 2>&1)"; ilm_rc=$?
  fi
  if [ "$vm_rc" -eq 0 ]; then vtxt="$(echo "$vm_out" | head -n 1)"; else vtxt="无法读取（rc=$vm_rc）"; fi
  if [ "$ilm_rc" -eq 0 ]; then itxt="$(echo "$ilm_out" | head -n 1)"; else itxt="无法读取（rc=$ilm_rc）"; fi
  echo "- 生产桶 $b：versioning=$vtxt；lifecycle=$itxt（只读探测，未对任何桶做修改）" >> "$REPORT"
done
# [F14] mc mirror 结构性不含版本历史 / ILM 规则 / 对象元数据与 tag：本脚本**永远**不自行主张这些维度已通过。
#   计划允许用**独立取证结果**覆盖该维度，但必须同时满足 5 个条件才会记为"独立证据已覆盖"：
#     ① 结果文件由 scripts/backup/minio-metadata-verify.sh verify 产出（drill=minio-object-metadata）；
#     ② verdict=PASS；③ versions/ilm/tags/metadata 四个维度全部完成；
#     ④ 其 source_semantics_file 正是**本次备份目录**的 bucket-semantics.json（基线必须同一份）；
#     ⑤ 每个必需桶的 target_bucket 恰为**本次演练**新建的目标桶。
#   ①~⑤ 任一不满足 = fail_check + 仍然 NV：错配的证据链本身就是缺陷，既不静默放行也不静默退回。
# [F16] 单轮闭环：版本/ILM/元数据比对必须在**本轮演练自己新建的目标桶**上做，
#   而目标桶只有在 §3 之后才存在（先有目标才谈得上比对）。因此提供 RESTORE_DRILL_MINIO_META_VERIFY=1：
#   就地调用 scripts/backup/minio-metadata-verify.sh verify（只读）对本次目标桶取证，
#   并要求备份目录**自带备份时刻的语义基线** bucket-semantics.json（见 backup-all.sh [F13]）。
#   没有基线 / 工具非 0 / 结果不达 PASS 一律 fail_check + 仍然 NV：不允许用"没基线"冒充通过。
META_VERIFY="${RESTORE_DRILL_MINIO_META_VERIFY:-0}"
META_REF="${RESTORE_DRILL_MINIO_META_RESULT:-}"
if [ -z "$META_REF" ] && [ "$META_VERIFY" = "1" ]; then
  # 基线位置：minio-metadata-verify.sh 写在 <备份目录>/minio/bucket-semantics.json；
  # 同时接受 <备份目录>/bucket-semantics.json（历史/人工放置）。
  SEM_BASELINE="$SRC/minio/bucket-semantics.json"; [ -s "$SEM_BASELINE" ] || SEM_BASELINE="$SRC/bucket-semantics.json"
  if [ ! -s "$SEM_BASELINE" ]; then
    fail_check "RESTORE_DRILL_MINIO_META_VERIFY=1，但备份目录缺少 bucket-semantics.json（备份时未做语义 capture）：没有基线无法比对，禁止按「没有版本/没有规则」处理"
  else
    RD_ARGS=""
    for b in $REQUIRED_BUCKETS; do
      tb="$(kv_get "minio_target_$b")"
      [ -n "$tb" ] || continue
      RD_ARGS="$RD_ARGS --restored-drill $b=$tb"
    done
    META_MARKER="$LOG_DIR/minio-meta-verify.marker"
    : > "$META_MARKER"
    # shellcheck disable=SC2086
    MINIO_META_BUCKETS="$REQUIRED_BUCKETS" bash "$PROJECT/scripts/backup/minio-metadata-verify.sh" verify "$SRC" $RD_ARGS > "$LOG_DIR/minio-meta-verify.out" 2> "$LOG_DIR/minio-meta-verify.err"
    META_VERIFY_RC=$?
    META_REF="$(find "$EV" -maxdepth 1 -name 'minio-object-metadata-*.json' -newer "$META_MARKER" 2>/dev/null | head -n 1)"
    if [ "$META_VERIFY_RC" -ne 0 ]; then
      fail_check "minio-metadata-verify.sh verify 退出码 $META_VERIFY_RC（见 $LOG_DIR/minio-meta-verify.err 与 $LOG_DIR/minio-meta-verify.out）"
    fi
    if [ -z "$META_REF" ]; then
      fail_check "minio-metadata-verify.sh verify 未产出新的 minio-object-metadata-*.json（无法复核）"
    else
      echo "  minio 元数据取证结果：$META_REF"
      kv minio_meta_verify_rc "$META_VERIFY_RC"
    fi
  fi
fi
if [ -z "$META_REF" ]; then
  {
  echo
  echo "- **结论：版本历史 / 生命周期规则 / 对象元数据与 tag = NOT_VERIFIED**。"
  echo "  理由：本次恢复使用 mc mirror，它只复制对象的**当前版本**：不复制历史版本、不复制 ILM 规则、"
  echo "  不复制对象元数据与 tag；本次演练也没有任何步骤验证过版本或生命周期。"
  echo "  因此本报告**不主张**版本 / 生命周期 / 元数据维度的恢复完整性；要拿到该维度证据，必须另用"
  echo "  版本列举 / 规则导出 / 对象元数据比对等工具单独演练并单独出报告"
  echo "  （scripts/backup/minio-metadata-verify.sh；其产物可经 RESTORE_DRILL_MINIO_META_RESULT 传入本脚本复核）。"
  } >> "$REPORT"
  not_verified structural "版本历史 / 生命周期规则 / 对象元数据与 tag（mc mirror 只反映当前对象版本，无法冒充完整性；本次未提供 RESTORE_DRILL_MINIO_META_RESULT 独立取证结果）"
else
  META_REF_ABS="$(cd "$(dirname "$META_REF")" 2>/dev/null && pwd)/$(basename "$META_REF")"
  if [ ! -s "$META_REF_ABS" ]; then
    fail_check "RESTORE_DRILL_MINIO_META_RESULT 指向的结果文件不存在或为空：$META_REF"
    {
    echo
    echo "- **结论：版本历史 / 生命周期规则 / 对象元数据与 tag = NOT_VERIFIED**（传入的独立取证结果不可用：$META_REF）"
    } >> "$REPORT"
    not_verified structural "版本历史 / 生命周期规则 / 对象元数据与 tag（传入的独立取证结果不存在或为空：$META_REF）"
  else
    META_REF_SHA="$(sha256sum "$META_REF_ABS" | cut -d' ' -f1)"
    META_CHECK="$(python3 - "$META_REF_ABS" "$LOG_DIR/result.kv" "$REQUIRED_BUCKETS" "$SRC" <<'PY'
import json, os, sys
ref, kv_path, req, src = sys.argv[1], sys.argv[2], sys.argv[3].split(), sys.argv[4]
kv = {}
for line in open(kv_path):
    line = line.rstrip()
    if "=" in line:
        k, v = line.split("=", 1); kv[k] = v
try:
    d = json.load(open(ref))
except Exception as e:
    print("BAD|结果文件无法解析为 JSON：%s" % e); raise SystemExit(0)
if d.get("drill") != "minio-object-metadata":
    print("BAD|结果文件不是 minio-metadata-verify.sh 的产物（drill=%s）" % d.get("drill")); raise SystemExit(0)
if d.get("verdict") != "PASS":
    print("BAD|独立取证结果 verdict=%s（必须 PASS）" % d.get("verdict")); raise SystemExit(0)
dims = d.get("dimensions") or {}
bad = [k for k in ("versions", "ilm", "tags", "metadata") if str(dims.get(k, "incomplete")).startswith("incomplete")]
if bad:
    print("BAD|独立取证结果维度未全部完成：%s" % ",".join(bad)); raise SystemExit(0)
want_sem = os.path.join(src, "bucket-semantics.json")
if os.path.realpath(str(d.get("source_semantics_file") or "")) != os.path.realpath(want_sem):
    print("BAD|独立取证结果的语义基线不是本次备份目录的基线（source_semantics_file=%s，期望 %s）" % (d.get("source_semantics_file"), want_sem)); raise SystemExit(0)
bs = d.get("buckets") or {}
mismatch, missing = [], []
for b in req:
    e = bs.get(b)
    if not e:
        missing.append(b); continue
    if str(e.get("target_bucket") or "") != str(kv.get("minio_target_%s" % b) or ""):
        mismatch.append("%s（取证=%s / 本次演练=%s）" % (b, e.get("target_bucket"), kv.get("minio_target_%s" % b)))
if missing or mismatch:
    print("BAD|独立取证结果与本次演练目标不一致：缺失桶=%s；目标桶不符=%s" % (",".join(missing) or "无", "; ".join(mismatch) or "无")); raise SystemExit(0)
print("OK|桶=%s；维度=%s" % (",".join(req), ",".join("%s=%s" % (k, dims.get(k)) for k in sorted(dims))))
PY
)"
    case "$META_CHECK" in
      OK*)
        META_REF_NOTE="${META_CHECK#OK|}"
        kv minio_meta_ref "$META_REF_ABS"
        kv minio_meta_ref_sha256 "$META_REF_SHA"
        {
        echo
        echo "- **版本历史 / 生命周期规则 / 对象元数据与 tag：由独立取证结果覆盖（不是本脚本证明的）**。"
        echo "  - 独立取证结果：$META_REF_ABS"
        echo "  - 该文件 sha256：$META_REF_SHA"
        echo "  - 覆盖范围：$META_REF_NOTE"
        echo "  - 本次演练据此**不再**对该维度声明 NOT_VERIFIED；内容维度（对象数 / 键集合 / size / 内容 hash）仍由本报告 §3 独立证明。"
        echo "  - 口径：本脚本**不**自行验证版本 / ILM / 元数据，只复核该结果文件的判定、维度完整性与桶级目标是否与本次演练一一对应。"
        } >> "$REPORT"
        ;;
      *)
        META_REF_REASON="${META_CHECK#BAD|}"
        fail_check "RESTORE_DRILL_MINIO_META_RESULT 复核不通过：$META_REF_REASON"
        {
        echo
        echo "- **结论：版本历史 / 生命周期规则 / 对象元数据与 tag = NOT_VERIFIED**（传入的独立取证结果复核不通过：$META_REF_REASON）"
        } >> "$REPORT"
        not_verified structural "版本历史 / 生命周期规则 / 对象元数据与 tag（传入的独立取证结果复核不通过：$META_REF_REASON）"
        ;;
    esac
  fi
fi

# =====================================================================
# 4. 汇总
# =====================================================================
FAIL_N="$(wc -l < "$FAILURES" | tr -d ' ')"
NV_N="$(wc -l < "$NOT_VERIFIED" | tr -d ' ')"
NV_STRUCT_N="$(wc -l < "$NOT_VERIFIED_STRUCTURAL" | tr -d ' ')"
NV_CHECK_N="$(wc -l < "$NOT_VERIFIED_CHECK" | tr -d ' ')"
PARTIAL_N="$(wc -l < "$PARTIAL" | tr -d ' ')"
# [GATE-01] 判定口径：只有 FAIL=0 且 NV(任何分级)=0 且 PARTIAL=0 才是 PASS。
#           structural NV 同样**不构成通过**——gate 会阻断它，直到能力被真实补齐。
if [ "$FAIL_N" -gt 0 ]; then
  VERDICT="FAIL"
elif [ "$NV_N" -gt 0 ] || [ "$PARTIAL_N" -gt 0 ]; then
  VERDICT="INCOMPLETE"
else
  VERDICT="PASS"
fi
{
echo
echo "## 4. 汇总"
echo
echo "| 组件 | RTO 实测 | 语义核对 |"
echo "|---|---|---|"
echo "| PostgreSQL | $PG_RTO s | 逐表行数比对（查询失败即 FAIL，不按 0 处理）、与 manifest 备份点交叉核对、auditverify、对象引用核对 |"
echo "| ClickHouse | $CH_RTO s | 独立恢复库；逐表 count(FINAL) 比对 + 表数量三方核对 |"
echo "| MinIO | 见上表 | 逐桶对象数 / 键集合 / size / 内容（etag 与抽样 sha256）；版本与生命周期 NOT_VERIFIED |"
echo
echo "- FAIL 条目：$FAIL_N；NOT_VERIFIED 条目：$NV_N（structural $NV_STRUCT_N / check $NV_CHECK_N）；PARTIAL 条目：$PARTIAL_N"
case "$VERDICT" in
  FAIL)
    echo "- **本次演练判定：FAIL**（失败条目见下）"
    echo
    sed -e 's/^/- /' "$FAILURES"
    ;;
  INCOMPLETE)
    echo "- **本次演练判定：INCOMPLETE**（不存在 FAIL，但仍有 NOT_VERIFIED $NV_N 条 / PARTIAL $PARTIAL_N 条）"
    echo "  —— 该口径**不等于通过**，且 scripts/s7/dr-gate.sh 会据此阻断 S7-DR 整体验收。"
    ;;
  PASS)
    echo "- **本次演练判定：PASS**（FAIL=0、NOT_VERIFIED=0、PARTIAL=0）"
    ;;
esac
if [ "$NV_N" -gt 0 ]; then
  echo
  echo "### NOT_VERIFIED（不得当作已通过；gate 一律阻断）"
  echo
  if [ "$NV_STRUCT_N" -gt 0 ]; then
    echo
    echo "**structural（脚本层结构性无法证明，需另配工具/产品侧改造才能真正闭合）**"
    echo
    sed -e 's/^/- /' "$NOT_VERIFIED_STRUCTURAL"
  fi
  if [ "$NV_CHECK_N" -gt 0 ]; then
    echo
    echo "**check（本次核对未完成：环境/取值/前置缺失）**"
    echo
    sed -e 's/^/- /' "$NOT_VERIFIED_CHECK"
  fi
fi
if [ "$PARTIAL_N" -gt 0 ]; then
  echo
  echo "### PARTIAL（部分覆盖，不得计入通过；gate 一律阻断）"
  echo
  sed -e 's/^/- /' "$PARTIAL"
fi
echo
echo "### RPO 说明（禁止用行数 delta 宣称跨库一致性）"
echo
echo "- 备份为快照式（非 PITR）。manifest 记录 cross_store_consistency=$MANIFEST_CROSS_STORE，三个 store 的"
echo "  备份窗口见 manifest.consistency.stores；PG 备份点由导出快照给定"
echo "  （pg_snapshot_consistent=$MANIFEST_PG_SNAPSHOT_CONSISTENT，snapshot=$MANIFEST_PG_SNAPSHOT_ID，wal_lsn=$MANIFEST_PG_WAL_LSN）。"
echo "- 生产库相对备份的行数差合计 $TOTAL_DELTA 行，表示「上一次备份之后写入、若此刻故障会丢失」的量级。"
echo "  它是**行数证据**，不能用来宣称 PG / CH / MinIO 落在同一时刻。"
echo
echo "## 5. 演练目标的去向（不自动清理）"
echo
echo "| 目标 | 名称 | 保留原因 |"
echo "|---|---|---|"
echo "| PostgreSQL 库 | $PGR | 供人工诊断与复核；**脚本不做 DROP DATABASE** |"
echo "| ClickHouse 库 | $CHR | 供人工诊断与复核；**脚本不做 DROP DATABASE**（生产库 $CLICKHOUSE_DB 全程未被触碰） |"
for b in $REQUIRED_BUCKETS; do
  tb="$(kv_get "minio_target_$b")"
  [ -n "$tb" ] && echo "| MinIO bucket | $tb | 供人工复核；**脚本不做 mc rb / 不删除对象** |"
done
echo
echo "清理需人工确认后执行（本脚本刻意不自动清理，避免掩盖失败与丢失现场）："
echo "$FENCE"bash
echo "docker exec -e PGPASSWORD=<redacted> deploy-postgres-1 psql -U $POSTGRES_USER -d postgres -c 'DROP DATABASE $PGR'"
echo "docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password <redacted> --query 'DROP DATABASE $CHR'"
for b in $REQUIRED_BUCKETS; do
  tb="$(kv_get "minio_target_$b")"
  [ -n "$tb" ] && echo "docker run --rm --network host -e MC_HOST_s3=<redacted> $MC_IMAGE rb --force s3/$tb"
done
echo "$FENCE"
echo
echo "- 命令原始日志目录：$LOG_DIR"
echo "- 机器可读结果：$RESULT"
} >> "$REPORT"

python3 - "$LOG_DIR/result.kv" "$FAILURES" "$NOT_VERIFIED" "$RESULT" "$TS" "$SRC" "$FAIL_N" "$NV_N" "$LOG_DIR" "$VERDICT" "$NOT_VERIFIED_STRUCTURAL" "$NOT_VERIFIED_CHECK" "$PARTIAL" <<'PY'
import json, sys
kv_path, fail_path, nv_path, out_path, ts, src, fail_n, nv_n, log_dir, verdict, nv_struct_path, nv_check_path, partial_path = sys.argv[1:14]
data = {}
for line in open(kv_path):
    line = line.rstrip(chr(10))
    if not line or "=" not in line:
        continue
    k, v = line.split("=", 1)
    data[k] = v
def lines(p):
    try:
        return [x.strip() for x in open(p).read().splitlines() if x.strip()]
    except OSError:
        return []
targets = {"postgres": {"database": data.get("pg_target")}, "clickhouse": {"database": data.get("ch_target")}, "minio": {}}
for k, v in data.items():
    if k.startswith("minio_target_"):
        targets["minio"][k[len("minio_target_"):]] = v
res = {
    "drill": "restore-drill",
    "ts": ts,
    "backup_dir": src,
    "verdict": verdict,
    "verdict_note": "PASS 仅当 FAIL=0 且 NOT_VERIFIED=0 且 PARTIAL=0；structural NV 同样不构成通过",
    "failures": lines(fail_path),
    "not_verified": lines(nv_path),
    "nv_class": {"structural": lines(nv_struct_path), "check": lines(nv_check_path)},
    "nv_counts": {"total": int(nv_n), "structural": len(lines(nv_struct_path)), "check": len(lines(nv_check_path))},
    "partial": lines(partial_path),
    "partial_count": len(lines(partial_path)),
    "targets": targets,
    "pg_dsn_redacted": data.get("pg_dsn_redacted"),
    "restore_rc": {"postgres": data.get("pg_restore_rc"), "clickhouse": data.get("ch_restore_rc")},
    "rto_seconds": {"postgres": data.get("pg_rto_seconds"), "clickhouse": data.get("ch_rto_seconds"), "minio": {k[len("minio_rto_"):]: v for k, v in data.items() if k.startswith("minio_rto_")}},
    "logs_dir": log_dir,
    "cleanup": "演练目标不自动清理；需人工确认后按报告 §5 的命令删除",
    "retention_note": "本脚本不删除任何备份、不删除任何演练目标；失败的目标一律保留",
}
with open(out_path, "w") as f:
    json.dump(res, f, indent=2, ensure_ascii=False)
print(json.dumps({k: v for k, v in res.items() if k not in ("failures", "not_verified")}, indent=2, ensure_ascii=False))
PY
RESULT_RC=$?
if [ "$RESULT_RC" -ne 0 ]; then fail_check "结果文件写入失败（rc=$RESULT_RC）"; fi

MARK=""
if [ "$FAIL_N" -gt 0 ]; then
  MARK="$EV/FAILED-restore-drill-$TS.md"
  {
    echo "# 恢复演练 FAIL（$TS）"
    echo
    echo "- 备份源：$SRC"
    echo "- 完整报告：$REPORT"
    echo "- 日志目录：$LOG_DIR"
    echo "- 演练目标一律保留，脚本未做任何清理（不做 DROP / 不做删除）"
    echo
    echo "## FAIL 条目"
    echo
    sed -e 's/^/- /' "$FAILURES"
  } > "$MARK"
fi

echo "report: $REPORT"
echo "result: $RESULT"
echo "logs:   $LOG_DIR"
cleanup_mc_tmp
if [ "$FAIL_N" -gt 0 ]; then
  echo "FAILED marker: $MARK"
  echo "演练判定：FAIL（$FAIL_N 条）—— 失败目标已全部保留，供人工诊断" >&2
  exit 1
fi
# [GATE-01] 退出码：0=PASS；1=FAIL；2=前置/中止；3=INCOMPLETE（仍有 NV(任何分级) 或 PARTIAL）
# 注意：exit 3 表示"证据已产出但**未达验收**"，绝不等于通过。
if [ "$NV_N" -gt 0 ] || [ "$PARTIAL_N" -gt 0 ]; then
  echo "演练判定：INCOMPLETE（NOT_VERIFIED $NV_N 条 [structural $NV_STRUCT_N / check $NV_CHECK_N]、PARTIAL $PARTIAL_N 条）—— 未达验收，见报告 §4" >&2
  exit 3
fi
echo "演练判定：PASS（FAIL=0、NOT_VERIFIED=0、PARTIAL=0）"
exit 0
