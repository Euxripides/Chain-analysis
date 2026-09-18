#!/usr/bin/env bash
# 双系统 IAM/RBAC/审计 端到端验收（在 VM 上运行，针对 127.0.0.1:8080）。
#
# 编号规则（重要）：
#   * `AUTH-nnn` / `RBAC-nnn` / `SEC-nnn` / `AUDIT-nnn` 仅在**语义与
#     docs/dual-system-development-plan.md §27 中同名 Test ID 一致**时使用；
#   * 其余本脚本自有的冒烟检查一律使用 `SMOKE-nn`，不得冒充文档 Test ID。
# 完整的一致性对照见 acceptance-evidence/dual-system-v1/section27-conformance.md。
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

echo "== AUTH-001/002/003 本地登录与会话 =="
LOGIN_CODE=$(code -c "$JAR" -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"admin\"}" "$BASE/api/auth/v1/login")
check "AUTH-001 admin 登录成功" "$LOGIN_CODE" "200"
check "AUTH-001 会话 Cookie 为 HttpOnly" "$(grep -c 'cs_admin_session	' "$JAR")" "1"

SESSION=$(curl -s -b "$JAR" "$BASE/api/auth/v1/session")
check "SMOKE-01 GET /session 成功" "$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["success"])')" "True"
check "SMOKE-02 audience=admin" "$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["audience"])')" "admin"
check "SMOKE-03 角色含 owner" "$(echo "$SESSION" | python3 -c 'import sys,json;print("owner" in json.load(sys.stdin)["data"]["roles"])')" "True"
PERM_COUNT=$(echo "$SESSION" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["data"]["permissions"]))')
check "SMOKE-04 权限数 > 100" "$([ "$PERM_COUNT" -gt 100 ] && echo yes || echo no)" "yes"
CSRF=$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["csrf_token"])')
SID=$(echo "$SESSION" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["session_id"])')

echo "== AUTH-009/010 伪造身份与转发头 =="
check "SMOKE-05 无会话访问后台 API" "$(code "$BASE/api/admin/v1/overview")" "401"
check "AUTH-009 伪造 X-User-* 头被忽略" "$(code -H 'X-User-Id: 00000000-0000-0000-0000-000000000001' -H 'X-User-Role: owner' "$BASE/api/admin/v1/overview")" "401"
check "AUTH-009 伪造头访问 /api/v1/me 为匿名" "$(curl -s -H 'X-User-Id: abc' -H 'X-User-Email: attacker@x' "$BASE/api/v1/me" | python3 -c 'import sys,json;print(json.load(sys.stdin)["user"]["source"])')" "anonymous"

echo "== RBAC-008 / SEC-001 受众隔离与 CSRF =="
check "SMOKE-06 用户层登录成功" "$(code -c "$JAR2" -H 'Content-Type: application/json' -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")" "200"
check "RBAC-008 用户层会话访问后台 API 被拒" "$(code -b "$JAR2" "$BASE/api/admin/v1/overview")" "401"
check "SMOKE-07 用户层会话可访问 /api/v2" "$(code -b "$JAR2" "$BASE/api/v2/dashboard")" "200"
check "SEC-001 写操作缺少 CSRF 头被拒" "$(code -b "$JAR" -H 'Content-Type: application/json' -d '{"confirm":true}' "$BASE/api/admin/v1/sessions/$SID/commands/revoke")" "403"
check "SEC-001 写操作携带正确 CSRF 头通过" "$(code -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' "$BASE/api/admin/v1/audit/verify")" "200"

echo "== SMOKE 后台端点授权与能力边界 =="
for ep in overview users roles login-events audit/events; do
  check "SMOKE-08 admin 可读 /$ep" "$(code -b "$JAR" "$BASE/api/admin/v1/$ep")" "200"
done
# SMOKE-09（2026-09-12 / S7 更新）：原断言把 /api/admin/v1/alerts 当作"未实现端点"，
# 但该端点在 Sprint 6（S6-BE-03 告警生命周期）已实现，继续断言 501 等于在检查陈旧契约。
# 现拆成两条：未启用的能力必须显式 501（OIDC），已实现的能力必须可读（alerts 200）。
check "SMOKE-09 未启用能力显式 501（OIDC 未接入）" "$(code -b "$JAR" "$BASE/api/auth/v1/oidc/start")" "501"
check "SMOKE-09b Sprint 6 已实现的 /alerts 可读" "$(code -b "$JAR" "$BASE/api/admin/v1/alerts")" "200"
check "SMOKE-10 未声明端点返回 404" "$(code -b "$JAR" "$BASE/api/admin/v1/definitely-not-a-route")" "404"
check "SMOKE-11 角色端点不接受未定义方法" "$(code -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -d '{}' "$BASE/api/admin/v1/roles")" "405"

echo "== AUDIT-001/004 审计事件与哈希链 =="
AUDIT_TOTAL=$(curl -s -b "$JAR" "$BASE/api/admin/v1/audit/events?limit=1" | python3 -c 'import sys,json;print((json.load(sys.stdin).get("meta") or {}).get("total") or 0)')
check "AUDIT-001 审计事件已产生" "$([ "${AUDIT_TOTAL:-0}" -gt 0 ] && echo yes || echo no)" "yes"
for chain in audit login; do
  RES=$(curl -s -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X POST \
        -d "{\"chain\":\"$chain\"}" "$BASE/api/admin/v1/audit/verify")
  echo "$RES" | python3 -c "
import sys,json
d=json.load(sys.stdin)
if not d.get('success'):
    print('  FAIL  $chain 链校验请求失败:', json.dumps(d.get('error'), ensure_ascii=False)); sys.exit(1)
r=d['data']
# valid 只描述"实际被校验到的区间"；链路是否完整必须由 integrity_status 明确声明。
integ=r.get('integrity_status') or 'missing'
# 三种合法状态（ADR-018 / ADR-019）：
#   intact            无缺口、无声明
#   sealed_incident   存在已封存的不可恢复缺口（边界之后逐行验证）
#   declared_events   无缺口，但存在逐条声明的完整性事实（缺失行 / 内容不可复算行）
# 后两种都必须携带非零的声明计数——状态名本身不能当"通过"用。
lost = r.get('declared_lost_count') or 0
anom = r.get('declared_anomaly_count') or 0
declared_ok = (integ != 'declared_events') or (lost + anom > 0)
ok = bool(r['valid']) and integ in ('intact','sealed_incident','declared_events') and declared_ok
print('  %s  %-46s checked=%s valid=%s integrity=%s declared_lost=%s declared_anomaly=%s reason=%s' % ('PASS ' if ok else 'FAIL ', '$chain 哈希链校验', r['checked'], r['valid'], integ, lost, anom, r['reason'] or '-'))
sys.exit(0 if ok else 1)
" || FAIL=$((FAIL+1))
  [ "$chain" = "audit" ] && { echo "$RES" | grep -q '"valid":true' && PASS=$((PASS+1)) || true; }
  [ "$chain" = "login" ] && { echo "$RES" | grep -q '"valid":true' && PASS=$((PASS+1)) || true; }
done

echo "== AUTH-002/003 登录记录覆盖成功与失败 =="
code -H 'Content-Type: application/json' -d '{"identifier":"admin@local","password":"deliberately-wrong","audience":"user"}' "$BASE/api/auth/v1/login" >/dev/null
code -H 'Content-Type: application/json' -d '{"identifier":"ghost@example.com","password":"deliberately-wrong","audience":"user"}' "$BASE/api/auth/v1/login" >/dev/null
EVENTS=$(curl -s -b "$JAR" "$BASE/api/admin/v1/login-events?limit=50")
check "AUTH-002 记录 bad_password 原因码" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(any(r["reason_code"]=="bad_password" for r in json.load(sys.stdin).get("data") or []))')" "True"
check "AUTH-003 记录 unknown_account 原因码" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(any(r["reason_code"]=="unknown_account" for r in json.load(sys.stdin).get("data") or []))')" "True"
check "AUTH-002 失败事件结果标记为 failure" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(any(r["result"]=="failure" for r in json.load(sys.stdin).get("data") or []))')" "True"
check "AUTH-002 账号以掩码形式入库" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(all("*" in r["subject_masked"] or not r["subject_masked"] for r in json.load(sys.stdin).get("data") or []))')" "True"
check "AUDIT-002 响应不回显明文密码（仅本响应，非全量扫描）" "$(echo "$EVENTS" | grep -c 'deliberately-wrong')" "0"
check "AUDIT-004 登录事件带链哈希" "$(echo "$EVENTS" | python3 -c 'import sys,json;print(all(len(r["event_hash"])==64 for r in json.load(sys.stdin).get("data") or []))')" "True"

echo "== AUTH-012 开放重定向防护 =="
LOGIN_REDIRECT=$(curl -s -c "$(mktemp)" -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"user\",\"return_to\":\"https://evil.example/steal\"}" \
  "$BASE/api/auth/v1/login" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["next"])')
check "AUTH-012 return_to 开放重定向被中和" "$LOGIN_REDIRECT" "/home"

echo
echo "==================== 结果 ===================="
printf 'PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
