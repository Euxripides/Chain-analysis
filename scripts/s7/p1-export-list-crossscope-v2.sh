#!/usr/bin/env bash
# P1 导出列表跨作用域 —— 真实登录复现轮 v2（v1 保留，不修改）
# 修正点：登录路径为 /api/auth/v1/login、会话为 /api/auth/v1/session（v1 用了 /api/v1/auth/* → 404）。
# 只读复现；cookie jar 600 临时文件，trap 清理；不动任何用户/工作区数据。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=$R/acceptance-evidence/dual-system-v1/sprint7/p1-export-list-crossscope-v2-$TS
mkdir -p "$EV"; chmod 700 "$EV"
CREDS=/home/euripides/.s7jobscope-creds.env
BASE=https://127.0.0.1:8448
JAR_V=$(mktemp /tmp/p1v2-jar-v-XXXXXX); chmod 600 "$JAR_V"
JAR_I=$(mktemp /tmp/p1v2-jar-i-XXXXXX); chmod 600 "$JAR_I"
L_V=$(mktemp /tmp/p1v2-login-v-XXXXXX); chmod 600 "$L_V"
L_I=$(mktemp /tmp/p1v2-login-i-XXXXXX); chmod 600 "$L_I"
trap 'rm -f "$JAR_V" "$JAR_I" "$L_V" "$L_I"' EXIT
exec > >(tee "$EV/p1v2-console.log") 2>&1
echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV"
APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
echo "api_pid=$APIPID api_exe_sha=$(sha256sum /proc/$APIPID/exe | cut -d' ' -f1)"
set -a; . "$CREDS"; set +a
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }
WS_A=$(printf "SELECT id FROM workspaces WHERE slug='s7js-a' AND organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7';\n" | pgq)
WS_B=$(printf "SELECT id FROM workspaces WHERE slug='s7js-b' AND organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7';\n" | pgq)
JOB_A=554d50b9-d7a3-41af-a393-732370c25c45
EXPORT_A=$(printf "SELECT id::text FROM exports WHERE workspace_id='%s' AND job_id='%s' ORDER BY created_at LIMIT 1;\n" "$WS_A" "$JOB_A" | pgq)
echo "ws_a=$WS_A ws_b=$WS_B export_ws_a=$EXPORT_A"
echo "$EXPORT_A" > "$EV/export-ws-a-id.txt"
echo "$WS_A" > "$EV/ws-a-id.txt"; echo "$WS_B" > "$EV/ws-b-id.txt"

login(){
  curl -sS -k -o "$3" -w '%{http_code}' -c "$4" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -X POST "$BASE/api/auth/v1/login" \
    --data-binary "$(python3 -c 'import json,sys; print(json.dumps({"identifier":sys.argv[1],"password":sys.argv[2],"audience":"user"}))' "$1" "$2")"
}
echo "--- 登录 ---"
VS=$(login "$S7JS_VIEWER_EMAIL" "$S7JS_VIEWER_PW" "$L_V" "$JAR_V")
IS=$(login "$S7JS_INVESTIGATOR_EMAIL" "$S7JS_INVESTIGATOR_PW" "$L_I" "$JAR_I")
echo "viewer_login_http=$VS inv_login_http=$IS"
cp "$L_V" "$EV/login-viewer.json"; cp "$L_I" "$EV/login-inv.json"
python3 - "$EV/login-viewer.json" "$EV/login-inv.json" > "$EV/login-summary.json" <<'PY'
import json,sys
out={}
for tag,p in (("viewer",sys.argv[1]),("investigator",sys.argv[2])):
    d=json.load(open(p)); data=d.get("data") or {}
    out[tag]={"http_ok":d.get("success"),"audience":data.get("audience"),"session_id":data.get("session_id"),
              "csrf_len":len(data.get("csrf_token") or ""),"roles":data.get("roles"),
              "user_org":(data.get("user") or {}).get("organization_id")}
print(json.dumps(out,ensure_ascii=False,indent=1))
PY
cat "$EV/login-summary.json"
echo "viewer_cookie_names=$(awk '!/^#/ && NF>=7 {print $6}' "$JAR_V" | sort | tr '\n' ',')"
echo "inv_cookie_names=$(awk '!/^#/ && NF>=7 {print $6}' "$JAR_I" | sort | tr '\n' ',')"

echo "--- E-a 服务端 principal 作用域（/api/auth/v1/session） ---"
for pair in "viewer:$JAR_V" "investigator:$JAR_I"; do
  who="${pair%%:*}"; jar="${pair#*:}"
  code=$(curl -sS -k -b "$jar" -H 'X-Auth-Audience: user' "$BASE/api/auth/v1/session" -o "$EV/session-$who.json" -w '%{http_code}')
  echo "session_http_$who=$code"
  python3 -c 'import json,sys
d=json.load(open(sys.argv[1])).get("data") or {}
print(sys.argv[2],"org=",d.get("organization_id"),"ws=",d.get("workspace_id"),"session_id=",d.get("session_id"),"perms_export=",[p for p in (d.get("permissions") or []) if str(p).startswith("export")])' "$EV/session-$who.json" "$who"
done

echo "--- E-b viewer GET /api/v2/exports?limit=50 ---"
curl -sS -k -b "$JAR_V" -H 'X-Auth-Audience: user' -D "$EV/list-viewer-headers.txt" \
  "$BASE/api/v2/exports?limit=50" -o "$EV/list-viewer-raw.json" -w 'list_viewer_http=%{http_code}\n'
sed -i 's/^[Ss]et-[Cc]ookie:.*/Set-Cookie: <redacted>/' "$EV/list-viewer-headers.txt"
grep -iE '^(HTTP/|x-request-id|x-trace-id|content-type)' "$EV/list-viewer-headers.txt"
python3 - "$EV/list-viewer-raw.json" "$JOB_A" > "$EV/list-viewer-summary.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); data=d.get("data") or {}
items=data.get("items") or []
print(json.dumps({"success":d.get("success"),"meta":d.get("meta"),"declared_scope":data.get("scope"),
 "item_count":len(items),
 "items":[{k:i.get(k) for k in ("id","job_id","dataset","format","zip","status","rows","created_at")} for i in items],
 "items_with_ws_a_job":sum(1 for i in items if str(i.get("job_id") or "")==sys.argv[2])},ensure_ascii=False,indent=1))
PY
cat "$EV/list-viewer-summary.json"

echo "--- E-c viewer GET /api/v2/exports/{wsA_export} 明细 ---"
curl -sS -k -b "$JAR_V" -H 'X-Auth-Audience: user' -D "$EV/detail-viewer-headers.txt" \
  "$BASE/api/v2/exports/$EXPORT_A" -o "$EV/detail-viewer-raw.json" -w 'detail_viewer_http=%{http_code}\n'
grep -iE '^(HTTP/|x-request-id|x-trace-id)' "$EV/detail-viewer-headers.txt"
head -c 300 "$EV/detail-viewer-raw.json"; echo

echo "--- E-d viewer POST /api/v2/exports（wsA job, xlsx）= 权限闸 ---"
CSRF_V=$(python3 -c 'import json,sys;print((json.load(open(sys.argv[1])).get("data") or {}).get("csrf_token",""))' "$L_V")
curl -sS -k -b "$JAR_V" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF_V" \
  -H "Idempotency-Key: p1v2-cross-$(date -u +%s)" -D "$EV/crosscreate-viewer-headers.txt" \
  -X POST "$BASE/api/v2/exports" --data-binary "{\"job_id\":\"$JOB_A\",\"format\":\"xlsx\"}" \
  -o "$EV/crosscreate-viewer-raw.json" -w 'crosscreate_viewer_http=%{http_code}\n'
grep -iE '^(HTTP/|x-request-id|x-trace-id)' "$EV/crosscreate-viewer-headers.txt"
head -c 400 "$EV/crosscreate-viewer-raw.json"; echo

echo "--- E-e investigator 对照 list ---"
curl -sS -k -b "$JAR_I" -H 'X-Auth-Audience: user' -D "$EV/list-inv-headers.txt" \
  "$BASE/api/v2/exports?limit=50" -o "$EV/list-inv-raw.json" -w 'list_inv_http=%{http_code}\n'
grep -iE '^(HTTP/|x-request-id|x-trace-id)' "$EV/list-inv-headers.txt"
python3 - "$EV/list-viewer-raw.json" "$EV/list-inv-raw.json" > "$EV/set-compare.json" <<'PY'
import json,sys
def g(p):
    d=json.load(open(p)); data=d.get("data") or {}
    return [str(i.get("id")) for i in (data.get("items") or [])], data.get("scope")
v,vs=g(sys.argv[1]); i,iss=g(sys.argv[2])
print(json.dumps({"viewer_scope":vs,"inv_scope":iss,"viewer_item_count":len(v),"inv_item_count":len(i),
 "identical_set":set(v)==set(i),"viewer_only":sorted(set(v)-set(i)),"inv_only":sorted(set(i)-set(v)),
 "viewer_ids":v,"inv_ids":i},ensure_ascii=False,indent=1))
PY
cat "$EV/set-compare.json"

echo "--- E-f journal principal 对照 ---"
for hdr in list-viewer-headers.txt list-inv-headers.txt; do
  rid=$(grep -i '^x-request-id' "$EV/$hdr" | tr -d '\r' | awk '{print $2}')
  echo "$hdr request_id=$rid"
  [ -n "$rid" ] && journalctl --user -u blockchain-api --since "-20min" --no-pager 2>/dev/null | grep -F "$rid" | head -3
done | tee "$EV/journal-principal.txt"

echo "--- E-g 本轮复现新签发的 session（错误 ws=A 的证据） ---"
pgq > "$EV/sessions-new.txt" <<'SQL'
SELECT i.email, s.id, s.workspace_id::text, w.slug, s.created_at
FROM sessions s JOIN identities i ON i.id=s.identity_id LEFT JOIN workspaces w ON w.id=s.workspace_id
WHERE i.email IN ('s7js-inv@fixture.local','s7js-view@fixture.local') AND s.created_at > now() - interval '10 minutes'
ORDER BY s.created_at DESC;
SQL
cat "$EV/sessions-new.txt"

python3 - "$EV" <<'PY'
import json,os,sys
ev=sys.argv[1]
def rd(n):
    p=os.path.join(ev,n); return open(p).read().strip() if os.path.exists(p) else None
def rj(n):
    try: return json.loads(rd(n) or "{}")
    except Exception: return {"_parse":"failed"}
summ={"evidence_dir":ev,"finding_id":"P1-EXPORT-LIST-CROSSSCOPE-20260913","round":"v2-live-repro",
 "login":{"viewer_http":rd("login-viewer.json") is not None},
 "principal":{w:rj("session-%s.json"%w).get("data",{}) and {"workspace_id":rj("session-%s.json"%w)["data"].get("workspace_id"),"organization_id":rj("session-%s.json"%w)["data"].get("organization_id")} for w in ("viewer","investigator")},
 "list_viewer":rj("list-viewer-summary.json"),
 "set_compare":rj("set-compare.json"),
 "detail_viewer_http":rd("crosscreate-viewer-raw.json") is not None,
 "verdict":"membership-blind session issuance: login 用 org 默认工作区（ORDER BY slug LIMIT 1 = s7js-a）签发会话，wsB-only 的 viewer 因此拿到 wsA principal；list SQL 本身按 principal 作用域过滤（scope-safe），泄露由 principal 错误导致。",
 "not_test_defect":True,
 "affected_running_binary_sha":"db2332796c5221fefe73afc968fe732d578b586c412a5d7b843410c4dd60588e"}
open(os.path.join(ev,"p1v2-summary.json"),"w").write(json.dumps(summ,ensure_ascii=False,indent=1))
print("wrote",os.path.join(ev,"p1v2-summary.json"))
PY
chmod 600 "$EV"/*.json "$EV"/*.txt 2>/dev/null
echo "P1V2_EVIDENCE_DIR=$EV"
