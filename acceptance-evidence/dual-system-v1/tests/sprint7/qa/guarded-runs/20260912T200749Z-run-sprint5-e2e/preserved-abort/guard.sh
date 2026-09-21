#!/usr/bin/env bash
# 故障注入演练的"用户在工作"保护壳（2026-09-13 重写；监控者两轮复核后修订）。
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
# 用法：bash run-fault-drill-guarded.sh <套件脚本> [参数...]
# 退出码：0=PASS 1=FAIL 2=REVIEW 3=INVALID（INVALID 表示未运行套件或无法得出结论）
set -uo pipefail

PG_CONTAINER="${PG_CONTAINER:-deploy-postgres-1}"
PGUSER="${PGUSER:-blockchain}"
PGDB="${PGDB:-blockchain_platform}"
SUITE="${1:?需要被测套件脚本路径}"; shift || true
OUT="${DRILL_OUT:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/sprint7/qa/drill-guard-$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$OUT"
echo "DRILL_OUT=$OUT"
echo "guard_self_sha256=$(sha256sum "${BASH_SOURCE[0]}" | awk '{print $1}')" | tee "$OUT/guard-self.txt"
echo "guard_self_size=$(stat -c %s "${BASH_SOURCE[0]}")" >> "$OUT/guard-self.txt"
echo "suite_self_sha256=$(sha256sum "$SUITE" | awk '{print $1}')" >> "$OUT/guard-self.txt"
echo "suite_self_size=$(stat -c %s "$SUITE")" >> "$OUT/guard-self.txt"

psql_json() { docker exec -i "$PG_CONTAINER" psql -U "$PGUSER" -d "$PGDB" -At -v ON_ERROR_STOP=1; }

verdict() { # verdict <status> <rc> <message>
  printf '{"verdict":"%s","rc":%s,"message":"%s","out_dir":"%s"}\n' "$1" "$2" "$3" "$OUT" \
    | tee "$OUT/drill-guard-verdict.json"
  printf "suite_rc=%s guard_rc=%s verdict=%s\n" "${SUITE_RC:--}" "$2" "$1" | tee "$OUT/SUMMARY.txt"
  exit "$2"
}

SUITE_RC=-

# ---------------- 1. BEFORE 快照 ----------------
BEFORE_JSON=$(psql_json <<'SQL'
WITH active_jobs AS (
  SELECT id, status, chain, effective_mode FROM download_jobs
  WHERE status IN ('RUNNING','QUEUED','PAUSE_REQUESTED','PAUSED','PAUSED_FALLBACK_TOO_EXPENSIVE')
)
SELECT json_build_object(
  'schema_version', 1,
  'taken_at', to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  'audit_max_seq', (SELECT coalesce(max(seq),0) FROM audit_events_v2),
  'audit_count', (SELECT count(*) FROM audit_events_v2),
  'outbox_total', (SELECT count(*) FROM outbox_events),
  'outbox_pending', (SELECT count(*) FROM outbox_events WHERE published_at IS NULL),
  'job_ids', (SELECT coalesce(json_agg(id::text ORDER BY id),'[]'::json) FROM active_jobs),
  'outbox_ids', (SELECT coalesce(json_agg(id::text ORDER BY id),'[]'::json) FROM outbox_events),
  'jobs', (SELECT coalesce(json_agg(json_build_object('id',id,'status',status,'chain',chain,'mode',effective_mode) ORDER BY id),'[]'::json) FROM active_jobs),
  'shards', (
    SELECT coalesce(json_agg(json_build_object(
      'id', sh.id, 'job_id', sh.job_id, 'status', sh.status,
      'page_no', coalesce(sh.page_no, cp.page_no, 0), 'rows', coalesce(sh.downloaded_rows, cp.downloaded_rows, 0),
      'token_hash', (SELECT p.next_page_token_hash FROM download_pages p WHERE p.shard_id=sh.id ORDER BY p.page_no DESC LIMIT 1),
      'success_pages', (SELECT count(*) FROM download_pages p WHERE p.shard_id=sh.id AND p.status IN ('VALIDATED','INGESTED','DURABLE')),
      'provider_success_pages', (SELECT count(*) FROM provider_usage u WHERE u.shard_id=sh.id AND u.status IN ('success','ok'))
    ) ORDER BY sh.id), '[]'::json)
    FROM download_shards sh LEFT JOIN checkpoints cp ON cp.shard_id=sh.id
    WHERE sh.job_id IN (SELECT id FROM active_jobs)
  )
)::text;
SQL
)
BEFORE_RC=$?
if [ "$BEFORE_RC" != "0" ] || [ -z "${BEFORE_JSON//[[:space:]]/}" ]; then
  verdict INVALID 3 "before 快照 SQL 失败（rc=$BEFORE_RC），未运行故障套件"
fi
printf '%s' "$BEFORE_JSON" > "$OUT/baseline-before.json"

# ---------------- 2. 严格预检（失败则不跑套件） ----------------
python3 - "$OUT/baseline-before.json" "$OUT/_guard_after.sql" <<'PY_PRE'
import json, re, sys
d = json.load(open(sys.argv[1]))
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
sql = """
WITH frozen_jobs AS (SELECT unnest(__JOBIDS__) AS id),
     frozen_shards AS (SELECT unnest(__SHARDIDS__) AS id)
SELECT json_build_object(
  'schema_version', 1,
  'taken_at', to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  'audit_max_seq', (SELECT coalesce(max(seq),0) FROM audit_events_v2),
  'audit_count', (SELECT count(*) FROM audit_events_v2),
  'outbox_total', (SELECT count(*) FROM outbox_events),
  'outbox_pending', (SELECT count(*) FROM outbox_events WHERE published_at IS NULL),
  'outbox_baseline_present', (SELECT count(*) FROM outbox_events WHERE id = ANY(__OUTBOXIDS__)),
  'jobs_missing', (SELECT count(*) FROM frozen_jobs f WHERE NOT EXISTS (SELECT 1 FROM download_jobs j WHERE j.id=f.id)),
  'shards_missing', (SELECT count(*) FROM frozen_shards f WHERE NOT EXISTS (SELECT 1 FROM download_shards sh WHERE sh.id=f.id)),
  'jobs', (SELECT coalesce(json_agg(json_build_object('id',j.id,'status',j.status,'chain',j.chain,'mode',j.effective_mode) ORDER BY j.id),'[]'::json) FROM download_jobs j WHERE j.id = ANY(__JOBIDS__)),
  'shards', (
    SELECT coalesce(json_agg(json_build_object(
      'id', sh.id, 'job_id', sh.job_id, 'status', sh.status,
      'page_no', coalesce(sh.page_no, cp.page_no, 0), 'rows', coalesce(sh.downloaded_rows, cp.downloaded_rows, 0),
      'token_hash', (SELECT p.next_page_token_hash FROM download_pages p WHERE p.shard_id=sh.id ORDER BY p.page_no DESC LIMIT 1),
      'success_pages', (SELECT count(*) FROM download_pages p WHERE p.shard_id=sh.id AND p.status IN ('VALIDATED','INGESTED','DURABLE')),
      'provider_success_pages', (SELECT count(*) FROM provider_usage u WHERE u.shard_id=sh.id AND u.status IN ('success','ok'))
    ) ORDER BY sh.id), '[]'::json)
    FROM download_shards sh LEFT JOIN checkpoints cp ON cp.shard_id=sh.id
    WHERE sh.id = ANY(__SHARDIDS__)
  )
)::text;
"""
sql = sql.replace("__JOBIDS__", uarr(list(d["job_ids"])))
sql = sql.replace("__SHARDIDS__", uarr([s["id"] for s in d["shards"]]))
sql = sql.replace("__OUTBOXIDS__", uarr(list(d["outbox_ids"])))
open(sys.argv[2], "w").write(sql)
print("PREFLIGHT OK jobs=%d shards=%d outbox=%d active_work=%s" % (
      len(d["job_ids"]), len(d["shards"]), len(d["outbox_ids"]), "yes" if d["job_ids"] else "no"))
PY_PRE
PRE_RC=$?
if [ "$PRE_RC" != "0" ]; then
  verdict INVALID 3 "before 快照未通过严格预检（rc=$PRE_RC），未运行故障套件"
fi

# ---------------- 3. 运行被测套件 ----------------
# 刻意**不**打开 errexit：套件失败、快照失败、比较脚本失败都必须继续走到
# "写 verdict + 返回真实 rc"，绝不留下没有结论/没有 rc 的目录。
set +e
bash "$SUITE" "$@"
SUITE_RC=$?
echo "suite_rc=$SUITE_RC" | tee "$OUT/suite-rc.txt"

# ---------------- 4. AFTER 快照（同一冻结 ID 集合） ----------------
AFTER_JSON=$(psql_json < "$OUT/_guard_after.sql")
AFTER_RC=$?
if [ "$AFTER_RC" != "0" ] || [ -z "${AFTER_JSON//[[:space:]]/}" ]; then
  verdict INVALID 3 "after 快照 SQL 失败（rc=$AFTER_RC）；套件 rc=$SUITE_RC"
fi
printf '%s' "$AFTER_JSON" > "$OUT/baseline-after.json"

# ---------------- 5. 不变量比对 ----------------
set +e
python3 - "$OUT/baseline-before.json" "$OUT/baseline-after.json" "$OUT/drill-guard-verdict.json" "$SUITE_RC" <<'PY_CMP'
import json, sys
b = json.load(open(sys.argv[1])); a = json.load(open(sys.argv[2]))
out_p, suite_rc = sys.argv[3], int(sys.argv[4])
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
res = {"verdict": "FAIL" if violations else ("REVIEW" if review else "PASS"),
       "suite_rc": suite_rc,
       "checked": {"baseline_jobs": len(b["jobs"]), "baseline_shards": len(b["shards"]),
                   "baseline_outbox_ids": len(b["outbox_ids"])},
       "invariants_checked": ["audit_monotonic","outbox_rows_not_decreased","outbox_baseline_ids_all_present",
                              "baseline_jobs_still_exist","baseline_shards_still_exist",
                              "shard_counter_not_decreased(page_no/rows/success_pages/provider_success_pages)",
                              "cursor_digest_change_classified"],
       "scope_note": "本结论只覆盖上述可比对不变量；不等于用户数据保留已被完整证明。",
       "violations": violations, "review": review, "notes": notes}
if suite_rc != 0:
    res["violations"].append({"invariant":"suite_exit_code","rc":suite_rc})
    if res["verdict"] != "INVALID":
        res["verdict"] = "FAIL"
json.dump(res, open(out_p, "w"), ensure_ascii=False, indent=2)
print(json.dumps(res, ensure_ascii=False, indent=2))
sys.exit(0 if res["verdict"] == "PASS" else (2 if res["verdict"] == "REVIEW" else 1))
PY_CMP
CMP_RC=$?
if [ ! -s "$OUT/drill-guard-verdict.json" ]; then
  printf '{"verdict":"INVALID","rc":3,"suite_rc":%s,"message":"比较脚本未产出结论（cmp_rc=%s）"}\n' "$SUITE_RC" "$CMP_RC" \
    > "$OUT/drill-guard-verdict.json"
fi
VERDICT_NAME=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('verdict','unknown'))" "$OUT/drill-guard-verdict.json" 2>/dev/null || echo unknown)
printf "suite_rc=%s guard_cmp_rc=%s verdict=%s\n" "$SUITE_RC" "$CMP_RC" "$VERDICT_NAME" | tee -a "$OUT/SUMMARY.txt"
if [ "$SUITE_RC" != "0" ]; then echo "套件返回非 0（$SUITE_RC）→ 整体判失败"; exit 1; fi
exit "$CMP_RC"
