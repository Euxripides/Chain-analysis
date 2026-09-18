#!/usr/bin/env bash
# S7-DR-01：进程级 Kill -9 恢复演练（Worker / API / NATS）。
#
# 覆盖 §27 DATA-006（Worker Kill-9 保留游标与成功页、恢复无幽灵任务）与 DATA-007（NATS 重启后 Outbox 补发与幂等）。
#
# 【独立复核修复，2026-09】编号对应 scripts/backup/README-DR-FIXES.md：
#   [F11] checkpoint 核对不再只看总 count：演练前冻结基线，演练后**逐 (shard, page_no)** 比对
#         page_no / downloaded_rows 不回退；逐页比对 next_page_token_hash 不变化；成功页
#         （download_pages 中 VALIDATED / INGESTED / DURABLE）集合不丢失且状态不回退；
#         Provider 成功页（provider_usage.status in ('success','ok')）按 shard 计数不回退。
#   [F12] 「audit 无重复」用 audit_events_v2 id 主键**不能**证明消费者幂等。现在：
#         ① 断言 outbox_events 的 id 集合「演练后仍全部存在」（这是 outbox 不丢事件的证据）；
#         ② 消费者幂等必须用真实的 event id / Nats-Msg-Id 维度核对——先在库里找投递/去重表，
#            找到就按同一 ID 集合比对；找不到就如实写 NOT_VERIFIED（并记录 JetStream 只读证据），
#            **不再用主键唯一性冒充幂等**；audit_events_v2 的重复 id 计数只作为链完整性观察项单独列出。
#   [F13] 演练前先 baseline（冻结 job / shard / checkpoint / page / provider / outbox 的 ID 集合），
#         演练后**按同一 ID 集合**核对，绝不按「当前活跃 status」重新筛选（否则丢行会被筛掉看不见）。
#   另修：旧脚本把「残留 IN_FLIGHT 页」当作幽灵任务，但代码里**不存在 IN_FLIGHT 这个状态**
#         （状态机只有 DURABLE / INGESTED / VALIDATED / FAILED），该检查恒为 0、属于假成功；
#         现在改为「非预期状态页」+「未被回收的过期租约（stale lease）」两项真实检查。
#
# 用法：bash scripts/backup/kill9-drill.sh
# 环境：KILL9_BASELINE_MAX_ROWS（基线单表最多抓取行数，默认 200000；截断会显式标注为 PARTIAL）
# 输出：acceptance-evidence/dual-system-v1/sprint7/dr/kill9-<TS>.md 与 kill9-<TS>.logs/
# 退出码：0 = 全部不变量通过；1 = 存在 FAIL（证据全部保留，绝不自动删除）

set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/dr}"
BASELINE_MAX="${KILL9_BASELINE_MAX_ROWS:-200000}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT="$EV/kill9-$TS.md"
WORK="$EV/kill9-$TS.logs"
BASELINE="$WORK/baseline"
AFTER="$WORK/after"
FAILURES="$WORK/FAILURES.txt"
NOT_VERIFIED="$WORK/NOT_VERIFIED.txt"
NOT_VERIFIED_STRUCTURAL="$WORK/NOT_VERIFIED.structural.txt"
NOT_VERIFIED_CHECK="$WORK/NOT_VERIFIED.check.txt"
PARTIAL="$WORK/PARTIAL.txt"
TRUNCATED="$WORK/truncated.txt"
RESULT="$EV/kill9-$TS.json"
FENCE='```'
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

mkdir -p "$EV" "$WORK" "$BASELINE" "$AFTER" || { echo "无法创建输出目录 $EV / $WORK" >&2; exit 2; }
: > "$FAILURES"
: > "$NOT_VERIFIED"
: > "$NOT_VERIFIED_STRUCTURAL"
: > "$NOT_VERIFIED_CHECK"
: > "$PARTIAL"
: > "$TRUNCATED"

fail_check() { echo "$1" >> "$FAILURES"; echo "FAIL: $1" | tee -a "$REPORT" >&2; }
# [GATE-01] NOT_VERIFIED 分级（**仅用于诊断，不构成任何豁免**）：
#   structural = 脚本层结构性无法证明（需另配工具 / 产品侧改造才能真正闭合）
#   check      = 本次核对未完成（环境/取值/前置缺失）
#   两种分级都**不构成通过**：scripts/s7/dr-gate.sh 对任何分级一律阻断整体验收。
not_verified() { # [class] message  或  message（默认 check）
  local cls="check" msg="$1"
  if [ "$#" -ge 2 ]; then cls="$1"; msg="$2"; fi
  case "$cls" in structural|check) ;; *) cls="check" ;; esac
  printf '%s|%s\n' "$cls" "$msg" >> "$NOT_VERIFIED"
  if [ "$cls" = "structural" ]; then printf '%s\n' "$msg" >> "$NOT_VERIFIED_STRUCTURAL"
  else printf '%s\n' "$msg" >> "$NOT_VERIFIED_CHECK"; fi
  echo "NOT_VERIFIED[$cls]: $msg" | tee -a "$REPORT" >&2
}
partial() { echo "$1" >> "$PARTIAL"; echo "PARTIAL: $1" | tee -a "$REPORT" >&2; }
abort_hard() { echo "ABORT: $*" >&2; exit 2; }

set -a
if ! . "$PROJECT/deploy/.env"; then abort_hard "无法加载 $PROJECT/deploy/.env"; fi
set +a
if [ -f /tmp/s6-e2e-env.sh ]; then . /tmp/s6-e2e-env.sh; fi

pgq() { docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -F '|' -c "$1"; }
pgn() { docker exec -e "PGPASSWORD=$POSTGRES_PASSWORD" deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"; }
is_num() { case "$1" in ''|*[!0-9]*) return 1 ;; *) return 0 ;; esac; }
BASE=http://127.0.0.1:8080

# 基线/复核对使用**完全相同**的 SQL，保证可比；LIMIT 只用于防止意外全表拉取。
SQL_JOBS="select id::text, status from download_jobs order by id limit $BASELINE_MAX"
SQL_SHARDS="select id::text, job_id::text, status, downloaded_rows from download_shards order by id limit $BASELINE_MAX"
SQL_CKPT="select shard_id::text, page_no, downloaded_rows, coalesce(md5(next_page_token),'') from checkpoints order by shard_id limit $BASELINE_MAX"
SQL_PAGES="select shard_id::text, page_no, status, next_page_token_hash, rows_inserted from download_pages order by shard_id, page_no limit $BASELINE_MAX"
SQL_PROVIDER="select coalesce(shard_id::text,'-'), count(*)::text from provider_usage where status in ('success','ok') group by 1 order by 1 limit $BASELINE_MAX"
SQL_GHOST="select count(*)::text from download_shards where status='RUNNING' and coalesce(lease_owner,'') <> '' and (leased_until is null or leased_until < now())"
# 2026-09-16 修正：白名单漏了 FAILED_FINAL —— 它是**合法终态**（backend/internal/model/model.go:37
# ShardFailedFinal；engine.go:109 明确 "permanent = FAILED_FINAL（原始 spool 对象不可恢复）"）。
# 漏掉它会让演练把 2 条合法终态页判成"非预期状态"，产出一个假 FAIL（基线 2 → 演练后 2，与 Kill -9 无关）。
SQL_ODD_PAGES="select count(*)::text from download_pages where status not in ('DURABLE','INGESTED','VALIDATED','FAILED','FAILED_FINAL')"

snap() { # snap <目录> <文件名> <SQL> <标签>
  local dir="$1" name="$2" sql="$3" label="$4" out rc n
  out="$(pgq "$sql" 2>"$WORK/pg.err")"; rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "基线/复核对查询失败：$label（rc=$rc）：$(paste -sd ' ' "$WORK/pg.err" | cut -c1-300)" >&2
    return 1
  fi
  echo "$out" > "$dir/$name"
  n="$(wc -l < "$dir/$name" | tr -d ' ')"
  if [ "$n" -ge "$BASELINE_MAX" ]; then
    echo "$name" >> "$TRUNCATED"
    echo "WARN: $label 达到抓取上限 $BASELINE_MAX，该表核对为 PARTIAL" >&2
    # [GATE-02] PARTIAL 必须同时进入机器可读面与 NV 统计；
    # 否则会出现"只核对了前缀却 exit 0"的假通过。
    partial "$label（数据集 $name，上限 $BASELINE_MAX）"
    not_verified check "$label 的基线达到抓取上限 $BASELINE_MAX（数据集 $name）——该表核对不完整，不得计入通过"
  fi
  return 0
}

wait_ready() { # wait_ready <max_seconds>
  local n=0
  while [ "$n" -lt "$1" ]; do
    if curl -sf -o /dev/null --max-time 2 "$BASE/health/ready"; then echo "$n"; return 0; fi
    sleep 1; n=$((n+1))
  done
  echo "-1"; return 1
}

wait_until_equal() { # wait_until_equal <SQL> <期望值> <最多秒> → 打印最终值
  local sql="$1" want="$2" max="$3" i=0 v=""
  while [ "$i" -lt "$max" ]; do
    v="$(pgq "$sql")"
    if [ "$v" = "$want" ]; then echo "$v"; return 0; fi
    sleep 2; i=$((i+2))
  done
  echo "$v"; return 1
}

{
echo "# Kill -9 恢复演练（$TS）"
echo
echo "- 目标服务：blockchain-worker.service / blockchain-api.service（systemd **用户**单元）、deploy-nats-1（docker）"
echo "- 判定口径：进程被杀后必须由编排自动拉起；数据侧必须满足下表不变量，否则记 FAIL。"
echo "- **核对口径**：演练前冻结 job / shard / checkpoint / page / provider / outbox 的 ID 集合（见 $BASELINE），"
echo "  演练后**按同一 ID 集合**核对；不按当前活跃 status 重新筛选（否则丢失的行会被筛掉、看不见）。"
echo "- 证据目录：$WORK（基线、复核对 TSV、原始输出全部保留，不自动删除）"
echo
} > "$REPORT"

# =====================================================================
# 0) 基线（演练前冻结 ID 集合）
# =====================================================================
echo "== 0. baseline =="
snap "$BASELINE" jobs.tsv "$SQL_JOBS" "download_jobs 基线" || fail_check "无法获取 download_jobs 基线"
snap "$BASELINE" shards.tsv "$SQL_SHARDS" "download_shards 基线" || fail_check "无法获取 download_shards 基线"
snap "$BASELINE" checkpoints.tsv "$SQL_CKPT" "checkpoints 基线" || fail_check "无法获取 checkpoints 基线"
snap "$BASELINE" pages.tsv "$SQL_PAGES" "download_pages 基线" || fail_check "无法获取 download_pages 基线"
snap "$BASELINE" provider.tsv "$SQL_PROVIDER" "provider_usage 成功页基线" || fail_check "无法获取 provider_usage 基线"

OUTBOX_TABLE="$(pgn "select table_name from information_schema.tables where table_schema='public' and table_name like '%outbox%' order by table_name limit 1" 2>"$WORK/pg.err")"
OUTBOX_RC=$?
if [ "$OUTBOX_RC" -ne 0 ] || [ -z "$OUTBOX_TABLE" ]; then
  OUTBOX_TABLE=""
  not_verified check "outbox 表探测失败或不存在，outbox id 集合断言无法执行（见 $WORK/pg.err）"
else
  snap "$BASELINE" outbox.tsv "select id::text from $OUTBOX_TABLE order by id limit $BASELINE_MAX" "outbox 基线" || fail_check "无法获取 $OUTBOX_TABLE 基线"
fi

GHOST_BEFORE="$(pgq "$SQL_GHOST")"
ODD_PAGES_BEFORE="$(pgq "$SQL_ODD_PAGES")"
if ! is_num "$GHOST_BEFORE" || ! is_num "$ODD_PAGES_BEFORE"; then
  fail_check "无法读取 baseline 的 stale lease / 非预期状态页计数（见 $WORK/pg.err）"
fi
FOCUS_JOB="$(pgn "select id::text from download_jobs where status='RUNNING' order by updated_at desc limit 1" 2>/dev/null)"
echo "$FOCUS_JOB" > "$WORK/focus_job.txt"

JOBS_N="$(wc -l < "$BASELINE/jobs.tsv" | tr -d ' ')"
SHARDS_N="$(wc -l < "$BASELINE/shards.tsv" | tr -d ' ')"
CKPT_N="$(wc -l < "$BASELINE/checkpoints.tsv" | tr -d ' ')"
PAGES_N="$(wc -l < "$BASELINE/pages.tsv" | tr -d ' ')"
PROV_N="$(wc -l < "$BASELINE/provider.tsv" | tr -d ' ')"
OUTBOX_N=""
if [ -f "$BASELINE/outbox.tsv" ]; then OUTBOX_N="$(wc -l < "$BASELINE/outbox.tsv" | tr -d ' ')"; fi
{
echo "## 0. 基线（冻结 ID 集合）"
echo
echo "| 数据集 | 基线行数 |"
echo "|---|---:|"
echo "| download_jobs | $JOBS_N |"
echo "| download_shards | $SHARDS_N |"
echo "| checkpoints | $CKPT_N |"
echo "| download_pages | $PAGES_N |"
echo "| provider_usage(success/ok) 按 shard 分组 | $PROV_N |"
echo "| outbox（$OUTBOX_TABLE） | ${OUTBOX_N:-n/a} |"
echo
echo "- 关注作业（基线 RUNNING，最近更新）：${FOCUS_JOB:-none}"
echo "- 基线 stale lease（RUNNING 且租约已过期）= $GHOST_BEFORE；非预期状态页 = $ODD_PAGES_BEFORE"
} >> "$REPORT"

# =====================================================================
# 1) Worker Kill -9（DATA-006）
# =====================================================================
echo "== 1. worker kill -9 =="
WPID_BEFORE="$(systemctl --user show -p MainPID --value blockchain-worker 2>"$WORK/systemctl.err")"
SYS_RC=$?
if [ "$SYS_RC" -ne 0 ] || [ -z "$WPID_BEFORE" ] || [ "$WPID_BEFORE" = "0" ]; then
  fail_check "无法取得 blockchain-worker 的 MainPID（rc=$SYS_RC，值=$WPID_BEFORE，见 $WORK/systemctl.err）"
  WPID_BEFORE=""
fi
{
echo
echo "## 1. Worker Kill -9"
echo
echo "- 演练前：RUNNING 作业 ${FOCUS_JOB:-none}；基线见 §0"
echo "- 目标 PID：${WPID_BEFORE:-未取得}（kill -9，SIGKILL，不做优雅退出）"
} >> "$REPORT"
kill -9 "$WPID_BEFORE" 2>"$WORK/kill-worker.err"
KILL_RC=$?
if [ "$KILL_RC" -ne 0 ]; then fail_check "kill -9 worker（pid=$WPID_BEFORE）失败 rc=$KILL_RC：$(paste -sd ' ' "$WORK/kill-worker.err")"; fi
T0="$(date +%s.%N)"
WSEC=""
NEWPID=""
for i in $(seq 1 60); do
  NEWPID="$(systemctl --user show -p MainPID --value blockchain-worker 2>/dev/null)"
  if [ -n "$NEWPID" ] && [ "$NEWPID" != "0" ] && [ "$NEWPID" != "$WPID_BEFORE" ] && kill -0 "$NEWPID" 2>/dev/null; then
    T1="$(date +%s.%N)"; WSEC="$(python3 -c "print(round($T1-$T0,2))")"; break
  fi
  sleep 1
done
if [ -z "$WSEC" ]; then fail_check "worker 在 60 秒内未被 systemd 拉起（未观察到新 PID）"; fi
sleep 5
STATE="$(systemctl --user is-active blockchain-worker 2>/dev/null)"
if [ "$STATE" != "active" ]; then fail_check "worker 单元状态不是 active（实际：$STATE）"; fi
GHOST_AFTER="$(wait_until_equal "$SQL_GHOST" "$GHOST_BEFORE" 60)"
ODD_PAGES_AFTER="$(pgq "$SQL_ODD_PAGES")"
JOB_AFTER="$(pgn "select status from download_jobs where id='${FOCUS_JOB:-00000000-0000-0000-0000-000000000000}'" 2>/dev/null)"
{
echo "- **RTO（新进程存活）= ${WSEC:-未观察到} s**；单元状态 = $STATE；新 PID = ${NEWPID:-none}"
echo "- 原 RUNNING 作业状态：${JOB_AFTER:-none}"
echo "- stale lease（未被回收的过期租约）：$GHOST_BEFORE → $GHOST_AFTER（等待上限 60s，期望回到基线）"
echo "- 非预期状态页（状态机只有 DURABLE/INGESTED/VALIDATED/FAILED）：$ODD_PAGES_BEFORE → $ODD_PAGES_AFTER"
echo "- 逐 (shard, page) 的详细不变量核对见 §4（按 §0 冻结的 ID 集合比对）"
} >> "$REPORT"
if [ "$GHOST_AFTER" != "$GHOST_BEFORE" ]; then
  fail_check "worker Kill-9 后存在未被回收的过期租约：$GHOST_BEFORE → $GHOST_AFTER（幽灵任务信号）"
fi
if ! is_num "$ODD_PAGES_AFTER" || [ "$ODD_PAGES_AFTER" != "0" ]; then
  fail_check "download_pages 出现非预期状态：计数=${ODD_PAGES_AFTER:-未取得}（应为 0）"
fi
if [ -n "$FOCUS_JOB" ] && [ -n "$JOB_AFTER" ] && [ "$JOB_AFTER" != "RUNNING" ]; then
  not_verified check "原 RUNNING 作业在演练后状态为 $JOB_AFTER（非 RUNNING）：需人工判读是正常完成还是被中断，不能算通过"
fi

# =====================================================================
# 2) API Kill -9（会话与审计链连续性）
# =====================================================================
echo "== 2. api kill -9 =="
JAR="$WORK/api-cookies.txt"
CSRF=""
SESS_BEFORE=""
CODE_LOGIN=""
if [ -n "${ADMIN_PASSWORD:-}" ]; then
  LOGIN_BODY='{"identifier":"admin@local","password":"'"$ADMIN_PASSWORD"'","audience":"admin"}'
  CODE_LOGIN="$(curl -s -o "$WORK/login.json" -w '%{http_code}' -c "$JAR" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "$LOGIN_BODY" "$BASE/api/auth/v1/login")"
  if [ "$CODE_LOGIN" != "200" ] && [ "$CODE_LOGIN" != "204" ]; then
    not_verified check "登录失败（HTTP $CODE_LOGIN，见 $WORK/login.json），与登录态相关的核对无法执行"
  else
    CSRF="$(grep -i 'csrf' "$JAR" | awk '{print $7}')"
    SESS_BEFORE="$(pgn "select count(*) from sessions where revoked_at is null" 2>/dev/null)"
  fi
else
  not_verified check "未提供 ADMIN_PASSWORD（/tmp/s6-e2e-env.sh 未定义），与登录态相关的核对无法执行"
fi
SEQ_BEFORE="$(pgn "select coalesce(max(seq),0) from audit_events_v2 where chain='audit'" 2>"$WORK/pg.err")"
if ! is_num "$SEQ_BEFORE"; then fail_check "无法读取 audit 链 head seq（见 $WORK/pg.err）"; fi
APID_BEFORE="$(systemctl --user show -p MainPID --value blockchain-api 2>"$WORK/systemctl.err")"
if [ -z "$APID_BEFORE" ] || [ "$APID_BEFORE" = "0" ]; then
  fail_check "无法取得 blockchain-api 的 MainPID（见 $WORK/systemctl.err）"
  APID_BEFORE=""
fi
{
echo
echo "## 2. API Kill -9"
echo
echo "- 演练前：活跃会话 ${SESS_BEFORE:-未取得}，audit 链 head seq=${SEQ_BEFORE:-未取得}，目标 PID=${APID_BEFORE:-未取得}"
} >> "$REPORT"
kill -9 "$APID_BEFORE" 2>"$WORK/kill-api.err"
AKILL_RC=$?
if [ "$AKILL_RC" -ne 0 ]; then fail_check "kill -9 api（pid=$APID_BEFORE）失败 rc=$AKILL_RC：$(paste -sd ' ' "$WORK/kill-api.err")"; fi
T0="$(date +%s.%N)"
READY="$(wait_ready 90)"
if [ "$READY" = "-1" ]; then
  fail_check "API 在 90 秒内未恢复 /health/ready"
else
  T1="$(date +%s.%N)"; ASEC="$(python3 -c "print(round($T1-$T0,2))")"
  echo "- **RTO（/health/ready 恢复 200）= $ASEC s**（含依赖探测），新 PID=$(systemctl --user show -p MainPID --value blockchain-api 2>/dev/null)" >> "$REPORT"
  if [ -n "$CSRF" ]; then
    CODE_SESSION="$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" --max-time 5 "$BASE/api/admin/v1/overview")"
    CODE_AFTER="$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' --max-time 10 "$BASE/api/admin/v1/audit/verify")"
    curl -s -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' --max-time 10 "$BASE/api/admin/v1/audit/verify" > "$WORK/audit_verify.json" 2>"$WORK/audit_verify.err"
    AV_RC=$?
    {
    echo "- 旧会话在重启后仍可用：GET /api/admin/v1/overview → HTTP $CODE_SESSION（期望 200）"
    echo "- 审计链 verify 端点：HTTP $CODE_AFTER（期望 200），curl rc=$AV_RC"
    echo "- verify 响应：$(head -c 600 "$WORK/audit_verify.json")"
    } >> "$REPORT"
    if [ "$CODE_SESSION" != "200" ]; then fail_check "API 重启后旧会话不可用：HTTP $CODE_SESSION（期望 200）"; fi
    if [ "$CODE_AFTER" != "200" ]; then fail_check "audit/verify 端点未返回 200（实际 $CODE_AFTER，见 $WORK/audit_verify.json）"; fi
    if [ ! -s "$WORK/audit_verify.json" ]; then
      fail_check "audit/verify 响应为空（见 $WORK/audit_verify.err）"
    else
      VALID="$(python3 - "$WORK/audit_verify.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print((d.get("data") or {}).get("valid"))
PY
)"
      echo "- audit/verify valid=$VALID" >> "$REPORT"
      if [ "$VALID" != "True" ] && [ "$VALID" != "true" ]; then
        fail_check "audit/verify 返回 valid=$VALID（期望 true，见 $WORK/audit_verify.json）"
      fi
    fi
  fi
fi
SEQ_AFTER="$(pgn "select coalesce(max(seq),0) from audit_events_v2 where chain='audit'" 2>"$WORK/pg.err")"
{
echo "- 审计链：head seq ${SEQ_BEFORE:-未取得} → ${SEQ_AFTER:-未取得}（单调不回退）"
echo "- 不变量：会话不因进程被杀而丢失；审计链在重启后继续推进且可验证"
} >> "$REPORT"
if is_num "$SEQ_BEFORE" && is_num "$SEQ_AFTER"; then
  if [ "$SEQ_AFTER" -lt "$SEQ_BEFORE" ]; then fail_check "audit 链 head seq 回退：$SEQ_BEFORE → $SEQ_AFTER"; fi
else
  fail_check "audit 链 head seq 读取失败（$SEQ_BEFORE → $SEQ_AFTER，见 $WORK/pg.err）"
fi

# =====================================================================
# 3) NATS Kill -9（Outbox 补发与幂等，DATA-007）
# =====================================================================
echo "== 3. nats kill -9 =="
PENDING_SQL="select count(*)::text from $OUTBOX_TABLE where published_at is null"
PENDING_BEFORE="n/a"
if [ -n "$OUTBOX_TABLE" ]; then
  PENDING_BEFORE="$(pgq "$PENDING_SQL" 2>"$WORK/pg.err")"
  if ! is_num "$PENDING_BEFORE"; then
    fail_check "无法读取 $OUTBOX_TABLE 未发布计数（见 $WORK/pg.err）"
    PENDING_BEFORE="n/a"
  fi
fi
sudo -n docker kill -s KILL deploy-nats-1 > "$WORK/nats-kill.log" 2>&1
NATS_KILL_RC=$?
if [ "$NATS_KILL_RC" -ne 0 ]; then fail_check "docker kill -s KILL deploy-nats-1 失败 rc=$NATS_KILL_RC（见 $WORK/nats-kill.log）"; fi
T0="$(date +%s.%N)"
NSEC=""
for i in $(seq 1 60); do
  if curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8222/healthz; then
    T1="$(date +%s.%N)"; NSEC="$(python3 -c "print(round($T1-$T0,2))")"; break
  fi
  sleep 1
done
if [ -z "$NSEC" ]; then fail_check "NATS 监控端点在 60 秒内未恢复（容器看门狗周期约 30s）"; fi
sleep 8
NATS_STATE="$(sudo -n docker inspect -f '{{.State.Status}}' deploy-nats-1 2>"$WORK/nats-inspect.err")"
NATS_INSPECT_RC=$?
if [ "$NATS_INSPECT_RC" -ne 0 ]; then fail_check "无法读取 deploy-nats-1 容器状态（见 $WORK/nats-inspect.err）"; fi
{
echo
echo "## 3. NATS Kill -9"
echo
echo "- outbox 表：${OUTBOX_TABLE:-未找到}"
echo "- **NATS 监控端点恢复耗时 = ${NSEC:-未恢复} s**；容器状态=${NATS_STATE:-未取得}"
} >> "$REPORT"

JSZ_RC=1
JSZ_QUERY="streams=true&consumers=true"
# [F13] NATS 2.10+ 的监控端点只接受**布尔**参数：/jsz?streams=true&consumers=true。
#   历史缺陷：这里固定请求 /jsz?streams=EVENTS&consumers=1，NATS 2.14.6 直接回 HTTP 400，
#   于是把一个**工具缺陷**写成了环境性的 NOT_VERIFIED（"端点不可用"），并顺带掩盖了
#   "EVENTS 流当前没有任何 durable consumer（不存在服务端重投窗口）"这一真实事实。
#   现在按正确参数探测；仍失败再退回无参数 /jsz（只证明端点可用、不含明细，并如实记录）。
if curl -sf --max-time 5 "http://127.0.0.1:8222/jsz?$JSZ_QUERY" -o "$WORK/jsz.json" 2>"$WORK/jsz.err"; then
  JSZ_RC=0
elif curl -sf --max-time 5 "http://127.0.0.1:8222/jsz" -o "$WORK/jsz.json" 2>>"$WORK/jsz.err"; then
  JSZ_RC=0; JSZ_QUERY="(无参数：端点可用但不含 stream/consumer 明细)"
fi
if [ "$JSZ_RC" -eq 0 ]; then
  python3 - "$WORK/jsz.json" "$WORK/nats-evidence.txt" "$JSZ_QUERY" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
out = ["jsz 探测参数：%s" % sys.argv[3]]
for a in (d.get("account_details") or []):
    for s in (a.get("stream_detail") or []):
        st = s.get("state") or {}
        cds = s.get("consumer_detail") or []
        out.append("stream %s: messages=%s first_seq=%s last_seq=%s durable_consumers=%s deleted=%s" % (
            s.get("name"), st.get("messages"), st.get("first_seq"), st.get("last_seq"), len(cds), st.get("deleted")))
        for c in cds:
            out.append("  consumer %s: delivered=%s ack_floor=%s num_ack_pending=%s num_redelivered=%s" % (
                c.get("name"), (c.get("delivered") or {}).get("consumer_seq"),
                (c.get("ack_floor") or {}).get("consumer_seq"), c.get("num_ack_pending"), c.get("num_redelivered")))
        if not cds:
            out.append("  注意：该流没有 durable consumer 明细 —— 服务端不存在重投（redelivery）窗口；"
                       "消费者幂等只能由库内落库维度证明（见下方 ③ 消费者幂等核对）")
if len(out) == 1:
    out.append("jsz 返回中未找到任何 stream 明细（参数 %s）" % sys.argv[3])
open(sys.argv[2], "w").write(chr(10).join(out) + chr(10))
print(chr(10).join(out))
PY
  {
  echo
  echo "### 3.1 JetStream 只读证据（Nats-Msg-Id 去重维度的现状）"
  echo
  echo "$FENCE"text
  cat "$WORK/nats-evidence.txt"
  echo "$FENCE"
  } >> "$REPORT"
else
  not_verified check "NATS JetStream 监控端点 /jsz 不可用（已按布尔参数 /jsz?streams=true&consumers=true 与无参数两种形式探测，均失败；见 $WORK/jsz.err），无法采集 Nats-Msg-Id 去重维度的证据"
fi

DEDUPE_TABLE="$(pgn "select table_name from information_schema.tables where table_schema='public' and table_name not like '%notification%' and (table_name like '%dedupe%' or table_name like '%processed_event%' or table_name like '%consumed_event%' or table_name like '%event_delivery%' or table_name like '%event_offset%' or table_name like '%consumer_offset%' or table_name like '%inbox%') order by table_name limit 1" 2>/dev/null)"
DEDUPE_COL=""
if [ -n "$DEDUPE_TABLE" ]; then
  DEDUPE_COL="$(pgn "select column_name from information_schema.columns where table_schema='public' and table_name='$DEDUPE_TABLE' and column_name in ('event_id','nats_msg_id','msg_id','message_id','id') order by case column_name when 'event_id' then 1 when 'nats_msg_id' then 2 when 'msg_id' then 3 when 'message_id' then 4 else 5 end limit 1" 2>/dev/null)"
fi
if [ -n "$DEDUPE_TABLE" ] && [ -n "$DEDUPE_COL" ]; then
  echo "消费者幂等核对维度：$DEDUPE_TABLE.$DEDUPE_COL" > "$WORK/dedupe-dimension.txt"
else
  echo "消费者幂等核对维度：未找到库内去重/投递表" > "$WORK/dedupe-dimension.txt"
  not_verified structural "消费者幂等（DATA-007）：库中不存在消费者去重/投递维度（处理过的 event id / Nats-Msg-Id 未落库），JetStream 的 Nats-Msg-Id 去重只在服务端窗口内生效且 /jsz 不暴露重复计数；因此本次演练**无法证明**订阅者幂等，禁止用 audit_events_v2 主键唯一性冒充。要拿到该证据需先让消费者把处理过的 event id（或 Nats-Msg-Id）落库，再重跑本演练。"
fi

PENDING_AFTER="n/a"
if [ -n "$OUTBOX_TABLE" ] && is_num "$PENDING_BEFORE"; then
  PENDING_AFTER="$(wait_until_equal "$PENDING_SQL" "$PENDING_BEFORE" 60)"
  if ! is_num "$PENDING_AFTER"; then
    fail_check "无法读取 $OUTBOX_TABLE 未发布计数（复核对，见 $WORK/pg.err）"
    PENDING_AFTER="n/a"
  fi
fi
DUPS_AFTER="$(pgn "select count(*) from (select id from audit_events_v2 group by id having count(*) > 1) x" 2>/dev/null)"
{
echo "- outbox 未发布：${PENDING_BEFORE} → ${PENDING_AFTER}（期望不增长；补发等待上限 60s）"
echo "- audit_events_v2 重复 id 计数（**仅作链完整性观察项，不作为幂等证据**）：${DUPS_AFTER:-未取得}"
} >> "$REPORT"
if is_num "$PENDING_BEFORE" && is_num "$PENDING_AFTER" && [ "$PENDING_AFTER" -gt "$PENDING_BEFORE" ]; then
  fail_check "outbox 未发布数增长：$PENDING_BEFORE → $PENDING_AFTER（补发未生效）"
fi
if ! is_num "$DUPS_AFTER"; then
  fail_check "无法统计 audit_events_v2 重复 id（见 $WORK/pg.err）"
elif [ "$DUPS_AFTER" != "0" ]; then
  fail_check "audit_events_v2 存在重复 id：$DUPS_AFTER 组（链完整性异常）"
fi

# =====================================================================
# 4) 演练后按同一 ID 集合核对（F11 / F12 / F13）
# =====================================================================
echo "== 4. 复核对（按冻结的 ID 集合）=="
snap "$AFTER" jobs.tsv "$SQL_JOBS" "download_jobs 复核对" || fail_check "无法获取 download_jobs 复核对"
snap "$AFTER" shards.tsv "$SQL_SHARDS" "download_shards 复核对" || fail_check "无法获取 download_shards 复核对"
snap "$AFTER" checkpoints.tsv "$SQL_CKPT" "checkpoints 复核对" || fail_check "无法获取 checkpoints 复核对"
snap "$AFTER" pages.tsv "$SQL_PAGES" "download_pages 复核对" || fail_check "无法获取 download_pages 复核对"
snap "$AFTER" provider.tsv "$SQL_PROVIDER" "provider_usage 复核对" || fail_check "无法获取 provider_usage 复核对"
if [ -n "$OUTBOX_TABLE" ]; then
  snap "$AFTER" outbox.tsv "select id::text from $OUTBOX_TABLE order by id limit $BASELINE_MAX" "outbox 复核对" || fail_check "无法获取 $OUTBOX_TABLE 复核对"
fi
if [ -f "$WORK/dedupe-dimension.txt" ] && grep -q "已找到" "$WORK/dedupe-dimension.txt"; then
  snap "$BASELINE" dedupe.tsv "select $DEDUPE_COL::text from $DEDUPE_TABLE order by 1 limit $BASELINE_MAX" "消费者去重维度基线" || fail_check "无法获取消费者去重维度基线"
  snap "$AFTER" dedupe.tsv "select $DEDUPE_COL::text from $DEDUPE_TABLE order by 1 limit $BASELINE_MAX" "消费者去重维度复核对" || fail_check "无法获取消费者去重维度复核对"
fi

INVARIANTS_MD="$WORK/invariants.md"
python3 - "$BASELINE" "$AFTER" "$TRUNCATED" "$WORK/invariant-failures.txt" "$FOCUS_JOB" > "$INVARIANTS_MD" 2>"$WORK/invariants.err" <<'PY'
import os, sys

base, after, trunc_path, fail_path, focus_job = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]
SUCCESS = ("DURABLE", "INGESTED", "VALIDATED")
RANK = {"DURABLE": 1, "INGESTED": 2, "VALIDATED": 3}


def load(d, name):
    try:
        with open(os.path.join(d, name)) as f:
            text = f.read()
    except OSError:
        return None
    rows = []
    for line in text.splitlines():
        if not line:
            continue
        rows.append(line.split("|"))
    return rows


truncated = set()
try:
    with open(trunc_path) as f:
        for line in f.read().splitlines():
            if line.strip():
                truncated.add(line.strip())
except OSError:
    pass

fails = []
out = []


def partial(name):
    if name in truncated:
        return " [PARTIAL：基线被截断，核对不完整]"
    return ""


def fail(msg):
    fails.append(msg)


def line(msg):
    out.append(msg)


# ---------- 1) job：ID 集合必须保留（按基线冻结的集合，不按当前 status 重新筛选）----------
bj = load(base, "jobs.tsv")
aj = load(after, "jobs.tsv")
bstat, astat = {}, {}
if bj is None or aj is None:
    fail("download_jobs 基线/复核对数据缺失，job 不变量无法核对")
else:
    bstat = {r[0]: r[1] for r in bj if len(r) >= 2}
    astat = {r[0]: r[1] for r in aj if len(r) >= 2}
    missing = [k for k in bstat if k not in astat]
    if missing:
        fail("download_jobs：基线 %d 个 job 中有 %d 个在演练后不存在（例：%s）" % (len(bstat), len(missing), ", ".join(missing[:5])))
    line("- download_jobs：基线 %d 个，演练后仍存在 %d 个，缺失 %d 个%s" % (len(bstat), len(bstat) - len(missing), len(missing), partial("jobs.tsv")))
    if focus_job:
        line("  - 关注作业 %s：%s -> %s" % (focus_job, bstat.get(focus_job, "不在基线"), astat.get(focus_job, "已不存在")))
        if focus_job not in astat:
            fail("关注作业 %s 在演练后不存在" % focus_job)

# ---------- 2) shard：ID 集合保留、downloaded_rows 不回退、COMPLETED 不回退 ----------
bs = load(base, "shards.tsv")
ash = load(after, "shards.tsv")
if bs is None or ash is None:
    fail("download_shards 基线/复核对数据缺失，shard 不变量无法核对")
else:
    bmap = {r[0]: r for r in bs if len(r) >= 4}
    amap = {r[0]: r for r in ash if len(r) >= 4}
    missing, row_bad, status_bad = [], [], []
    for k, r in bmap.items():
        a = amap.get(k)
        if a is None:
            missing.append(k)
            continue
        if not r[3].isdigit() or not a[3].isdigit():
            fail("download_shards：%s 的 downloaded_rows 不是数字（基线 %s / 演练后 %s）" % (k, r[3], a[3]))
            continue
        if int(a[3]) < int(r[3]):
            row_bad.append("%s(%s->%s)" % (k, r[3], a[3]))
        if r[2] == "COMPLETED" and a[2] != "COMPLETED":
            status_bad.append("%s(%s->%s)" % (k, r[2], a[2]))
    if missing:
        fail("download_shards：基线 %d 个 shard 中有 %d 个在演练后不存在（例：%s）" % (len(bmap), len(missing), ", ".join(missing[:5])))
    if row_bad:
        fail("download_shards downloaded_rows 回退：%d 个（例：%s）" % (len(row_bad), ", ".join(row_bad[:5])))
    if status_bad:
        fail("download_shards COMPLETED 状态回退：%d 个（例：%s）" % (len(status_bad), ", ".join(status_bad[:5])))
    line("- download_shards：基线 %d；缺失 %d；downloaded_rows 回退 %d；COMPLETED 回退 %d%s" % (len(bmap), len(missing), len(row_bad), len(status_bad), partial("shards.tsv")))

# ---------- 3) checkpoint：逐 (shard) 比对 page_no / downloaded_rows 不回退 ----------
bc = load(base, "checkpoints.tsv")
ac = load(after, "checkpoints.tsv")
if bc is None or ac is None:
    fail("checkpoints 基线/复核对数据缺失，checkpoint 不变量无法核对")
else:
    bmap = {r[0]: r for r in bc if len(r) >= 4}
    amap = {r[0]: r for r in ac if len(r) >= 4}
    missing, page_bad, row_bad, token_changed = [], [], [], []
    for k, r in bmap.items():
        a = amap.get(k)
        if a is None:
            missing.append(k)
            continue
        if not (r[1].isdigit() and a[1].isdigit() and r[2].isdigit() and a[2].isdigit()):
            fail("checkpoints：%s 的 page_no/downloaded_rows 不是数字（基线 %s/%s，演练后 %s/%s）" % (k, r[1], r[2], a[1], a[2]))
            continue
        if int(a[1]) < int(r[1]):
            page_bad.append("%s(%s->%s)" % (k, r[1], a[1]))
        if int(a[2]) < int(r[2]):
            row_bad.append("%s(%s->%s)" % (k, r[2], a[2]))
        if r[3] and a[3] and r[3] != a[3]:
            token_changed.append(k)
    if missing:
        fail("checkpoints：基线 %d 个 shard 的 checkpoint 中有 %d 个在演练后不存在（例：%s）" % (len(bmap), len(missing), ", ".join(missing[:5])))
    if page_bad:
        fail("checkpoints page_no 回退：%d 个（例：%s）" % (len(page_bad), ", ".join(page_bad[:5])))
    if row_bad:
        fail("checkpoints downloaded_rows 回退：%d 个（例：%s）" % (len(row_bad), ", ".join(row_bad[:5])))
    line("- checkpoints：基线 %d；缺失 %d；page_no 回退 %d；downloaded_rows 回退 %d%s" % (len(bmap), len(missing), len(page_bad), len(row_bad), partial("checkpoints.tsv")))
    if token_changed:
        line("  - 提示（非 FAIL）：%d 个 shard 的 checkpoint 游标 token 发生变化（page_no 未回退，属正常推进/重取），需人工判读" % len(token_changed))

# ---------- 4) page：成功页集合不丢失、状态不回退、逐页 next_page_token_hash 不变 ----------
bp = load(base, "pages.tsv")
ap = load(after, "pages.tsv")
if bp is None or ap is None:
    fail("download_pages 基线/复核对数据缺失，成功页不变量无法核对")
else:
    bmap = {(r[0], r[1]): r for r in bp if len(r) >= 5}
    amap = {(r[0], r[1]): r for r in ap if len(r) >= 5}
    b_success = {k: r for k, r in bmap.items() if r[2] in SUCCESS}
    lost, back, hash_changed = [], [], []
    for k, r in b_success.items():
        a = amap.get(k)
        if a is None:
            lost.append("%s:%s(已不存在)" % (k[0], k[1]))
            continue
        if a[2] not in SUCCESS:
            lost.append("%s:%s(%s->%s)" % (k[0], k[1], r[2], a[2]))
            continue
        if RANK.get(a[2], 0) < RANK.get(r[2], 0):
            back.append("%s:%s(%s->%s)" % (k[0], k[1], r[2], a[2]))
    for k, r in bmap.items():
        a = amap.get(k)
        if a is not None and r[3] != a[3]:
            hash_changed.append("%s:%s" % (k[0], k[1]))
    a_success = sum(1 for r in amap.values() if r[2] in SUCCESS)
    if lost:
        fail("download_pages 成功页丢失：%d 个（例：%s）" % (len(lost), ", ".join(lost[:5])))
    if back:
        fail("download_pages 成功页状态回退：%d 个（例：%s）" % (len(back), ", ".join(back[:5])))
    if hash_changed:
        fail("download_pages 的 next_page_token_hash 发生变化（要求同一 (shard,page) 不回退/不变）：%d 个（例：%s）" % (len(hash_changed), ", ".join(hash_changed[:5])))
    if a_success < len(b_success):
        fail("download_pages 成功页总数回退：%d -> %d" % (len(b_success), a_success))
    line("- download_pages 成功页（VALIDATED/INGESTED/DURABLE）：基线 %d，演练后 %d；丢失 %d，状态回退 %d，页游标哈希变化 %d%s" % (len(b_success), a_success, len(lost), len(back), len(hash_changed), partial("pages.tsv")))

# ---------- 5) Provider 成功页按 shard 计数不回退 ----------
bpr = load(base, "provider.tsv")
apr = load(after, "provider.tsv")
if bpr is None or apr is None:
    fail("provider_usage 基线/复核对数据缺失，Provider 成功页不变量无法核对")
else:
    bmap = {r[0]: int(r[1]) for r in bpr if len(r) >= 2 and r[1].isdigit()}
    amap = {r[0]: int(r[1]) for r in apr if len(r) >= 2 and r[1].isdigit()}
    bad = []
    for k, v in bmap.items():
        if amap.get(k, 0) < v:
            bad.append("%s(%d->%d)" % (k, v, amap.get(k, 0)))
    if bad:
        fail("provider_usage 成功页（status in ('success','ok')）按 shard 计数回退：%d 个（例：%s）" % (len(bad), ", ".join(bad[:5])))
    line("- provider_usage 成功页按 shard：基线 %d 个分组/合计 %d；演练后合计 %d；回退 %d%s" % (len(bmap), sum(bmap.values()), sum(amap.values()), len(bad), partial("provider.tsv")))

# ---------- 6) outbox：id 集合必须全部存在（这是 outbox 不丢事件的证据）----------
bo = load(base, "outbox.tsv")
ao = load(after, "outbox.tsv")
if bo is None:
    line("- outbox_events：无基线数据（探测失败），id 集合断言未执行")
else:
    bset = {r[0] for r in bo if r and r[0]}
    aset = set()
    if ao is not None:
        aset = {r[0] for r in ao if r and r[0]}
    missing = sorted(bset - aset)
    if missing:
        fail("outbox_events：基线 %d 个事件 id 中有 %d 个在演练后不存在（outbox 丢事件；例：%s）" % (len(bset), len(missing), ", ".join(missing[:5])))
    line("- outbox_events id 集合：基线 %d，演练后 %d，缺失 %d（证明 outbox 未丢事件；**不等于**消费者幂等）%s" % (len(bset), len(aset), len(missing), partial("outbox.tsv")))

# ---------- 7) 消费者幂等：只用真实的 event id 维度（找不到就是 NOT_VERIFIED，不冒充）----------
bd = load(base, "dedupe.tsv")
ad = load(after, "dedupe.tsv")
if bd is None or ad is None:
    line("- 消费者幂等维度：本次没有可比的库内去重/投递维度（结论见报告 NOT_VERIFIED 段，禁止用主键唯一性冒充）")
else:
    bset = {r[0] for r in bd if r and r[0]}
    aset = {r[0] for r in ad if r and r[0]}
    missing = sorted(bset - aset)
    if missing:
        fail("消费者去重维度：基线 %d 个已处理 event id 中有 %d 个在演练后消失（例：%s）" % (len(bset), len(missing), ", ".join(missing[:5])))
    line("- 消费者幂等维度（event id 集合）：基线 %d，演练后 %d，缺失 %d" % (len(bset), len(aset), len(missing)))

with open(fail_path, "w") as f:
    if fails:
        f.write(chr(10).join(fails) + chr(10))
print("### 4. 逐 ID 集合不变量核对（F11 / F12 / F13）")
print()
print(chr(10).join(out))
print()
if fails:
    print("- **本节 FAIL %d 条**（已计入 §5 汇总）" % len(fails))
else:
    print("- 本节未发现不变量违规（NOT_VERIFIED 项另见 §5）")
PY
CHK_RC=$?
if [ "$CHK_RC" -ne 0 ]; then
  fail_check "不变量核对脚本执行失败（rc=$CHK_RC，见 $WORK/invariants.err）"
fi
cat "$INVARIANTS_MD" >> "$REPORT"
if [ -s "$WORK/invariant-failures.txt" ]; then
  while IFS= read -r l; do
    [ -n "$l" ] || continue
    fail_check "$l"
  done < "$WORK/invariant-failures.txt"
fi

# =====================================================================
# 5) 结论
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
echo "## 5. 结论"
echo
echo "- FAIL 条目：$FAIL_N；NOT_VERIFIED 条目：$NV_N（structural $NV_STRUCT_N / check $NV_CHECK_N）；PARTIAL 条目：$PARTIAL_N"
case "$VERDICT" in
  FAIL)
    echo "- **本次演练判定：FAIL**"
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
echo "- 基线/复核对原始数据：$BASELINE 与 $AFTER；不变量明细：$INVARIANTS_MD；日志：$WORK"
echo "- 本脚本不删除任何证据、不清理任何目标。"
} >> "$REPORT"
if [ -s "$TRUNCATED" ]; then
  echo "WARN: 以下数据集达到抓取上限 $BASELINE_MAX，其核对为 PARTIAL：$(paste -sd ' ' "$TRUNCATED")" >&2
fi

MARK=""
if [ "$FAIL_N" -gt 0 ]; then
  MARK="$EV/FAILED-kill9-$TS.md"
  {
    echo "# Kill -9 演练 FAIL（$TS）"
    echo
    echo "- 完整报告：$REPORT"
    echo "- 证据目录：$WORK"
    echo
    echo "## FAIL 条目"
    echo
    sed -e 's/^/- /' "$FAILURES"
  } > "$MARK"
fi

# ---------- 机器可读结果（此前本脚本**没有** result.json，gate 无从消费）----------
python3 - "$FAILURES" "$NOT_VERIFIED" "$NOT_VERIFIED_STRUCTURAL" "$NOT_VERIFIED_CHECK" "$PARTIAL" "$TRUNCATED" "$RESULT" "$TS" "$VERDICT" "$FAIL_N" "$NV_N" "$PARTIAL_N" "$WORK" "$REPORT" "$FOCUS_JOB" <<'PY' || abort_hard "结果文件写入失败：无法产出机器可读证据，本次演练不得计为通过"
import json, os, sys
(fail_path, nv_path, nvs_path, nvc_path, part_path, trunc_path, out_path,
 ts, verdict, fail_n, nv_n, part_n, work, report, focus_job) = sys.argv[1:16]
def lines(p):
    try:
        return [x.strip() for x in open(p).read().splitlines() if x.strip()]
    except OSError:
        return []
inv = []
p = os.path.join(work, "invariants.md")
if os.path.isfile(p):
    inv = lines(p)
res = {
    "drill": "kill9-drill",
    "ts": ts,
    "verdict": verdict,
    "verdict_note": "PASS 仅当 FAIL=0 且 NOT_VERIFIED=0 且 PARTIAL=0；structural NV 同样不构成通过",
    "focus_job": focus_job,
    "failures": lines(fail_path),
    "not_verified": lines(nv_path),
    "nv_class": {"structural": lines(nvs_path), "check": lines(nvc_path)},
    "nv_counts": {"total": int(nv_n), "structural": len(lines(nvs_path)), "check": len(lines(nvc_path))},
    "partial": lines(part_path),
    "partial_count": int(part_n),
    "truncated_datasets": lines(trunc_path),
    "invariants": inv,
    "baseline_dir": os.path.join(work, "baseline"),
    "after_dir": os.path.join(work, "after"),
    "evidence_dir": work,
    "report": report,
    "cleanup": "本脚本不删除任何证据、不清理任何目标",
    "retention_note": "基线/复核对原始 TSV 全部保留（含 baseline/ 与 after/）",
}
with open(out_path, "w") as fh:
    json.dump(res, fh, indent=2, ensure_ascii=False)
print(json.dumps({k: v for k, v in res.items() if k not in ("failures", "not_verified", "invariants")}, indent=2, ensure_ascii=False))
PY

echo "report: $REPORT"
echo "result: $RESULT"
echo "evidence: $WORK"
if [ "$FAIL_N" -gt 0 ]; then
  echo "FAILED marker: $MARK"
  echo "演练判定：FAIL（$FAIL_N 条）" >&2
  exit 1
fi
# [GATE-01] 退出码：0=PASS；1=FAIL；2=前置/中止；3=INCOMPLETE（仍有 NV(任何分级) 或 PARTIAL）
# 注意：exit 3 表示"证据已产出但**未达验收**"，绝不等于通过。
if [ "$NV_N" -gt 0 ] || [ "$PARTIAL_N" -gt 0 ]; then
  echo "演练判定：INCOMPLETE（NOT_VERIFIED $NV_N 条 [structural $NV_STRUCT_N / check $NV_CHECK_N]、PARTIAL $PARTIAL_N 条）—— 未达验收，见报告 §5" >&2
  exit 3
fi
echo "演练判定：PASS（FAIL=0、NOT_VERIFIED=0、PARTIAL=0）"
exit 0
