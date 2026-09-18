#!/usr/bin/env bash
# r10-audit-completed-probe-v1.sh — 针对「后台导出完成事件的 v2 审计链真实行」的**定点真实测量**。
#
# 目的（D-R10V4-01 闭环的第 1 步）：在**不改任何源码**的前提下，用真实 HTTP + 真实 DB，
# 把 export.completed 在 audit_events_v2 里的真实行（scope/actor/request/trace/链链接）**量出来**，
# 而不是靠读代码推断。量出来的事实决定"审计修复是否真的闭环"。
#
# 红线：只做真实登录 + 真实建导出（追加行，不删任何行）；不 TRUNCATE/reset/clean；不改运行态。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=${PROBE_EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-audit-completed-probe-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
EXPECT_API_SHA="${PROBE_EXPECT_API_SHA:-569f40ec66e982804072591628692d993cbb59f01e5477a6134a22d6e9391527}"
BASE=https://127.0.0.1:8448
JOB_NO="${PROBE_JOB_NO:-DL-20260913-0018}"
JARS=$(mktemp -d /tmp/r10probe-jars-XXXXXX); chmod 700 "$JARS"
trap 'rm -rf "$JARS"' EXIT
exec > >(tee "$EV/probe-console.log") 2>&1
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1 :: $2"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1 :: $2"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3" "have=$1"; else bad "$3" "have=$1 want=$2"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }
jqget(){ python3 -c 'import json,sys
try:
    d=json.load(open(sys.argv[1]))
except Exception:
    print(""); raise SystemExit(0)
def dig(o,*ks):
    for k in ks:
        o=(o or {}).get(k) if isinstance(o,dict) else None
    return o or ""
print(dig(d,"data",sys.argv[2]) or dig(d,sys.argv[2]) or "")' "$1" "$2"; }
csrf(){ python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(((d.get("data") or {}).get("csrf_token")) or "")' "$1"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV expect_api_sha=$EXPECT_API_SHA"
APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
API_SHA=$(sha256sum /proc/$APIPID/exe | cut -d' ' -f1)
echo "api_pid=$APIPID api_sha=$API_SHA" | tee "$EV/probe-meta.txt"
chk "$API_SHA" "$EXPECT_API_SHA" "P0 running api sha equals expected artifact" "pid=$APIPID"

set -a; . /home/euripides/.s7jobscope-creds.env; set +a
IE="$S7JS_INVESTIGATOR_EMAIL"; IPW="$S7JS_INVESTIGATOR_PW"; IWS="$S7JS_INVESTIGATOR_WS"
JOB_ID=$(printf "SELECT id FROM download_jobs WHERE job_no='%s';\n" "$JOB_NO" | pgq | tr -d '[:space:]')
echo "job_no=$JOB_NO job_id=$JOB_ID ws=$IWS" | tee -a "$EV/probe-meta.txt"
L=$(printf %s "$JOB_ID" | wc -c); chk "$L" "36" "P1 job fixture resolved" "job_no=$JOB_NO"

H=$(curl -sS -k -o "$JARS/inv.login.json" -w '%{http_code}' -c "$JARS/inv.ck" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -X POST "$BASE/api/auth/v1/login" \
  --data-binary "$(python3 -c 'import json,sys;print(json.dumps({"identifier":sys.argv[1],"password":sys.argv[2],"audience":"user","workspace_id":sys.argv[3]}))' "$IE" "$IPW" "$IWS")")
chk "$H" "200" "P2 investigator login 200" "identifier_present=$([ -n "$IE" ] && echo yes || echo no)"
CT=$(csrf "$JARS/inv.login.json")
if [ -n "$CT" ]; then ok "P3 csrf token present" "len=${#CT}"; else bad "P3 csrf token present" "empty"; fi

echo "=== P4 真实创建导出（POST /api/v2/exports） ==="
IK="r10probe-$(date -u +%s%N)"
H=$(curl -sS -k -b "$JARS/inv.ck" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CT" -H "Idempotency-Key: $IK" \
  -X POST "$BASE/api/v2/exports" --data-binary "{\"job_id\":\"$JOB_ID\",\"format\":\"xlsx\"}" \
  -o "$EV/export-create.json" -w '%{http_code}')
echo "create_http=$H idempotency_key_sha12=$(printf %s "$IK" | sha256sum | cut -c1-12)" | tee -a "$EV/probe-meta.txt"
EXPORT_ID=$(jqget "$EV/export-create.json" id)
echo "export_id=$EXPORT_ID"
EL=$(printf %s "$EXPORT_ID" | wc -c); chk "$EL" "36" "P5 export created via real API" "http=$H"
if [ "$EL" != "36" ]; then echo "PROBE_ABORT no export id — 不伪造结论"; echo "PROBE_VERDICT=ABORT"; exit 3; fi

echo "=== P6 有界等待导出完成（真实 DB 状态） ==="
ST=""; i=0
for i in $(seq 1 90); do
  ST=$(printf "SELECT status FROM exports WHERE id='%s';\n" "$EXPORT_ID" | pgq | tr -d '[:space:]')
  case "$ST" in COMPLETED|FAILED) break;; esac
  sleep 2
done
echo "export_status=$ST attempts=$i" | tee -a "$EV/probe-meta.txt"
chk "$ST" "COMPLETED" "P6 export reached COMPLETED" "attempts=$i"

echo "=== P7 audit_events_v2 真实行（该导出的全部动作） ==="
printf "SELECT seq, action, occurred_at, actor_type, coalesce(actor_id::text,''), actor_email, coalesce(organization_id::text,''), coalesce(workspace_id::text,''), coalesce(session_id::text,''), http_method, route_template, request_id, trace_id, result, prev_hash, event_hash FROM audit_events_v2 WHERE resource_id='%s' ORDER BY seq;\n" "$EXPORT_ID" | pgq > "$EV/audit-v2-rows.txt"
cat "$EV/audit-v2-rows.txt"

echo "=== P8 audit_events (legacy) 真实行（对照保留） ==="
printf "SELECT action, created_at, coalesce(user_id::text,''), resource_type, detail::text FROM audit_events WHERE resource_id='%s' ORDER BY created_at;\n" "$EXPORT_ID" | pgq > "$EV/audit-legacy-rows.txt"
cat "$EV/audit-legacy-rows.txt"

echo "=== P9 export.completed 的 request 维度真值判定 ==="
python3 - "$EV" "$EXPORT_ID" <<'PY' | tee "$EV/verdict.txt"
import json, os, sys
ev, export_id = sys.argv[1], sys.argv[2]
rows = []
for line in open(os.path.join(ev, "audit-v2-rows.txt"), encoding="utf-8"):
    line = line.rstrip("\n")
    if not line:
        continue
    p = line.split("|")
    if len(p) < 16:
        continue
    rows.append({"seq": p[0], "action": p[1], "occurred_at": p[2], "actor_type": p[3], "actor_id": p[4],
                 "actor_email": p[5], "organization_id": p[6], "workspace_id": p[7], "session_id": p[8],
                 "http_method": p[9], "route": p[10], "request_id": p[11], "trace_id": p[12],
                 "result": p[13], "prev_hash": p[14], "event_hash": p[15]})
legacy_actions = []
for line in open(os.path.join(ev, "audit-legacy-rows.txt"), encoding="utf-8"):
    line = line.rstrip("\n")
    if line:
        legacy_actions.append(line.split("|")[0])
completed = [r for r in rows if r["action"] == "export.completed"]
res = {"export_id": export_id, "audit_v2_actions": [r["action"] for r in rows],
       "legacy_actions": legacy_actions, "export_completed_row_count": len(completed),
       "export_completed": completed[0] if completed else None}
REAL_EXPORT_ROUTES = ("/api/v2/exports", "/api/v1/exports", "/api/v1/actions/export", "/api/v1/download-exports")
if completed:
    r = completed[0]
    res["checks"] = {
        "present_in_v2": True,
        "scope_ok": bool(r["organization_id"]) and bool(r["workspace_id"]),
        "actor_ok": bool(r["actor_id"]) and bool(r["actor_email"]),
        "request_id_present": bool(r["request_id"]),
        "trace_id_present": bool(r["trace_id"]),
        "route_present": bool(r["route"]),
        "route_is_real_export_route": r["route"] in REAL_EXPORT_ROUTES,
        "route_looks_fabricated_auth_refresh": r["route"] == "/api/auth/v1/refresh",
        "event_hash_present": bool(r["event_hash"]),
        "prev_hash_present": bool(r["prev_hash"]),
    }
else:
    res["checks"] = {"present_in_v2": False}
json.dump(res, open(os.path.join(ev, "audit-completed-verdict.json"), "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print(json.dumps(res, ensure_ascii=False, indent=1))
PY

echo "=== P10 结论 ==="
python3 - "$EV" <<'PY'
import json, os, sys
ev = sys.argv[1]
res = json.load(open(os.path.join(ev, "audit-completed-verdict.json"), encoding="utf-8"))
c = res["checks"]
row = res.get("export_completed") or {}
print("present_in_v2=%s route=%s method=%s request_id_present=%s trace_id_present=%s route_is_real=%s fabricated_refresh=%s" % (
    c.get("present_in_v2"), row.get("route"), row.get("http_method"),
    c.get("request_id_present"), c.get("trace_id_present"),
    c.get("route_is_real_export_route"), c.get("route_looks_fabricated_auth_refresh")))
PY
echo "PROBE_EVIDENCE_DIR=$EV"
echo "PROBE_PASS=$PASS PROBE_FAIL=$FAIL"
exit 0
