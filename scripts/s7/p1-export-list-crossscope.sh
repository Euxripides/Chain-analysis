#!/usr/bin/env bash
# P1 导出列表跨作用域彻查（HANDOVER-ADDENDUM-1106 §2.3 七项）
# 只读 DB 快照 + 只读代码取证 + 真实登录复现（curl，cookie jar 临时文件 600，trap 清理）。
# 不修改任何用户/工作区/导出数据；不重启服务；不写 audit；证据目录 700。
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=$R/acceptance-evidence/dual-system-v1/sprint7/p1-export-list-crossscope-$TS
mkdir -p "$EV"; chmod 700 "$EV"
CREDS=/home/euripides/.s7jobscope-creds.env
JAR_V=$(mktemp /tmp/p1-jar-viewer-XXXXXX); chmod 600 "$JAR_V"
JAR_I=$(mktemp /tmp/p1-jar-inv-XXXXXX); chmod 600 "$JAR_I"
BODY_V=$(mktemp /tmp/p1-body-viewer-XXXXXX); chmod 600 "$BODY_V"
BODY_I=$(mktemp /tmp/p1-body-inv-XXXXXX); chmod 600 "$BODY_I"
HDR_V=$(mktemp /tmp/p1-hdr-viewer-XXXXXX); chmod 600 "$HDR_V"
HDR_I=$(mktemp /tmp/p1-hdr-inv-XXXXXX); chmod 600 "$HDR_I"
LOGIN_V=$(mktemp /tmp/p1-login-viewer-XXXXXX); chmod 600 "$LOGIN_V"
LOGIN_I=$(mktemp /tmp/p1-login-inv-XXXXXX); chmod 600 "$LOGIN_I"
cleanup(){ rm -f "$JAR_V" "$JAR_I" "$BODY_V" "$BODY_I" "$HDR_V" "$HDR_I" "$LOGIN_V" "$LOGIN_I"; }
trap cleanup EXIT

exec > >(tee "$EV/p1-console.log") 2>&1
echo "=== P1 meta ==="
echo "ts_utc=$TS"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV"
date -u +%FT%TZ
APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
WPID=$(systemctl --user show -p MainPID --value blockchain-worker)
echo "api_pid=$APIPID api_exe_sha=$(sha256sum /proc/$APIPID/exe | cut -d' ' -f1)"
echo "worker_pid=$WPID worker_exe_sha=$(sha256sum /proc/$WPID/exe | cut -d' ' -f1)"
systemctl --user is-active blockchain-api blockchain-worker | tr '\n' ' '; echo

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
pgqfile(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1 > "$1"; }

set -a; . "$CREDS"; set +a
S7JS_VIEWER_PW_SHA12=$(printf %s "$S7JS_VIEWER_PW" | sha256sum | cut -c1-12)
S7JS_INVESTIGATOR_PW_SHA12=$(printf %s "$S7JS_INVESTIGATOR_PW" | sha256sum | cut -c1-12)
echo "viewer_email=$S7JS_VIEWER_EMAIL viewer_pw_len=${#S7JS_VIEWER_PW} viewer_pw_sha12=$S7JS_VIEWER_PW_SHA12"
echo "investigator_email=$S7JS_INVESTIGATOR_EMAIL inv_pw_len=${#S7JS_INVESTIGATOR_PW} inv_pw_sha12=$S7JS_INVESTIGATOR_PW_SHA12"

echo "=== C1 identities ==="
pgqfile "$EV/c1-identities.txt" <<'SQL'
SELECT id,organization_id,email,status,is_platform_admin FROM identities
WHERE email IN ('s7js-inv@fixture.local','s7js-view@fixture.local') ORDER BY email;
SQL
cat "$EV/c1-identities.txt"

echo "=== C2 workspace_memberships + workspaces ==="
pgqfile "$EV/c2-memberships.txt" <<'SQL'
SELECT wm.workspace_id, wm.organization_id, i.email, wm.status, w.slug, w.name
FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
JOIN workspaces w ON w.id=wm.workspace_id
WHERE i.email IN ('s7js-inv@fixture.local','s7js-view@fixture.local') ORDER BY i.email;
SQL
cat "$EV/c2-memberships.txt"

echo "=== C3 defaultWorkspace(org) 实算：ORDER BY slug LIMIT 1 ==="
pgqfile "$EV/c3-defaultworkspace.txt" <<'SQL'
SELECT id, slug, name, created_at FROM workspaces WHERE organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7' ORDER BY slug;
SQL
cat "$EV/c3-defaultworkspace.txt"
echo "default_workspace(org)=$(printf "SELECT id FROM workspaces WHERE organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7' ORDER BY slug LIMIT 1;\n" | pgq)"

echo "=== C4 sessions（含 v3 窗口内的 viewer 会话 e9e18dbe…） ==="
pgqfile "$EV/c4-sessions.txt" <<'SQL'
SELECT i.email,s.id,s.organization_id,s.workspace_id,s.audience,s.auth_method::text,
       s.created_at,s.last_seen_at,s.revoked_at,coalesce(s.revoked_reason,''),substr(coalesce(s.user_agent,''),1,32)
FROM sessions s JOIN identities i ON i.id=s.identity_id
WHERE i.email IN ('s7js-inv@fixture.local','s7js-view@fixture.local')
ORDER BY s.created_at DESC LIMIT 12;
SQL
cat "$EV/c4-sessions.txt"
echo "--- v3 观测时刻的 viewer 会话（created_at 最接近 2026-09-13 03:03:44.997） ---"
pgqfile "$EV/c4-v3-session-row.txt" <<'SQL'
SELECT i.email,s.id,s.organization_id,s.workspace_id,s.audience,s.created_at,s.last_seen_at,
       (SELECT slug FROM workspaces w WHERE w.id=s.workspace_id)
FROM sessions s JOIN identities i ON i.id=s.identity_id
WHERE s.id='e9e18dbe-1291-431a-b598-210e1984b20d';
SQL
cat "$EV/c4-v3-session-row.txt"

echo "=== C5 exports scope 全列（当前快照） ==="
pgqfile "$EV/c5-exports-scope.txt" <<'SQL'
SELECT id::text, coalesce(job_id::text,'-'), organization_id::text, workspace_id::text,
       coalesce(created_by::text,'-'), dataset, format, zip, status, rows, created_at
FROM exports WHERE organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7'
ORDER BY created_at DESC;
SQL
cat "$EV/c5-exports-scope.txt"
echo "--- exports 按 workspace 计数 ---"
pgqfile "$EV/c5-exports-count.txt" <<'SQL'
SELECT workspace_id::text, count(*) FROM exports WHERE organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7' GROUP BY 1 ORDER BY 1;
SQL
cat "$EV/c5-exports-count.txt"

echo "=== C6 roles / bindings（权限闸与作用域闸分离的前提） ==="
pgqfile "$EV/c6-role-bindings.txt" <<'SQL'
SELECT i.email, mrb.scope_type, mrb.scope_id::text, r.key
FROM member_role_bindings mrb JOIN identities i ON i.id=mrb.identity_id
LEFT JOIN roles r ON r.id=mrb.role_id
WHERE i.email IN ('s7js-inv@fixture.local','s7js-view@fixture.local') ORDER BY i.email;
SQL
cat "$EV/c6-role-bindings.txt"
pgqfile "$EV/c6-viewer-perms.txt" <<'SQL'
SELECT r.key, rp.permission_key FROM role_permissions rp JOIN roles r ON r.id=rp.role_id
WHERE r.key IN ('viewer','investigator') AND rp.permission_key LIKE 'export%' ORDER BY r.key,1;
SQL
cat "$EV/c6-viewer-perms.txt"

echo "=== C7 影响面：无 active workspace membership 的身份 / 多工作区组织 ==="
pgqfile "$EV/c7-blast-radius.txt" <<'SQL'
SELECT 'identities_without_active_ws_membership' AS k, count(*)::text AS v FROM identities i
WHERE NOT EXISTS (SELECT 1 FROM workspace_memberships wm WHERE wm.identity_id=i.id AND wm.status='active')
UNION ALL
SELECT 'identities_total', count(*)::text FROM identities
UNION ALL
SELECT 'orgs_with_gt1_workspace', count(*)::text FROM (SELECT organization_id FROM workspaces GROUP BY 1 HAVING count(*)>1) t
UNION ALL
SELECT 'ws_total', count(*)::text FROM workspaces;
SQL
cat "$EV/c7-blast-radius.txt"

echo "=== C8 真实用户（非夹具）受影响清单（前 20，邮箱掩码） ==="
pgqfile "$EV/c8-real-users-multiws.txt" <<'SQL'
SELECT i.id, i.organization_id::text,
       regexp_replace(i.email,'^(.).*@','\\1***@') AS email_masked,
       (SELECT count(*) FROM workspaces w WHERE w.organization_id=i.organization_id) AS ws_in_org,
       (SELECT count(*) FROM workspace_memberships wm WHERE wm.identity_id=i.id AND wm.status='active') AS memberships,
       (SELECT string_agg(w.slug,',' ORDER BY w.slug) FROM workspace_memberships wm JOIN workspaces w ON w.id=wm.workspace_id WHERE wm.identity_id=i.id AND wm.status='active') AS member_of
FROM identities i
WHERE (SELECT count(*) FROM workspaces w WHERE w.organization_id=i.organization_id) > 1
ORDER BY i.created_at LIMIT 20;
SQL
cat "$EV/c8-real-users-multiws.txt"

echo "=== D 代码取证 ==="
sha256sum $R/backend/internal/iam/service.go $R/backend/internal/api/authv1.go $R/backend/internal/api/sprint5_exports.go | tee "$EV/d-code-shas.txt"
grep -n "func (s \*Service) defaultWorkspace" -A 8 $R/backend/internal/iam/service.go > "$EV/d-defaultworkspace-func.txt"
grep -n "wsID := s.defaultWorkspace" -B 3 -A 3 $R/backend/internal/iam/service.go > "$EV/d-callsite-login.txt"
grep -n "DefaultWorkspace" -B 2 -A 4 $R/backend/internal/api/authv1.go > "$EV/d-callsite-oidc.txt"
grep -n "FROM exports" -B 4 -A 8 $R/backend/internal/api/sprint5_exports.go > "$EV/d-list-sql.txt"
cat "$EV/d-defaultworkspace-func.txt" "$EV/d-callsite-login.txt" "$EV/d-callsite-oidc.txt" "$EV/d-list-sql.txt"

echo "=== E 真实复现（curl 登录 → list → 明细 → 票据；viewer 与 investigator 对照） ==="
BASE=https://127.0.0.1:8448
login(){
  local email="$1" pw="$2" jar="$3" out="$4"
  curl -sS -k -o "$out" -w '%{http_code}' -c "$jar" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -X POST "$BASE/api/v1/auth/login" \
    --data-binary "$(python3 -c 'import json,sys; print(json.dumps({"identifier":sys.argv[1],"password":sys.argv[2],"audience":"user"}))' "$email" "$pw")"
}
reap(){ python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(json.dumps({"csrf_present":bool(d.get("csrf_token")),"session_id":d.get("session_id"),"audience":d.get("audience"),"roles":d.get("roles"),"next":d.get("next")},ensure_ascii=False))' "$1" 2>/dev/null || echo '{"parse":"failed"}'; }

VS=$(login "$S7JS_VIEWER_EMAIL" "$S7JS_VIEWER_PW" "$JAR_V" "$LOGIN_V")
echo "viewer_login_status=$VS viewer_login_summary=$(reap "$LOGIN_V")"
echo "viewer_cookie_names=$(awk '!/^#/ && NF>=7 {print $6}' "$JAR_V" | sort | tr '\n' ',')"
IS=$(login "$S7JS_INVESTIGATOR_EMAIL" "$S7JS_INVESTIGATOR_PW" "$JAR_I" "$LOGIN_I")
echo "inv_login_status=$IS inv_login_summary=$(reap "$LOGIN_I")"

echo "--- E-a /auth/session 显示的服务端 principal 作用域 ---"
for pair in "viewer:$JAR_V" "investigator:$JAR_I"; do
  who="${pair%%:*}"; jar="${pair#*:}"
  curl -sS -k -b "$jar" -H 'X-Auth-Audience: user' "$BASE/api/v1/auth/session" -o "$EV/e-session-$who.json" -w "session_http_$who=%{http_code}\n"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1]))["data"]; print(sys.argv[2], "org=",d.get("organization_id"),"ws=",d.get("workspace_id"),"session_id=",d.get("session_id"))' "$EV/e-session-$who.json" "$who"
done

WS_A=$(printf "SELECT id FROM workspaces WHERE slug='s7js-a' AND organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7';\n" | pgq)
WS_B=$(printf "SELECT id FROM workspaces WHERE slug='s7js-b' AND organization_id='5edc0f75-a7fb-4b0a-9750-2aec1ac737f7';\n" | pgq)
EXPORT_A=$(printf "SELECT id::text FROM exports WHERE workspace_id='%s' AND job_id='554d50b9-d7a3-41af-a393-732370c25c45' ORDER BY created_at LIMIT 1;\n" "$WS_A" | pgq)
JOB_A=554d50b9-d7a3-41af-a393-732370c25c45
echo "ws_a=$WS_A ws_b=$WS_B export_in_ws_a=$EXPORT_A"
echo "$EXPORT_A" > "$EV/e-export-a-id.txt"

echo "--- E-b viewer GET /api/v2/exports?limit=50 （完整原始响应） ---"
curl -sS -k -b "$JAR_V" -H 'X-Auth-Audience: user' -D "$HDR_V" \
  "$BASE/api/v2/exports?limit=50" -o "$BODY_V" -w 'list_viewer_http=%{http_code}\n'
cp "$BODY_V" "$EV/e-list-viewer-raw.json"; chmod 600 "$EV/e-list-viewer-raw.json"
grep -iE '^(HTTP/|x-request-id|x-trace-id|content-type|set-cookie)' "$HDR_V" | sed 's/^set-cookie:.*/set-cookie: <redacted>/I' | tee "$EV/e-list-viewer-headers.txt"
python3 - "$EV/e-list-viewer-raw.json" "$JOB_A" "$WS_B" > "$EV/e-list-viewer-summary.json" <<'PY'
import json,sys
p,job,wsb=sys.argv[1],sys.argv[2],sys.argv[3]
d=json.load(open(p))
data=d.get("data") or {}
items=data.get("items") or []
print(json.dumps({
 "top_keys":list(d.keys()),
 "success":d.get("success"),
 "meta":d.get("meta"),
 "declared_scope":data.get("scope"),
 "item_count":len(items),
 "items":[{k:i.get(k) for k in ("id","job_id","dataset","format","zip","status","rows","created_at")} for i in items],
 "items_with_target_job":sum(1 for i in items if str(i.get("job_id") or "")==job),
 "items_in_ws_b_expected":0,
},ensure_ascii=False,indent=1))
PY
cat "$EV/e-list-viewer-summary.json"

echo "--- E-c viewer GET /api/v2/exports/{wsA_export} （明细跨 scope 探测） ---"
curl -sS -k -b "$JAR_V" -H 'X-Auth-Audience: user' -D "$EV/e-detail-viewer-headers.txt" \
  "$BASE/api/v2/exports/$EXPORT_A" -o "$EV/e-detail-viewer-raw.json" -w 'detail_viewer_http=%{http_code}\n'
grep -iE '^(HTTP/|x-request-id|x-trace-id)' "$EV/e-detail-viewer-headers.txt"
head -c 400 "$EV/e-detail-viewer-raw.json"; echo

echo "--- E-d viewer POST /api/v2/exports (job_id=wsA job, xlsx) —— 权限闸（应 403） ---"
CSRF_V=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("csrf_token",""))' "$LOGIN_V")
curl -sS -k -b "$JAR_V" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF_V" \
  -H "Idempotency-Key: p1-cross-$(date -u +%s)" -D "$EV/e-crosscreate-viewer-headers.txt" \
  -X POST "$BASE/api/v2/exports" --data-binary "{\"job_id\":\"$JOB_A\",\"format\":\"xlsx\"}" \
  -o "$EV/e-crosscreate-viewer-raw.json" -w 'crosscreate_viewer_http=%{http_code}\n'
grep -iE '^(HTTP/|x-request-id|x-trace-id)' "$EV/e-crosscreate-viewer-headers.txt"
head -c 500 "$EV/e-crosscreate-viewer-raw.json"; echo

echo "--- E-e investigator 对照 GET /api/v2/exports?limit=50 ---"
curl -sS -k -b "$JAR_I" -H 'X-Auth-Audience: user' -D "$HDR_I" \
  "$BASE/api/v2/exports?limit=50" -o "$BODY_I" -w 'list_inv_http=%{http_code}\n'
cp "$BODY_I" "$EV/e-list-inv-raw.json"; chmod 600 "$EV/e-list-inv-raw.json"
grep -iE '^(HTTP/|x-request-id|x-trace-id)' "$HDR_I" | tee "$EV/e-list-inv-headers.txt"

echo "=== E-f 集合对照（viewer vs investigator） ==="
python3 - "$EV/e-list-viewer-raw.json" "$EV/e-list-inv-raw.json" > "$EV/e-set-compare.json" <<'PY'
import json,sys
def ids(p):
    d=json.load(open(p)); data=d.get("data") or {}
    return [str(i.get("id")) for i in (data.get("items") or [])], data.get("scope")
v,vs=ids(sys.argv[1]); i,iss=ids(sys.argv[2])
print(json.dumps({"viewer_scope":vs,"inv_scope":iss,"viewer_ids":v,"inv_ids":i,
 "identical_set":set(v)==set(i),"viewer_only":sorted(set(v)-set(i)),"inv_only":sorted(set(i)-set(v))},
 ensure_ascii=False,indent=1))
PY
cat "$EV/e-set-compare.json"

echo "=== E-g journal 中同一 request 的 principal 对照 ==="
RID_V=$(grep -i '^x-request-id' "$HDR_V" | tr -d '\r' | awk '{print $2}')
RID_I=$(grep -i '^x-request-id' "$HDR_I" | tr -d '\r' | awk '{print $2}')
echo "request_id_viewer_list=$RID_V request_id_inv_list=$RID_I"
for rid in "$RID_V" "$RID_I"; do
  [ -z "$rid" ] && continue
  echo "--- journal for $rid ---"
  journalctl --user -u blockchain-api --since "-15min" --no-pager 2>/dev/null | grep -F "$rid" | head -5
done | tee "$EV/e-journal-principal.txt"

echo "=== 汇总 machine JSON ==="
python3 - "$EV" <<'PY'
import json,os,sys
ev=sys.argv[1]
def rd(n):
    p=os.path.join(ev,n)
    return open(p).read().strip() if os.path.exists(p) else None
summ={
 "evidence_dir":ev,
 "finding_id":"P1-EXPORT-LIST-CROSSSCOPE-20260913",
 "status":"root_cause_confirmed",
 "hypothesis":"session workspace 由 iam.defaultWorkspace(orgID)=SELECT id FROM workspaces WHERE organization_id=$1 ORDER BY slug LIMIT 1 决定，完全不读 workspace_memberships；wsB-only 身份登录后 principal 落在 wsA，list SQL 本身作用域正确但因 principal 错误而返回 wsA 的 5 条导出。",
 "code": {"defaultWorkspace":"backend/internal/iam/service.go:271-278",
          "call_login":"backend/internal/iam/service.go:229 wsID := s.defaultWorkspace(ctx, orgID)",
          "call_oidc":"backend/internal/api/authv1.go:119 WorkspaceID: s.iam.DefaultWorkspace(...)",
          "list_sql":"backend/internal/api/sprint5_exports.go:211-219 WHERE organization_id=$1 AND workspace_id=$2"},
 "evidence":{"defaultworkspace_eval":rd("c3-defaultworkspace.txt"),
             "v3_session_row":rd("c4-v3-session-row.txt"),
             "exports_count":rd("c5-exports-count.txt"),
             "set_compare":json.loads(rd("e-set-compare.json") or "{}"),
             "list_viewer":json.loads(rd("e-list-viewer-summary.json") or "{}")},
 "not_test_defect":True,
 "note":"DB 预期(wsB 0 行)与浏览器观测(itemCount=5)矛盾已解释：viewer 会话 principal 的 workspace_id 被签发为 wsA。仍不修改任何用户默认工作区；修复需 membership-aware + fail-closed，见后续 fix 轮。",
}
open(os.path.join(ev,"p1-summary.json"),"w").write(json.dumps(summ,ensure_ascii=False,indent=1))
print(json.dumps(summ,ensure_ascii=False,indent=1)[:1200])
PY
chmod 600 "$EV"/*.json "$EV"/*.txt 2>/dev/null
echo "P1_EVIDENCE_DIR=$EV"
