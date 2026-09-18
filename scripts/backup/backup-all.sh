#!/usr/bin/env bash
# S7-DR-01：PostgreSQL / ClickHouse / MinIO 统一备份。
#
# 【独立复核修复，2026-09】逐条编号对应 scripts/backup/README-DR-FIXES.md：
#   [F1] 默认**禁用**自动裁剪（原实现 BACKUP_RETENTION_DAYS=7 + find ... -exec rm -rf 无审批删旧备份）。
#        现在：BACKUP_RETENTION_DAYS 默认 0；只有 >0 且 BACKUP_RETENTION_APPROVED=1（书面批准）才裁剪；
#        裁剪前先把「将被删除的目录清单」写入日志与裁剪记录文件；永远保留最近 KEEP_MIN 份与任何
#        带 __FAILED / __INCONSISTENT / __INCOMPLETE 标记的目录；绝不删除没有 manifest.json 的目录
#        （即非本脚本产出）；备份本身不完整时**根本不进入**裁剪流程。
#   [F2] manifest 里 minio 的 object_count 读取路径错误：文件实际在 DIR/minio/<bucket>.object_count，
#        原代码 read(b + ".object_count") 相对 DIR 读取，恒为 null。已改为 minio/<bucket>.object_count。
#   [F3] manifest 生成（python heredoc）失败后仍打印 backup done。现在检查退出码，并二次校验
#        manifest 可解析且必需字段齐全；失败即 fail 并写 __FAILED。
#   [F4] schema_migrations 计数 / audit_head_seq / CH table_count 原先一律 2>/dev/null 吞错并允许空值。
#        现在：查询失败即写 ERROR:<原因> 到对应口径文件、把该备份标记为不完整（__INCOMPLETE + 
#        manifest.complete=false），且脚本整体非 0 退出。
#   [F5] 一致性边界：原来 PG dump 与随后读取 audit_head_seq 不在同一备份点，PG/CH/MinIO 三段也可能
#        相隔很久。现在用 PostgreSQL 导出快照建立真正的备份点：在同一个 psql 会话里
#        BEGIN ISOLATION LEVEL REPEATABLE READ; SELECT pg_export_snapshot(); 并保持该事务存活，
#        用 pg_dump --snapshot=<id> 导出，并在**同一快照内**读取 audit_events_v2 的 max(seq)/count(*)、
#        schema_migrations 计数与 pg_current_wal_lsn()。manifest 增加 consistency 段：三个 store 各自的
#        started_at/finished_at/秒数 + 整体窗口，并显式写 cross_store_consistency=bounded_by_window。
#        **禁止**用行数 delta 宣称跨库一致性。若快照无法建立（退化路径），manifest 显式写
#        pg_snapshot_consistent=false 与原因，并写 __INCONSISTENT 标记；除非显式设置
#        BACKUP_ALLOW_INCONSISTENT=1（表示已知并接受），否则脚本非 0 退出。
#   [F6] ClickHouse：① 行数核对改用 count(FINAL)（ReplacingMergeTree 裸 count() 会把未合并的历史版本
#        算进去），结果写入 manifest 与 clickhouse.rowcounts；② 注释与实现统一为 **File 引擎**
#        （本实例 system.disks 只有 default、未配置 backups.allowed_disk，Disk('backups',...) 会直接
#        报 INVALID_CONFIG_PARAMETER；config.xml 里配的是 backups.allowed_path，对应 BACKUP ... TO File）。
#
# 设计要点（保留）：
#   - 每次产出带时间戳的独立目录 + manifest.json（每个文件的 size + sha256）；
#   - 失败即非 0 退出并保留 __FAILED 标记，定时器据此告警；
#   - MinIO 采用 mc mirror 到本地目录，blockchain-raw 为大对象桶（约 16 GiB），可用 BACKUP_SKIP_BUCKETS 排除，
#     被排除的桶必须写进 manifest.minio_detail.skipped_buckets，不得默认为已覆盖。
#
# 用法：bash scripts/backup/backup-all.sh
# 环境变量：
#   BACKUP_ROOT                 备份根目录
#   BACKUP_BUCKETS              桶清单（默认 5 个桶，含 blockchain-raw）
#   BACKUP_SKIP_BUCKETS         要跳过的桶（显式声明不覆盖）
#   BACKUP_RETENTION_DAYS       默认 0 = 禁用自动裁剪
#   BACKUP_RETENTION_APPROVED   必须为 1 才允许裁剪（表示保留策略已获书面批准）
#   BACKUP_RETENTION_KEEP_MIN   无论如何都保留的最近 N 份（默认 3）
#   BACKUP_CH_FINAL_TABLES      需要比对 count() ... FINAL 的 CH 表
#   BACKUP_ALLOW_INCONSISTENT   1 = 接受「未取得一致快照」的备份（仍写 __INCONSISTENT 并置 manifest 标志）
# 退出码：0 = 完整且一致性边界已建立；1 = 失败 / 不完整 / 不一致（证据全部保留，绝不自动删除）

set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
BACKUP_ROOT="${BACKUP_ROOT:-/home/euripides/backups/blockchain-platform}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-0}"
RETENTION_APPROVED="${BACKUP_RETENTION_APPROVED:-0}"
RETENTION_KEEP_MIN="${BACKUP_RETENTION_KEEP_MIN:-3}"
SKIP_BUCKETS="${BACKUP_SKIP_BUCKETS:-}"
BUCKETS="${BACKUP_BUCKETS:-blockchain-audit blockchain-backups blockchain-exports blockchain-reports blockchain-raw}"
CH_FINAL_TABLES="${BACKUP_CH_FINAL_TABLES:-transactions token_transfers address_activity logs}"
ALLOW_INCONSISTENT="${BACKUP_ALLOW_INCONSISTENT:-0}"
MC_IMAGE="${MC_IMAGE:-minio/mc:latest}"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
DIR="$BACKUP_ROOT/$TS"
STARTED_EPOCH="$(date +%s.%N)"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if ! mkdir -p "$DIR"; then
  echo "无法创建备份目录：$DIR" >&2
  exit 1
fi
echo "backup start $TS -> $DIR"

WORK="$(mktemp -d /tmp/s7-backup.XXXXXX)" || { echo "无法创建临时目录" >&2; exit 1; }
SNAP_FIFO=""
SNAP_PID=""
SNAP_OUT="$WORK/pg-metrics.out"
SNAP_ERR="$WORK/pg-metrics.err"
METRIC_SQL="$WORK/pg-metrics.sql"
TIMING="$DIR/consistency.timing"

cleanup_scratch() { # 尽力而为的清理（非关键路径，失败不影响结论）
  exec 9>&- 2>/dev/null || true
  if [ -n "$SNAP_PID" ]; then kill "$SNAP_PID" 2>/dev/null || true; fi
  if [ -n "$SNAP_FIFO" ]; then rm -f "$SNAP_FIFO" 2>/dev/null || true; fi
  rm -rf "$WORK" 2>/dev/null || true
}

fail() { # 硬失败：写 __FAILED 标记（该目录永不被裁剪），非 0 退出
  echo "FAILED [$(date -u +%Y-%m-%dT%H:%M:%SZ)]: $*" | tee "$DIR/__FAILED" >&2
  echo "backup FAILED: $DIR（证据保留在 $DIR）" >&2
  cleanup_scratch
  exit 1
}

mark_incomplete() { # 备份产出但不可信：写 __INCOMPLETE（该目录永不被裁剪），整体非 0 退出
  echo "$1" >> "$DIR/__INCOMPLETE"
  echo "INCOMPLETE [$(date -u +%Y-%m-%dT%H:%M:%SZ)]: $1" >&2
}

mark_inconsistent() { # 一致性边界未建立：写 __INCONSISTENT
  echo "$1" >> "$DIR/__INCONSISTENT"
  echo "INCONSISTENT [$(date -u +%Y-%m-%dT%H:%M:%SZ)]: $1" >&2
  if [ "$ALLOW_INCONSISTENT" != "1" ]; then
    mark_incomplete "一致性边界未建立且未显式接受（BACKUP_ALLOW_INCONSISTENT != 1）：$1"
  fi
}

mark_timing() { # mark_timing <store> <started_at> <start_epoch> <finished_at> <end_epoch>
  echo "$1 $2 $3 $4 $5" >> "$TIMING"
}

incomplete_count() {
  if [ -f "$DIR/__INCOMPLETE" ]; then wc -l < "$DIR/__INCOMPLETE" | tr -d ' '; else echo 0; fi
}

is_num() { case "$1" in ''|*[!0-9]*) return 1 ;; *) return 0 ;; esac; }

set -a
if ! . "$PROJECT/deploy/.env"; then
  fail "无法加载 $PROJECT/deploy/.env"
fi
set +a

MC_HOST_S3="${MC_HOST_S3:-http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9001}"
# mc 一律通过函数调用：docker run 的 -v/-e 必须放在镜像名之前，写成字符串变量再拼接
# 会把卷参数当成 mc 自身的参数传给 mc（旧实现即为此类隐患）。
mc_plain() { docker run --rm --network host -e "MC_HOST_s3=$MC_HOST_S3" "$MC_IMAGE" "$@"; }
mc_vol() { local mnt="$1"; shift; docker run --rm --network host -v "$mnt" -e "MC_HOST_s3=$MC_HOST_S3" "$MC_IMAGE" "$@"; }

# 会话/查询封装：全部走函数，避免把密码塞进会被词拆分的字符串变量。
pgq() { docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"; }
pgi() { docker exec -i -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"; }
chq() { docker exec deploy-clickhouse-1 clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --query "$1"; }

# =====================================================================
# 1. PostgreSQL —— 导出快照（真正的一致备份点）+ pg_dump --snapshot
# =====================================================================
echo "[1/4] postgres（REPEATABLE READ 导出快照 + pg_dump --snapshot）"
PG_START_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; PG_START_EPOCH="$(date +%s.%N)"
PG_SNAPSHOT_CONSISTENT=true
PG_SNAPSHOT=""
PG_SNAPSHOT_NOTE=""

cat > "$METRIC_SQL" <<'SQL'
SELECT 'S7MIG=' || (SELECT count(*)::text FROM schema_migrations);
SELECT 'S7SEQ=' || COALESCE((SELECT max(seq)::text FROM audit_events_v2 WHERE chain='audit'),'0');
SELECT 'S7CNT=' || (SELECT count(*)::text FROM audit_events_v2 WHERE chain='audit');
SELECT 'S7LSN=' || pg_current_wal_lsn()::text;
SELECT 'S7NOW=' || now()::text;
SQL

SNAP_FIFO="$(mktemp -u /tmp/s7-pgsnap.XXXXXX)"
if mkfifo "$SNAP_FIFO"; then
  # 长活事务：保持 REPEATABLE READ 事务打开，直到审计边界读完再 COMMIT。
  pgi -Atq -f - < "$SNAP_FIFO" > "$SNAP_OUT" 2> "$SNAP_ERR" &
  SNAP_PID=$!
  exec 9<> "$SNAP_FIFO"   # O_RDWR 打开 FIFO 不会阻塞；关闭 fd 9 时读端才会看到 EOF
  echo 'BEGIN ISOLATION LEVEL REPEATABLE READ;' >&9
  echo "SELECT 'S7SNAP=' || pg_export_snapshot();" >&9
  i=0
  while [ "$i" -lt 100 ]; do
    PG_SNAPSHOT="$(sed -n 's/^S7SNAP=//p' "$SNAP_OUT" | head -n 1)"
    if [ -n "$PG_SNAPSHOT" ]; then break; fi
    if ! kill -0 "$SNAP_PID" 2>/dev/null; then break; fi
    sleep 0.2
    i=$((i+1))
  done
else
  mark_inconsistent "无法创建 FIFO $SNAP_FIFO（快照事务无法建立）"
fi

PG_DUMP_RC=1
if [ -n "$PG_SNAPSHOT" ]; then
  echo "  pg snapshot id = $PG_SNAPSHOT"
  docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc --snapshot="$PG_SNAPSHOT" -f /tmp/s7pg.dump
  PG_DUMP_RC=$?
else
  PG_SNAPSHOT_CONSISTENT=false
  PG_SNAPSHOT_NOTE="pg_export_snapshot 未返回快照 id（$(paste -sd ' ' "$SNAP_ERR" 2>/dev/null | cut -c1-300)）；本次退化为非快照 pg_dump，manifest.pg_snapshot_consistent=false，不得据此宣称任何跨库一致性"
  echo "  WARN: $PG_SNAPSHOT_NOTE"
  mark_inconsistent "$PG_SNAPSHOT_NOTE"
  if [ -n "$SNAP_PID" ]; then
    exec 9>&-
    wait "$SNAP_PID" 2>/dev/null
    SNAP_PID=""
  fi
  docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc -f /tmp/s7pg.dump
  PG_DUMP_RC=$?
fi
if [ "$PG_DUMP_RC" -ne 0 ]; then fail "pg_dump 失败（rc=$PG_DUMP_RC）"; fi

docker cp deploy-postgres-1:/tmp/s7pg.dump "$DIR/postgres.dump" >/dev/null || fail "docker cp postgres.dump"

# 审计边界 + 迁移版本：快照路径在**同一快照/同一事务**内读取；退化路径记录为非快照读取。
if [ -n "$PG_SNAPSHOT" ]; then
  cat "$METRIC_SQL" >&9
  echo 'COMMIT;' >&9
  exec 9>&-
  if ! wait "$SNAP_PID"; then
    echo "  WARN: 快照会话异常退出（metric 读取可能不完整）" >&2
  fi
  SNAP_PID=""
else
  pgi -At -f "$METRIC_SQL" > "$SNAP_OUT" 2> "$SNAP_ERR"
  if [ $? -ne 0 ]; then
    echo "  WARN: 非快照 metric 查询返回非 0" >&2
  fi
fi

pick() { sed -n "s/^$1=//p" "$SNAP_OUT" | head -n 1; }
write_metric() { # write_metric <文件> <值> <标签> <是否必须是数字>
  f="$DIR/$1"; v="$2"; label="$3"; want_num="$4"
  if [ -z "$v" ]; then
    echo "ERROR:查询失败/未返回（$label）；psql stderr: $(paste -sd ' ' "$SNAP_ERR" 2>/dev/null | cut -c1-300)" > "$f"
    mark_incomplete "$label 查询失败 —— 备份不完整，禁止当作可用备份点"
    return 1
  fi
  if [ "$want_num" = "num" ] && ! is_num "$v"; then
    echo "ERROR:返回值不是数字（$label）：$(echo "$v" | cut -c1-200)" > "$f"
    mark_incomplete "$label 返回值非法（$v）—— 备份不完整，禁止当作可用备份点"
    return 1
  fi
  echo "$v" > "$f"
  return 0
}

write_metric postgres.migration_count "$(pick S7MIG)" "schema_migrations 计数" num
write_metric postgres.audit_head_seq "$(pick S7SEQ)" "audit_events_v2 audit 链 head seq" num
write_metric postgres.audit_event_count "$(pick S7CNT)" "audit_events_v2 audit 链事件计数" num
write_metric postgres.wal_lsn "$(pick S7LSN)" "pg_current_wal_lsn()" any
write_metric postgres.metrics_read_at "$(pick S7NOW)" "快照内读取时间（now()）" any
echo "$PG_SNAPSHOT" > "$DIR/postgres.snapshot_id"
if [ -z "$PG_SNAPSHOT" ]; then
  echo "ERROR:未建立导出快照（见 manifest.pg_snapshot_consistent=false 与 __INCONSISTENT）" > "$DIR/postgres.snapshot_id"
fi

PG_END_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; PG_END_EPOCH="$(date +%s.%N)"
mark_timing postgres "$PG_START_AT" "$PG_START_EPOCH" "$PG_END_AT" "$PG_END_EPOCH"

# =====================================================================
# 2. ClickHouse —— BACKUP DATABASE ... TO File(...)
# =====================================================================
echo "[2/4] clickhouse"
CH_START_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; CH_START_EPOCH="$(date +%s.%N)"
# 引擎说明（与实现一致）：本实例 system.disks 只有 default，且未设置 backups.allowed_disk，
# 因此 Disk('backups', ...) 会直接报 INVALID_CONFIG_PARAMETER。
# config.xml 里配置的是 backups.allowed_path，对应可用的是 **File 引擎**：
#   BACKUP DATABASE db TO File('<name>')  ->  <data_path>/backups/<name>/
# 报告/注释一律按 File 描述，不得再写成 Disk('backups', ...)。
if ! chq "BACKUP DATABASE $CLICKHOUSE_DB TO File('$TS')"; then
  fail "clickhouse BACKUP（File 引擎）失败"
fi
if ! docker exec deploy-clickhouse-1 sh -c "cd /var/lib/clickhouse/backups && tar czf - $TS" > "$DIR/clickhouse.tar.gz"; then
  fail "clickhouse 归档导出失败（$DIR/clickhouse.tar.gz 可能不完整）"
fi
if [ ! -s "$DIR/clickhouse.tar.gz" ]; then fail "clickhouse 归档为空"; fi

CH_COUNT_OUT="$(chq "select count() from system.tables where database='$CLICKHOUSE_DB'" 2>&1)"
CH_COUNT_RC=$?
CH_COUNT="$(echo "$CH_COUNT_OUT" | grep -E '^[0-9]+' | tail -n 1)"
if [ "$CH_COUNT_RC" -ne 0 ] || ! is_num "$CH_COUNT"; then
  echo "ERROR:表计数查询失败：$(echo "$CH_COUNT_OUT" | cut -c1-300)" > "$DIR/clickhouse.table_count"
  mark_incomplete "ClickHouse 表计数查询失败 —— 备份不完整"
else
  echo "$CH_COUNT" > "$DIR/clickhouse.table_count"
fi

# 2026-09-16 修正（真实缺陷）：正确写法是 SELECT count() FROM t FINAL；
# 写成 count(FINAL) 在 ClickHouse 26.7 上是语法错误（Unknown expression or function identifier FINAL），
# 会让**每一次**备份都被标记 __INCOMPLETE，并让 restore-drill 的 CH 判定必然失败 —— DR 门禁因此长期结构性不可通过。
# [F6] ReplacingMergeTree 必须用 FINAL 核对：裸 count() 统计的是尚未合并的物理行
# （同一排序键的历史版本仍在），既不能用于一致性判定，也不能写进报告当作数据量。
echo "# table count count_final verdict  （count() 仅为参考；一致性判定必须用 count_final）" > "$DIR/clickhouse.rowcounts"
for t in $CH_FINAL_TABLES; do
  raw="$(chq "select count() from $CLICKHOUSE_DB.$t" 2>&1)"; raw_rc=$?
  raw="$(echo "$raw" | grep -E '^[0-9]+' | tail -n 1)"
  fin="$(chq "select count() from $CLICKHOUSE_DB.$t FINAL" 2>&1)"; fin_rc=$?
  fin="$(echo "$fin" | grep -E '^[0-9]+' | tail -n 1)"
  verdict="ok"
  if [ "$raw_rc" -ne 0 ] || ! is_num "$raw"; then verdict="ERROR:count()"; fi
  if [ "$fin_rc" -ne 0 ] || ! is_num "$fin"; then verdict="ERROR:count(FINAL)"; fi
  echo "$t $raw $fin $verdict" >> "$DIR/clickhouse.rowcounts"
  if [ "$verdict" != "ok" ]; then
    mark_incomplete "ClickHouse 行数核对失败：表 $t 的 count(FINAL) 未取到（$verdict）"
  fi
done

CH_END_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; CH_END_EPOCH="$(date +%s.%N)"
mark_timing clickhouse "$CH_START_AT" "$CH_START_EPOCH" "$CH_END_AT" "$CH_END_EPOCH"

# =====================================================================
# 3. MinIO —— 逐桶 mc mirror
# =====================================================================
echo "[3/4] minio"
MI_START_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; MI_START_EPOCH="$(date +%s.%N)"
mkdir -p "$DIR/minio" || fail "无法创建 $DIR/minio"
SKIPPED_HIT=""
for b in $BUCKETS; do
  case " $SKIP_BUCKETS " in
    *" $b "*) echo "  skip $b（显式排除，必须在报告中声明未覆盖）"; SKIPPED_HIT="$SKIPPED_HIT $b"; continue ;;
  esac
  mkdir -p "$DIR/minio/$b" || fail "无法创建 $DIR/minio/$b"
  if ! mc_vol "$DIR/minio:/backup" mirror --quiet --overwrite "s3/$b" "/backup/$b" >/dev/null; then
    fail "minio mirror $b"
  fi
  if ! cnt="$(find "$DIR/minio/$b" -type f 2>>"$WORK/minio.find.err" | wc -l | tr -d ' ')"; then
    echo "ERROR:对象计数失败" > "$DIR/minio/$b.object_count"
    mark_incomplete "MinIO 桶 $b 的对象计数失败"
    continue
  fi
  echo "$cnt" > "$DIR/minio/$b.object_count"
done
MI_END_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; MI_END_EPOCH="$(date +%s.%N)"
mark_timing minio "$MI_START_AT" "$MI_START_EPOCH" "$MI_END_AT" "$MI_END_EPOCH"

# =====================================================================
# 3.1 MinIO 桶语义基线（版本 / ILM 规则 / 标签 / 对象用户元数据）
# =====================================================================
# [F13] mc mirror 结构性只反映对象的**当前版本**：不复制版本历史、不复制 ILM 规则、
#   不复制对象用户元数据与 tag。恢复侧要证明这些维度，必须先有一份**备份时刻**的语义基线：
#   本步骤对生产桶做一次**只读** capture，产出 $DIR/bucket-semantics.json，
#   供 scripts/backup/minio-metadata-verify.sh verify 与恢复目标桶逐项比对。
#   历史缺陷：备份流程从未调用过 capture，因此所有已产出备份目录都没有语义基线，
#   DR gate 第 4 项（MinIO 版本/ILM/标签/元数据）在任何备份上都**永远无法取证**
#   （不是环境问题，是流程缺口）。capture 只新建不覆写（见该脚本 D1）。
#   基线缺失 = 本次备份标记 __INCOMPLETE（内容仍可用于内容演练，需显式 RESTORE_DRILL_ACCEPT_INCOMPLETE=1），
#   绝不静默降级成"完整"。
SEM_BUCKETS=""
for b in $BUCKETS; do
  case " $SKIP_BUCKETS " in *" $b "*) continue ;; esac
  SEM_BUCKETS="$SEM_BUCKETS $b"
done
SEM_BUCKETS="${SEM_BUCKETS# }"
if [ -z "$SEM_BUCKETS" ]; then
  mark_incomplete "MinIO 桶语义 capture 未执行：没有任何桶需要覆盖（全部被 BACKUP_SKIP_BUCKETS 排除）"
else
  echo "[3.1/4] minio 桶语义 capture（版本/ILM/标签/元数据）：$SEM_BUCKETS"
  if ! MINIO_META_BUCKETS="$SEM_BUCKETS" bash "$PROJECT/scripts/backup/minio-metadata-verify.sh" capture "$DIR" > "$WORK/minio-semantics.out" 2> "$WORK/minio-semantics.err"; then
    mark_incomplete "MinIO 桶语义 capture 失败（rc!=0）：版本/ILM/标签/元数据维度在恢复侧无法取证（见 $WORK/minio-semantics.err 与 $DIR/bucket-semantics.json）"
  elif [ ! -s "$DIR/minio/bucket-semantics.json" ] && [ ! -s "$DIR/bucket-semantics.json" ]; then
    # 注意：minio-metadata-verify.sh 把基线写在 <备份目录>/minio/bucket-semantics.json
    # （脚本内 INV = DIR/minio）。2026-09-17 修正：原检查写成 $DIR/bucket-semantics.json，
    # 明明采集成功却被判 __INCOMPLETE（当日首跑每日备份即因此误判）。这里两个位置都接受。
    mark_incomplete "MinIO 桶语义 capture 未产出 $DIR/minio/bucket-semantics.json（恢复侧元数据比对没有基线）"
  else
    SEM_FILE="$DIR/minio/bucket-semantics.json"; [ -s "$SEM_FILE" ] || SEM_FILE="$DIR/bucket-semantics.json"
    echo "  bucket-semantics.json 已产出：$(wc -c < "$SEM_FILE" | tr -d ' ') 字节（$SEM_FILE）"
  fi
fi

# =====================================================================
# 4. manifest + 校验
# =====================================================================
echo "[4/4] manifest"
FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
FINISHED_EPOCH="$(date +%s.%N)"

export S7_TS="$TS"
export S7_STARTED_AT="$STARTED_AT"
export S7_STARTED_EPOCH="$STARTED_EPOCH"
export S7_FINISHED_AT="$FINISHED_AT"
export S7_FINISHED_EPOCH="$FINISHED_EPOCH"
export S7_PG_SNAPSHOT_CONSISTENT="$PG_SNAPSHOT_CONSISTENT"
export S7_PG_SNAPSHOT_NOTE="$PG_SNAPSHOT_NOTE"
export S7_SKIPPED_BUCKETS="$SKIPPED_HIT"
export S7_BUCKETS="$BUCKETS"

# [F3] 检查退出码：manifest 生成失败必须 fail，绝不再打印 backup done。
python3 - "$DIR" <<'PY'
import hashlib, json, os, sys, time
d = sys.argv[1]
env = os.environ

def sha(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()

def read(name):
    p = os.path.join(d, name)
    try:
        with open(p) as f:
            return f.read().strip()
    except OSError:
        return None

files = {}
for root, _, names in os.walk(d):
    for n in names:
        p = os.path.join(root, n)
        if n == "manifest.json":
            continue
        # 统一用 / 分隔（os.path.relpath 在 Windows 上会给 \\，会让下游按 manifest 键匹配时全部失配）
        rel = os.path.relpath(p, d).replace(os.sep, "/")
        files[rel] = {"size": os.path.getsize(p), "sha256": sha(p)}

# [F2] object_count 文件在 DIR/minio/<bucket>.object_count，不是 DIR/<bucket>.object_count。
minio_counts = {}
minio_dir = os.path.join(d, "minio")
if os.path.isdir(minio_dir):
    for entry in sorted(os.listdir(minio_dir)):
        if os.path.isdir(os.path.join(minio_dir, entry)):
            minio_counts[entry] = read(os.path.join("minio", entry + ".object_count"))

skipped = [b for b in env.get("S7_SKIPPED_BUCKETS", "").split() if b]

# 一致性窗口：三个 store 各自的起止时间 + 整体窗口（**不得**用行数 delta 宣称一致性）。
stores = {}
for line in (read("consistency.timing") or "").splitlines():
    parts = line.split()
    if len(parts) != 5:
        continue
    name, s_at, s_ep, f_at, f_ep = parts
    try:
        seconds = round(float(f_ep) - float(s_ep), 2)
    except ValueError:
        seconds = None
    stores[name] = {"started_at": s_at, "finished_at": f_at, "seconds": seconds}

window_seconds = None
try:
    window_seconds = round(float(env["S7_FINISHED_EPOCH"]) - float(env["S7_STARTED_EPOCH"]), 2)
except (KeyError, ValueError):
    pass

incomplete = []
p = os.path.join(d, "__INCOMPLETE")
if os.path.exists(p):
    incomplete = [ln.strip() for ln in open(p).read().splitlines() if ln.strip()]
inconsistent = []
p = os.path.join(d, "__INCONSISTENT")
if os.path.exists(p):
    inconsistent = [ln.strip() for ln in open(p).read().splitlines() if ln.strip()]

snapshot_consistent = env.get("S7_PG_SNAPSHOT_CONSISTENT", "false") == "true"
rowcounts = {}
rowcount_errors = []
for line in (read("clickhouse.rowcounts") or "").splitlines():
    if line.startswith("#"):
        continue
    parts = line.split()
    if len(parts) != 4:
        continue
    t, raw, fin, verdict = parts
    rowcounts[t] = {"count": raw, "count_final": fin, "verdict": verdict}
    if verdict != "ok":
        rowcount_errors.append(t)

def num_or_none(name):
    v = read(name)
    if v is None or v.startswith("ERROR:"):
        return v
    return int(v) if v.isdigit() else v

manifest = {
    "backup_id": env.get("S7_TS"),
    "manifest_schema": 2,
    "started_at": env.get("S7_STARTED_AT"),
    "finished_at": env.get("S7_FINISHED_AT"),
    "duration_seconds": window_seconds,
    # [F4] 任何口径查询失败都会把 complete 置假，并列出原因；调用方必须按非 0 退出处理。
    "complete": len(incomplete) == 0,
    "incomplete_reasons": incomplete,
    # [F5] 快照一致性：显式布尔值，退化路径必须为 false 且带原因。
    "pg_snapshot_consistent": snapshot_consistent,
    "pg_snapshot_note": env.get("S7_PG_SNAPSHOT_NOTE") or None,
    "postgres": {
        "dump": "postgres.dump",
        "migration_count": num_or_none("postgres.migration_count"),
        "audit_head_seq": num_or_none("postgres.audit_head_seq"),
        "audit_event_count": num_or_none("postgres.audit_event_count"),
        "pg_snapshot_id": read("postgres.snapshot_id"),
        "pg_current_wal_lsn": read("postgres.wal_lsn"),
        "metrics_read_at": read("postgres.metrics_read_at"),
        "read_within_exported_snapshot": snapshot_consistent,
    },
    "clickhouse": {
        "archive": "clickhouse.tar.gz",
        "engine": "File",
        "engine_note": "本实例无 backups 磁盘（system.disks 只有 default），使用 config.xml 的 backups.allowed_path，即 BACKUP DATABASE ... TO File('<name>')",
        "table_count": num_or_none("clickhouse.table_count"),
        "rowcounts": rowcounts,
        "rowcount_note": "ReplacingMergeTree 一致性判定必须用 count_final；裸 count 会把未合并的历史版本算进去",
        "rowcount_errors": rowcount_errors,
    },
    "minio": minio_counts,
    "minio_detail": {
        "buckets": sorted(minio_counts.keys()),
        "skipped_buckets": skipped,
        "mirror_note": "mc mirror 只反映当前对象版本，不复制版本历史 / 生命周期规则 / 对象元数据与 tag",
    },
    "consistency": {
        "cross_store_consistency": "bounded_by_window",
        "cross_store_consistency_note": "PG / CH / MinIO 三段备份不是同一个原子时刻；只能声明落在下方窗口内。禁止用行数 delta 宣称跨库一致性。",
        "window_started_at": env.get("S7_STARTED_AT"),
        "window_finished_at": env.get("S7_FINISHED_AT"),
        "window_seconds": window_seconds,
        "stores": stores,
        "pg_snapshot_consistent": snapshot_consistent,
        "pg_snapshot_id": read("postgres.snapshot_id"),
        "pg_current_wal_lsn": read("postgres.wal_lsn"),
        "inconsistent_reasons": inconsistent,
    },
    "files": files,
}

tmp = os.path.join(d, "manifest.json.tmp")
with open(tmp, "w") as f:
    json.dump(manifest, f, indent=2, ensure_ascii=False)
os.replace(tmp, os.path.join(d, "manifest.json"))
print(json.dumps({k: v for k, v in manifest.items() if k not in ("files", "clickhouse")}, indent=2, ensure_ascii=False))
PY
MANIFEST_RC=$?
if [ "$MANIFEST_RC" -ne 0 ]; then fail "manifest 生成失败（python rc=$MANIFEST_RC）"; fi

# 二次校验：文件存在、可解析、必需字段齐全（少了就 fail，绝不打印 done 了事）
if ! python3 - "$DIR/manifest.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
missing = [k for k in ("backup_id", "complete", "pg_snapshot_consistent", "postgres", "clickhouse", "minio", "consistency", "files") if k not in m]
if missing:
    print("manifest 缺少字段: " + ",".join(missing), file=sys.stderr)
    sys.exit(3)
if "postgres.dump" not in m["files"]:
    print("manifest.files 中缺少 postgres.dump", file=sys.stderr)
    sys.exit(4)
if m["consistency"].get("cross_store_consistency") != "bounded_by_window":
    print("manifest.consistency.cross_store_consistency 缺失/非法", file=sys.stderr)
    sys.exit(5)
PY
then
  fail "manifest.json 校验失败（字段缺失或内容非法）"
fi

# =====================================================================
# 4.1 [F17] 清理 ClickHouse 容器内的**解包备份目录**
# =====================================================================
# ClickHouse 的 `BACKUP DATABASE ... TO File(...)` 会把整库解包写到
#   /var/lib/clickhouse/backups/<backup_id>/   （实测每份 ~21 GB）
# 而我们的归档 clickhouse.tar.gz 只是它的副本。不清理的话，**每日备份会让数据盘每天净增 ~21 GB**
# —— 2026-09-17 实测该目录已累积 6 份 / 159 GB，是当时数据盘最大的单项占用。
# 时机：放在 manifest 校验**之后** —— manifest 生成阶段已对 clickhouse.tar.gz 做过完整 sha256 读，
#       等于顺带验证了归档可读，此时删除解包目录才安全。删除失败只告警（归档已产出、可人工清理）。
if [ -d "$DIR" ] && [ -s "$DIR/clickhouse.tar.gz" ]; then
  if docker exec deploy-clickhouse-1 sh -c "rm -rf /var/lib/clickhouse/backups/$TS" 2>/dev/null; then
    echo "  已清理容器内解包目录 /var/lib/clickhouse/backups/$TS（归档 clickhouse.tar.gz 保留）"
  else
    echo "  WARN: 容器内解包目录 /var/lib/clickhouse/backups/$TS 未删除（归档已产出，可人工清理）" >&2
  fi
fi

# =====================================================================
# 5. 结论 + 保留策略（默认禁用）
# =====================================================================
INC="$(incomplete_count)"
if [ "$INC" -ne 0 ]; then
  echo "backup 完成但**不完整**（$INC 条）: $DIR" >&2
  cat "$DIR/__INCOMPLETE" >&2
  echo "证据已保留（__INCOMPLETE 标记，不会被裁剪）；退出码 1，禁止当作成功备份" >&2
  if [ -f "$DIR/__INCONSISTENT" ]; then
    echo "一致性边界未建立（__INCONSISTENT）：" >&2
    cat "$DIR/__INCONSISTENT" >&2
  fi
  echo "backup INCOMPLETE: $DIR"
  cleanup_scratch
  exit 1
fi

echo "backup done: $DIR ($(du -sh "$DIR" | cut -f1))"

# [F1] 保留策略：默认禁用；只有显式 DAYS>0 **且** APPROVED=1 才裁剪。
#      裁剪顺序固定为「先出清单（日志 + 裁剪记录文件）→ 再删除」，且只在本次备份完整时进入。
if [ "$RETENTION_DAYS" -gt 0 ] 2>/dev/null; then
  if [ "$RETENTION_APPROVED" != "1" ]; then
    echo "保留策略：BACKUP_RETENTION_DAYS=$RETENTION_DAYS 但 BACKUP_RETENTION_APPROVED != 1（无书面批准的保留策略）→ 本次不删除任何备份"
  else
    RETENTION_LOG="$BACKUP_ROOT/retention-$TS.log"
    KEEP_LIST="$WORK/keep.list"
    DEL_LIST="$WORK/del.list"
    : > "$KEEP_LIST"
    : > "$DEL_LIST"
    # 候选集由 ls -1dt 给出（最新在前）。目录名本身是 UTC 时间戳，字典序即时间序；
    # 这里用 while read 遍历而不是 for 遍历命令替换结果，路径含空格也不会被词拆分。
    ls -1dt "$BACKUP_ROOT"/20*/ 2>/dev/null | sed -e 's:/$::' > "$WORK/all.list"
    echo "保留策略候选目录：$(wc -l < "$WORK/all.list" | tr -d ' ') 个（最新在前）"
    idx=0
    while IFS= read -r d; do
      [ -n "$d" ] || continue
      idx=$((idx+1))
      if [ "$d" = "$DIR" ]; then echo "$d 本次备份（永远保留）" >> "$KEEP_LIST"; continue; fi
      if [ "$idx" -le "$RETENTION_KEEP_MIN" ]; then
        echo "$d 最近 $RETENTION_KEEP_MIN 份之一（保底保留）" >> "$KEEP_LIST"; continue
      fi
      if [ ! -f "$d/manifest.json" ]; then
        echo "$d 无 manifest.json：非本脚本产出，绝不删除" >> "$KEEP_LIST"; continue
      fi
      if [ -e "$d/__FAILED" ] || [ -e "$d/__INCONSISTENT" ] || [ -e "$d/__INCOMPLETE" ]; then
        echo "$d 带失败/不完整标记：保留供诊断" >> "$KEEP_LIST"; continue
      fi
      aged="$(find "$d" -maxdepth 0 -mtime +"$RETENTION_DAYS" -print 2>/dev/null)"
      if [ -n "$aged" ]; then
        echo "$d" >> "$DEL_LIST"
      else
        echo "$d 未超过 $RETENTION_DAYS 天" >> "$KEEP_LIST"
      fi
    done < "$WORK/all.list"
    DEL_N="$(wc -l < "$DEL_LIST" | tr -d ' ')"
    {
      echo "# 备份裁剪记录 $TS"
      echo "# 策略：BACKUP_RETENTION_DAYS=$RETENTION_DAYS BACKUP_RETENTION_APPROVED=$RETENTION_APPROVED BACKUP_RETENTION_KEEP_MIN=$RETENTION_KEEP_MIN"
      echo "# 本次备份目录（已完成且完整）：$DIR"
      echo "## 将被删除（$DEL_N 个）"
      if [ "$DEL_N" -eq 0 ]; then echo "(无)"; else cat "$DEL_LIST"; fi
      echo "## 保留"
      cat "$KEEP_LIST"
    } | tee "$RETENTION_LOG"
    if [ "$DEL_N" -gt 0 ]; then
      while IFS= read -r d; do
        [ -n "$d" ] || continue
        # 删除前再确认一次：只删带 manifest.json 且无失败标记的目录（TOCTOU 兜底）
        if [ ! -f "$d/manifest.json" ] || [ -e "$d/__FAILED" ] || [ -e "$d/__INCONSISTENT" ] || [ -e "$d/__INCOMPLETE" ]; then
          echo "跳过（复核未通过）：$d"
          continue
        fi
        echo "删除旧备份：$d"
        rm -rf "$d"
      done < "$DEL_LIST"
    fi
  fi
else
  echo "保留策略：已禁用（BACKUP_RETENTION_DAYS=$RETENTION_DAYS；默认 0，不自动删除任何备份）"
fi

cleanup_scratch
exit 0
