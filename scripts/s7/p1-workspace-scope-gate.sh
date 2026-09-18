#!/usr/bin/env bash
# P1 工作区作用域闸门（P1-20260913）——HTTP + DB + 审计真实核验（浏览器见 playwright p1 配置）。
# 全部走真实 API（curl + 真实 cookie jar + 真实 CSRF）；断言 DB 与审计真实行。
# 唯一的写操作：本轮**专用夹具**的成员资格 removed/active 切换 + 错误作用域夹具会话撤销（保留审计）。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=${P1_EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p1-workspace-scope-gate-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
EXPECT_API_SHA="${P1_EXPECT_API_SHA:-4ec819add8efa61f7d5f25e8d499ce9bef712f17a1c36293ee08f458fb0e3978}"
BASE=https://127.0.0.1:8448
JARS=$(mktemp -d /tmp/p1gate-jars-XXXXXX); chmod 700 "$JARS"
trap 'rm -rf "$JARS"' EXIT
exec > >(tee "$EV/gate-console.log") 2>&1
PASS=0; FAIL=0; CHECKS="$EV/checks.jsonl"; : > "$CHECKS"
jq1(){ python3 -c 'import json,sys;print(json.dumps(sys.argv[1],ensure_ascii=False))' "$1"; }
ok(){ PASS=$((PASS+1)); printf '{"id":"%s","result":"PASS","detail":%s}\n' "$1" "$(jq1 "$2")" >> "$CHECKS"; echo "PASS[$PASS] $1 :: $2"; }
bad(){ FAIL=$((FAIL+1)); printf '{"id":"%s","result":"FAIL","detail":%s}\n' "$1" "$(jq1 "$2")" >> "$CHECKS"; echo "FAIL[$FAIL] $1 :: $2"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3" "$4"; else bad "$3" "$4 (have=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV expect_api_sha=$EXPECT_API_SHA"
APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
API_SHA=$(sha256sum /proc/$APIPID/exe | cut -d' ' -f1)
echo "api_pid=$APIPID api_sha=$API_SHA"
chk "$API_SHA" "$EXPECT_API_SHA" "G1 running api sha equals the P1-fix binary" "pid=$APIPID"

WS_A=8b41836b-6d53-45e4-9a8a-3248a6e24e28
WS_B=62a87634-da99-40b9-a08e-9edbd339551c
JOB_A=554d50b9-d7a3-41af-a393-732370c25c45
EXPORT_A=$(printf "SELECT id::text FROM exports WHERE job_id='%s' AND workspace_id='%s' ORDER BY created_at LIMIT 1;\n" "$JOB_A" "$WS_A" | pgq)
WS_NOMEM=$(printf "SELECT id::text FROM workspaces WHERE slug='p1-nomem-ws';\n" | pgq)
LEN=$(printf %s "$EXPORT_A" | wc -c)
echo "ws_a=$WS_A ws_b=$WS_B ws_nomem=$WS_NOMEM export_a=$EXPORT_A"
chk "$LEN" "36" "G0 wsA 目标导出夹具解析成功" "export_a=$EXPORT_A"

login(){ # email pw jar -> http code；body: $jar.login.json；cookie: $jar.ck
  curl -sS -k -o "$3.login.json" -w '%{http_code}' -c "$3.ck" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -X POST "$BASE/api/auth/v1/login" \
    --data-binary "$(python3 -c 'import json,sys;print(json.dumps({"identifier":sys.argv[1],"password":sys.argv[2],"audience":"user"}))' "$1" "$2")"
}
login_ws(){ # email pw jar wsid -> http code（显式选择）
  curl -sS -k -o "$3.login.json" -w '%{http_code}' -c "$3.ck" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -X POST "$BASE/api/auth/v1/login" \
    --data-binary "$(python3 -c 'import json,sys;print(json.dumps({"identifier":sys.argv[1],"password":sys.argv[2],"audience":"user","workspace_id":sys.argv[3]}))' "$1" "$2" "$4")"
}
sess_ws(){ printf '%s\n' "$(curl -sS -k -b "$1.ck" -H 'X-Auth-Audience: user' "$BASE/api/auth/v1/session" -o "$1.session.json" -w '%{http_code}')"; python3 -c 'import json,sys;d=json.load(open(sys.argv[1])).get("data") or {};print(d.get("workspace_id") or "")' "$1.session.json"; }
get(){ curl -sS -k -b "$1.ck" -H 'X-Auth-Audience: user' -o "$2" -w '%{http_code}' "$3"; }
csrf(){ python3 -c 'import json,sys;print((json.load(open(sys.argv[1])).get("data") or {}).get("csrf_token",""))' "$1.login.json"; }
post_create(){ curl -sS -k -b "$1.ck" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $(csrf "$1")" -H "Idempotency-Key: p1gate-$(date -u +%s%N)" \
    -X POST "$BASE/api/v2/exports" --data-binary "{\"job_id\":\"$2\",\"format\":\"xlsx\"}" -o "$3" -w '%{http_code}'; }
jcode(){ python3 -c 'import json,sys
d=json.load(open(sys.argv[1]));print(((d.get("error") or {}) or {}).get("code") or "")' "$1"; }
jitems(){ python3 -c 'import json,sys
d=json.load(open(sys.argv[1]));data=d.get("data") or {};items=data.get("items")
print(len(items) if isinstance(items,list) else -1)' "$1"; }
jhas(){ python3 -c 'import json,sys
d=json.load(open(sys.argv[1]));data=d.get("data") or {};items=data.get("items") or []
print(sum(1 for i in items if str(i.get("id"))==sys.argv[2]))' "$1" "$2"; }
jlen(){ python3 -c 'import json,sys
d=json.load(open(sys.argv[1]));err=d.get("error") or {};det=err.get("details") or {};ws=det.get("workspaces")
print(len(ws) if isinstance(ws,list) else -1)' "$1"; }
jcontract(){ python3 -c 'import json,sys
d=json.load(open(sys.argv[1]));v=d.get(sys.argv[2])
print("null" if v is None else ("array" if isinstance(v,list) else "other"))' "$1" "$2"; }

CV=/home/euripides/.p1gate-view-b.env; CI=/home/euripides/.p1gate-inv-b.env; CA=/home/euripides/.p1gate-inv-a.env
CM=/home/euripides/.p1gate-multi.env; CN=/home/euripides/.p1gate-nomem.env
for f in "$CV" "$CI" "$CA" "$CM" "$CN"; do [ -s "$f" ] || { echo "MISSING_FIXTURE_CREDS $f"; exit 3; }; done
VE=$(awk -F= '/_EMAIL=/{print $2}' "$CV"); VP=$(awk -F= '/_PW=/{print $2}' "$CV")
IE=$(awk -F= '/_EMAIL=/{print $2}' "$CI"); IP=$(awk -F= '/_PW=/{print $2}' "$CI")
AE=$(awk -F= '/_EMAIL=/{print $2}' "$CA"); AP=$(awk -F= '/_PW=/{print $2}' "$CA")
ME=$(awk -F= '/_EMAIL=/{print $2}' "$CM"); MP=$(awk -F= '/_PW=/{print $2}' "$CM")
NE=$(awk -F= '/_EMAIL=/{print $2}' "$CN"); NP=$(awk -F= '/_PW=/{print $2}' "$CN")
. /home/euripides/.s7jobscope-creds.env
SVE=$S7JS_VIEWER_EMAIL; SVP=$S7JS_VIEWER_PW
echo "fixtures: view_b=$VE inv_b=$IE inv_a=$AE multi=$ME nomem=$NE s7js_view=$SVE"

echo "=== A. wsB 负向：list 0 + wsA 目标不可见 ==="
H=$(login "$VE" "$VP" "$JARS/viewb"); chk "$H" "200" "A1 wsB viewer login 200" "http=$H"
OUT=$(sess_ws "$JARS/viewb"); SW=$(printf '%s' "$OUT" | tail -1); SC=$(printf '%s' "$OUT" | head -1)
chk "$SC" "200" "A2 /auth/v1/session 200" "code=$SC"
chk "$SW" "$WS_B" "A3 会话工作区由成员资格决定（wsB，不再取组织首个工作区）" "session_ws=$SW"
H=$(get "$JARS/viewb" "$EV/a4-list-viewb.json" "$BASE/api/v2/exports?limit=50"); chk "$H" "200" "A4 导出列表 200" "http=$H"
N=$(jitems "$EV/a4-list-viewb.json"); chk "$N" "0" "A5 wsB viewer 列表 0 项（作用域闸）" "items=$N"
H=$(get "$JARS/viewb" "$EV/a6-detail-wsa.json" "$BASE/api/v2/exports/$EXPORT_A"); chk "$H" "404" "A6 读 wsA 导出明细 404（跨 scope 不可见）" "http=$H target=$EXPORT_A"
H=$(get "$JARS/viewb" "$EV/a7-ticket-wsa.json" "$BASE/api/v2/exports/$EXPORT_A/download-ticket"); chk "$H" "403" "A7 对 wsA 导出取票据 403（权限闸先于作用域）" "http=$H"
H=$(post_create "$JARS/viewb" "$JOB_A" "$EV/a8-create-wsa-job.json"); C=$(jcode "$EV/a8-create-wsa-job.json")
chk "$H|$C" "403|permission_denied" "A8 用 wsA job 建导出 403（权限闸：无 export:create）" "http=$H code=$C"

echo "=== B. wsB 且有 export:create：作用域闸单独成立 ==="
H=$(login "$IE" "$IP" "$JARS/invb"); chk "$H" "200" "B1 wsB investigator login 200" "http=$H"
OUT=$(sess_ws "$JARS/invb"); SW=$(printf '%s' "$OUT" | tail -1)
chk "$SW" "$WS_B" "B2 会话工作区=wsB" "session_ws=$SW"
H=$(get "$JARS/invb" "$EV/b3-list-invb.json" "$BASE/api/v2/exports?limit=50"); N=$(jitems "$EV/b3-list-invb.json")
chk "$H|$N" "200|0" "B3 列表 0 项" "http=$H items=$N"
H=$(post_create "$JARS/invb" "$JOB_A" "$EV/b4-create-wsa-job.json"); C=$(jcode "$EV/b4-create-wsa-job.json")
chk "$H" "404" "B4 用 wsA job 建导出 404（作用域闸，非权限闸）" "http=$H code=$C"

echo "=== C. wsA 正向可见 ==="
H=$(login "$AE" "$AP" "$JARS/inva"); chk "$H" "200" "C1 wsA investigator login 200" "http=$H"
OUT=$(sess_ws "$JARS/inva"); SW=$(printf '%s' "$OUT" | tail -1)
chk "$SW" "$WS_A" "C2 会话工作区=wsA" "session_ws=$SW"
H=$(get "$JARS/inva" "$EV/c3-list-inva.json" "$BASE/api/v2/exports?limit=50"); N=$(jitems "$EV/c3-list-inva.json"); K=$(jhas "$EV/c3-list-inva.json" "$EXPORT_A")
chk "$H" "200" "C3 列表 200" "http=$H items=$N"
if [ "$N" -ge 1 ] && [ "$K" -ge 1 ]; then ok "C4 wsA 目标对 wsA 身份可见（正向对照）" "items=$N target_present=$K"; else bad "C4 wsA 目标对 wsA 身份可见" "items=$N target_present=$K"; fi
H=$(get "$JARS/inva" "$EV/c5-detail-wsa.json" "$BASE/api/v2/exports/$EXPORT_A"); chk "$H" "200" "C5 读 wsA 导出明细 200" "http=$H"

echo "=== D. 事故复现夹具回归（s7js-view 仅属 wsB） ==="
H=$(login "$SVE" "$SVP" "$JARS/s7view"); chk "$H" "200" "D1 s7js-view login 200" "http=$H"
OUT=$(sess_ws "$JARS/s7view"); SW=$(printf '%s' "$OUT" | tail -1)
chk "$SW" "$WS_B" "D2 原事故身份会话工作区=wsB（事故路径已修复）" "session_ws=$SW"
H=$(get "$JARS/s7view" "$EV/d3-list.json" "$BASE/api/v2/exports?limit=50"); N=$(jitems "$EV/d3-list.json")
chk "$H|$N" "200|0" "D3 列表 0 项（v3 轮曾观测到 wsA 的 5 条）" "http=$H items=$N"
H=$(get "$JARS/s7view" "$EV/d4-detail-wsa.json" "$BASE/api/v2/exports/$EXPORT_A"); chk "$H" "404" "D4 读 wsA 导出明细 404（v3 轮曾 200）" "http=$H"

echo "=== E. 零成员资格 fail closed ==="
BEFORE=$(printf "SELECT count(*) FROM sessions WHERE identity_id=(SELECT id FROM identities WHERE email='%s');\n" "$NE" | pgq)
H=$(login "$NE" "$NP" "$JARS/nomem"); C=$(jcode "$JARS/nomem.login.json")
chk "$H" "403" "E1 零成员资格登录被拒绝（不回退组织默认工作区）" "http=$H code=$C"
chk "$C" "no_workspace_membership" "E2 错误码 no_workspace_membership" "code=$C"
AFTER=$(printf "SELECT count(*) FROM sessions WHERE identity_id=(SELECT id FROM identities WHERE email='%s');\n" "$NE" | pgq)
chk "$BEFORE|$AFTER" "0|0" "E3 拒绝时不签发会话" "before=$BEFORE after=$AFTER"
NC=$(printf "SELECT count(*) FROM login_events WHERE reason_code='no_workspace_membership' AND created_at > now() - interval '10 minutes';\n" | pgq)
if [ "$NC" -ge 1 ]; then ok "E4 拒绝写入安全审计（login_events）" "rows=$NC"; else bad "E4 拒绝写入安全审计" "rows=$NC"; fi

echo "=== F. 多成员资格 ==="
H=$(login "$ME" "$MP" "$JARS/multi0"); C=$(jcode "$JARS/multi0.login.json"); NO=$(jlen "$JARS/multi0.login.json")
chk "$H" "409" "F1 未选择工作区被拒绝（不得任取首行/组织默认）" "http=$H code=$C"
chk "$C" "workspace_selection_required" "F2 错误码 workspace_selection_required" "code=$C"
chk "$NO" "2" "F3 响应携带可选工作区列表（选择器契约）" "options=$NO"
H=$(login_ws "$ME" "$MP" "$JARS/multiB" "$WS_B"); chk "$H" "200" "F4 显式选择 wsB 后登录成功" "http=$H"
OUT=$(sess_ws "$JARS/multiB"); SW=$(printf '%s' "$OUT" | tail -1)
chk "$SW" "$WS_B" "F5 显式选择被真实绑定到会话" "session_ws=$SW"
H=$(login_ws "$ME" "$MP" "$JARS/multiX" "$WS_NOMEM"); C=$(jcode "$JARS/multiX.login.json")
chk "$H|$C" "403|workspace_not_member" "F6 选择非成员工作区被拒绝" "http=$H code=$C"

echo "=== G. 撤权 fail closed ==="
H=$(login "$IE" "$IP" "$JARS/rev"); chk "$H" "200" "G1 撤权前登录 200" "http=$H"
H=$(get "$JARS/rev" "$EV/g2-pre-list.json" "$BASE/api/v2/exports?limit=50"); chk "$H" "200" "G2 撤权前列表 200" "http=$H"
pgq > "$EV/g3-revoke.txt" <<SQL
UPDATE workspace_memberships wm SET status='removed', updated_at=now()
FROM identities i, workspaces w
WHERE wm.identity_id=i.id AND wm.workspace_id=w.id AND i.email='$IE' AND w.slug='s7js-b' AND wm.status='active';
SQL
cat "$EV/g3-revoke.txt"
H=$(get "$JARS/rev" "$EV/g4-post-list.json" "$BASE/api/v2/exports?limit=50"); C=$(jcode "$EV/g4-post-list.json")
chk "$H" "401" "G3 撤权后同一会话立即被拒绝（fail closed）" "http=$H code=$C"
chk "$C" "AUTHZ_WORKSPACE_ACCESS_REVOKED" "G4 显式撤销错误码（不静默降级为未登录）" "code=$C"
RR=$(printf "SELECT coalesce(revoked_reason,'') FROM sessions s JOIN identities i ON i.id=s.identity_id WHERE i.email='%s' AND s.workspace_id='%s' ORDER BY s.created_at DESC LIMIT 1;\n" "$IE" "$WS_B" | pgq)
chk "$RR" "workspace_membership_revoked" "G5 会话已撤销并写入 revoked_reason" "revoked_reason=$RR"
LE=$(printf "SELECT count(*) FROM login_events WHERE reason_code='workspace_access_revoked' AND created_at > now() - interval '10 minutes';\n" | pgq)
if [ "$LE" -ge 1 ]; then ok "G6 撤权写入登录/安全事件" "login_events=$LE"; else bad "G6 撤权写入登录/安全事件" "login_events=$LE"; fi
AU=$(printf "SELECT count(*) FROM audit_events_v2 WHERE action='auth.workspace.access_revoked' AND occurred_at > now() - interval '10 minutes';\n" | pgq)
if [ "$AU" -ge 1 ]; then ok "G7 撤权写入 audit_events_v2（不可抵赖审计）" "rows=$AU"; else bad "G7 撤权写入 audit_events_v2" "rows=$AU"; fi
pgq > "$EV/g8-restore.txt" <<SQL
UPDATE workspace_memberships wm SET status='active', updated_at=now()
FROM identities i, workspaces w
WHERE wm.identity_id=i.id AND wm.workspace_id=w.id AND i.email='$IE' AND w.slug='s7js-b' AND wm.status='removed';
SQL
cat "$EV/g8-restore.txt"
H=$(login "$IE" "$IP" "$JARS/rev2"); chk "$H" "200" "G8 恢复成员资格后重新登录成功（夹具复原）" "http=$H"

echo "=== H. nil slice 契约（D-R10-01 契约层） ==="
H=$(curl -sS -k -b "$JARS/viewb.ck" -H 'X-Auth-Audience: user' -o "$EV/h1-cases.json" -w '%{http_code}' "$BASE/api/v1/cases")
CJ=$(jcontract "$EV/h1-cases.json" cases)
H2=$(curl -sS -k -b "$JARS/viewb.ck" -H 'X-Auth-Audience: user' -o "$EV/h2-tags.json" -w '%{http_code}' "$BASE/api/v1/tags")
TJ=$(jcontract "$EV/h2-tags.json" tags)
chk "$H|$CJ" "200|array" "H1 空工作区 /api/v1/cases 返回 [] 而非 null" "http=$H cases=$CJ"
chk "$H2|$TJ" "200|array" "H2 空工作区 /api/v1/tags 返回 [] 而非 null" "http=$H2 tags=$TJ"

echo "=== I. 错误作用域夹具会话清理（保留审计） ==="
pgq > "$EV/i1-wrong-scope-sessions.txt" <<'SQL'
SELECT i.email, s.id, s.workspace_id::text, s.created_at
FROM sessions s JOIN identities i ON i.id=s.identity_id
WHERE i.email='s7js-view@fixture.local' AND s.revoked_at IS NULL
  AND s.workspace_id NOT IN (SELECT wm.workspace_id FROM workspace_memberships wm WHERE wm.identity_id=i.id AND wm.status='active');
SQL
cat "$EV/i1-wrong-scope-sessions.txt"
CNT=$(grep -c . "$EV/i1-wrong-scope-sessions.txt" || true)
pgq > "$EV/i2-revoked.txt" <<'SQL'
UPDATE sessions s SET revoked_at=now(), revoked_reason='workspace_membership_revoked'
FROM identities i
WHERE s.identity_id=i.id AND i.email='s7js-view@fixture.local' AND s.revoked_at IS NULL
  AND s.workspace_id NOT IN (SELECT wm.workspace_id FROM workspace_memberships wm WHERE wm.identity_id=i.id AND wm.status='active')
RETURNING s.id;
SQL
cat "$EV/i2-revoked.txt"
ok "I1 仅撤销 wsB 夹具身份上的错误作用域会话（保留行与审计，不全局清会话）" "candidates=$CNT"
TOT=$(printf "SELECT count(*) FROM sessions WHERE revoked_at IS NULL;\n" | pgq)
ok "I2 未做全局会话清理" "active_sessions=$TOT"

python3 - "$EV" "$PASS" "$FAIL" <<'PY'
import json,sys,os,subprocess
ev,p,f=sys.argv[1],int(sys.argv[2]),int(sys.argv[3])
checks=[json.loads(l) for l in open(os.path.join(ev,"checks.jsonl"),encoding="utf-8") if l.strip()]
pid=subprocess.check_output(["systemctl","--user","show","-p","MainPID","--value","blockchain-api"],text=True).strip()
sha=subprocess.check_output(["sha256sum",f"/proc/{pid}/exe"],text=True).split()[0]
summary={"evidence_dir":ev,"checks_total":len(checks),"pass":p,"fail":f,
         "failed_ids":[c["id"] for c in checks if c["result"]=="FAIL"],
         "finding":"P1-EXPORT-LIST-CROSSSCOPE-20260913",
         "fix":"login/session 工作区只认该身份在该组织的 active workspace_memberships；0 成员 fail closed；多成员需显式 workspace_id 选择；撤权使既有会话 fail closed 并写审计",
         "api_pid":pid,"api_sha256":sha}
open(os.path.join(ev,"p1-gate-summary.json"),"w").write(json.dumps(summary,ensure_ascii=False,indent=1))
print(json.dumps(summary,ensure_ascii=False,indent=1))
PY
chmod 600 "$EV"/*.json "$EV"/*.txt 2>/dev/null
echo "P1_GATE_EV=$EV PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
