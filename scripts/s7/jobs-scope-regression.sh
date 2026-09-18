#!/usr/bin/env bash
# jobs-scope-regression.sh — R1..R8 真实作用域回归（HTTP + DB 逐 JobID 比对）。
#
# 背景：旧实现 listJobs 把 org 参数序号错用到 workspace 比较上，且缺 ::uuid，
# 导致 GET /api/v1/download-jobs 500（SQLSTATE 42883）并可能静默返回错误集合。
# 本脚本对**当前运行的二进制**做真实回归，断言集合级正确性而不只是 200。
#
# 夹具：专用测试组织 s7jobscope（wsA=3 个任务 / wsB=2 个任务），
#       另有 ACME 夹具组织（acme-lab 72 个任务）作为跨组织对照。
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
BASE="${BASE:-http://127.0.0.1:8080}"
S7JS_CREDS="${S7JS_CREDS:-/home/euripides/.s7jobscope-creds.env}"
ACME_CREDS="${ACME_CREDS:-/home/euripides/.blockchain-fixture-creds.env}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/jobs-scope-r1r8-$TS}"
mkdir -p "$EV"; chmod 700 "$EV"
PG=(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc)
q() { "${PG[@]}" "$1"; }

PASS=0; FAIL=0; NV=0
ok()   { PASS=$((PASS+1)); echo "PASS $*" | tee -a "$EV/run.log"; }
bad()  { FAIL=$((FAIL+1)); echo "FAIL $*" | tee -a "$EV/run.log"; }
nv()   { NV=$((NV+1));     echo "NOT_VERIFIED $*" | tee -a "$EV/run.log"; }
: > "$EV/run.log"
echo "=== jobs-scope-regression $TS base=$BASE ===" | tee -a "$EV/run.log"
sha256sum "$0" | tee -a "$EV/run.log"

[ -r "$S7JS_CREDS" ] || { echo "missing $S7JS_CREDS"; exit 2; }
[ -r "$ACME_CREDS" ] || { echo "missing $ACME_CREDS"; exit 2; }
set -a; . "$S7JS_CREDS"; . "$ACME_CREDS"; set +a
AUD="user"

login() { # login <email> <password> <jarfile>
  local email="$1" pw="$2" jar="$3"
  curl -s -c "$jar" -o "$jar.login.json" -w "%{http_code}" -X POST "$BASE/api/auth/v1/login" \
    -H "Content-Type: application/json" -H "X-Auth-Audience: $AUD" \
    -d "{\"email\":\"$email\",\"password\":\"$pw\"}"
}
csrf() { python3 -c "import json,sys;print(json.load(open(sys.argv[1]))[\"data\"][\"csrf_token\"])" "$1" 2>/dev/null; }

# ---------- 夹具基线（DB） ----------
ORG="$S7JS_ORG_ID"; WSA="$S7JS_WS_S7JS_A"; WSB="$S7JS_WS_S7JS_B"
q "SELECT id::text FROM download_jobs WHERE organization_id='$ORG' AND workspace_id='$WSA' ORDER BY created_at DESC, id" > "$EV/db-wsA-ids.txt"
q "SELECT id::text FROM download_jobs WHERE organization_id='$ORG' AND workspace_id='$WSB' ORDER BY created_at DESC, id" > "$EV/db-wsB-ids.txt"
q "SELECT id::text FROM download_jobs WHERE organization_id='$ORG' ORDER BY created_at DESC, id" > "$EV/db-org-ids.txt"
q "SELECT id::text FROM download_jobs WHERE organization_id='627c131a-3842-4e19-bade-d20ffb8aea8f' ORDER BY created_at DESC, id" > "$EV/db-acme-ids.txt"
echo "db wsA=$(wc -l < "$EV/db-wsA-ids.txt") wsB=$(wc -l < "$EV/db-wsB-ids.txt") org=$(wc -l < "$EV/db-org-ids.txt") acme=$(wc -l < "$EV/db-acme-ids.txt")" | tee -a "$EV/run.log"

# ---------- 登录 ----------
C1=$(login "$S7JS_INVESTIGATOR_EMAIL" "$S7JS_INVESTIGATOR_PW" "$EV/s7js.jar")
C2=$(login "$INVESTIGATOR_EMAIL" "$INVESTIGATOR_PW" "$EV/acme.jar")
echo "login s7js=$C1 acme=$C2" | tee -a "$EV/run.log"
[ "$C1" = "200" ] || { bad "R0 s7js login http=$C1"; }
[ "$C2" = "200" ] || { bad "R0 acme login http=$C2"; }

api_ids() { # api_ids <jar> <query> <outfile>
  local jar="$1" query="$2" out="$3"
  curl -s -b "$jar" -H "X-Auth-Audience: $AUD" -o "$out.json" -w "%{http_code}" "$BASE/api/v1/download-jobs$query" > "$out.code"
  python3 - "$out.json" "$out.ids" <<PY 2>/dev/null
import json,sys
try:
    d=json.load(open(sys.argv[1]))
except Exception:
    open(sys.argv[2],"w").write(""); sys.exit(2)
jobs = d.get("jobs")
if jobs is None:
    open(sys.argv[2],"w").write(""); print("NO_JOBS_KEY", file=sys.stderr); sys.exit(3)
if not isinstance(jobs, list):
    open(sys.argv[2],"w").write(""); sys.exit(4)
open(sys.argv[2],"w").write("".join((j.get("id","")+"\n") for j in jobs))
sys.exit(0)
PY
  echo "$?"
}

# ---------- R1 同组织不同工作区 ----------
RC=$(api_ids "$EV/s7js.jar" "?limit=200" "$EV/r1")
if [ "$RC" != "0" ]; then bad "R1 api parse rc=$RC http=$(cat "$EV/r1.code")"; else
  if [ "$(cat "$EV/r1.code")" != "200" ]; then bad "R1 http=$(cat "$EV/r1.code") expected 200";
  else
    python3 - "$EV/r1.ids" "$EV/db-wsA-ids.txt" "$EV/db-wsB-ids.txt" <<PY | tee -a "$EV/run.log"
import sys
api=[l.strip() for l in open(sys.argv[1]) if l.strip()]
a=[l.strip() for l in open(sys.argv[2]) if l.strip()]
b=[l.strip() for l in open(sys.argv[3]) if l.strip()]
print("R1 api=%d db_wsA=%d db_wsB=%d"%(len(api),len(a),len(b)))
print("R1 identical_to_wsA=%s"%(set(api)==set(a)))
print("R1 leaked_wsB=%s"%(sorted(set(api)&set(b))))
sys.exit(0 if set(api)==set(a) else 1)
PY
    [ "${PIPESTATUS[0]}" = "0" ] && ok "R1 same-org different-workspace exact ID set == wsA (no wsB leak)" || bad "R1 scope mismatch"
  fi
fi

# ---------- R2 跨组织隔离 ----------
RC=$(api_ids "$EV/s7js.jar" "?limit=200" "$EV/r2s7")
RC2=$(api_ids "$EV/acme.jar" "?limit=200" "$EV/r2acme")
python3 - "$EV/r2s7.ids" "$EV/r2acme.ids" "$EV/db-acme-ids.txt" "$EV/db-org-ids.txt" <<PY | tee -a "$EV/run.log"
import sys
s=[l.strip() for l in open(sys.argv[1]) if l.strip()]
ac=[l.strip() for l in open(sys.argv[2]) if l.strip()]
dbac=set(l.strip() for l in open(sys.argv[3]) if l.strip())
dborg=set(l.strip() for l in open(sys.argv[4]) if l.strip())
print("R2 s7js_session=%d acme_session=%d db_acme=%d"%(len(s),len(ac),len(dbac)))
print("R2 s7js_leaks_acme=%s"%(sorted(set(s)&dbac)))
print("R2 acme_leaks_s7js=%s"%(sorted(set(ac)&dborg)))
print("R2 acme_is_subset_of_db_acme=%s"%(set(ac)<=dbac))
bad = (set(s)&dbac) or (set(ac)&dborg) or not (set(ac)<=dbac)
sys.exit(1 if bad else 0)
PY
[ "${PIPESTATUS[0]}" = "0" ] && ok "R2 cross-org isolation both directions" || bad "R2 cross-org leak"

# ---------- R3 组织级（空工作区） ----------
nv "R3 HTTP org-level empty-workspace session: 无产品路径（密码登录的 workspace 由 iam.defaultWorkspace 取该组织 slug 最小者，永不空）；伪造 session 行被拒绝。store 层真实代码+真实 DB 证据见 go test -tags live ./internal/store（org-level 5 jobs / overlap 0）"

# ---------- R4 请求可控输入不得改变作用域 ----------
curl -s -b "$EV/s7js.jar" -H "X-Auth-Audience: $AUD" -H "X-Org-Id: 627c131a-3842-4e19-bade-d20ffb8aea8f" \
  -H "X-Workspace-Id: $WSB" -H "X-User-Id: 00000000-0000-0000-0000-0000000000aa" -H "X-User-Role: owner" \
  -o "$EV/r4.json" -w "%{http_code}" "$BASE/api/v1/download-jobs?limit=200" > "$EV/r4.code"
python3 - "$EV/r4.json" "$EV/db-wsA-ids.txt" <<PY | tee -a "$EV/run.log"
import json,sys
d=json.load(open(sys.argv[1])); api=set(j["id"] for j in d.get("jobs") or [])
a=set(l.strip() for l in open(sys.argv[2]) if l.strip())
print("R4 header_spoof api=%d unchanged_vs_wsA=%s"%(len(api), api==a))
sys.exit(0 if api==a else 1)
PY
[ "${PIPESTATUS[0]}" = "0" ] && ok "R4 header-supplied org/workspace/user did not change scope" || bad "R4 scope changed by headers"

# ---------- R5 status/limit/offset ----------
python3 - "$BASE" "$EV" "$WSB" <<PY | tee -a "$EV/run.log"
import json,subprocess,sys,urllib.request
base,ev,wsb=sys.argv[1],sys.argv[2],sys.argv[3]
import http.cookiejar as cj
jar=cj.MozillaCookieJar(ev+"/s7js.jar"); jar.load(ignore_discard=True, ignore_expires=True)
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def get(q):
    r=op.open(urllib.request.Request(base+"/api/v1/download-jobs"+q, headers={"X-Auth-Audience":"user"}))
    d=json.loads(r.read().decode())
    return r.status,[j["id"] for j in d.get("jobs") or []]
def db(sql):
    out=subprocess.run(["docker","exec","deploy-postgres-1","psql","-U","blockchain","-d","blockchain_platform","-tAc",sql],capture_output=True,text=True)
    return [l.strip() for l in out.stdout.splitlines() if l.strip()]
org=open(ev+"/db-org-ids.txt").read().split()
wsa=open(ev+"/db-wsA-ids.txt").read().split()
okc=0; badc=0
checks=[]
# status filter
s,ids=get("?limit=200&status=CANCELLED"); checks.append(("status=CANCELLED", set(ids)==set(wsa)))
s,ids=get("?limit=200&status=RUNNING");   checks.append(("status=RUNNING(empty)", ids==[]))
# limit / offset against DB order (created_at DESC)
for lim,off in [(1,0),(2,0),(2,1),(1,2),(3,0)]:
    s,ids=get("?limit=%d&offset=%d"%(lim,off))
    checks.append(("limit=%d offset=%d"%(lim,off), ids==wsa[off:off+lim]))
# per-workspace API for the second workspace must be empty for this session scope
s,ids=get("?limit=200"); checks.append(("session ws set", set(ids)==set(wsa)))
for name,good in checks:
    print("R5 %-24s %s"%(name,"OK" if good else "MISMATCH"))
    okc+=1 if good else 0; badc+=0 if good else 1
sys.exit(1 if badc else 0)
PY
[ "${PIPESTATUS[0]}" = "0" ] && ok "R5 status/limit/offset all match DB order" || bad "R5 pagination/status mismatch"

# ---------- R6 空结果必须是 [] 而不是 null ----------
curl -s -b "$EV/s7js.jar" -H "X-Auth-Audience: $AUD" -o "$EV/r6.json" -w "%{http_code}" "$BASE/api/v1/download-jobs?limit=5&status=RUNNING" > "$EV/r6.code"
python3 - "$EV/r6.json" <<PY | tee -a "$EV/run.log"
import json,sys
d=json.load(open(sys.argv[1]))
j=d.get("jobs","MISSING")
print("R6 jobs_type=%s is_empty_list=%s"%(type(j).__name__, j==[]))
sys.exit(0 if j==[] else 1)
PY
[ "${PIPESTATUS[0]}" = "0" ] && ok "R6 empty result is [] (not null)" || bad "R6 empty result not []"

# ---------- trace 契约：401/403 正文 meta 与响应头一致 ----------
curl -s -D "$EV/trace.hdr" -o "$EV/trace.json" "$BASE/api/v1/download-jobs?limit=1"
python3 - "$EV/trace.json" "$EV/trace.hdr" <<PY | tee -a "$EV/run.log"
import json,sys,re
b=json.load(open(sys.argv[1])); h=open(sys.argv[2]).read()
meta=b.get("meta") or {}
hr=re.search(r"(?im)^X-Request-Id:\s*(\S+)",h); ht=re.search(r"(?im)^X-Trace-Id:\s*(\S+)",h)
br=meta.get("request_id",""); bt=meta.get("trace_id","")
print("trace body_request_id_set=%s header_request_id_set=%s match=%s"%(bool(br),bool(hr),bool(hr) and br==hr.group(1)))
print("trace body_trace_id_set=%s header_trace_id_set=%s match=%s"%(bool(bt),bool(ht),bool(ht) and bt==ht.group(1)))
sys.exit(0 if (br and bt and hr and ht and br==hr.group(1) and bt==ht.group(1)) else 1)
PY
[ "${PIPESTATUS[0]}" = "0" ] && ok "trace meta.request_id/trace_id non-empty and equal to headers (401)" || bad "trace contract mismatch on 401"

# ---------- R8 旧 500 证据保留 ----------
OLD="$PROJECT/acceptance-evidence/dual-system-v1/sprint7/r3-jobs500-handover-20260912T232500Z"
if [ -d "$OLD" ] && [ -s "$OLD/HANDOVER.md" ]; then ok "R8 preserved original jobs500 evidence present: $OLD"; else bad "R8 original 500 evidence missing"; fi

echo "=== SUMMARY PASS=$PASS FAIL=$FAIL NOT_VERIFIED=$NV ===" | tee -a "$EV/run.log"
printf '{"run":"%s","pass":%d,"fail":%d,"not_verified":%d,"base":"%s","evidence":"%s"}\n' \
  "$TS" "$PASS" "$FAIL" "$NV" "$BASE" "$EV" > "$EV/result.json"
cat "$EV/result.json"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
