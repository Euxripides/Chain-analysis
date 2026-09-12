#!/usr/bin/env bash
# RBAC-001..008 / 跨租户负向验收（docs/dual-system-development-plan.md §27.2、§28.5 S3-QA-01）
#
# 前置：
#   1. ./backend/bin/admin-fixture -org acme -workspace acme-lab -users viewer,analyst,auditor,investigator
#      夹具账号口令打印在该命令输出中，通过环境变量传入本脚本。
#   2. ADMIN_PASSWORD 为平台 Owner 的密码。
#
# 设计原则：**每条断言都必须能观察到服务端的拒绝**，不接受"前端隐藏了按钮"当作通过。
# 未实现的被测功能一律标 BLOCKED 并说明阻塞原因，不标 PASS（§27.7）。

set -uo pipefail

BASE=${BASE:-http://127.0.0.1:8080}
PW=${ADMIN_PASSWORD:?需要 ADMIN_PASSWORD}
PW_VIEWER=${FIXTURE_VIEWER_PASSWORD:?需要 FIXTURE_VIEWER_PASSWORD}
PW_ANALYST=${FIXTURE_ANALYST_PASSWORD:?需要 FIXTURE_ANALYST_PASSWORD}
PW_INVESTIGATOR=${FIXTURE_INVESTIGATOR_PASSWORD:?需要 FIXTURE_INVESTIGATOR_PASSWORD}
FIXTURE_ENV=${FIXTURE_ENV:-/tmp/dual-system-fixture.env}

[ -r "$FIXTURE_ENV" ] || { echo "缺少夹具环境文件 $FIXTURE_ENV，请先运行 admin-fixture"; exit 2; }
# shellcheck disable=SC1090
. "$FIXTURE_ENV"

PASS=0; FAIL=0; BLOCKED=0
JAR_OWNER=$(mktemp); JAR_OWNER_USER=$(mktemp); JAR_VIEWER=$(mktemp); JAR_ANALYST=$(mktemp); JAR_INV=$(mktemp)
trap 'rm -f "$JAR_OWNER" "$JAR_OWNER_USER" "$JAR_VIEWER" "$JAR_ANALYST" "$JAR_INV"' EXIT

check() { # check <ID> <描述> <实际> <期望>
  if [ "$3" = "$4" ]; then printf '  PASS     %-10s %-44s %s\n' "$1" "$2" "$3"; PASS=$((PASS+1));
  else printf '  FAIL     %-10s %-44s got=%s want=%s\n' "$1" "$2" "$3" "$4"; FAIL=$((FAIL+1)); fi
}
blocked() { printf '  BLOCKED  %-10s %-44s %s\n' "$1" "$2" "$3"; BLOCKED=$((BLOCKED+1)); }
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

login() { # login <jar> <email> <password> <audience>
  local jar=$1 email=$2 pass=$3 aud=${4:-user}
  curl -s -o /dev/null -w '%{http_code}' -c "$jar" -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pass\",\"audience\":\"$aud\"}" "$BASE/api/auth/v1/login"
}
csrf() { grep "$1" "$2" 2>/dev/null | awk '{print $7}'; }
sid() { curl -s -b "$1" "$BASE/api/auth/v1/session" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["session_id"])' 2>/dev/null; }

echo "== 准备：登录各角色 =="
check "SETUP" "Owner 登录（后台受众）" "$(login "$JAR_OWNER" admin@local "$PW" admin)" "200"
# 后台会话不能调用用户层 API（受众隔离），因此 Owner 另需一个用户层会话。
check "SETUP" "Owner 登录（用户层受众）" "$(login "$JAR_OWNER_USER" admin@local "$PW" user)" "200"
OWNER_CSRF=$(csrf cs_admin_session_csrf "$JAR_OWNER")
check "SETUP" "viewer 登录（用户层）" "$(login "$JAR_VIEWER" fixture-viewer@fixture.local "$PW_VIEWER")" "200"
check "SETUP" "analyst 登录（用户层）" "$(login "$JAR_ANALYST" fixture-analyst@fixture.local "$PW_ANALYST")" "200"
check "SETUP" "investigator 登录（用户层）" "$(login "$JAR_INV" fixture-investigator@fixture.local "$PW_INVESTIGATOR")" "200"

VIEWER_ID=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/users?search=fixture-viewer" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print((d["data"] or [{}])[0].get("id",""))')
ANALYST_ID=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/users?search=fixture-analyst" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print((d["data"] or [{}])[0].get("id",""))')
echo "  viewer=$VIEWER_ID analyst=$ANALYST_ID"

echo
echo "== RBAC-001 Viewer 无法执行高权限操作 =="
# 案件创建属 S4，这里用"需要更高级权限的写操作"验证同一条授权链路：
# 路由级 require() 必须拒绝，而不是依赖前端隐藏按钮。
check "RBAC-001" "viewer 调用需 admin:session:revoke 的写操作" \
  "$(code -b "$JAR_VIEWER" -H 'Content-Type: application/json' -X POST -d '{"confirm":true}' \
      "$BASE/api/admin/v1/sessions/00000000-0000-0000-0000-000000000000/commands/revoke")" "401"
viewer_perms=$(curl -s -b "$JAR_VIEWER" "$BASE/api/auth/v1/session" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["data"]["permissions"]))')
check "RBAC-001" "viewer 权限集合不含任何 admin:*" \
  "$(curl -s -b "$JAR_VIEWER" "$BASE/api/auth/v1/session" | python3 -c 'import sys,json;print(any(p.startswith("admin:") for p in json.load(sys.stdin)["data"]["permissions"]))')" "False"
check "RBAC-001" "viewer 调用 /api/v2/dashboard 正常（有 dashboard:read）" \
  "$(code -b "$JAR_VIEWER" "$BASE/api/v2/dashboard")" "200"

echo
echo "== RBAC-002 猜测其他组织的对象 ID =="
check "RBAC-002" "owner 可读本组织案件（用户层会话）" \
  "$(code -b "$JAR_OWNER_USER" "$BASE/api/v2/cases/$FIXTURE_CASE_PRIMARY")" "200"
check "RBAC-002" "viewer(acme) 读默认组织案件 -> 404（不泄露存在性）" \
  "$(code -b "$JAR_VIEWER" "$BASE/api/v2/cases/$FIXTURE_CASE_PRIMARY")" "404"
check "RBAC-002" "owner 读 acme 组织案件 -> 404（反向同样拒绝）" \
  "$(code -b "$JAR_OWNER_USER" "$BASE/api/v2/cases/$FIXTURE_CASE_LAB")" "404"
check "RBAC-002" "viewer 读本组织案件 -> 200" \
  "$(code -b "$JAR_VIEWER" "$BASE/api/v2/cases/$FIXTURE_CASE_LAB")" "200"
check "RBAC-002" "不存在的 UUID 与越权返回同一状态码" \
  "$(code -b "$JAR_VIEWER" "$BASE/api/v2/cases/00000000-0000-0000-0000-0000000000ff")" "404"
DENY_AUDIT=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/audit/events?action=authorization.object_denied&limit=20" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print(len(d.get("data") or []))')
check "RBAC-002" "越权尝试已写入审计（authorization.object_denied）" \
  "$([ "${DENY_AUDIT:-0}" -gt 0 ] && echo yes || echo no)" "yes"
CROSS_REASON=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/audit/events?action=authorization.object_denied&limit=20" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print(any(r.get("deny_reason")=="cross_organization" for r in d.get("data") or []))')
check "RBAC-002" "deny_reason 标记为 cross_organization" "$CROSS_REASON" "True"

echo
echo "== RBAC-003 工作区角色只作用于目标工作区 =="
# 撤销 analyst 的组织级绑定，改为只在 acme-other 工作区绑定。
BINDINGS=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/users/$ANALYST_ID/roles")
ORG_BINDING=$(echo "$BINDINGS" | python3 -c 'import sys,json;d=json.load(sys.stdin)["data"];print(next((b["id"] for b in d if b["scope_type"]=="organization"),""))')
curl -s -o /dev/null -b "$JAR_OWNER" -H "X-CSRF-Token: $OWNER_CSRF" -H 'Content-Type: application/json' -X DELETE \
  -d '{"confirm":true,"reason":"RBAC-003 改为工作区级绑定"}' "$BASE/api/admin/v1/users/$ANALYST_ID/roles/$ORG_BINDING"
GRANT=$(curl -s -b "$JAR_OWNER" -H "X-CSRF-Token: $OWNER_CSRF" -H 'Content-Type: application/json' -X POST \
  -d "{\"role_key\":\"analyst\",\"scope_type\":\"workspace\",\"scope_id\":\"$FIXTURE_OTHER_WORKSPACE_ID\",\"confirm\":true,\"reason\":\"RBAC-003\"}" \
  "$BASE/api/admin/v1/users/$ANALYST_ID/roles")
check "RBAC-003" "授予 acme-other 工作区级 analyst 绑定" \
  "$(echo "$GRANT" | python3 -c 'import sys,json;print(json.load(sys.stdin)["success"])')" "True"
# 重新登录以刷新会话内的主体
login "$JAR_ANALYST" fixture-analyst@fixture.local "$PW_ANALYST" >/dev/null
check "RBAC-003" "analyst 读 acme-lab 案件 -> 404（未绑定该工作区）" \
  "$(code -b "$JAR_ANALYST" "$BASE/api/v2/cases/$FIXTURE_CASE_LAB")" "404"
check "RBAC-003" "analyst 读 acme-other 案件 -> 200（已绑定）" \
  "$(code -b "$JAR_ANALYST" "$BASE/api/v2/cases/$FIXTURE_CASE_OTHER")" "200"
# 恢复组织级绑定，供后续用例使用
curl -s -o /dev/null -b "$JAR_OWNER" -H "X-CSRF-Token: $OWNER_CSRF" -H 'Content-Type: application/json' -X POST \
  -d "{\"role_key\":\"analyst\",\"scope_type\":\"organization\",\"scope_id\":\"$FIXTURE_ORG_ID\",\"confirm\":true,\"reason\":\"RBAC-003 恢复\"}" \
  "$BASE/api/admin/v1/users/$ANALYST_ID/roles"

echo
echo "== RBAC-004 无证据权限读取正文 =="
blocked "RBAC-004" "evidence 端点读取正文被拒" "依赖 S4/S5 的 evidence 端点（当前未实现）"
# 可验证的一半：敏感读取必须留痕。admin:audit:read 在权限目录中标记为 sensitive=true，
# 因此即便只是 GET，也必须产生审计事件。
BEFORE=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/audit/events?action=admin:audit:read&limit=1" \
  | python3 -c 'import sys,json;print((json.load(sys.stdin).get("meta") or {}).get("total") or 0)')
curl -s -o /dev/null -b "$JAR_OWNER" "$BASE/api/admin/v1/audit/events?limit=1"
sleep 1
AFTER=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/audit/events?action=admin:audit:read&limit=1" \
  | python3 -c 'import sys,json;print((json.load(sys.stdin).get("meta") or {}).get("total") or 0)')
check "RBAC-004" "敏感读取（GET）产生审计事件" \
  "$([ "${AFTER:-0}" -gt "${BEFORE:-0}" ] && echo yes || echo no)" "yes"

echo
echo "== RBAC-005 移除最后一个 Owner 必须被拒绝 =="
OWNER_ID=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/users?search=admin@local" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print((d["data"] or [{}])[0].get("id",""))')
OWNER_BINDING=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/users/$OWNER_ID/roles" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin)["data"];print(next((b["id"] for b in d if b["role_key"]=="owner"),""))')
check "RBAC-005" "撤销最后一个 owner 绑定 -> 422" \
  "$(code -b "$JAR_OWNER" -H "X-CSRF-Token: $OWNER_CSRF" -H 'Content-Type: application/json' -X DELETE \
      -d '{"confirm":true,"reason":"RBAC-005"}' "$BASE/api/admin/v1/users/$OWNER_ID/roles/$OWNER_BINDING")" "422"
check "RBAC-005" "owner 绑定仍然有效（未被误删）" \
  "$(code -b "$JAR_OWNER" "$BASE/api/admin/v1/overview")" "200"

echo
echo "== RBAC-006 角色变更后旧会话的授权必须随之变化 =="
check "RBAC-006" "analyst 变更前可列案件" "$(code -b "$JAR_ANALYST" "$BASE/api/v2/cases")" "200"
# 注意：必须撤销该身份的**全部**有效绑定。只撤组织级绑定时，
# RBAC-003 留下的 acme-other 工作区绑定仍然授予 analyst 权限，测不出缓存失效。
ALL_BINDINGS=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/users/$ANALYST_ID/roles"   | python3 -c 'import sys,json;print(" ".join(b["id"] for b in json.load(sys.stdin)["data"]))')
for b in $ALL_BINDINGS; do
  curl -s -o /dev/null -b "$JAR_OWNER" -H "X-CSRF-Token: $OWNER_CSRF" -H 'Content-Type: application/json' -X DELETE     -d '{"confirm":true,"reason":"RBAC-006 撤销全部绑定"}' "$BASE/api/admin/v1/users/$ANALYST_ID/roles/$b"
done
T0=$(date +%s)
DENIED_AT=""
for _ in $(seq 1 12); do
  if [ "$(code -b "$JAR_ANALYST" "$BASE/api/v2/cases")" = "403" ]; then DENIED_AT=$(( $(date +%s) - T0 )); break; fi
  sleep 0.5
done
check "RBAC-006" "撤销后旧会话在 5 秒内失去权限（实测 ${DENIED_AT:->5}s）"   "$([ -n "$DENIED_AT" ] && [ "$DENIED_AT" -le 5 ] && echo within_sla || echo over_sla)" "within_sla"
check "RBAC-006" "同一会话的权限集合已同步收敛"   "$(curl -s -b "$JAR_ANALYST" "$BASE/api/auth/v1/session" | python3 -c 'import sys,json;print("case:list" in json.load(sys.stdin)["data"]["permissions"])')" "False"

# 恢复组织级绑定，验证授予方向同样在数秒内生效
curl -s -o /dev/null -b "$JAR_OWNER" -H "X-CSRF-Token: $OWNER_CSRF" -H 'Content-Type: application/json' -X POST   -d "{\"role_key\":\"analyst\",\"scope_type\":\"organization\",\"scope_id\":\"$FIXTURE_ORG_ID\",\"confirm\":true,\"reason\":\"RBAC-006 恢复\"}"   "$BASE/api/admin/v1/users/$ANALYST_ID/roles"
GRANTED_AT=""
T0=$(date +%s)
for _ in $(seq 1 12); do
  if [ "$(code -b "$JAR_ANALYST" "$BASE/api/v2/cases")" = "200" ]; then GRANTED_AT=$(( $(date +%s) - T0 )); break; fi
  sleep 0.5
done
check "RBAC-006" "重新授予后 5 秒内恢复权限（实测 ${GRANTED_AT:-<未恢复>}s）"   "$([ -n "$GRANTED_AT" ] && [ "$GRANTED_AT" -le 5 ] && echo within_sla || echo over_sla)" "within_sla"

echo "== RBAC-007 Canvas 引用跨案件 Evidence =="
blocked "RBAC-007" "跨案件证据引用保存被拒" "依赖 S5 的 canvas/evidence 端点（当前未实现）"

echo
echo "== RBAC-008 API Client / 受众隔离 =="
check "RBAC-008" "用户层会话访问后台 API -> 401" "$(code -b "$JAR_INV" "$BASE/api/admin/v1/overview")" "401"
check "RBAC-008" "用户层会话访问后台会话列表 -> 401" "$(code -b "$JAR_INV" "$BASE/api/admin/v1/sessions")" "401"
check "RBAC-008" "investigator 无 admin 权限（用户层受众）" \
  "$(curl -s -b "$JAR_INV" "$BASE/api/auth/v1/session" | python3 -c 'import sys,json;print(any(p.startswith("admin:") for p in json.load(sys.stdin)["data"]["permissions"]))')" "False"
check "RBAC-008" "无凭证访问后台 -> 401" "$(code "$BASE/api/admin/v1/overview")" "401"

echo
echo "==================== 结果 ===================="
printf 'PASS=%d FAIL=%d BLOCKED=%d\n' "$PASS" "$FAIL" "$BLOCKED"
[ "$FAIL" -eq 0 ]
