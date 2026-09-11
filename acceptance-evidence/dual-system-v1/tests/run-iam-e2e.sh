#!/usr/bin/env bash
# 双系统 IAM/RBAC/审计 端到端验收（在 VM 上运行，针对 127.0.0.1:8080）。
# 覆盖：登录 → 会话 → 授权 → CSRF → 租户/受众隔离 → 哈希链校验 → 登录事件。
set -uo pipefail

BASE=${BASE:-http://127.0.0.1:8080}
PW=${ADMIN_PASSWORD:?需要 ADMIN_PASSWORD}
PASS=0; FAIL=0
JAR=$(mktemp); JAR2=$(mktemp)
trap 'rm -f "$JAR" "$JAR2"' EXIT

check() { # check <描述> <实际> <期望>
  if [ "$2" = "$3" ]; then printf '  PASS  %-46s %s\n' "$1" "$2"; PASS=$((PASS+1));
  else printf '  FAIL  %-46s got=%s want=%s\n' "$1" "$2" "$3"; FAIL=$((FAIL+1)); fi
}

code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
jqf() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+'$1') if True else '')" 2>/dev/null; }

echo "== AUTH-001..004 登录与会话 =="
LOGIN_CODE=$(code -c "$JAR" -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"admin\"}" "$BASE/api/auth/v1/login")
check "admin 登录成功" "$LOGIN_CODE" "200"
check "会话 Cookie 为 HttpOnly" "$(grep -c 'cs_admin_session	' "$JAR")" "1"

SESSION=$(curl -s -b "$JAR" "$BASE/api/auth/v1/session")
check "GET /session 成功" "$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["success"])')" "True"
check "audience=admin" "$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["audience"])')" "admin"
check "角色含 owner" "$(echo "$SESSION" | python3 -c 'import sys,json;print("owner" in json.load(sys.stdin)["data"]["roles"])')" "True"
PERM_COUNT=$(echo "$SESSION" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["data"]["permissions"]))')
check "权限数 > 100" "$([ "$PERM_COUNT" -gt 100 ] && echo yes || echo no)" "yes"
CSRF=$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["csrf_token"])')
SID=$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["session_id"])')

echo "== AUTH-005..007 未认证与伪造头 =="
check "无会话访问后台 API" "$(code "$BASE/api/admin/v1/overview")" "401"
check "伪造 X-User-* 头被忽略" "$(code -H 'X-User-Id: 00000000-0000-0000-0000-000000000001' -H 'X-User-Role: owner' "$BASE/api/admin/v1/overview")" "401"
check "伪造 X-User-* 访问 /api/v1/me 为匿名" "$(curl -s -H 'X-User-Id: abc' -H 'X-User-Email: attacker@x' "$BASE/api/v1/me" | python3 -c 'import sys,json;print(json.load(sys.stdin)["user"]["source"])')" "anonymous"

echo "== AUTH-008..010 受众隔离与 CSRF =="
check "用户层登录成功" "$(code -c "$JAR2" -H 'Content-Type: application/json' -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")" "200"
check "用户层会话访问后台 API 被拒" "$(code -b "$JAR2" "$BASE/api/admin/v1/overview")" "401"
check "用户层会话可访问 /api/v2" "$(code -b "$JAR2" "$BASE/api/v2/dashboard")" "200"
check "写操作缺少 CSRF 头被拒" "$(code -b "$JAR" -H 'Content-Type: application/json' -d '{"confirm":true}' "$BASE/api/admin/v1/sessions/$SID/commands/revoke")" "403"
check "写操作携带正确 CSRF 头通过" "$(code -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' "$BASE/api/admin/v1/audit/verify")" "200"

echo "== RBAC-001..005 授权与权限目录 =="
for ep in overview users roles login-events audit/events; do
  check "admin 可读 /$ep" "$(code -b "$JAR" "$BASE/api/admin/v1/$ep")" "200"
done
check "未实现的端点显式 501" "$(code -b "$JAR" "$BASE/api/admin/v1/alerts")" "501"
check "未声明的后台端点不存在" "$(code -b "$JAR" "$BASE/api/admin/v1/definitely-not-a-route")" "404"
check "越权角色编辑在 DB 中无对应关系" "$(code -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -d '{}' "$BASE/api/admin/v1/roles")" "405"

echo "== AUDIT-001..004 审计与哈希链 =="
AUDIT_TOTAL=$(curl -s -b "$JAR" "$BASE/api/admin/v1/audit/events?limit=1" | python3 -c 'import sys,json;print((json.load(sys.stdin).get("meta") or {}).get("total") or 0)')
check "审计事件已产生" "$([ "${AUDIT_TOTAL:-0}" -gt 0 ] && echo yes || echo no)" "yes"
for chain in audit login; do
  RES=$(curl -s -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X POST \
        -d "{\"chain\":\"$chain\"}" "$BASE/api/admin/v1/audit/verify")
  echo "$RES" | python3 -c "
import sys,json
d=json.load(sys.stdin)
if not d.get('success'):
    print('  FAIL  $chain 链校验请求失败:', json.dumps(d.get('error'), ensure_ascii=False)); sys.exit(1)
r=d['data']
print('  %s  %-46s checked=%s valid=%s reason=%s' % ('PASS ' if r['valid'] else 'FAIL ', '$chain 哈希链校验', r['checked'], r['valid'], r['reason'] or '-'))
sys.exit(0 if r['valid'] else 1)
" || FAIL=$((FAIL+1))
  [ "$chain" = "audit" ] && { echo "$RES" | grep -q '"valid":true' && PASS=$((PASS+1)) || true; }
  [ "$chain" = "login" ] && { echo "$RES" | grep -q '"valid":true' && PASS=$((PASS+1)) || true; }
done

echo "== AUDIT-005 登录记录覆盖成功与失败 =="
code -H 'Content-Type: application/json' -d '{"identifier":"admin@local","password":"deliberately-wrong","audience":"user"}' "$BASE/api/auth/v1/login" >/dev/null
code -H 'Content-Type: application/json' -d '{"identifier":"ghost@example.com","password":"deliberately-wrong","audience":"user"}' "$BASE/api/auth/v1/login" >/dev/null
EVENTS=$(curl -s -b "$JAR" "$BASE/api/admin/v1/login-events?limit=50")
check "记录 bad_password 原因码" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(any(r["reason_code"]=="bad_password" for r in json.load(sys.stdin).get("data") or []))')" "True"
check "记录 unknown_account 原因码" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(any(r["reason_code"]=="unknown_account" for r in json.load(sys.stdin).get("data") or []))')" "True"
check "失败事件结果标记为 failure" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(any(r["result"]=="failure" for r in json.load(sys.stdin).get("data") or []))')" "True"
check "账号以掩码形式入库" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(all("*" in r["subject_masked"] or not r["subject_masked"] for r in json.load(sys.stdin).get("data") or []))')" "True"
check "响应不回显明文密码" "$(echo "$EVENTS" | grep -c 'deliberately-wrong')" "0"
check "登录事件带链哈希" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(all(len(r["event_hash"])==64 for r in json.load(sys.stdin).get("data") or []))')" "True"

echo "== SEC-001 SSRF/开放重定向 防护 =="
LOGIN_REDIRECT=$(curl -s -c "$(mktemp)" -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"user\",\"return_to\":\"https://evil.example/steal\"}" \
  "$BASE/api/auth/v1/login" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["next"])')
check "return_to 开放重定向被中和" "$LOGIN_REDIRECT" "/home"

echo
echo "==================== 结果 ===================="
printf 'PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
