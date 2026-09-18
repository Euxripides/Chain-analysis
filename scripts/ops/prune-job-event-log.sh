#!/usr/bin/env bash
# =====================================================================
# job_event_log 进度事件保留清理（用户指令 2026-09-17）
#
# 背景：`job_event_log` 是 SSE 的 Last-Event-ID 重放日志。实测该表 99.9% 的行是
#   `event_type=progress`（2,027,041 行 / 全表 2,028,723 行），而进度事件**每秒每活跃任务**一条，
#   导致表涨到 3.99 GB、并放大 WAL 与 autovacuum 开销。
#
# 口径（只删进度类，且只删过期的）：
#   DELETE ... WHERE event_type = 'progress' AND created_at < now() - interval '<N> hours'
#   状态/终态/控制/分片类事件（jobs / shards / control）**一律不删** —— SSE 重放与审计依赖它们。
#
# 用法：
#   bash scripts/ops/prune-job-event-log.sh              # 真删（默认保留 24 小时）
#   bash scripts/ops/prune-job-event-log.sh --dry-run    # 只统计不删除
# 环境变量：
#   PRUNE_RETENTION_HOURS  默认 24
#   PRUNE_BATCH            每批删除行数，默认 20000（分批避免长事务与长锁）
# 输出：acceptance-evidence/dual-system-v1/sprint7/ops/prune-job-event-log-<TS>.log
# 退出码：0 = 成功（含"没有需要删的"）；2 = 前置/用法错误
# =====================================================================
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
HOURS="${PRUNE_RETENTION_HOURS:-24}"
BATCH="${PRUNE_BATCH:-20000}"
DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1

cd "$PROJECT" || exit 2
set -a; . deploy/.env; set +a
EV="$PROJECT/acceptance-evidence/dual-system-v1/sprint7/ops"
mkdir -p "$EV" || exit 2
TS="$(date -u +%Y%m%dT%H%M%SZ)"
LOG="$EV/prune-job-event-log-$TS.log"

psql_at() { sudo -n docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"; }

exec > >(tee "$LOG") 2>&1
echo "== job_event_log 清理 $TS（保留 ${HOURS}h，批大小 $BATCH，dry-run=$DRY）=="
BEFORE_TOTAL="$(psql_at "select count(*) from job_event_log")"
BEFORE_SIZE="$(psql_at "select pg_size_pretty(pg_total_relation_size('job_event_log'))")"
CAND="$(psql_at "select count(*) from job_event_log where event_type='progress' and created_at < now() - interval '$HOURS hours'")"
echo "清理前：总行数=$BEFORE_TOTAL 表大小=$BEFORE_SIZE 待删进度行=$CAND"

if [ "$DRY" = "1" ]; then
  echo "dry-run：未删除任何行"
  exit 0
fi

TOTAL_DEL=0
while :; do
  DEL="$(psql_at "with d as (select id from job_event_log where event_type='progress' and created_at < now() - interval '$HOURS hours' limit $BATCH) delete from job_event_log t using d where t.id = d.id returning 1" | wc -l | tr -d ' ')"
  [ -z "$DEL" ] && DEL=0
  TOTAL_DEL=$((TOTAL_DEL + DEL))
  echo "  已删除 $TOTAL_DEL 行"
  [ "$DEL" -lt "$BATCH" ] && break
done
AFTER_TOTAL="$(psql_at "select count(*) from job_event_log")"
AFTER_SIZE="$(psql_at "select pg_size_pretty(pg_total_relation_size('job_event_log'))")"
echo "清理后：总行数=$AFTER_TOTAL 表大小=$AFTER_SIZE（注意：DELETE 只标记死元组，空间要靠 VACUUM FULL/pg_repack 回收）"
echo "本次删除 $TOTAL_DEL 行；日志：$LOG"
