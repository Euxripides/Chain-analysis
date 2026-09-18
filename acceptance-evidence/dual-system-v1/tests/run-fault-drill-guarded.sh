#!/usr/bin/env bash
# 故障注入演练的"用户在工作"保护壳（2026-09-13 重写；监控者两轮复核后修订；
# 2026-09-14 第三轮：快照 SQL 改为"冻结 ID 预聚合 + JOIN"，并补齐超时/观测/EXPLAIN 证据）。
#
# 目的：跑会主动杀 worker / 停 NATS / 删 MinIO 对象的故障套件前后，用**同一批冻结 ID**
# 证明既有用户任务、checkpoint、成功页、Provider 用量、审计链、outbox 行没有被破坏。
#
# 与首版的关键差异（首版会产生假 FAIL/假 PASS，已废弃）：
#   1) before 快照冻结 job_ids / shard_ids / outbox_ids；after **按同一 ID 集合**查询，
#      不再按"当前活跃 status"重新筛选（用户任务正常完成后 after 不再命中会误记 lost）。
#   2) 全部走 machine-readable JSON（psql json_build_object）+ 严格 schema 校验；
#      任何必需 SQL/字段缺失 => INVALID，**不运行故障套件**。
#   3) next_page_token 用 download_pages.next_page_token_hash 摘要比较，不输出明文；
#      并区分"合法进展"与真正的 cursor 丢失。
#   4) outbox 不变量按 baseline ID 集合断言"没有既有行消失"，不用总 count 掩盖丢失。
#   5) 结论只声明**本次可比对的不变量**；不等于"用户数据保留已被完整证明"。
#   6) 【运行中不可覆盖】本脚本被修改时，运行中的 bash 会读到错位内容 —— 部署必须
#      "写 .tmp 再原子 mv"，并且每次执行都用**冻结副本**（见 run-guarded-*.sh）。
#
# ---- 第三轮新增（2026-09-14）-------------------------------------------------
# 起因：上一轮真实数据上 before 快照疑似过慢、被人工中止（未产出 PASS/FAIL）。监控者实测
# 中止时 PG 有一个 backend 处于 Rs / CPU 81.5%，说明当时**很可能真的在执行该 SQL**，
# 因此"stdin 死锁"的结论证据不足 —— 本轮的每一步都用可复查的证据把几种可能分开：
#   7) 【快照 SQL 重写】before/after 不再对每个 shard 做 3 个相关子查询，改为
#      "先冻结 ID（job_ids / shard_ids）+ 按 shard_id 预聚合一次 + JOIN 回 shard 列表"。
#      逐字段语义等价性论证见同目录 QA 说明：
#      tests/sprint7/qa/GUARD-SQL-OPTIMIZATION-NOTES.md
#      **ID 集合不缩小、不跳过任何任务**：before 仍用原来的活跃/暂停 status 谓词冻结
#      job_ids，再由 job_id ∈ 冻结集合得到 shard_ids；after 仍用 unnest 的同一批数组。
#   8) 【连接标记 + 超时】每次 psql 都带 PGAPPNAME=drill-guard，且 SQL 开头是
#      SET application_name / statement_timeout(60s) / lock_timeout(5s)。
#      psql -c 每次都是独立会话，所以 SET 必须与查询放在同一次 -c 里；另外加了外层硬超时
#      （timeout），覆盖"客户端/管道挂起"这类服务端超时管不到的情况。
#   9) 【证据而非猜测】任何 SQL 失败/超时都判 INVALID（绝不把超时当成 0/0 的 PASS），
#      保留原始 stdout/stderr，并在 SQL **运行期间**按固定间隔采样 pg_stat_activity，
#      再用采样结果分类：执行中 / 锁等待 / IO / 客户端侧挂起（完全没有本次 guard 的后端）。
#      采样器用独立的 application_name，不会污染按 application_name='drill-guard' 的诊断查询。
#  10) 【只取消自己的只读查询】唯一的取消动作是 pg_cancel_backend(pid)，且必须同时满足
#      application_name='drill-guard' + query 含**本次 run 标记** + query 不含写动词。
#      绝不 pg_terminate_backend、绝不按 pid 猜测、绝不触碰用户作业，也绝不清理任何任务/对象。
#  11) 【EXPLAIN 证据】新增只读步骤，把**优化前后**的关键查询各跑一次
#      EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) 写入 explain-before.txt / explain-after.txt；
#      失败/超时的输出原样保留（"没在超时内跑完"本身就是证据，不是 0 行/0 耗时）。
#      Windows 工作树上没有 docker/PG，本机产不出这两份文件，必须在 VM 上执行。
#  12) 【结论全覆盖】所有退出路径都写 drill-guard-verdict.json（EXIT trap 兜底），
#      返回的 rc 就是结论的 rc（0=PASS 1=FAIL 2=REVIEW 3=INVALID）；
#      冻结运行器与 systemd 收集器都 exit 该 rc。
#
# ---- 既有教训（本轮不改动其约束，仍是硬要求）--------------------------------
#   - 运行中的脚本文件绝不能被覆盖（bash 边读边执行；真正的保护是 run-guarded-fault-suite.sh
#     的"每次执行都先冻结副本 + 记录 sha256/size"机制）；
#   - 维护持有位所有权（run-sprint5-e2e.sh 修复二）：既有 hold 一律沿用，
#     绝不覆盖、绝不在结束时删除他人的维护位；
#   - 恢复责任登记（run-sprint5-e2e.sh 修复五）：本次真的停过/删过的对象，恢复责任写进
#     套件自己的 trap。保护壳只观测，不代替套件清理，也绝不停止/启动任何容器或用户作业；
#   - 保护壳不改任何业务数据：本脚本发出的 SQL 只有 SET 与会话只读 SELECT/WITH，
#     且全部通过 -c 一次性发出，从不进入交互会话。
#
# 用法：bash run-fault-drill-guarded.sh <套件脚本> [参数...]
# 退出码：0=PASS 1=FAIL 2=REVIEW 3=INVALID（INVALID 表示未运行套件或无法得出结论）
# 可调环境变量：
#   DRILL_OUT / PG_CONTAINER / PGUSER / PGDB / GUARD_APP_NAME(drill-guard) /
#   GUARD_STATEMENT_TIMEOUT(60s) / GUARD_LOCK_TIMEOUT(5s) / GUARD_PSQL_HARD_TIMEOUT_S(180) /
#   GUARD_SAMPLE_INTERVAL_S(2，整数秒) / GUARD_SAMPLE_MAX(120) /
#   GUARD_EXPLAIN_NEW(1) / GUARD_EXPLAIN_NEW_GATING(1) / GUARD_EXPLAIN_OLD(1) /
#   GUARD_EXPLAIN_TIMEOUT(60s) / GUARD_EXPLAIN_HARD_TIMEOUT_S(180)
set -uo pipefail

PG_CONTAINER="${PG_CONTAINER:-deploy-postgres-1}"
PGUSER="${PGUSER:-blockchain}"
PGDB="${PGDB:-blockchain_platform}"
SUITE="${1:?需要被测套件脚本路径}"; shift || true

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="${DRILL_OUT:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/sprint7/qa/drill-guard-$STAMP}"

# ---------------- 0. 运行参数 ----------------
GUARD_APP_NAME="${GUARD_APP_NAME:-drill-guard}"
GUARD_SAMPLER_APP="${GUARD_SAMPLER_APP:-drill-guard-sampler}"
GUARD_STATEMENT_TIMEOUT="${GUARD_STATEMENT_TIMEOUT:-60s}"
GUARD_LOCK_TIMEOUT="${GUARD_LOCK_TIMEOUT:-5s}"
GUARD_PSQL_HARD_TIMEOUT_S="${GUARD_PSQL_HARD_TIMEOUT_S:-180}"
GUARD_SAMPLE_INTERVAL_S="${GUARD_SAMPLE_INTERVAL_S:-2}"
GUARD_SAMPLE_MAX="${GUARD_SAMPLE_MAX:-120}"
GUARD_EXPLAIN_NEW="${GUARD_EXPLAIN_NEW:-1}"
GUARD_EXPLAIN_NEW_GATING="${GUARD_EXPLAIN_NEW_GATING:-1}"
GUARD_EXPLAIN_OLD="${GUARD_EXPLAIN_OLD:-1}"
GUARD_EXPLAIN_TIMEOUT="${GUARD_EXPLAIN_TIMEOUT:-60s}"
GUARD_EXPLAIN_HARD_TIMEOUT_S="${GUARD_EXPLAIN_HARD_TIMEOUT_S:-180}"
# 本次运行的 SQL 标记：只用于"认出属于本次 guard 的会话"（取消规则与采样判读），
# 不写进任何业务数据。带 PID，避免同一秒内两次运行互相误判。
GUARD_MARK_INNER="drill-guard-run:${STAMP}-$$"

SUITE_RC=-
SQL_FAILURE_KIND=none
PSQL_LAST_OUT=""
PSQL_LAST_ERR=""
EXPLAIN_NEW_RC=n/a
EXPLAIN_OLD_RC=n/a
INTERRUPTED=none

if ! mkdir -p "$OUT" 2>/dev/null; then
  # 连 OUT 都建不出来：仍然输出一份 verdict（到 stdout）+ 真实的 rc=3。
  printf '{"verdict":"INVALID","rc":3,"message":"无法创建 OUT 目录","out_dir":"%s"}\n' "$OUT"
  exit 3
fi
echo "DRILL_OUT=$OUT"

# ---------------- 0.1 时间戳 / 阶段日志 ----------------
ts_iso() { # 带毫秒的 UTC 时间戳；非 GNU date 时退回整秒
  local v
  v="$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ 2>/dev/null)" || v=""
  case "$v" in *[0-9]Z) printf '%s' "$v" ;; *) date -u +%Y-%m-%dT%H:%M:%SZ ;; esac
}
ts_ms() { # 毫秒时间戳（整数，永不返回空值）
  local v
  v="$(date -u +%s%3N 2>/dev/null)" || v=""
  case "$v" in ''|*[!0-9]*) v=$(( $(date -u +%s) * 1000 )) ;; esac
  printf '%s' "$v"
}
ms_to_s() { # 毫秒 -> 秒（3 位小数）
  awk -v ms="$1" 'BEGIN{printf "%.3f", ms/1000}' 2>/dev/null || printf '%s' "$(( $1 / 1000 ))"
}
stage_log() { # stage_log <stage> <event> [k=v ...] —— 同时进 $OUT/stage-log.txt 与 stdout
  local line
  line="$(printf 'stage=%s event=%s t=%s epoch_ms=%s' "$1" "$2" "$(ts_iso)" "$(ts_ms)")"
  shift 2
  local kv
  for kv in "$@"; do line="$line $kv"; done
  printf '%s\n' "$line" >> "$OUT/stage-log.txt" 2>/dev/null || true
  printf '%s\n' "$line"
}
TIMEOUT_BIN=""
command -v timeout >/dev/null 2>&1 && TIMEOUT_BIN="$(command -v timeout)"
stage_log guard start "app_name=$GUARD_APP_NAME" "statement_timeout=$GUARD_STATEMENT_TIMEOUT" \
  "lock_timeout=$GUARD_LOCK_TIMEOUT" "psql_hard_timeout_s=$GUARD_PSQL_HARD_TIMEOUT_S" \
  "hard_timeout_bin=${TIMEOUT_BIN:-none}" "sql_mark=$GUARD_MARK_INNER"
# stdin 策略（2026-09-13 死锁的结构性排除，见文件头教训）：
printf '# stdin_policy: 每次 psql 调用都 < /dev/null，且 docker exec 不带 -i —— docker CLI 不会去读 stdin\n' \
  >> "$OUT/stage-log.txt"

# ---------------- 0.2 统一出口 ----------------
verdict() { # verdict <status> <rc> <message>
  printf '{"verdict":"%s","rc":%s,"message":"%s","out_dir":"%s","stage_log":"%s","pg_stat_activity":"%s","statement_timeout":"%s","lock_timeout":"%s"}\n' \
    "$1" "$2" "$3" "$OUT" "$OUT/stage-log.txt" "$OUT/pg-stat-activity.txt" \
    "$GUARD_STATEMENT_TIMEOUT" "$GUARD_LOCK_TIMEOUT" \
    | tee "$OUT/drill-guard-verdict.json"
  printf "suite_rc=%s guard_rc=%s verdict=%s\n" "${SUITE_RC:--}" "$2" "$1" | tee "$OUT/SUMMARY.txt"
  stage_log verdict end "verdict=$1" "rc=$2"
  exit "$2"
}
on_exit() { # 兜底：任何路径都不能留下"没有结论"的目录；已有结论时不覆盖、不改 rc
  local rc=$?
  if [ -s "$OUT/drill-guard-verdict.json" ]; then return 0; fi
  printf '{"verdict":"INVALID","rc":3,"message":"guard 异常退出未产出结论（rc=%s interrupted=%s）","out_dir":"%s","stage_log":"%s"}\n' \
    "$rc" "$INTERRUPTED" "$OUT" "$OUT/stage-log.txt" | tee "$OUT/drill-guard-verdict.json"
  stage_log guard end "rc=3" "verdict=INVALID" "reason=exit_trap"
  exit 3
}
on_interrupt() { # 只取消确认属于本次 guard 的只读查询；绝不清理任何任务/对象
  INTERRUPTED="$1"
  stage_log guard interrupt "signal=$1"
  # 极早期收到信号时这些函数可能还没定义：能调用就调用，不能也不影响结论落盘。
  if command -v cancel_own_queries >/dev/null 2>&1; then cancel_own_queries "signal=$1" || true; fi
  if command -v write_sql_diagnosis >/dev/null 2>&1; then write_sql_diagnosis "interrupt" 130 || true; fi
  verdict INVALID 3 "收到 $1：已按白名单取消本次 guard 自己的只读查询，未得出结论"
}
trap on_exit EXIT
trap 'on_interrupt INT' INT
trap 'on_interrupt TERM' TERM

if [ ! -f "$SUITE" ]; then
  verdict INVALID 3 "套件脚本不存在：$SUITE"
fi

{
  echo "guard_self_sha256=$(sha256sum "${BASH_SOURCE[0]}" | awk '{print $1}')"
  echo "guard_self_size=$(stat -c %s "${BASH_SOURCE[0]}")"
  echo "suite_path=$SUITE"
  echo "suite_self_sha256=$(sha256sum "$SUITE" | awk '{print $1}')"
  echo "suite_self_size=$(stat -c %s "$SUITE")"
  echo "guard_sql_mark=$GUARD_MARK_INNER"
  echo "application_name=$GUARD_APP_NAME sampler_application_name=$GUARD_SAMPLER_APP"
  echo "statement_timeout=$GUARD_STATEMENT_TIMEOUT lock_timeout=$GUARD_LOCK_TIMEOUT psql_hard_timeout_s=$GUARD_PSQL_HARD_TIMEOUT_S"
  echo "explain_new=$GUARD_EXPLAIN_NEW explain_new_gating=$GUARD_EXPLAIN_NEW_GATING explain_old=$GUARD_EXPLAIN_OLD explain_timeout=$GUARD_EXPLAIN_TIMEOUT"
  echo "stdin_policy=每次 psql 调用 < /dev/null 且 docker exec 不带 -i"
} | tee "$OUT/guard-self.txt"

# ---------------- 0.3 SQL 渲染（占位符替换，SQL 正文不做 shell 展开） ----------------
GUARD_SQL_PRELUDE="/*__MARK__*/
SET application_name='__APPNAME__'; SET statement_timeout='__STMTO__'; SET lock_timeout='__LOCKTO__';"
render_sql() { # render_sql <raw_sql> [statement_timeout 覆盖]
  local s="$1" st="${2:-$GUARD_STATEMENT_TIMEOUT}"
  s="${s//__MARK__/$GUARD_MARK_INNER}"
  s="${s//__APPNAME__/$GUARD_APP_NAME}"
  s="${s//__STMTO__/$st}"
  s="${s//__LOCKTO__/$GUARD_LOCK_TIMEOUT}"
  printf '%s' "$s"
}

# 诊断用的 pg_stat_activity 查询（本文件要求保留其**原始输出**；过滤条件就是
# application_name='drill-guard'，采样器自己用另一个名字，因此不会自采样）。
PGSTAT_SQL_RAW=$(cat <<'SQL'
/*__MARK__*/
SET application_name='__SAMPLERAPP__'; SET statement_timeout='10s'; SET lock_timeout='2s';
SELECT pid, application_name, state, wait_event_type, wait_event, query_start, now()-query_start AS dur, left(query,200) FROM pg_stat_activity WHERE application_name='__FILTERAPP__';
SQL
)
render_pgstat_sql() {
  local s="$PGSTAT_SQL_RAW"
  s="${s//__MARK__/$GUARD_MARK_INNER}"
  s="${s//__SAMPLERAPP__/$GUARD_SAMPLER_APP}"
  s="${s//__FILTERAPP__/$GUARD_APP_NAME}"
  printf '%s' "$s"
}
PGSTAT_SQL="$(render_pgstat_sql)"

{
  echo "# pg_stat_activity 采样：# 开头是本脚本加的时间戳/标签注释，其余行是 psql -A -t -F '|' 的**原始输出**。"
  echo "# 列顺序：pid|application_name|state|wait_event_type|wait_event|query_start|dur|query(截断200字符)"
  echo "# 过滤：application_name='$GUARD_APP_NAME'；采样器自己叫 '$GUARD_SAMPLER_APP'，不会自采样。"
  echo "# 判读：active+空等待事件=在执行(CPU/扫描)；active+Lock=锁等待；active+IO=IO 等待；"
  echo "#       完全没有本次 guard 的后端=客户端侧挂起（与 stdin/管道死锁假说相容，需结合 stage-log 计时一起看）。"
} > "$OUT/pg-stat-activity.txt"

sample_activity() { # sample_activity <label> <n> —— 一次采样，原样追加
  local label="$1" n="$2"
  printf '# sample label=%s n=%s at=%s\n' "$label" "$n" "$(ts_iso)" >> "$OUT/pg-stat-activity.txt"
  local -a cmd
  cmd=(docker exec -e "PGAPPNAME=$GUARD_SAMPLER_APP" -e PGCONNECT_TIMEOUT=10 "$PG_CONTAINER" \
       psql -X -U "$PGUSER" -d "$PGDB" -At -F '|' -v ON_ERROR_STOP=1 -c "$PGSTAT_SQL")
  [ -n "$TIMEOUT_BIN" ] && cmd=("$TIMEOUT_BIN" 20s "${cmd[@]}")
  "${cmd[@]}" < /dev/null >> "$OUT/pg-stat-activity.txt" 2>> "$OUT/pg-stat-activity.err" || true
}

sample_loop() { # sample_loop <label> <child_pid> —— 子进程活着时按间隔采样
  local label="$1" child="$2"
  local interval_ms=$(( GUARD_SAMPLE_INTERVAL_S * 1000 ))
  local start_ms n=0 now elapsed
  start_ms="$(ts_ms)"
  while kill -0 "$child" 2>/dev/null; do
    sleep 0.5
    kill -0 "$child" 2>/dev/null || break
    now="$(ts_ms)"; elapsed=$(( now - start_ms ))
    if [ "$elapsed" -ge "$(( (n + 1) * interval_ms ))" ]; then
      n=$(( n + 1 ))
      sample_activity "$label" "$n"
      if [ "$n" -ge "$GUARD_SAMPLE_MAX" ]; then
        printf '# sampling stopped label=%s n=%s (GUARD_SAMPLE_MAX)\n' "$label" "$n" >> "$OUT/pg-stat-activity.txt"
        break
      fi
    fi
  done
  return 0
}

classify_sql_failure() { # classify_sql_failure <rc> <err_file> -> timeout_kind
  local rc="$1" err="$2" kind=""
  if [ "$rc" = "124" ]; then kind="outer_hard_timeout"
  elif [ "$rc" = "137" ]; then kind="outer_hard_kill"
  elif grep -qi 'canceling statement due to statement timeout' "$err" 2>/dev/null; then kind="statement_timeout"
  elif grep -qi 'canceling statement due to lock timeout' "$err" 2>/dev/null; then kind="lock_timeout"
  elif grep -qi 'deadlock detected' "$err" 2>/dev/null; then kind="deadlock"
  elif grep -qi 'could not connect\|connection refused\|the database system is\|no such file or directory' "$err" 2>/dev/null; then kind="connection_error"
  elif [ "$rc" = "0" ]; then kind="none"   # rc=0 就是没失败；"空输出"由调用点显式标注（见 empty_output）
  else kind="sql_error"; fi
  printf '%s' "$kind"
}

observed_states() { # observed_states <label> -> "state|wait_event_type|wait_event(次数)" 列表
  [ -f "$OUT/pg-stat-activity.txt" ] || return 0
  awk -F'|' -v lab="$1" '
    /^# sample / { on = (index($0, " label=" lab " ") > 0); next }
    /^#/ { next }
    NF >= 5 && on { print $3 "|" $4 "|" $5 }
  ' "$OUT/pg-stat-activity.txt" 2>/dev/null | sort | uniq -c | awk '{printf "%s(%s) ", $2, $1}'
}

execution_evidence() { # execution_evidence <states_summary> <backend_observed yes/no>
  local s="$1" obs="$2" out=""
  if [ -z "$s" ]; then
    if [ "$obs" = "yes" ]; then out="backend_seen_but_no_sample"; else out="no_guard_backend_observed(未执行或客户端侧挂起，与 stdin 死锁假说相容)"; fi
    printf '%s' "$out"; return 0
  fi
  printf '%s' "$s" | grep -q 'active||' && out="$out executing"
  printf '%s' "$s" | grep -q 'active|Lock|' && out="$out lock_wait"
  printf '%s' "$s" | grep -q 'active|IO|' && out="$out io_wait"
  printf '%s' "$s" | grep -q 'active|LWLock|' && out="$out lwlock"
  printf '%s' "$s" | grep -q 'active|BufferPin|' && out="$out bufferpin"
  printf '%s' "$s" | grep -q 'idle' && out="$out idle_seen"
  [ -z "$out" ] && out="unclassified"
  printf '%s' "${out# }"
}

write_failure_sample() { # write_failure_sample <label> <rc>
  printf '# ---- failure sample label=%s rc=%s at=%s ----\n' "$1" "$2" "$(ts_iso)" >> "$OUT/pg-stat-activity.txt"
  sample_activity "$1" failure
}

write_sql_diagnosis() { # write_sql_diagnosis <label> <rc> [timeout_kind 覆盖] —— 原始输出 + 采样 + 分类，全部保留
  local label="$1" rc="$2" kind states obs diag outbytes=0
  kind="${3:-$(classify_sql_failure "$rc" "${PSQL_LAST_ERR:-/dev/null}")}"
  SQL_FAILURE_KIND="$kind"
  states="$(observed_states "$label")"
  if [ -n "$states" ]; then obs=yes; else obs=no; fi
  [ -n "${PSQL_LAST_OUT:-}" ] && [ -f "${PSQL_LAST_OUT:-}" ] && outbytes="$(wc -c < "$PSQL_LAST_OUT" 2>/dev/null || echo 0)"
  diag="$OUT/sql-failure-${label}.txt"
  {
    echo "# 快照/EXPLAIN SQL 阶段失败诊断（原始输出与采样均已保留；本脚本不做任何清理）"
    echo "label=$label"
    echo "rc=$rc"
    echo "timeout_kind=$kind"
    echo "at=$(ts_iso)"
    echo "statement_timeout=$GUARD_STATEMENT_TIMEOUT lock_timeout=$GUARD_LOCK_TIMEOUT outer_hard_timeout_s=$GUARD_PSQL_HARD_TIMEOUT_S"
    echo "guard_backend_observed_in_samples=$obs"
    echo "observed_state_wait_joint=${states:-none}"
    echo "execution_evidence=$(execution_evidence "$states" "$obs")"
    echo "out_file=${PSQL_LAST_OUT:-none} out_bytes=$outbytes"
    echo "err_file=${PSQL_LAST_ERR:-none}"
    echo "# ---- stderr（原样，尾部 40 行）----"
    tail -n 40 "${PSQL_LAST_ERR:-/dev/null}" 2>/dev/null
    echo "# ---- stdout（原样，头部 2000 字节）----"
    head -c 2000 "${PSQL_LAST_OUT:-/dev/null}" 2>/dev/null
    echo
  } > "$diag" 2>&1
  printf 'sql_failure label=%s rc=%s timeout_kind=%s backend_observed=%s diag=%s pg_stat=%s\n' \
    "$label" "$rc" "$kind" "$obs" "$diag" "$OUT/pg-stat-activity.txt" | tee -a "$OUT/SUMMARY.txt"
}

cancel_own_queries() { # cancel_own_queries <reason>
  # 唯一允许的取消动作。白名单三重条件（缺一不可）：
  #   1) application_name = guard 名（本次 guard 的连接标记）；
  #   2) query 里含**本次 run 的标记** ${GUARD_MARK_INNER}（同机其他会话/其他 guard 不会被误伤）；
  #   3) query 不含任何写动词（insert/update/delete/truncate/drop/alter/create/grant/revoke/
  #      copy/vacuum/refresh）—— 只要有一点像写操作就**不取消**（宁可留着，也不误伤）。
  # 只用 pg_cancel_backend（取消语句），绝不用 pg_terminate_backend（断开连接），
  # 绝不按 pid 猜、绝不清理任务/对象/锁。
  local reason="$1"
  local sql
  sql="$(render_sql "/*__MARK__*/
SET application_name='__SAMPLERAPP__'; SET statement_timeout='10s'; SET lock_timeout='2s';
SELECT pid, state, wait_event_type AS wet, now()-query_start AS dur, pg_cancel_backend(pid) AS cancelled, left(query,200) AS q FROM pg_stat_activity WHERE application_name='__FILTERAPP__' AND pid <> pg_backend_pid() AND position('__RUNMARK__' in coalesce(query,'')) > 0 AND query !~* '(insert|update|delete|truncate|drop|alter|create|grant|revoke|copy|vacuum|refresh)';")"
  sql="${sql//__SAMPLERAPP__/$GUARD_SAMPLER_APP}"
  sql="${sql//__FILTERAPP__/$GUARD_APP_NAME}"
  sql="${sql//__RUNMARK__/$GUARD_MARK_INNER}"
  local -a cmd
  cmd=(docker exec -e "PGAPPNAME=$GUARD_SAMPLER_APP" -e PGCONNECT_TIMEOUT=10 "$PG_CONTAINER" \
       psql -X -U "$PGUSER" -d "$PGDB" -At -F '|' -v ON_ERROR_STOP=1 -c "$sql")
  [ -n "$TIMEOUT_BIN" ] && cmd=("$TIMEOUT_BIN" 20s "${cmd[@]}")
  {
    printf '# ---- cancel_own_queries reason=%s at=%s ----\n' "$reason" "$(ts_iso)"
    "${cmd[@]}" < /dev/null 2>&1 || true
  } >> "$OUT/cancel-own-queries.txt"
  printf 'cancel_own_queries reason=%s -> %s\n' "$reason" "$OUT/cancel-own-queries.txt" | tee -a "$OUT/SUMMARY.txt"
}

psql_capture() { # psql_capture <label> <sql> <out_file> <err_file> [outer_hard_timeout_s]
  # 所有 psql 调用都走这里：
  #   - PGAPPNAME=guard 名 + -X（忽略容器里的 .psqlrc，输出可复现）；
  #   - SQL 必须已带 SET application_name/statement_timeout/lock_timeout 前置（缺了自动补）；
  #   - 运行期间采样 pg_stat_activity；失败时保留原始输出 + 写诊断 + 取消自己的只读查询。
  local label="$1" sql="$2" out="$3" err="$4" hard="${5:-$GUARD_PSQL_HARD_TIMEOUT_S}"
  local start_ms rc elapsed child
  PSQL_LAST_OUT="$out"; PSQL_LAST_ERR="$err"; SQL_FAILURE_KIND=none
  case "$sql" in
    *"SET application_name="*) : ;;
    *) sql="$(render_sql "$GUARD_SQL_PRELUDE")
$sql" ;;
  esac
  start_ms="$(ts_ms)"
  stage_log "$label" start "outer_hard_timeout_s=$hard" "out=$out"
  : > "$out" 2>/dev/null || true
  : > "$err" 2>/dev/null || true
  local -a cmd
  cmd=(docker exec -e "PGAPPNAME=$GUARD_APP_NAME" -e PGCONNECT_TIMEOUT=10 "$PG_CONTAINER" \
       psql -X -U "$PGUSER" -d "$PGDB" -At -F '|' -v ON_ERROR_STOP=1 -c "$sql")
  [ -n "$TIMEOUT_BIN" ] && cmd=("$TIMEOUT_BIN" "--kill-after=10s" "${hard}s" "${cmd[@]}")
  # 注意：**不要**用 "`docker exec -i ... psql`" + heredoc 的形式（2026-09-13 实测死锁：
  # heredoc 在 $( ) 里变成管道，docker CLI 读该管道时与 bash 的写入方互等，进程永久挂起）。
  # 统一把 SQL 作为 -c 参数传入，且显式 "` < /dev/null`"，完全不碰 stdin。
  "${cmd[@]}" < /dev/null > "$out" 2> "$err" &
  child=$!
  sample_loop "$label" "$child"
  wait "$child"; rc=$?
  elapsed=$(( $(ts_ms) - start_ms ))
  stage_log "$label" end "rc=$rc" "elapsed_ms=$elapsed" "elapsed_s=$(ms_to_s "$elapsed")" \
    "out_bytes=$(wc -c < "$out" 2>/dev/null || echo 0)" "timeout_kind=$(classify_sql_failure "$rc" "$err")"
  if [ "$rc" != "0" ]; then
    write_failure_sample "$label" "$rc"
    write_sql_diagnosis "$label" "$rc"
    # 绝不把本次 guard 的只读查询留在用户库上（外层硬超时杀掉 docker CLI 时后端可能还在跑）。
    cancel_own_queries "sql_failure label=$label rc=$rc timeout_kind=$SQL_FAILURE_KIND" || true
  fi
  return "$rc"
}

# ---------------- 1. DB 连通性预检（失败即 INVALID，不跑套件） ----------------
CONN_SQL="$(render_sql "$(cat <<'SQL'
/*__MARK__*/
SET application_name='__APPNAME__'; SET statement_timeout='__STMTO__'; SET lock_timeout='__LOCKTO__';
SELECT current_database(), current_user, now(), current_setting('statement_timeout'), current_setting('application_name');
SQL
)" 15s)"
psql_capture preflight_db "$CONN_SQL" "$OUT/preflight-db.txt" "$OUT/preflight_db.err"
CONN_RC=$?
if [ "$CONN_RC" != "0" ] || [ ! -s "$OUT/preflight-db.txt" ]; then
  verdict INVALID 3 "DB 连通性预检失败（rc=$CONN_RC，timeout_kind=$SQL_FAILURE_KIND），未运行故障套件"
fi

# ---------------- 1.1 目录证据（VM 实测的索引/规模，只读；不参与判定） ----------------
CATALOG_SQL="$(render_sql "$(cat <<'SQL'
/*__MARK__*/
SET application_name='__APPNAME__'; SET statement_timeout='__STMTO__'; SET lock_timeout='__LOCKTO__';
SELECT '## indexes' AS section, tablename, indexname, indexdef FROM pg_indexes WHERE schemaname='public' AND tablename IN ('download_jobs','download_shards','download_pages','provider_usage','checkpoints','outbox_events','audit_events_v2') ORDER BY tablename, indexname;
SELECT '## tables' AS section, relname, n_live_tup, seq_scan, idx_scan, last_analyze, last_autoanalyze FROM pg_stat_user_tables WHERE relname IN ('download_jobs','download_shards','download_pages','provider_usage','checkpoints','outbox_events','audit_events_v2') ORDER BY relname;
SELECT '## sizes' AS section, c.relname, pg_total_relation_size(c.oid) AS total_bytes, pg_relation_size(c.oid) AS heap_bytes FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname='public' AND c.relname IN ('download_jobs','download_shards','download_pages','provider_usage','checkpoints','outbox_events','audit_events_v2') ORDER BY c.relname;
SELECT '## settings' AS section, name, setting FROM pg_settings WHERE name IN ('server_version','shared_buffers','work_mem','maintenance_work_mem','effective_cache_size','max_parallel_workers_per_gather','random_page_cost') ORDER BY name;
SQL
)" 30s)"
psql_capture catalog "$CATALOG_SQL" "$OUT/catalog.txt" "$OUT/catalog.err"
CATALOG_RC=$?
if [ "$CATALOG_RC" != "0" ] || [ ! -s "$OUT/catalog.txt" ]; then
  # 目录证据缺失只记录，不阻断演练（判定不依赖它；但会被明确写进 SUMMARY 与本行）。
  printf 'catalog_unavailable rc=%s timeout_kind=%s file=%s（不阻断；判定不依赖目录证据）\n' \
    "$CATALOG_RC" "$SQL_FAILURE_KIND" "$OUT/catalog.txt" | tee -a "$OUT/SUMMARY.txt"
else
  printf 'catalog_ok file=%s（VM 实测的索引定义/表规模/参数，见文件）\n' "$OUT/catalog.txt" | tee -a "$OUT/SUMMARY.txt"
fi

# ---------------- 2. BEFORE 快照 ----------------
# 旧形态（本轮废弃，只保留在 EXPLAIN 对照里）：对每个 shard 做 3 个相关子查询，其中
#   provider_usage 上**没有 shard_id 索引**（仓库 DDL 证据见 QA 说明），等于每个 shard
#   扫一遍 41 万行；download_pages 有 (shard_id,page_no) 索引，但仍是 O(shard 数) 次子计划。
# 新形态：冻结 ID -> 按 shard_id 各聚合一次 -> LEFT JOIN 回 shard 列表（逐字段语义等价）。
BEFORE_SQL="$(render_sql "$(cat <<'SQL'
/*__MARK__*/
SET application_name='__APPNAME__'; SET statement_timeout='__STMTO__'; SET lock_timeout='__LOCKTO__';
WITH active_jobs AS (
  SELECT id, status, chain, effective_mode FROM download_jobs
  WHERE status IN ('RUNNING','QUEUED','PAUSE_REQUESTED','PAUSED','PAUSED_FALLBACK_TOO_EXPENSIVE')
),
frozen_jobs AS (
  SELECT id FROM active_jobs
),
frozen_shards AS (
  SELECT sh.id FROM download_shards sh WHERE sh.job_id IN (SELECT id FROM frozen_jobs)
),
page_latest AS (
  SELECT DISTINCT ON (p.shard_id) p.shard_id, p.next_page_token_hash
  FROM download_pages p
  WHERE p.shard_id IN (SELECT id FROM frozen_shards)
  ORDER BY p.shard_id, p.page_no DESC, p.id DESC
),
page_success AS (
  SELECT p.shard_id, count(*) AS success_pages
  FROM download_pages p
  WHERE p.shard_id IN (SELECT id FROM frozen_shards)
    AND p.status IN ('VALIDATED','INGESTED','DURABLE')
  GROUP BY p.shard_id
),
usage_success AS (
  SELECT u.shard_id, count(*) AS provider_success_pages
  FROM provider_usage u
  WHERE u.shard_id IN (SELECT id FROM frozen_shards)
    AND u.status IN ('success','ok')
  GROUP BY u.shard_id
)
SELECT json_build_object(
  'schema_version', 1,
  'taken_at', to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  'audit_max_seq', (SELECT coalesce(max(seq),0) FROM audit_events_v2),
  'audit_count', (SELECT count(*) FROM audit_events_v2),
  'outbox_total', (SELECT count(*) FROM outbox_events),
  'outbox_pending', (SELECT count(*) FROM outbox_events WHERE published_at IS NULL),
  'job_ids', (SELECT coalesce(json_agg(f.id::text ORDER BY f.id),'[]'::json) FROM frozen_jobs f),
  'outbox_ids', (SELECT coalesce(json_agg(id::text ORDER BY id),'[]'::json) FROM outbox_events),
  'jobs', (SELECT coalesce(json_agg(json_build_object('id',aj.id,'status',aj.status,'chain',aj.chain,'mode',aj.effective_mode) ORDER BY aj.id),'[]'::json) FROM active_jobs aj),
  'shards', (
    SELECT coalesce(json_agg(json_build_object(
      'id', sh.id, 'job_id', sh.job_id, 'status', sh.status,
      'page_no', coalesce(sh.page_no, cp.page_no, 0), 'rows', coalesce(sh.downloaded_rows, cp.downloaded_rows, 0),
      'token_hash', pl.next_page_token_hash,
      'success_pages', coalesce(ps.success_pages, 0),
      'provider_success_pages', coalesce(us.provider_success_pages, 0)
    ) ORDER BY sh.id), '[]'::json)
    FROM frozen_shards fs
    JOIN download_shards sh ON sh.id = fs.id
    LEFT JOIN checkpoints cp ON cp.shard_id = sh.id
    LEFT JOIN page_latest pl ON pl.shard_id = sh.id
    LEFT JOIN page_success ps ON ps.shard_id = sh.id
    LEFT JOIN usage_success us ON us.shard_id = sh.id
  )
)::text;
SQL
)")"
psql_capture before_sql "$BEFORE_SQL" "$OUT/baseline-before.json" "$OUT/before_sql.err"
BEFORE_RC=$?
if [ "$BEFORE_RC" != "0" ] || [ ! -s "$OUT/baseline-before.json" ]; then
  if [ "$BEFORE_RC" = "0" ]; then
    # rc=0 但没有输出：同样按 INVALID 处理（空输出绝不能当成"没有任务/0 行"）。
    write_failure_sample before_sql 0
    write_sql_diagnosis before_sql 0 empty_output
  fi
  verdict INVALID 3 "before 快照 SQL 失败（rc=$BEFORE_RC，timeout_kind=$SQL_FAILURE_KIND），未运行故障套件"
fi
printf 'before_snapshot_ok bytes=%s\n' "$(wc -c < "$OUT/baseline-before.json")" | tee -a "$OUT/SUMMARY.txt"

# ---------------- 3. 严格预检 + 生成 after / EXPLAIN SQL ----------------
# 预检失败 => INVALID，不跑套件。生成物与 before 快照共用同一批冻结 ID。
PRE_OUT="$(python3 - "$OUT/baseline-before.json" "$OUT/_guard_after.sql" "$OUT/_guard_explain_new.sql" "$OUT/_guard_explain_old.sql" \
                     "$GUARD_APP_NAME" "$GUARD_STATEMENT_TIMEOUT" "$GUARD_LOCK_TIMEOUT" "$GUARD_MARK_INNER" "$GUARD_EXPLAIN_TIMEOUT" <<'PY_PRE'
import json, re, sys
bf, after_out, exp_new_out, exp_old_out, app, stmt_to, lock_to, mark, exp_to = sys.argv[1:10]
d = json.load(open(bf))
REQ = ["schema_version","taken_at","audit_max_seq","audit_count","outbox_total","outbox_pending",
       "job_ids","outbox_ids","jobs","shards"]
missing = [k for k in REQ if k not in d or d[k] is None]
if missing:
    print("INVALID: 缺少必需字段: " + ",".join(missing)); sys.exit(3)
if d["schema_version"] != 1:
    print("INVALID: schema_version=%r" % d["schema_version"]); sys.exit(3)
UUID = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
SHARD_REQ = ("id","job_id","status","page_no","rows","success_pages","provider_success_pages","token_hash")
for s in d["shards"]:
    miss = [k for k in SHARD_REQ if k not in s]
    if miss:
        print("INVALID: shard 字段缺失 %s in %r" % (miss, s.get("id"))); sys.exit(3)
for i in list(d["job_ids"]) + [s["id"] for s in d["shards"]] + list(d["outbox_ids"]):
    if not isinstance(i, str) or not UUID.match(i):
        print("INVALID: 非 UUID 标识 %r" % (i,)); sys.exit(3)
for k in ("audit_max_seq","audit_count","outbox_total","outbox_pending"):
    if not isinstance(d[k], int):
        print("INVALID: %s 非整数 %r" % (k, d[k])); sys.exit(3)
def uarr(xs):
    return "'{}'::uuid[]" if not xs else "'{" + ",".join(xs) + "}'::uuid[]"
JA, SA, OA = uarr(list(d["job_ids"])), uarr([s["id"] for s in d["shards"]]), uarr(list(d["outbox_ids"]))
PRE = "/* %s */\nSET application_name='%s'; SET statement_timeout='%s'; SET lock_timeout='%s';\n" % (mark, app, stmt_to, lock_to)
EXP_PRE = "/* %s */\nSET application_name='%s'; SET statement_timeout='%s'; SET lock_timeout='%s';\n" % (mark, app, exp_to, lock_to)
# 按 shard_id 预聚合（before/after/EXPLAIN 共用；只依赖 frozen_shards 这个冻结 ID 集合）
AGG = """frozen_shards AS (
  SELECT unnest(__SA__) AS id
),
page_latest AS (
  SELECT DISTINCT ON (p.shard_id) p.shard_id, p.next_page_token_hash
  FROM download_pages p
  WHERE p.shard_id IN (SELECT id FROM frozen_shards)
  ORDER BY p.shard_id, p.page_no DESC, p.id DESC
),
page_success AS (
  SELECT p.shard_id, count(*) AS success_pages
  FROM download_pages p
  WHERE p.shard_id IN (SELECT id FROM frozen_shards)
    AND p.status IN ('VALIDATED','INGESTED','DURABLE')
  GROUP BY p.shard_id
),
usage_success AS (
  SELECT u.shard_id, count(*) AS provider_success_pages
  FROM provider_usage u
  WHERE u.shard_id IN (SELECT id FROM frozen_shards)
    AND u.status IN ('success','ok')
  GROUP BY u.shard_id
)"""
PROJ_NEW = """json_build_object(
    'id', sh.id, 'job_id', sh.job_id, 'status', sh.status,
    'page_no', coalesce(sh.page_no, cp.page_no, 0), 'rows', coalesce(sh.downloaded_rows, cp.downloaded_rows, 0),
    'token_hash', pl.next_page_token_hash,
    'success_pages', coalesce(ps.success_pages, 0),
    'provider_success_pages', coalesce(us.provider_success_pages, 0)
  )"""
FROM_NEW = """FROM frozen_shards fs
  JOIN download_shards sh ON sh.id = fs.id
  LEFT JOIN checkpoints cp ON cp.shard_id = sh.id
  LEFT JOIN page_latest pl ON pl.shard_id = sh.id
  LEFT JOIN page_success ps ON ps.shard_id = sh.id
  LEFT JOIN usage_success us ON us.shard_id = sh.id"""
AFTER_TMPL = """WITH frozen_jobs AS (
  SELECT unnest(__JA__) AS id
),
__AGG__
SELECT json_build_object(
  'schema_version', 1,
  'taken_at', to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  'audit_max_seq', (SELECT coalesce(max(seq),0) FROM audit_events_v2),
  'audit_count', (SELECT count(*) FROM audit_events_v2),
  'outbox_total', (SELECT count(*) FROM outbox_events),
  'outbox_pending', (SELECT count(*) FROM outbox_events WHERE published_at IS NULL),
  'outbox_baseline_present', (SELECT count(*) FROM outbox_events WHERE id = ANY(__OA__)),
  'jobs_missing', (SELECT count(*) FROM frozen_jobs f WHERE NOT EXISTS (SELECT 1 FROM download_jobs j WHERE j.id=f.id)),
  'shards_missing', (SELECT count(*) FROM frozen_shards f WHERE NOT EXISTS (SELECT 1 FROM download_shards sh WHERE sh.id=f.id)),
  'jobs', (SELECT coalesce(json_agg(json_build_object('id',j.id,'status',j.status,'chain',j.chain,'mode',j.effective_mode) ORDER BY j.id),'[]'::json) FROM download_jobs j WHERE j.id = ANY(__JA__)),
  'shards', (
    SELECT coalesce(json_agg(__PROJ__ ORDER BY sh.id), '[]'::json)
    __FROM__
  )
)::text;
"""
after_sql = PRE + (AFTER_TMPL.replace("__JA__", JA).replace("__OA__", OA)
                   .replace("__AGG__", AGG.replace("__SA__", SA))
                   .replace("__PROJ__", PROJ_NEW).replace("__FROM__", FROM_NEW))
open(after_out, "w").write(after_sql)
# 优化后形态的 EXPLAIN（与 after 快照同构、同一批冻结 ID）
exp_new = EXP_PRE + ("WITH " + AGG.replace("__SA__", SA) + "\n"
                     "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)\n"
                     "SELECT coalesce(json_agg(" + PROJ_NEW + " ORDER BY sh.id), '[]'::json)::text\n"
                     + FROM_NEW + ";\n")
open(exp_new_out, "w").write(exp_new)
# 优化前形态的 EXPLAIN：逐 shard 的相关子查询（本轮被替换掉的原始写法）。
# 行集合用冻结 ID 固定，保证优化前后跑在同一批 shard 上。
OLD_PROJ = """json_build_object(
    'id', sh.id, 'job_id', sh.job_id, 'status', sh.status,
    'page_no', coalesce(sh.page_no, cp.page_no, 0), 'rows', coalesce(sh.downloaded_rows, cp.downloaded_rows, 0),
    'token_hash', (SELECT p.next_page_token_hash FROM download_pages p WHERE p.shard_id=sh.id ORDER BY p.page_no DESC LIMIT 1),
    'success_pages', (SELECT count(*) FROM download_pages p WHERE p.shard_id=sh.id AND p.status IN ('VALIDATED','INGESTED','DURABLE')),
    'provider_success_pages', (SELECT count(*) FROM provider_usage u WHERE u.shard_id=sh.id AND u.status IN ('success','ok'))
  )"""
exp_old = EXP_PRE + ("WITH frozen_shards AS (\n  SELECT unnest(" + SA + ") AS id\n)\n"
                     "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)\n"
                     "SELECT coalesce(json_agg(" + OLD_PROJ + " ORDER BY sh.id), '[]'::json)::text\n"
                     "FROM download_shards sh LEFT JOIN checkpoints cp ON cp.shard_id = sh.id\n"
                     "WHERE sh.id IN (SELECT id FROM frozen_shards);\n")
open(exp_old_out, "w").write(exp_old)
print("PREFLIGHT OK jobs=%d shards=%d outbox=%d active_work=%s" % (
      len(d["job_ids"]), len(d["shards"]), len(d["outbox_ids"]), "yes" if d["job_ids"] else "no"))
PY_PRE
)"
PRE_RC=$?
printf '%s\n' "$PRE_OUT" | tee "$OUT/preflight.txt"
if [ "$PRE_RC" != "0" ]; then
  verdict INVALID 3 "before 快照未通过严格预检（rc=$PRE_RC），未运行故障套件"
fi
JOB_N="$(printf '%s' "$PRE_OUT" | sed -n 's/.*jobs=\([0-9][0-9]*\).*/\1/p' | head -n 1)"; JOB_N="${JOB_N:-0}"
SHARD_N="$(printf '%s' "$PRE_OUT" | sed -n 's/.*shards=\([0-9][0-9]*\).*/\1/p' | head -n 1)"; SHARD_N="${SHARD_N:-0}"
OUTBOX_N="$(printf '%s' "$PRE_OUT" | sed -n 's/.*outbox=\([0-9][0-9]*\).*/\1/p' | head -n 1)"; OUTBOX_N="${OUTBOX_N:-0}"
stage_log preflight end "jobs=$JOB_N" "shards=$SHARD_N" "outbox_ids=$OUTBOX_N"

# ---------------- 4. EXPLAIN 证据（优化后形态）：只读、在套件之前 ----------------
write_explain_skipped() { # write_explain_skipped <target> <title> <reason>
  {
    echo "# $2"
    echo "# taken_at=$(ts_iso)"
    echo "# rc=n/a reason=$3"
    echo "# 未取到 EXPLAIN 计划：$3"
    echo "# 本文件不构成任何证据，也不得被当作 PASS/FAIL 或 0 行/0 耗时。"
    echo "# 需要计划证据时，用 GUARD_EXPLAIN_NEW=1 / GUARD_EXPLAIN_OLD=1 在 VM 上重跑。"
  } > "$1"
}
run_explain() { # run_explain <label> <sql_file> <target> <title> <explain_timeout> [hard_s]
  local label="$1" sqlf="$2" target="$3" title="$4" eto="${5:-$GUARD_EXPLAIN_TIMEOUT}" hard="${6:-$GUARD_EXPLAIN_HARD_TIMEOUT_S}"
  local rc elapsed start_ms
  start_ms="$(ts_ms)"
  stage_log "$label" start "sql_file=$sqlf" "statement_timeout=$eto"
  psql_capture "$label" "$(cat "$sqlf")" "$OUT/${label}.out" "$OUT/${label}.err" "$hard"
  rc=$?
  elapsed=$(( $(ts_ms) - start_ms ))
  {
    echo "# $title"
    echo "# taken_at=$(ts_iso)"
    echo "# sql_file=$sqlf"
    echo "# sql_sha256=$(sha256sum "$sqlf" 2>/dev/null | awk '{print $1}')"
    echo "# session: application_name=$GUARD_APP_NAME statement_timeout=$eto lock_timeout=$GUARD_LOCK_TIMEOUT outer_hard_timeout_s=$hard"
    echo "# rc=$rc timeout_kind=$SQL_FAILURE_KIND elapsed_ms=$elapsed elapsed_s=$(ms_to_s "$elapsed")"
    echo "# 说明：以下是 EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) 的**原始输出**；rc!=0 表示该形态"
    echo "#       没有在超时内跑完 —— 这本身就是证据，绝不能被读成 0 行 / 0 耗时。"
    echo "# ---- raw output ----"
    cat "$OUT/${label}.out" 2>/dev/null
    if [ "$rc" != "0" ]; then
      echo "# ---- stderr（原样，尾部 40 行）----"
      tail -n 40 "$OUT/${label}.err" 2>/dev/null
    fi
  } > "$target"
  stage_log "$label" end "rc=$rc" "elapsed_ms=$elapsed" "elapsed_s=$(ms_to_s "$elapsed")" "target=$target"
  return "$rc"
}

if [ "$GUARD_EXPLAIN_NEW" = "1" ] && [ "$SHARD_N" -gt 0 ]; then
  run_explain explain_new "$OUT/_guard_explain_new.sql" "$OUT/explain-after.txt" \
    "优化后形态（冻结 ID 预聚合 + JOIN）EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)"
  EXPLAIN_NEW_RC=$?
  printf 'explain_new_rc=%s file=%s\n' "$EXPLAIN_NEW_RC" "$OUT/explain-after.txt" | tee -a "$OUT/SUMMARY.txt"
  if [ "$EXPLAIN_NEW_RC" != "0" ] && [ "$GUARD_EXPLAIN_NEW_GATING" = "1" ]; then
    # 与 before 快照同构的查询在预算内没跑完 —— 按"SQL 失败/超时 => INVALID 且不跑套件"处理。
    # （若确认只是瞬时争用，可显式 GUARD_EXPLAIN_NEW_GATING=0 再跑，但那条记录会留在证据里。）
    verdict INVALID 3 "优化后形态的 EXPLAIN 未通过（rc=$EXPLAIN_NEW_RC，timeout_kind=$SQL_FAILURE_KIND），未运行故障套件"
  fi
else
  if [ "$SHARD_N" -gt 0 ]; then
    write_explain_skipped "$OUT/explain-after.txt" "优化后形态（冻结 ID 预聚合 + JOIN）EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)" "GUARD_EXPLAIN_NEW=$GUARD_EXPLAIN_NEW（被显式关闭）"
  else
    write_explain_skipped "$OUT/explain-after.txt" "优化后形态（冻结 ID 预聚合 + JOIN）EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)" "冻结 shard 集合为空（shards=0），EXPLAIN 计划不具代表性"
  fi
  EXPLAIN_NEW_RC=skipped
  stage_log explain_new end "rc=skipped"
fi

# ---------------- 5. 运行被测套件 ----------------
# 刻意**不**打开 errexit：套件失败、快照失败、比较脚本失败都必须继续走到
# "写 verdict + 返回真实 rc"，绝不留下没有结论/没有 rc 的目录。
stage_log suite start "suite=$SUITE"
set +e
bash "$SUITE" "$@"
SUITE_RC=$?
stage_log suite end "rc=$SUITE_RC"
echo "suite_rc=$SUITE_RC" | tee "$OUT/suite-rc.txt"

# ---------------- 6. AFTER 快照（同一冻结 ID 集合） ----------------
AFTER_SQL="$(cat "$OUT/_guard_after.sql")"
psql_capture after_sql "$AFTER_SQL" "$OUT/baseline-after.json" "$OUT/after_sql.err"
AFTER_RC=$?
if [ "$AFTER_RC" != "0" ] || [ ! -s "$OUT/baseline-after.json" ]; then
  if [ "$AFTER_RC" = "0" ]; then
    write_failure_sample after_sql 0
    write_sql_diagnosis after_sql 0 empty_output
  fi
  # after 阶段判 INVALID：套件已经跑过，rc 里带着套件结果，但结论是"无法得出结论"。
  verdict INVALID 3 "after 快照 SQL 失败（rc=$AFTER_RC，timeout_kind=$SQL_FAILURE_KIND）；套件 rc=$SUITE_RC"
fi
printf 'after_snapshot_ok bytes=%s suite_rc=%s\n' "$(wc -c < "$OUT/baseline-after.json")" "$SUITE_RC" | tee -a "$OUT/SUMMARY.txt"

# ---------------- 7. 不变量比对 ----------------
stage_log compare start
set +e
python3 - "$OUT/baseline-before.json" "$OUT/baseline-after.json" "$OUT/drill-guard-verdict.json" "$SUITE_RC" <<'PY_CMP'
import hashlib, json, sys
b = json.load(open(sys.argv[1])); a = json.load(open(sys.argv[2]))
out_p, suite_rc = sys.argv[3], int(sys.argv[4])
def sha(p):
    return hashlib.sha256(open(p, "rb").read()).hexdigest()
REQ = ["schema_version","taken_at","audit_max_seq","audit_count","outbox_total","outbox_pending",
       "outbox_baseline_present","jobs_missing","shards_missing","jobs","shards"]
missing = [k for k in REQ if k not in a or a[k] is None]
if missing:
    json.dump({"verdict":"INVALID","rc":3,"suite_rc":suite_rc,
               "message":"after 快照缺少字段: " + ",".join(missing)}, open(out_p,"w"), ensure_ascii=False, indent=2)
    print("INVALID: after 缺少 " + ",".join(missing)); sys.exit(3)
violations, review, notes = [], [], []
if a["audit_count"] < b["audit_count"] or a["audit_max_seq"] < b["audit_max_seq"]:
    violations.append({"invariant":"audit_monotonic","before":[b["audit_count"],b["audit_max_seq"]],
                       "after":[a["audit_count"],a["audit_max_seq"]]})
if a["outbox_total"] < b["outbox_total"]:
    violations.append({"invariant":"outbox_rows_not_decreased","before":b["outbox_total"],"after":a["outbox_total"]})
if a["outbox_baseline_present"] != len(b["outbox_ids"]):
    violations.append({"invariant":"outbox_baseline_ids_all_present",
                       "expected":len(b["outbox_ids"]),"found":a["outbox_baseline_present"]})
if a["jobs_missing"] != 0:
    violations.append({"invariant":"baseline_jobs_still_exist","missing":a["jobs_missing"]})
if a["shards_missing"] != 0:
    violations.append({"invariant":"baseline_shards_still_exist","missing":a["shards_missing"]})
ajobs = {j["id"]: j for j in a["jobs"]}
for j in b["jobs"]:
    jj = ajobs.get(j["id"])
    if jj is None:
        continue
    if jj["status"] == "FAILED_FINAL" and j["status"] != "FAILED_FINAL":
        review.append({"kind":"baseline_job_terminal_failed","job_id":j["id"],
                       "from":j["status"],"to":jj["status"]})
    elif jj["status"] != j["status"]:
        notes.append({"kind":"baseline_job_status_change","job_id":j["id"],
                      "from":j["status"],"to":jj["status"]})
ashards = {s["id"]: s for s in a["shards"]}
for s in b["shards"]:
    ss = ashards.get(s["id"])
    if ss is None:
        continue
    for f in ("page_no","rows","success_pages","provider_success_pages"):
        if ss[f] < s[f]:
            violations.append({"invariant":"shard_counter_not_decreased","shard_id":s["id"],
                               "field":f,"before":s[f],"after":ss[f]})
    tb, ta = s.get("token_hash"), ss.get("token_hash")
    if tb != ta:
        progressed = (ss["page_no"] > s["page_no"]) or (ss["success_pages"] > s["success_pages"])
        ev = {"shard_id":s["id"],"before":("none" if tb is None else "set"),
              "after":("none" if ta is None else "set"),
              "page_no":[s["page_no"],ss["page_no"]],
              "success_pages":[s["success_pages"],ss["success_pages"]],
              "shard_status_after":ss["status"]}
        if progressed:
            ev["classification"] = "legitimate_progress"
            notes.append({"kind":"cursor_advanced","detail":ev})
        else:
            ev["classification"] = "cursor_changed_without_progress"
            violations.append({"invariant":"cursor_changed_without_progress","detail":ev})
if not b["job_ids"]:
    notes.append({"kind":"no_active_work_at_baseline",
                  "note":"基线没有活跃 job；本壳只能证明不变量为空集，不能证明用户任务未受影响"})
verdict = "FAIL" if violations else ("REVIEW" if review else "PASS")
if suite_rc != 0:
    violations.append({"invariant":"suite_exit_code","rc":suite_rc})
    if verdict != "INVALID":
        verdict = "FAIL"
rc = 0 if verdict == "PASS" else (2 if verdict == "REVIEW" else (3 if verdict == "INVALID" else 1))
res = {"verdict": verdict, "rc": rc, "suite_rc": suite_rc,
       "checked": {"baseline_jobs": len(b["jobs"]), "baseline_shards": len(b["shards"]),
                   "baseline_outbox_ids": len(b["outbox_ids"])},
       "invariants_checked": ["audit_monotonic","outbox_rows_not_decreased","outbox_baseline_ids_all_present",
                              "baseline_jobs_still_exist","baseline_shards_still_exist",
                              "shard_counter_not_decreased(page_no/rows/success_pages/provider_success_pages)",
                              "cursor_digest_change_classified"],
       "scope_note": "本结论只覆盖上述可比对不变量；不等于用户数据保留已被完整证明。",
       "evidence": {"baseline_before_sha256": sha(sys.argv[1]), "baseline_after_sha256": sha(sys.argv[2])},
       "violations": violations, "review": review, "notes": notes}
json.dump(res, open(out_p, "w"), ensure_ascii=False, indent=2)
print(json.dumps(res, ensure_ascii=False, indent=2))
sys.exit(rc)
PY_CMP
CMP_RC=$?
VERDICT_JSON_FALLBACK=0
if [ ! -s "$OUT/drill-guard-verdict.json" ]; then
  # 比较脚本没产出结论（例如 python3 缺失/异常）：按 INVALID 落盘，rc 也必须归到契约域。
  printf '{"verdict":"INVALID","rc":3,"suite_rc":%s,"message":"比较脚本未产出结论（cmp_rc=%s）"}\n' "$SUITE_RC" "$CMP_RC" \
    > "$OUT/drill-guard-verdict.json"
  VERDICT_JSON_FALLBACK=1
fi
VERDICT_NAME=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('verdict','unknown'))" "$OUT/drill-guard-verdict.json" 2>/dev/null || echo unknown)
stage_log compare end "rc=$CMP_RC" "verdict=$VERDICT_NAME"

# ---------------- 8. 真实 rc 归一（结论已落盘，这里只对齐 rc） ----------------
FINAL_RC="$CMP_RC"
[ "$VERDICT_JSON_FALLBACK" = "1" ] && FINAL_RC=3   # 兜底写出来的 INVALID 结论：rc 归到契约域，不把 python 的 127 之类漏出去
VJSON_RC="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('rc',''))" "$OUT/drill-guard-verdict.json" 2>/dev/null || true)"
case "$VJSON_RC" in ''|*[!0-9]*) ;; *) FINAL_RC="$VJSON_RC" ;; esac
if [ "$SUITE_RC" != "0" ] && [ "$FINAL_RC" = "0" ]; then
  # 双保险：套件失败绝不能被"比较脚本给出 PASS"吞掉（历史上正是这种吞法造成假 PASS）。
  FINAL_RC=1
  printf 'WARN: 套件 rc=%s 但比较脚本给出 PASS，按 FAIL 处理（rc=1）\n' "$SUITE_RC" | tee -a "$OUT/SUMMARY.txt"
  python3 - "$OUT/drill-guard-verdict.json" <<'PY_FIX'
import json, sys
p = sys.argv[1]; d = json.load(open(p))
d["verdict"] = "FAIL"; d["rc"] = 1
d.setdefault("violations", []).append({"invariant":"suite_exit_code_vs_compare",
    "detail":"比较脚本给出 PASS 而套件 rc!=0；按 FAIL 记录"})
json.dump(d, open(p, "w"), ensure_ascii=False, indent=2)
print("verdict_json_corrected")
PY_FIX
fi

# ---------------- 9. EXPLAIN 证据（优化前形态）：只读、诊断性质、绝不改 rc ----------------
# 放在 verdict 之后：旧形态很慢（这正是被替换的原因），绝不能让它的耗时挡住演练或改变结论。
if [ "$GUARD_EXPLAIN_OLD" = "1" ] && [ "$SHARD_N" -gt 0 ]; then
  run_explain explain_old "$OUT/_guard_explain_old.sql" "$OUT/explain-before.txt" \
    "优化前形态（逐 shard 相关子查询）EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)"
  EXPLAIN_OLD_RC=$?
  printf 'explain_old_rc=%s file=%s（不参与判定）\n' "$EXPLAIN_OLD_RC" "$OUT/explain-before.txt" | tee -a "$OUT/SUMMARY.txt"
else
  if [ "$SHARD_N" -gt 0 ]; then
    write_explain_skipped "$OUT/explain-before.txt" "优化前形态（逐 shard 相关子查询）EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)" "GUARD_EXPLAIN_OLD=$GUARD_EXPLAIN_OLD（被显式关闭）"
  else
    write_explain_skipped "$OUT/explain-before.txt" "优化前形态（逐 shard 相关子查询）EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)" "冻结 shard 集合为空（shards=0），EXPLAIN 计划不具代表性"
  fi
  EXPLAIN_OLD_RC=skipped
  stage_log explain_old end "rc=skipped"
fi

# 把诊断步骤的 rc 注入 verdict json（只加字段，不改 verdict/rc）。
python3 - "$OUT/drill-guard-verdict.json" "$EXPLAIN_NEW_RC" "$EXPLAIN_OLD_RC" "$OUT/explain-after.txt" "$OUT/explain-before.txt" "$OUT/stage-log.txt" "$OUT/catalog.txt" "$GUARD_EXPLAIN_NEW_GATING" "$GUARD_EXPLAIN_OLD" <<'PY_DIAG' || true
import json, sys
p, nr, orc, nf, of, sl, cat, gnew, gold = sys.argv[1:10]
try:
    d = json.load(open(p))
except Exception as e:
    print("diag_inject_skipped: %s" % e); sys.exit(0)
d["diagnostics"] = {"explain_new_rc": nr, "explain_old_rc": orc,
                    "explain_new_file": nf, "explain_old_file": of,
                    "catalog_file": cat, "stage_log": sl,
                    "pg_stat_activity": "%s/pg-stat-activity.txt" % d.get("out_dir", ""),
                    "explain_new_gating": (gnew == "1"), "explain_old_enabled": (gold == "1"),
                    "note": "explain_*_rc 只是诊断步骤退出码（rc!=0 是证据，不是 PASS）；PASS/FAIL 由不变量比对决定"}
json.dump(d, open(p, "w"), ensure_ascii=False, indent=2)
print("diag_injected")
PY_DIAG

# ---------------- 10. 收尾 ----------------
stage_log guard end "rc=$FINAL_RC" "verdict=$VERDICT_NAME" "suite_rc=$SUITE_RC" \
  "explain_new_rc=$EXPLAIN_NEW_RC" "explain_old_rc=$EXPLAIN_OLD_RC"
printf "suite_rc=%s guard_cmp_rc=%s guard_rc=%s verdict=%s\n" \
  "$SUITE_RC" "$CMP_RC" "$FINAL_RC" "$VERDICT_NAME" | tee -a "$OUT/SUMMARY.txt"
if [ "$SUITE_RC" != "0" ]; then
  echo "套件返回非 0（$SUITE_RC）-> 整体判失败" | tee -a "$OUT/SUMMARY.txt"
fi
# 真实 rc 就是结论的 rc：0=PASS 1=FAIL 2=REVIEW 3=INVALID
exit "$FINAL_RC"
