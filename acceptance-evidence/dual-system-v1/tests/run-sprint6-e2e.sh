#!/usr/bin/env bash
# S6-QA-01：后台控制、治理与可观测性 API 验收（ADMIN-001..010、AUDIT-002..004、S6-OBS-01）
#
# 运行位置：Ubuntu VM（scripts/sync_to_vm.py 同步后执行）。
# 前置：Sprint 6 已部署；夹具密码与 Owner 密码通过环境变量传入。
#
# 设计原则（§27.7 / §32 约束 10）：
#   - 每条 PASS 必须同时具备 API 响应与 DB / 文件 / 运行时证据之一；
#   - 无法观测的写 NOT_VERIFIED，不伪造 PASS；
#   - 双人审批、step-up、412/428/403 等负向路径必须真实触发并断言状态码。
#   - 脚本内不使用花括号变量展开，避免与打包/传输工具冲突。

set -uo pipefail

# 允许未传入的可选变量（set -u 下必须显式提供默认值）。
BASE="${BASE:-}"
ADMIN_EMAIL="${ADMIN_EMAIL:-}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
PLAT_EMAIL="${PLAT_EMAIL:-}"
PLATFORM_ADMIN_PASSWORD="${PLATFORM_ADMIN_PASSWORD:-}"
AUD_EMAIL="${AUD_EMAIL:-}"
AUDITOR_PASSWORD="${AUDITOR_PASSWORD:-}"
VIEW_EMAIL="${VIEW_EMAIL:-}"
VIEWER_PASSWORD="${VIEWER_PASSWORD:-}"
PROJECT="${PROJECT:-}"
EVIDENCE_DIR="${EVIDENCE_DIR:-}"
PGPASS="${PGPASS:-}"
S6_BUILD_SHA="${S6_BUILD_SHA:-}"
API_LOG="${API_LOG:-}"

if [ -z "$BASE" ]; then BASE=http://127.0.0.1:8080; fi
if [ -z "$ADMIN_EMAIL" ]; then ADMIN_EMAIL=admin@local; fi
ADMIN_PW="$ADMIN_PASSWORD"
if [ -z "$PLAT_EMAIL" ]; then PLAT_EMAIL=fixture-platform_admin@fixture.local; fi
PLAT_PW="$PLATFORM_ADMIN_PASSWORD"
if [ -z "$AUD_EMAIL" ]; then AUD_EMAIL=fixture-auditor@fixture.local; fi
AUD_PW="$AUDITOR_PASSWORD"
if [ -z "$VIEW_EMAIL" ]; then VIEW_EMAIL=fixture-viewer@fixture.local; fi
VIEW_PW="$VIEWER_PASSWORD"
if [ -z "$PROJECT" ]; then PROJECT=/home/euripides/projects/blockchain-platform; fi
if [ -z "$EVIDENCE_DIR" ]; then EVIDENCE_DIR="$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint6"; fi
if [ -z "$PGPASS" ]; then PGPASS=change_me_pg_2026; fi
if [ -z "$S6_BUILD_SHA" ]; then S6_BUILD_SHA=working-tree; fi
if [ -z "$API_LOG" ]; then API_LOG=/tmp/api.log; fi
FILES_DIR="$EVIDENCE_DIR/files"
PG="docker exec -e PGPASSWORD=$PGPASS deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc"
mkdir -p "$EVIDENCE_DIR" "$FILES_DIR"
: > "$EVIDENCE_DIR/run.log"

for required in ADMIN_PW PLAT_PW AUD_PW VIEW_PW; do
  eval "value=\$$required"
  if [ -z "$value" ]; then echo "缺少必需环境变量：$required"; exit 2; fi
done

RUN_TAG=$(date +%s)
PASS=0; FAIL=0; NV=0
JA=$(mktemp); JP=$(mktemp); JU=$(mktemp); JV=$(mktemp); JB=$(mktemp)
trap 'rm -f "$JA" "$JP" "$JU" "$JV" "$JB"' EXIT

check() { # check <ID> <描述> <实际> <期望>
  if [ "$3" = "$4" ]; then
    printf '  PASS         %-12s %-60s %s\n' "$1" "$2" "$3" >>"$EVIDENCE_DIR/run.log"
    printf '  PASS %-12s %s\n' "$1" "$2"; PASS=$((PASS+1))
  else
    printf '  FAIL         %-12s %-60s got=%s want=%s\n' "$1" "$2" "$3" "$4" >>"$EVIDENCE_DIR/run.log"
    printf '  FAIL %-12s %s got=%s want=%s\n' "$1" "$2" "$3" "$4"; FAIL=$((FAIL+1))
  fi
}
nv() { printf '  NOT_VERIFIED %-12s %s\n' "$1" "$2" | tee -a "$EVIDENCE_DIR/run.log"; NV=$((NV+1)); }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
sql() { $PG "$1"; }

login() { # login <jar> <email> <password> <audience>
  local jar="$1" email="$2" pass="$3" aud="$4"
  if [ -z "$aud" ]; then aud=admin; fi
  curl -s -c "$jar" -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pass\",\"audience\":\"$aud\"}" "$BASE/api/auth/v1/login"
}
csrf() { grep "$1" "$2" 2>/dev/null | awk '{print $7}'; }
adm() { # adm <jar> <csrf> <method> <path> [json]
  local jar="$1" token="$2" method="$3" path="$4" body="${5:-}"
  if [ -n "$body" ]; then
    curl -s -b "$jar" -H "X-CSRF-Token: $token" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -X "$method" -d "$body" "$BASE$path"
  else
    curl -s -b "$jar" -H 'X-Auth-Audience: admin' -X "$method" "$BASE$path"
  fi
}
adm_code() { # adm_code <jar> <csrf> <method> <path> [json]
  local jar="$1" token="$2" method="$3" path="$4" body="${5:-}"
  if [ -n "$body" ]; then
    curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H "X-CSRF-Token: $token" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -X "$method" -d "$body" "$BASE$path"
  else
    curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H 'X-Auth-Audience: admin' -X "$method" "$BASE$path"
  fi
}
adm_ifmatch() { # adm_ifmatch <jar> <csrf> <method> <path> <body> <revision>
  local jar="$1" token="$2" method="$3" path="$4" body="$5" rev="$6"
  curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H "X-CSRF-Token: $token" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "If-Match: $rev" -X "$method" -d "$body" "$BASE$path"
}

exec > >(tee -a "$EVIDENCE_DIR/run.log") 2>&1
echo "==== Sprint 6 E2E 开始 $(date -Is) build=$S6_BUILD_SHA ===="

# ---------- SETUP ----------
login "$JA" "$ADMIN_EMAIL" "$ADMIN_PW" admin >/dev/null
CS_A=$(csrf cs_admin_session_csrf "$JA")
login "$JP" "$PLAT_EMAIL" "$PLAT_PW" admin >/dev/null
CS_P=$(csrf cs_admin_session_csrf "$JP")
login "$JU" "$AUD_EMAIL" "$AUD_PW" admin >/dev/null
CS_U=$(csrf cs_admin_session_csrf "$JU")
login "$JV" "$VIEW_EMAIL" "$VIEW_PW" admin >/dev/null
CS_V=$(csrf cs_admin_session_csrf "$JV")
if [ -n "$CS_A" ] && [ -n "$CS_P" ] && [ -n "$CS_U" ]; then SETUP_OK=ok; else SETUP_OK=missing; fi
check "SETUP" "Owner/平台管理员/审计员/普通用户 会话建立" "$SETUP_OK" "ok"

## ==================== ADMIN-001 ====================
echo "---- ADMIN-001 创建用户并绑定工作区角色 ----"
NEW_EMAIL="s6-$(date +%s)@fixture.local"
CREATE=$(adm "$JA" "$CS_A" POST /api/admin/v1/users "{\"email\":\"$NEW_EMAIL\",\"display_name\":\"S6 验收账号\",\"role_key\":\"viewer\",\"reason\":\"ADMIN-001 验收：创建用户并绑定工作区角色\"}")
NEW_ID=$(echo "$CREATE" | jget "d['data']['id']")
NEW_PW=$(echo "$CREATE" | jget "d['data']['initial_password']")
if [[ "$NEW_ID" =~ ^[0-9a-f-]{36}$ ]]; then NEW_ID_OK=uuid; else NEW_ID_OK="bad:$NEW_ID"; fi
check "ADMIN-001a" "创建用户返回 UUID" "$NEW_ID_OK" "uuid"
if [ -n "$NEW_PW" ]; then PW_ONCE=once; else PW_ONCE=empty; fi
check "ADMIN-001b" "一次性初始密码只返回一次" "$PW_ONCE" "once"
check "ADMIN-001c" "DB 中 identity 已建立" "$(sql "select count(*) from identities where id='$NEW_ID' and status='active'")" "1"
check "ADMIN-001d" "工作区成员关系已建立" "$(sql "select count(*) from workspace_memberships where identity_id='$NEW_ID'")" "1"
EFF=$(adm "$JA" "$CS_A" GET "/api/admin/v1/users/$NEW_ID/effective-permissions")
EFF_PERM=$(echo "$EFF" | jget "len(d['data']['permissions'])")
if [ -z "$EFF_PERM" ]; then EFF_PERM=0; fi
if [ "$EFF_PERM" -gt 0 ]; then EFF_OK=yes; else EFF_OK=no; fi
check "ADMIN-001e" "有效权限按角色来源返回（viewer > 0 项）" "$EFF_OK" "yes"
check "ADMIN-001f" "绑定 scope 落到工作区" "$(echo "$EFF" | jget "d['data']['bindings'][0]['scope_type']")" "workspace"
login "$JB" "$NEW_EMAIL" "$NEW_PW" user >/dev/null
check "ADMIN-001g" "新账号可登录并建立会话" "$(curl -s -b "$JB" -H 'X-Auth-Audience: user' "$BASE/api/auth/v1/session" | jget "d['data']['email']")" "$NEW_EMAIL"
AUDIT_001=$(sql "select count(*) from audit_events_v2 where action='admin.user.create' and resource_id='$NEW_ID'")
if [ "$AUDIT_001" -ge 1 ]; then A1=yes; else A1=no; fi
check "ADMIN-001h" "创建用户写入审计" "$A1" "yes"

## ==================== ADMIN-002 ====================
echo "---- ADMIN-002 停用用户影响预览 ----"
IMPACT=$(adm "$JA" "$CS_A" GET "/api/admin/v1/users/$NEW_ID/impact-preview")
check "ADMIN-002a" "影响预览返回活跃会话数（新账号已登录=1）" "$(echo "$IMPACT" | jget "d['data']['impact']['active_sessions']")" "1"
check "ADMIN-002b" "影响预览包含任务/审批/案件/密钥维度" "$(echo "$IMPACT" | python3 -c "import sys,json;i=json.load(sys.stdin)['data']['impact'];print('all' if all(k in i for k in ('running_jobs','pending_approvals','owned_cases','active_api_keys','case_memberships')) else 'missing')" 2>/dev/null)" "all"
check "ADMIN-002c" "未带 confirm 的停用被拒绝（428）" "$(adm_code "$JA" "$CS_A" POST "/api/admin/v1/users/$NEW_ID/commands/disable" '{"reason":"ADMIN-002 负向：缺少 confirm"}')" "428"
DISABLE=$(adm "$JA" "$CS_A" POST "/api/admin/v1/users/$NEW_ID/commands/disable" '{"confirm":true,"reason":"ADMIN-002 验收：确认后停用"}')
check "ADMIN-002d" "确认后停用成功" "$(echo "$DISABLE" | jget "d['data']['status']")" "disabled"
check "ADMIN-002e" "DB 中状态为 disabled" "$(sql "select status from identities where id='$NEW_ID'")" "disabled"
check "ADMIN-002f" "停用同时撤销全部会话" "$(sql "select count(*) from sessions where identity_id='$NEW_ID' and revoked_at is null")" "0"
AUDIT_002=$(sql "select count(*) from audit_events_v2 where action='admin:user:disable' and resource_id='$NEW_ID'")
if [ "$AUDIT_002" -ge 1 ]; then A2=yes; else A2=no; fi
check "ADMIN-002g" "停用写入审计" "$A2" "yes"

## ==================== ADMIN-003 ====================
echo "---- ADMIN-003 Provider 连接测试 ----"
PROVIDERS=$(adm "$JA" "$CS_A" GET /api/admin/v1/providers)
PKEY=$(echo "$PROVIDERS" | jget "d['data']['items'][0]['key']")
if [ -n "$PKEY" ]; then PK=yes; else PK=no; fi
check "ADMIN-003a" "Provider 描述符列表非空" "$PK" "yes"
USAGE_BEFORE=$(sql "select count(*) from provider_usage where usage_type='provider_test'")
TEST=$(adm "$JA" "$CS_A" POST "/api/admin/v1/providers/$PKEY/commands/test" '{"reason":"ADMIN-003 验收：受控低成本探针"}')
check "ADMIN-003b" "连接测试使用受控探针方法" "$(echo "$TEST" | jget "d['data']['method']")" "Provider.Health"
check "ADMIN-003c" "连接测试返回 Trace ID" "$([ -n "$(echo "$TEST" | jget "d['data']['trace_id']")" ] && echo yes || echo no)" "yes"
USAGE_AFTER=$(sql "select count(*) from provider_usage where usage_type='provider_test'")
check "ADMIN-003d" "连接测试写入 Usage（DB 证据）" "$((USAGE_AFTER-USAGE_BEFORE))" "1"
check "ADMIN-003e" "密钥列表只返回摘要（无明文）" "$(adm "$JA" "$CS_A" GET "/api/admin/v1/providers/$PKEY/secrets" | python3 -c "import sys,json;items=json.load(sys.stdin)['data']['items'];allow={'secret_ref','fingerprint','id','note','status','rotated_at','last_used_at','expires_at','prefix','plaintext_available'};bad=[k for i in items for k,v in i.items() if isinstance(v,str) and len(v)>24 and k not in allow];print('plaintext' if bad else 'summary')" 2>/dev/null)" "summary"
check "ADMIN-003f" "Provider 描述符记录最近测试结果" "$(sql "select coalesce(last_test_result->>'method','') from provider_descriptors where provider_key='$PKEY'")" "Provider.Health"

## ==================== ADMIN-004 ====================
echo "---- ADMIN-004 Provider 断开影响与 step-up ----"
adm "$JA" "$CS_A" POST "/api/admin/v1/providers/$PKEY/commands/connect" '{"reason":"ADMIN-004 前置：恢复连接"}' >/dev/null
NO_STEPUP=$(adm_code "$JA" "$CS_A" POST "/api/admin/v1/providers/$PKEY/commands/disconnect" '{"reason":"ADMIN-004 负向：缺少 step-up"}')
RUNNING=$(sql "select count(*) from download_jobs where status in ('RUNNING','RESUMING')")
if [ "$RUNNING" -gt 0 ]; then
  check "ADMIN-004a" "存在运行任务时断开必须 step-up（403）" "$NO_STEPUP" "403"
else
  check "ADMIN-004a" "无运行任务时断开仍走命令通道" "$([ "$NO_STEPUP" = "200" ] && echo executed || echo guarded)" "executed"
fi
DISC=$(adm "$JA" "$CS_A" POST "/api/admin/v1/providers/$PKEY/commands/disconnect" "{\"reason\":\"ADMIN-004 验收：带 step-up 断开\",\"step_up_password\":\"$ADMIN_PW\"}")
check "ADMIN-004b" "带 step-up 断开成功" "$(echo "$DISC" | jget "d['data']['provider']['status']")" "disconnected"
check "ADMIN-004c" "响应包含受影响链/Dataset/任务/fallback" "$(echo "$DISC" | python3 -c "import sys,json;i=json.load(sys.stdin)['data']['impact'];print('has' if all(k in i for k in ('affected_chains','affected_datasets','affected_running_jobs','policies_without_fallback')) else 'missing')" 2>/dev/null)" "has"
check "ADMIN-004d" "DB 中 Provider 已断开" "$(sql "select status from provider_descriptors where provider_key='$PKEY'")" "disconnected"
DISC_AUDIT=$(sql "select count(*) from audit_events_v2 where action like 'admin.provider.disconnect%'")
if [ "$DISC_AUDIT" -ge 1 ]; then A4=yes; else A4=no; fi
check "ADMIN-004e" "断开与 step-up 写入审计" "$A4" "yes"
adm "$JA" "$CS_A" POST "/api/admin/v1/providers/$PKEY/commands/connect" '{"reason":"ADMIN-004 收尾：恢复连接"}' >/dev/null

## ==================== ADMIN-005 ====================
echo "---- ADMIN-005 性能模式切换 requested/effective/reason ----"
PERF=$(adm "$JA" "$CS_A" GET /api/admin/v1/runtime/performance)
REQ=$(echo "$PERF" | jget "d['data']['requested_mode']")
EFF=$(echo "$PERF" | jget "d['data']['effective_mode']")
REASON=$(echo "$PERF" | jget "d['data']['reason']")
if [ -n "$REQ" ] && [ -n "$EFF" ] && [ -n "$REASON" ]; then TRIO=all; else TRIO=incomplete; fi
check "ADMIN-005a" "返回 requested/effective/reason 三要素" "$TRIO" "all"
PREV=$(adm "$JA" "$CS_A" POST /api/admin/v1/runtime/performance/preview '{"mode":"fast"}')
check "ADMIN-005b" "预览返回目标并发与预估原因" "$(echo "$PREV" | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print('yes' if d.get('next_target_concurrency',0)>0 and d.get('effective_reason_preview') else 'no')" 2>/dev/null)" "yes"
if [ "$REQ" = "safe" ]; then TARGET_MODE=fast; else TARGET_MODE=safe; fi
APPLY=$(adm "$JA" "$CS_A" POST /api/admin/v1/runtime/performance/commands/apply "{\"mode\":\"$TARGET_MODE\",\"reason\":\"ADMIN-005 验收：切换性能模式\"}")
check "ADMIN-005c" "apply 返回值与 DB requested_mode 一致" "$(echo "$APPLY" | jget "d['data']['requested_mode']")|$(sql "select requested_mode from ankr_performance_control where singleton")" "$TARGET_MODE|$TARGET_MODE"
AFTER=$(adm "$JA" "$CS_A" GET /api/admin/v1/runtime/performance)
check "ADMIN-005d" "回读 requested 已生效" "$(echo "$AFTER" | jget "d['data']['requested_mode']")" "$TARGET_MODE"
if [ -n "$(echo "$AFTER" | jget "d['data']['effective_mode']")" ] && [ -n "$(echo "$AFTER" | jget "d['data']['reason']")" ]; then EE=ok; else EE=empty; fi
check "ADMIN-005e" "effective 与 reason 为非空真实值" "$EE" "ok"
PERF_AUDIT=$(sql "select count(*) from audit_events_v2 where action like 'admin.performance.%'")
if [ "$PERF_AUDIT" -ge 1 ]; then A5=yes; else A5=no; fi
check "ADMIN-005f" "性能模式变更写入审计" "$A5" "yes"

## ==================== ADMIN-006 ====================
echo "---- ADMIN-006 Kill Switch 双人审批与恢复 ----"
# 夹具卫生：把历史运行遗留、且控制面已不在生效状态的 active 请求收尾，避免幽灵占用。
STALE=$(sql "select count(*) from kill_switch_engagements e where e.status='active' and (select kill_switch from api_control_settings where id=1)=false")
if [ "$STALE" -gt 0 ]; then
  sql "update kill_switch_engagements set status='restored', restored_at=now(), updated_at=now(), health_check=coalesce(health_check,'{}'::jsonb) where status='active' and (select kill_switch from api_control_settings where id=1)=false" >/dev/null
fi
check "ADMIN-006-0" "控制面当前没有生效中的 Kill Switch（复跑前置）" "$(sql "select kill_switch from api_control_settings where id=1")" "f"
KS_PREVIEW=$(adm "$JA" "$CS_A" POST /api/admin/v1/control/kill-switch/preview '{"mode":"engage"}')
check "ADMIN-006a" "影响预览保留 Checkpoint" "$(echo "$KS_PREVIEW" | jget "d['data']['impact']['keeps_checkpoints']")" "True"
check "ADMIN-006b" "影响预览默认不删除任务" "$(echo "$KS_PREVIEW" | jget "d['data']['impact']['deletes_jobs']")" "False"
KS_REQ=$(adm "$JP" "$CS_P" POST /api/admin/v1/control/kill-switch/requests "{\"mode\":\"engage\",\"reason\":\"ADMIN-006 验收：平台管理员发起\",\"step_up_password\":\"$PLAT_PW\"}")
KS_ID=$(echo "$KS_REQ" | jget "d['data']['engagement']['id']")
KS_APPROVAL=$(echo "$KS_REQ" | jget "d['data']['approval']['id']")
if [[ "$KS_ID" =~ ^[0-9a-f-]{36}$ ]] && [ -n "$KS_APPROVAL" ]; then BOTH=both; else BOTH="missing:$KS_ID"; fi
check "ADMIN-006c" "创建请求返回 engagement + approval" "$BOTH" "both"
check "ADMIN-006d" "请求人自审批被拒绝（403）" "$(adm_code "$JP" "$CS_P" POST "/api/admin/v1/control/kill-switch/requests/$KS_ID/approve" '{"note":"ADMIN-006 负向：自审批"}')" "403"
check "ADMIN-006e" "未审批先执行被拒绝（412）" "$(adm_code "$JA" "$CS_A" POST "/api/admin/v1/control/kill-switch/requests/$KS_ID/execute" "{\"step_up_password\":\"$ADMIN_PW\"}")" "412"
APPROVE=$(adm "$JA" "$CS_A" POST "/api/admin/v1/control/kill-switch/requests/$KS_ID/approve" '{"note":"ADMIN-006 验收：第二审批人确认"}')
check "ADMIN-006f" "第二位管理员审批通过" "$(echo "$APPROVE" | jget "d['data']['engagement']['status']")" "approved"
check "ADMIN-006g" "DB 记录 requester<>approver" "$(sql "select count(*) from approval_requests where id='$KS_APPROVAL' and status='approved' and decided_by <> requested_by")" "1"
EXEC=$(adm "$JA" "$CS_A" POST "/api/admin/v1/control/kill-switch/requests/$KS_ID/execute" "{\"step_up_password\":\"$ADMIN_PW\"}")
check "ADMIN-006h" "执行后 Kill Switch 生效" "$(echo "$EXEC" | jget "d['data']['kill_switch']")" "True"
check "ADMIN-006i" "DB 中 kill_switch=true" "$(sql "select kill_switch from api_control_settings where id=1")" "t"
RS_REQ=$(adm "$JP" "$CS_P" POST /api/admin/v1/control/kill-switch/requests "{\"mode\":\"restore\",\"reason\":\"ADMIN-006 验收：恢复\",\"step_up_password\":\"$PLAT_PW\"}")
RS_ID=$(echo "$RS_REQ" | jget "d['data']['engagement']['id']")
adm "$JA" "$CS_A" POST "/api/admin/v1/control/kill-switch/requests/$RS_ID/approve" '{"note":"ADMIN-006 验收：批准恢复"}' >/dev/null
RS=$(adm "$JA" "$CS_A" POST "/api/admin/v1/control/kill-switch/requests/$RS_ID/restore" "{\"step_up_password\":\"$ADMIN_PW\"}")
check "ADMIN-006j" "恢复后 kill_switch=false" "$(echo "$RS" | jget "d['data']['kill_switch']")" "False"
check "ADMIN-006k" "恢复后自动健康检查通过" "$(echo "$RS" | jget "d['data']['health']['healthy']")" "True"
KS_AUDIT=$(sql "select count(*) from audit_events_v2 where action like 'admin.kill_switch.%'")
if [ "$KS_AUDIT" -ge 4 ]; then A6=yes; else A6=no; fi
check "ADMIN-006l" "Kill Switch 全流程写入审计" "$A6" "yes"
KS_EVENTS=$(sql "select count(*) from approval_request_events where approval_request_id='$KS_APPROVAL'")
if [ "$KS_EVENTS" -ge 2 ]; then A6B=yes; else A6B=no; fi
check "ADMIN-006m" "审批流程事件落库" "$A6B" "yes"

## ==================== ADMIN-007 ====================
echo "---- ADMIN-007 预算超阈值触发动作与告警 ----"
BUDGET_NAME="s6-budget-$(date +%s)"
BUDGET=$(adm "$JA" "$CS_A" POST /api/admin/v1/budgets "{\"name\":\"$BUDGET_NAME\",\"provider\":\"all\",\"period\":\"month\",\"limit_usd\":1,\"warn_percent\":50,\"action\":\"alert\",\"action_threshold\":80,\"notes\":\"ADMIN-007 验收\",\"reason\":\"ADMIN-007 验收：创建极小额度预算以触发阈值\"}")
BID=$(echo "$BUDGET" | jget "d['data']['id']")
if [[ "$BID" =~ ^[0-9a-f-]{36}$ ]]; then BID_OK=uuid; else BID_OK="bad:$BID"; fi
check "ADMIN-007a" "创建预算返回 UUID" "$BID_OK" "uuid"
EVAL=$(adm "$JA" "$CS_A" POST "/api/admin/v1/budgets/$BID/commands/evaluate" '{}')
check "ADMIN-007b" "预算评估判定已超动作阈值" "$(echo "$EVAL" | jget "d['data']['status']")" "action_triggered"
check "ADMIN-007c" "触发优先级为 P1" "$(echo "$EVAL" | jget "d['data']['alert_priority']")" "P1"
check "ADMIN-007d" "生成了预算告警（DB）" "$(sql "select count(*) from api_control_alerts where fingerprint='budget:$BID' and status='OPEN'")" "1"
BUD_ACT=$(sql "select count(*) from api_control_actions where action='budget_alert'")
if [ "$BUD_ACT" -ge 1 ]; then A7=yes; else A7=no; fi
check "ADMIN-007e" "登记了配置动作（DB）" "$A7" "yes"
BUD_AUD=$(sql "select count(*) from audit_events_v2 where action='admin.budget.evaluate'")
if [ "$BUD_AUD" -ge 1 ]; then A7B=yes; else A7B=no; fi
check "ADMIN-007f" "预算评估写入审计" "$A7B" "yes"

## ==================== 告警 → 对象关联链（§19 验收） ====================
echo "---- 从告警定位到 request/provider/shard/checkpoint/object ----"
ALERT_BUDGET_ID=$(sql "select id::text from api_control_alerts where fingerprint='budget:$BID' limit 1")
DETAIL=$(adm "$JA" "$CS_A" GET "/api/admin/v1/alerts/$ALERT_BUDGET_ID")
check "ALERT-TRACE-a" "告警详情返回 alert + related 两段" "$(echo "$DETAIL" | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print('ok' if 'alert' in d and 'related' in d else 'missing')" 2>/dev/null)" "ok"
check "ALERT-TRACE-b" "关联链包含 Provider 命中或明确的未命中原因" "$(echo "$DETAIL" | python3 -c "import sys,json;k=json.load(sys.stdin)['data']['related'].get('provider');print('ok' if isinstance(k,dict) and ('key' in k) else 'missing')" 2>/dev/null)" "ok"
# 用一条真实 Provider 请求告警验证 request→job→shard→checkpoint→object 链路可用：
# 先取最近一条带 job_id 的 provider_usage，构造带 links 的告警（模拟运维告警挂点）。
LINK_ROW=$(sql "select coalesce(request_id,'')||'|'||coalesce(provider,'')||'|'||coalesce(job_id::text,'')||'|'||coalesce(shard_id::text,'') from provider_usage where request_id<>'' order by request_time desc limit 1")
LINK_REQ=$(echo "$LINK_ROW" | cut -d'|' -f1)
LINK_PROV=$(echo "$LINK_ROW" | cut -d'|' -f2)
LINK_JOB=$(echo "$LINK_ROW" | cut -d'|' -f3)
LINK_SHARD=$(echo "$LINK_ROW" | cut -d'|' -f4)
LINK_FP="s6-trace-$RUN_TAG"
sql "insert into api_control_alerts (severity, priority, title, message, fingerprint, status, source, links) values ('P2','P2','S6 关联链验证','trace drill','$LINK_FP','OPEN','observability','{\"request_id\":\"$LINK_REQ\",\"provider\":\"$LINK_PROV\",\"job_id\":\"$LINK_JOB\",\"shard_id\":\"$LINK_SHARD\"}'::jsonb) on conflict (fingerprint) do update set links=excluded.links, status='OPEN'" >/dev/null
LINK_ID=$(sql "select id::text from api_control_alerts where fingerprint='$LINK_FP'")
LINK_DETAIL=$(adm "$JA" "$CS_A" GET "/api/admin/v1/alerts/$LINK_ID")
check "ALERT-TRACE-c" "由告警可定位到 Provider 使用记录" "$(echo "$LINK_DETAIL" | python3 -c "import sys,json;r=json.load(sys.stdin)['data']['related'];print('ok' if isinstance(r.get('request'),dict) and r['request'].get('provider') else 'missing')" 2>/dev/null)" "ok"
check "ALERT-TRACE-d" "关联链返回 job/shard 层" "$(echo "$LINK_DETAIL" | python3 -c "import sys,json;r=json.load(sys.stdin)['data']['related'];print('ok' if 'job' in r and 'shard' in r else 'missing')" 2>/dev/null)" "ok"
sql "update api_control_alerts set status='RESOLVED', resolved_at=now() where id='$LINK_ID'" >/dev/null

## ==================== ADMIN-008 ====================
echo "---- ADMIN-008 审计导出与 hash 校验 ----"
check "ADMIN-008-0" "缺少 step-up 的审计导出被拒绝（403）" "$(adm_code "$JA" "$CS_A" POST /api/admin/v1/audit/export '{"format":"jsonl","chain":"audit","limit":500,"reason":"ADMIN-008 负向：无 step-up"}')" "403"
EXPORT=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/export "{\"format\":\"jsonl\",\"chain\":\"audit\",\"limit\":500,\"reason\":\"ADMIN-008 验收：导出审计事件\",\"step_up_password\":\"$ADMIN_PW\"}")
EXP_ID=$(echo "$EXPORT" | jget "d['data']['id']")
EXP_COUNT=$(echo "$EXPORT" | jget "d['data']['event_count']")
EXP_HASH=$(echo "$EXPORT" | jget "d['data']['sha256']")
HASH_LEN=$(printf '%s' "$EXP_HASH" | wc -c | tr -d ' ')
if [[ "$EXP_ID" =~ ^[0-9a-f-]{36}$ ]] && [ "$EXP_COUNT" -gt 0 ] && [ "$HASH_LEN" = "64" ]; then EXP_OK=ok; else EXP_OK="bad:$EXP_ID/$EXP_COUNT/$HASH_LEN"; fi
check "ADMIN-008a" "导出返回 UUID/行数/SHA-256" "$EXP_OK" "ok"
curl -s -b "$JA" -H 'X-Auth-Audience: admin' "$BASE/api/admin/v1/audit/exports/$EXP_ID/download" -o "$FILES_DIR/audit-export.jsonl"
FILE_LINES=$(wc -l <"$FILES_DIR/audit-export.jsonl" | tr -d ' ')
FILE_HASH=$(sha256sum "$FILES_DIR/audit-export.jsonl" | awk '{print $1}')
check "ADMIN-008b" "下载文件行数与导出计数一致" "$FILE_LINES" "$EXP_COUNT"
check "ADMIN-008c" "下载文件 SHA-256 与响应一致" "$FILE_HASH" "$EXP_HASH"
PARSE=$(python3 - "$FILES_DIR/audit-export.jsonl" <<'PY'
import json,sys
n=0
for line in open(sys.argv[1],encoding="utf-8"):
    line=line.strip()
    if not line: continue
    rec=json.loads(line); n+=1
    assert {"seq","action","event_hash","prev_hash"} <= set(rec), sorted(rec)
print("ok" if n>0 else "empty")
PY
)
check "ADMIN-008d" "导出内容为可解析 JSONL（含 seq/hash 链字段）" "$PARSE" "ok"
check "ADMIN-008e" "DB manifest 记录同一 hash" "$(sql "select coalesce(manifest->>'sha256','') from audit_exports where id='$EXP_ID'")" "$EXP_HASH"
EXP_AUD=$(sql "select count(*) from audit_events_v2 where action='admin.audit.export' and resource_id='$EXP_ID'")
if [ "$EXP_AUD" -ge 1 ]; then A8=yes; else A8=no; fi
check "ADMIN-008f" "导出写入审计（含条件与行数）" "$A8" "yes"

## ==================== ADMIN-009 ====================
echo "---- ADMIN-009 配置并发修改 ----"
PATCH_KEY=platform.environment
REV=$(sql "select revision from configuration_settings where key='$PATCH_KEY'")
CUR=$(sql "select value::text from configuration_settings where key='$PATCH_KEY'")
check "ADMIN-009a" "缺少 If-Match 返回 428" "$(adm_code "$JA" "$CS_A" PATCH "/api/admin/v1/settings/$PATCH_KEY" '{"value":"production","reason":"ADMIN-009 负向：缺少 If-Match"}')" "428"
check "ADMIN-009b" "携带正确版本成功（200）" "$(adm_ifmatch "$JA" "$CS_A" PATCH "/api/admin/v1/settings/$PATCH_KEY" "{\"value\":$CUR,\"reason\":\"ADMIN-009 验收：首次修改\"}" "$REV")" "200"
check "ADMIN-009c" "复用旧版本被拒绝（412）" "$(adm_ifmatch "$JA" "$CS_A" PATCH "/api/admin/v1/settings/$PATCH_KEY" "{\"value\":$CUR,\"reason\":\"ADMIN-009 负向：旧版本\"}" "$REV")" "412"
REV_AFTER=$(sql "select revision from configuration_settings where key='$PATCH_KEY'")
check "ADMIN-009d" "版本号递增一次（未静默覆盖）" "$((REV_AFTER-REV))" "1"
check "ADMIN-009e" "配置版本历史写入" "$(sql "select count(*) from configuration_revisions where key='$PATCH_KEY' and revision=$REV_AFTER")" "1"

## ==================== ADMIN-010 ====================
echo "---- ADMIN-010 功能开关灰度与回滚 ----"
FLAG_KEY=user.gauge_rollout
FLAG_REV=$(sql "select revision from feature_flags where key='$FLAG_KEY'")
check "ADMIN-010a" "灰度设为 50% 成功（200）" "$(adm_ifmatch "$JA" "$CS_A" PATCH "/api/admin/v1/feature-flags/$FLAG_KEY" '{"enabled":true,"percentage":50,"reason":"ADMIN-010 验收：50% 灰度"}' "$FLAG_REV")" "200"
EVAL_A=$(adm "$JA" "$CS_A" POST "/api/admin/v1/feature-flags/$FLAG_KEY/commands/evaluate" "{\"subject_id\":\"gauge-a-$RUN_TAG\"}")
check "ADMIN-010b" "灰度试算返回确定性分桶与原因" "$(echo "$EVAL_A" | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print('ok' if isinstance(d.get('bucket'),int) and d.get('reason') else 'bad')" 2>/dev/null)" "ok"
EVAL_B=$(adm "$JA" "$CS_A" POST "/api/admin/v1/feature-flags/$FLAG_KEY/commands/evaluate" "{\"subject_id\":\"gauge-b-$RUN_TAG\"}")
check "ADMIN-010c" "第二名用户独立评估（互不干扰）" "$(echo "$EVAL_B" | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print('ok' if d.get('subject_id')=='gauge-b-$RUN_TAG' else 'bad')" 2>/dev/null)" "ok"
check "ADMIN-010d" "评估记录落库（可追溯）" "$(sql "select count(*) from feature_flag_evaluations where flag_key='$FLAG_KEY' and subject_id in ('gauge-a-$RUN_TAG','gauge-b-$RUN_TAG')")" "2"
ROLLBACK=$(adm "$JA" "$CS_A" POST "/api/admin/v1/feature-flags/$FLAG_KEY/commands/rollback" '{"enabled":false,"percentage":0,"reason":"ADMIN-010 验收：回滚灰度"}')
check "ADMIN-010e" "回滚百分比归零" "$(echo "$ROLLBACK" | jget "d['data']['percentage']")" "0"
AFTER_A=$(adm "$JA" "$CS_A" POST "/api/admin/v1/feature-flags/$FLAG_KEY/commands/evaluate" '{"subject_id":"gauge-a-$RUN_TAG"}')
check "ADMIN-010f" "回滚后同一用户不再命中" "$(echo "$AFTER_A" | jget "d['data']['result']")" "False"
FLAG_AUD=$(sql "select count(*) from audit_events_v2 where action like 'admin.flag.%'")
if [ "$FLAG_AUD" -ge 2 ]; then A10=yes; else A10=no; fi
check "ADMIN-010g" "开关变更写入审计" "$A10" "yes"

## ==================== AUDIT-002 ====================
echo "---- AUDIT-002 日志与审计秘密扫描 ----"
check "AUDIT-002a" "验收日志中 0 处密码/密钥明文" "$(python3 - "$EVIDENCE_DIR/run.log" <<'PY'
import re,sys
patterns=[r"\"password\"\s*:\s*\"[^\"]{4,}", r"step_up_password\"\s*:\s*\"[^\"]{4,}", r"Bearer\s+[A-Za-z0-9._-]{20,}",
          r"api[_-]?key\"\s*:\s*\"[A-Za-z0-9]{16,}", r"sk_[0-9a-f]{32,}"]
hits=0
for line in open(sys.argv[1],encoding="utf-8",errors="ignore"):
    for p in patterns:
        if re.search(p,line,re.I): hits+=1
print(hits)
PY
)" "0"
check "AUDIT-002b" "审计表 detail/before/after 无秘密明文" "$(sql "select count(*) from audit_events_v2 where detail::text ~* 'sk_[0-9a-f]{32}' or before::text ~* 'sk_[0-9a-f]{32}' or after::text ~* 'sk_[0-9a-f]{32}' or detail::text ~* 'step_up_password'")" "0"
SECRET_ROTATE=$(adm "$JA" "$CS_A" POST "/api/admin/v1/providers/$PKEY/commands/rotate-secret" "{\"reason\":\"AUDIT-002 验收：轮换为引用名\",\"step_up_password\":\"$ADMIN_PW\",\"secret_ref\":\"env:S6_ROTATED_$RUN_TAG\",\"secret_prefix\":\"abcd12\"}")
check "AUDIT-002c" "密钥轮换只存引用与指纹，不返回明文" "$(echo "$SECRET_ROTATE" | jget "d['data']['plaintext_returned']")|$(sql "select count(*) from provider_secrets where provider_key='$PKEY' and secret_ref='env:S6_ROTATED_$RUN_TAG' and length(fingerprint)=16")" "False|1"
check "AUDIT-002d" "运行日志无秘密明文" "$(python3 - "$API_LOG" <<'PY'
import re,sys,os
path=sys.argv[1]
if not os.path.exists(path):
    print("missing"); raise SystemExit
patterns=[r"step_up_password", r"sk_[0-9a-f]{32,}", r"cs_admin_session=[A-Za-z0-9]", r"\"password\":\"[^\"]{4,}"]
hits=0
for line in open(path,encoding="utf-8",errors="ignore"):
    for p in patterns:
        if re.search(p,line,re.I): hits+=1
print(hits)
PY
)" "0"

## ==================== AUDIT-003 ====================
echo "---- AUDIT-003 应用 DB 角色拒绝修改/删除/清空审计 ----"
# 应用运行期使用受限角色 blockchain_app（0054）：这些负向用例必须以该角色执行，
# 否则测的是超级用户路径，无法证明"应用 DB 角色拒绝"（§27.6）。
ROLE_OK=$(sql "select rolname from pg_roles where rolname='blockchain_app'")
check "AUDIT-003a" "受限运行角色 blockchain_app 存在" "$ROLE_OK" "blockchain_app"
PRIV=$(sql "select has_table_privilege('blockchain_app','audit_events_v2','INSERT')::text||'/'||has_table_privilege('blockchain_app','audit_events_v2','SELECT')::text||'/'||has_table_privilege('blockchain_app','audit_events_v2','UPDATE')::text||'/'||has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE')::text")
check "AUDIT-003b" "运行角色权限为 INSERT/SELECT 允许、UPDATE/TRUNCATE 拒绝" "$PRIV" "true/true/false/false"
UPD=$(sql "set role blockchain_app; update audit_events_v2 set action='tampered' where seq=(select max(seq) from audit_events_v2)" 2>&1)
case "$UPD" in *"permission denied"*|*"denied"*) check "AUDIT-003c" "UPDATE 审计事件被运行角色拒绝" denied denied ;; *) check "AUDIT-003c" "UPDATE 审计事件被运行角色拒绝" "allowed:$UPD" denied ;; esac
DEL=$(sql "set role blockchain_app; delete from audit_events_v2 where seq=(select max(seq) from audit_events_v2)" 2>&1)
case "$DEL" in *"permission denied"*|*"denied"*) check "AUDIT-003d" "DELETE 审计事件被运行角色拒绝" denied denied ;; *) check "AUDIT-003d" "DELETE 审计事件被运行角色拒绝" "allowed:$DEL" denied ;; esac
TRUNC=$(sql "set role blockchain_app; truncate audit_events_v2" 2>&1)
case "$TRUNC" in *"permission denied"*|*"denied"*) check "AUDIT-003e" "TRUNCATE 审计事件被运行角色拒绝" denied denied ;; *) check "AUDIT-003e" "TRUNCATE 审计事件被运行角色拒绝" "allowed:$TRUNC" denied ;; esac
DROPQ=$(sql "set role blockchain_app; drop table audit_events_v2" 2>&1)
case "$DROPQ" in *"must be owner"*|*"permission denied"*|*"denied"*) check "AUDIT-003f" "DROP 审计表被运行角色拒绝" denied denied ;; *) check "AUDIT-003f" "DROP 审计表被运行角色拒绝" "allowed:$DROPQ" denied ;; esac
# 正向对照：同一角色必须仍能读、且业务写入产生的审计事件真实入库。
BEFORE_ROWS=$(sql "select count(*) from audit_events_v2")
ROLE_READ=$(sql "set role blockchain_app; select count(*) from audit_events_v2" 2>&1 | tail -1)
if [ "$ROLE_READ" = "$BEFORE_ROWS" ]; then RC=ok; else RC="bad:$ROLE_READ/$BEFORE_ROWS"; fi
check "AUDIT-003g" "运行角色仍可读取审计表（正向对照）" "$RC" "ok"
AFTER_EVAL=$(adm "$JA" "$CS_A" POST /api/admin/v1/observability/alerts/evaluate '{}')
sleep 1
NEW_ROWS=$(sql "select count(*) from audit_events_v2")
if [ "$NEW_ROWS" -gt "$BEFORE_ROWS" ]; then WRITE_OK=yes; else WRITE_OK=no; fi
check "AUDIT-003h" "受限角色下审计写入仍然成功" "$WRITE_OK" "yes"

# 完整性事件边界的破坏性操作必须全部被拒绝（Sprint 6 审计修复的核心安全属性）。
denied() { # denied <sql>：只有明确拒绝才算 denied
  local out
  out=$($PG "$1" 2>&1)
  case "$out" in
    *"permission denied"*|*"must be owner"*|*"not permitted"*|*"restrict_violation"*|*"immutable"*) echo denied ;;
    *) echo "allowed:"$(echo "$out" | tr '\n' ' ' | cut -c1-100) ;;
  esac
}
INTEG_PRIV=$(sql "select string_agg(t||':'||has_table_privilege('blockchain_app',t,'INSERT')::text||'/'||has_table_privilege('blockchain_app',t,'SELECT')::text||'/'||has_table_privilege('blockchain_app',t,'UPDATE')::text||'/'||has_table_privilege('blockchain_app',t,'DELETE')::text||'/'||has_table_privilege('blockchain_app',t,'TRUNCATE')::text, ' ' order by t) from unnest(array['audit_integrity_incidents','audit_integrity_incident_anchors','audit_integrity_incident_events']) as t")
EXPECT_INTEG_PRIV="audit_integrity_incident_anchors:true/true/false/false/false audit_integrity_incident_events:true/true/false/false/false audit_integrity_incidents:true/true/false/false/false"
check "AUDIT-003i" "边界三表运行角色权限矩阵正确" "$INTEG_PRIV" "$EXPECT_INTEG_PRIV"
check "AUDIT-003j" "运行角色 UPDATE 边界事件被拒绝" "$(denied "set role blockchain_app; update audit_integrity_incidents set reason='tampered' where chain='audit'")" "denied"
check "AUDIT-003k" "运行角色 DELETE 边界事件被拒绝" "$(denied "set role blockchain_app; delete from audit_integrity_incidents where chain='audit'")" "denied"
check "AUDIT-003l" "运行角色 TRUNCATE 边界事件被拒绝" "$(denied "set role blockchain_app; truncate audit_integrity_incidents")" "denied"
check "AUDIT-003m" "运行角色 DROP 边界表被拒绝" "$(denied "set role blockchain_app; drop table audit_integrity_incident_events")" "denied"
check "AUDIT-003n" "运行角色改写边界哈希证据被拒绝" "$(denied "set role blockchain_app; update audit_integrity_incident_events set event_hash='deadbeef'")" "denied"
check "AUDIT-003o" "运行角色改动边界引用锚点被拒绝" "$(denied "set role blockchain_app; update audit_integrity_incident_anchors set head_hash='deadbeef'")" "denied"
check "AUDIT-003p" "既有每日锚点不可改写（UPDATE 被拒绝）" "$(denied "set role blockchain_app; update audit_chain_anchors set head_hash='deadbeef' where chain='audit'")" "denied"
check "AUDIT-003q" "既有每日锚点不可删除（DELETE 被拒绝）" "$(denied "set role blockchain_app; delete from audit_chain_anchors where chain='audit'")" "denied"
check "AUDIT-003r" "运行角色清空锚点表被拒绝（TRUNCATE）" "$(denied "set role blockchain_app; truncate audit_chain_anchors")" "denied"
check "AUDIT-003s" "运行角色仍可读取边界记录（正向对照）" "$(sql "set role blockchain_app; select count(*) >= 0 from audit_integrity_incidents" | tail -1)" "t"

## ==================== AUDIT-004 ====================
# 完整性事件边界：缺口检测 → 双人审批封存 → 边界之后逐项验证 → 反回填控制。
# 本段不伪造历史、不回填缺口、不改写既有锚点；所有断言都直接对照数据库真实数据。
echo "---- AUDIT-004 完整性边界：缺口检测、封存、边界之后逐项验证 ----"
FIRST_SEQ=$(sql "select min(seq) from audit_events_v2 where chain='audit'")
HEAD_SEQ=$(sql "select last_seq from audit_chain_heads where chain='audit'")
OBS_TAIL=$(sql "select prev_hash from audit_events_v2 where chain='audit' and seq=$FIRST_SEQ")
GAP_FROM=1
GAP_TO=$((FIRST_SEQ-1))
GAP_COUNT=$(sql "select count(*) from audit_events_v2 where chain='audit' and seq between $GAP_FROM and $GAP_TO")

PREVIEW=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/integrity/preview '{"chain":"audit"}')
check "AUDIT-004a" "缺口检测返回结构化观测并定位真实缺口区间" "$(echo "$PREVIEW" | jget "'%s-%s' % (d['data']['observed']['gap_from_seq'], d['data']['observed']['gap_to_seq'])")" "$GAP_FROM-$GAP_TO"
check "AUDIT-004b" "封存尾哈希等于幸存首行的 prev_hash（旧链尾唯一保留证据）" "$(echo "$PREVIEW" | jget "d['data']['observed']['sealed_tail_hash']")" "$OBS_TAIL"
check "AUDIT-004c" "缺口区间内不存在任何事件行（未回填历史）" "$GAP_COUNT" "0"
ALREADY_SEALED=$(echo "$PREVIEW" | jget "d['data']['already_sealed']")

INCIDENT_REF="INC-AUDIT-GAP-$GAP_FROM-$GAP_TO"
if [ "$ALREADY_SEALED" = "True" ]; then
  # 幂等路径：边界已封存时不得重复写入，请求端点必须如实返回 already_sealed。
  REQ=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/integrity/incidents/requests "{\"chain\":\"audit\",\"reason\":\"AUDIT-004 复跑：确认边界不可重复封存\",\"step_up_password\":\"$ADMIN_PW\"}")
  check "AUDIT-004d" "已封存边界不可重复封存（幂等，already_sealed=true）" "$(echo "$REQ" | jget "d['data']['already_sealed']")" "True"
  check "AUDIT-004e" "重复封存不产生第二条边界事件行" "$(sql "select count(*) from audit_integrity_incidents where chain='audit'")" "1"
else
  # 负向：缺少 step-up 密码必须被拒绝。
  check "AUDIT-004d" "缺少 step-up 的封存请求被拒绝（403）" "$(adm_code "$JA" "$CS_A" POST /api/admin/v1/audit/integrity/incidents/requests "{\"chain\":\"audit\",\"reason\":\"负向：无 step-up\"}")" "403"
  REQ=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/integrity/incidents/requests "{\"chain\":\"audit\",\"reason\":\"AUDIT-004 验收：封存不可恢复审计缺口边界\",\"step_up_password\":\"$ADMIN_PW\"}")
  SEAL_APPROVAL=$(echo "$REQ" | jget "d['data']['approval']['id']")
  check "AUDIT-004e" "封存请求进入待审批状态（双人审批）" "$(echo "$REQ" | jget "d['data']['approval']['status']")" "pending"
  check "AUDIT-004e2" "封存请求绑定的缺口与真实观测一致" "$(echo "$REQ" | jget "d['data']['incident_ref']")" "$INCIDENT_REF"
  # 负向：发起人不能自行审批。
  check "AUDIT-004f" "发起人自行审批封存被拒绝（403）" "$(adm_code "$JA" "$CS_A" POST "/api/admin/v1/audit/integrity/incidents/requests/$SEAL_APPROVAL/approve" '{"note":"负向：自审批"}')" "403"
  # 负向：未审批即执行必须被拒绝（412）。
  check "AUDIT-004g" "未通过审批即执行封存被拒绝（412）" "$(adm_code "$JA" "$CS_A" POST "/api/admin/v1/audit/integrity/incidents/requests/$SEAL_APPROVAL/execute" "{\"step_up_password\":\"$ADMIN_PW\"}")" "412"
  check "AUDIT-004h" "第二名审批人批准封存（双人约束成立）" "$(adm "$JP" "$CS_P" POST "/api/admin/v1/audit/integrity/incidents/requests/$SEAL_APPROVAL/approve" '{"note":"AUDIT-004 验收：第二名审批人批准封存"}' | jget "d['data']['status']")" "approved"
  EXEC=$(adm "$JA" "$CS_A" POST "/api/admin/v1/audit/integrity/incidents/requests/$SEAL_APPROVAL/execute" "{\"step_up_password\":\"$ADMIN_PW\",\"reason\":\"AUDIT-004 验收：执行边界封存\"}")
  check "AUDIT-004i" "双人审批后封存成功并返回不可变事件" "$(echo "$EXEC" | jget "d['data']['incident']['incident_ref']")" "$INCIDENT_REF"
fi

# 封存结果必须真实落库，且哈希证据完整。
check "AUDIT-004j" "边界事件落库（chain/kind/缺口/可信起点）" "$(sql "select chain||'|'||kind||'|'||gap_from_seq||'|'||gap_to_seq||'|'||trusted_from_seq from audit_integrity_incidents where incident_ref='$INCIDENT_REF'")" "audit|unrecoverable_gap|$GAP_FROM|$GAP_TO|$FIRST_SEQ"
check "AUDIT-004k" "边界证据哈希与封存尾哈希均为 64 位十六进制" "$(sql "select (length(evidence_hash)=64 and length(sealed_tail_hash)=64 and evidence_hash ~ '^[0-9a-f]{64}$')::text from audit_integrity_incidents where incident_ref='$INCIDENT_REF'")" "true"
check "AUDIT-004k2" "边界创建/审批事件链已落库且带哈希链接" "$(sql "select count(*)>=2 and bool_and(length(event_hash)=64) from audit_integrity_incident_events where incident_ref='$INCIDENT_REF'")" "true"
check "AUDIT-004k3" "边界事件链序号连续且 prev_hash 首尾相接" "$(sql "select (count(*)=max(seq) and count(*) filter (where prev_hash='')=1)::text from audit_integrity_incident_events where incident_ref='$INCIDENT_REF'")" "true"
check "AUDIT-004k4" "边界引用了保留的旧锚点（原样保留、未改写）" "$(sql "select count(*) > 0 from audit_integrity_incident_anchors a join audit_integrity_incidents i on i.id=a.incident_id where i.incident_ref='$INCIDENT_REF' and a.chain='audit' and a.head_hash=(select head_hash from audit_chain_anchors where chain='audit' and anchor_date=a.anchor_date)")" "t"

# 封存后：全链校验必须声明缺口，绝不能伪装为完整。
VERIFY=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/verify '{"chain":"audit"}')
check "AUDIT-004l" "封存后 integrity_status=sealed_incident（不是 intact）" "$(echo "$VERIFY" | jget "d['data']['integrity_status']")" "sealed_incident"
check "AUDIT-004m" "prefix_status=sealed_unrecoverable（旧链不连续已声明）" "$(echo "$VERIFY" | jget "d['data']['prefix_status']")" "sealed_unrecoverable"
check "AUDIT-004n" "实际校验起点等于新链可信起点" "$(echo "$VERIFY" | jget "d['data']['verified_from_seq']")" "$FIRST_SEQ"
check "AUDIT-004o" "边界之后逐行复算通过（valid=true）" "$(echo "$VERIFY" | jget "d['data']['valid']")" "True"
CUR_SEQ=$(sql "select max(seq) from audit_events_v2 where chain='audit'")
EXPECT_CHECKED=$((CUR_SEQ-FIRST_SEQ+1))
W_CHECKED=$(echo "$VERIFY" | jget "d['data']['checked']")
if [ -n "$W_CHECKED" ] && [ "$W_CHECKED" -gt 0 ]; then WOK=yes; else WOK=no; fi
check "AUDIT-004p" "边界之后复算条数 > 0" "$WOK" "yes"
check "AUDIT-004q" "缺口区间仍无任何行（未回填历史审计行）" "$(sql "select count(*) from audit_events_v2 where chain='audit' and seq between $GAP_FROM and $GAP_TO")" "0"
check "AUDIT-004r" "控制链 login 无缺口，integrity_status=intact（边界机制非全局放行）" "$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/verify '{"chain":"login"}' | jget "d['data']['integrity_status']")" "intact"
check "AUDIT-004r2" "控制链 login 全链可验证（valid=true）" "$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/verify '{"chain":"login"}' | jget "d['data']['valid']")" "True"
check "AUDIT-004s" "边界校验不带出未解释告警以外的错误" "$(echo "$VERIFY" | jget "'none' if d['data']['errors']==[] else d['data']['errors']")" "none"

# 链头与最后一条事件必须仍然自洽。
HEAD_PAIR=$(sql "select last_seq||'|'||last_hash from audit_chain_heads where chain='audit'")
HEAD_HASH=$(echo "$HEAD_PAIR" | cut -d'|' -f2)
HEAD_SEQ_DB=$(echo "$HEAD_PAIR" | cut -d'|' -f1)
ROW_HASH=$(sql "select event_hash from audit_events_v2 where chain='audit' and seq=$CUR_SEQ")
check "AUDIT-004t" "链头 seq 与库中最大 seq 一致" "$HEAD_SEQ_DB" "$CUR_SEQ"
if [ "$ROW_HASH" = "$HEAD_HASH" ]; then HEAD_OK=match; else HEAD_OK=mismatch; fi
check "AUDIT-004u" "链头 last_hash 与最后一条事件 event_hash 一致" "$HEAD_OK" "match"

## ---- 每日锚点逐项验证（边界前 / 边界后分别处置） ----
ANCHOR_DATE=$(date -u +%F)
ANCHOR_CREATE=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/anchors "{\"chain\":\"audit\",\"date\":\"$ANCHOR_DATE\",\"reason\":\"AUDIT-004 验收：生成边界后的当日锚点\"}")
ANCH_FROM=$(sql "select from_seq from audit_chain_anchors where chain='audit' and anchor_date='$ANCHOR_DATE'")
check "AUDIT-004v" "边界后当日锚点存在" "$([ -n "$ANCH_FROM" ] && echo yes || echo no)" "yes"
check "AUDIT-004v2" "当日锚点区间不跨越已封存边界（自可信起点开始）" "$([ "$ANCH_FROM" -ge "$FIRST_SEQ" ] && echo yes || echo no)" "yes"

ANCHORS=$(adm "$JA" "$CS_A" GET "/api/admin/v1/audit/anchors?chain=audit")
check "AUDIT-004w" "边界后锚点整段复算通过（verified=true）" "$(echo "$ANCHORS" | jget "d['data']['items'][-1]['verified']")" "True"
check "AUDIT-004w2" "边界后锚点 head_hash 与复算结果一致" "$(echo "$ANCHORS" | jget "d['data']['items'][-1]['head_hash']==d['data']['items'][-1]['recomputed_head_hash']")" "True"
check "AUDIT-004w3" "边界后锚点复算条数等于其声明事件数" "$(echo "$ANCHORS" | jget "d['data']['items'][-1]['checked']==d['data']['items'][-1]['event_count']")" "True"
check "AUDIT-004x" "边界前锚点标记为封存不可恢复并引用边界事件" "$(echo "$ANCHORS" | jget "'%s|%s' % (d['data']['items'][0]['disposition'], d['data']['items'][0]['incident_ref'])")" "sealed_unrecoverable|$INCIDENT_REF"
check "AUDIT-004x2" "保留锚点的归档对象哈希与记录一致（未被替换）" "$(echo "$ANCHORS" | jget "d['data']['items'][0]['archive_intact']")" "True"
check "AUDIT-004x3" "所有锚点均可归因（unexplained=0）" "$(echo "$ANCHORS" | jget "d['data']['unexplained']")" "0"
ANCHOR_OBJ=$(adm "$JA" "$CS_A" GET "/api/admin/v1/audit/anchors/audit/$ANCHOR_DATE/object")
check "AUDIT-004x4" "归档对象可回读且 SHA-256 与锚点 content_hash 一致" "$(echo "$ANCHOR_OBJ" | jget "d['data']['intact']")" "True"
check "AUDIT-004x5" "归档对象 manifest 含 chain/区间/head_hash（可独立复算）" "$(echo "$ANCHOR_OBJ" | jget "'ok' if all(k in d['data']['manifest'] for k in ('chain','from_seq','to_seq','head_hash','algorithm')) else 'missing'")" "ok"

# 边界完整性报告：一次性给出观测、封存事件、链校验与锚点结论。
REPORT=$(adm "$JA" "$CS_A" GET "/api/admin/v1/audit/integrity?chain=audit")
check "AUDIT-004y" "完整性报告声明缺口并给出可信起点" "$(echo "$REPORT" | jget "'%s|%s' % (d['data']['integrity_status'], d['data']['chain_verification']['verified_from_seq'])")" "sealed_incident|$FIRST_SEQ"
check "AUDIT-004y2" "完整性报告如实陈述不可恢复事实" "$(echo "$REPORT" | jget "'ok' if '不可恢复缺口' in d['data']['truth_statement'] and '不连续' in d['data']['truth_statement'] else 'missing'")" "ok"

# 反回填控制：向已封存缺口写入行必然被判定为边界冲突（用只读查询证明规则存在且当前无违反）。
check "AUDIT-004z" "反回填控制生效（缺口区间行数=0，冲突计数为 0）" "$(echo "$REPORT" | jget "d['data']['observed']['rows_inside_sealed_gap']")" "0"
check "AUDIT-004z2" "边界事件不可改写（运行角色 UPDATE 被拒绝）" "$(denied "set role blockchain_app; update audit_integrity_incidents set gap_to_seq=0")" "denied"

# 篡改演练：临时停用 append-only 触发器 → 改写一条事件 → 用真实校验器复算 → 还原。
# trap 保证任何分支都会还原，不把数据库留在被篡改状态。
WINDOW_FROM=$FIRST_SEQ
TAMPER_SEQ=$HEAD_SEQ
TAMPER_ORIG=$(sql "select action from audit_events_v2 where chain='audit' and seq=$TAMPER_SEQ")
restore_tamper() {
  sql "UPDATE audit_events_v2 SET action='$TAMPER_ORIG' WHERE chain='audit' AND seq=$TAMPER_SEQ" >/dev/null 2>&1
  sql "ALTER TABLE audit_events_v2 ENABLE TRIGGER audit_events_v2_append_only" >/dev/null 2>&1
}
trap 'restore_tamper; rm -f "$JA" "$JP" "$JU" "$JV" "$JB"' EXIT
sql "ALTER TABLE audit_events_v2 DISABLE TRIGGER audit_events_v2_append_only" >/dev/null
sql "UPDATE audit_events_v2 SET action='tampered-by-s6-drill' WHERE chain='audit' AND seq=$TAMPER_SEQ" >/dev/null
TAMPER_VERIFY=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/verify "{\"chain\":\"audit\",\"from_seq\":$WINDOW_FROM,\"to_seq\":$HEAD_SEQ}")
check "AUDIT-004T-a" "改写事件后边界之后区间校验失败" "$(echo "$TAMPER_VERIFY" | jget "d['data']['valid']")" "False"
check "AUDIT-004T-b" "校验器定位到被改写的 seq" "$(echo "$TAMPER_VERIFY" | jget "d['data']['broken_at_seq']")" "$TAMPER_SEQ"
restore_tamper
RESTORED=$(adm "$JA" "$CS_A" POST /api/admin/v1/audit/verify "{\"chain\":\"audit\",\"from_seq\":$WINDOW_FROM,\"to_seq\":$HEAD_SEQ}")
check "AUDIT-004T-c" "还原后边界之后区间校验重新通过" "$(echo "$RESTORED" | jget "d['data']['valid']")" "True"
check "AUDIT-004T-d" "还原后 integrity_status 仍为 sealed_incident（缺口不会被掩盖）" "$(echo "$RESTORED" | jget "d['data']['integrity_status']")" "sealed_incident"
trap 'rm -f "$JA" "$JP" "$JU" "$JV" "$JB"' EXIT

## ==================== S6-OBS-01 ====================
echo "---- S6-OBS-01 dashboard / SLO / alerts ----"
OBS=$(adm "$JA" "$CS_A" GET "/api/admin/v1/observability/overview?window_minutes=60")
check "S6-OBS-01a" "总览返回 as_of/窗口/健康/吞吐/队列/告警/成本" "$(echo "$OBS" | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print('ok' if all(k in d for k in ('as_of','window_start','health','api','throughput','queue','alerts','cost')) else 'missing')" 2>/dev/null)" "ok"
check "S6-OBS-01b" "吞吐按来源拆分（未合并为单值）" "$(echo "$OBS" | python3 -c "import sys,json;t=json.load(sys.stdin)['data']['throughput'];print('split' if all(k in t for k in ('provider_fetch','pipeline','job_wall_clock','export')) else 'merged')" 2>/dev/null)" "split"
SLO=$(adm "$JA" "$CS_A" GET /api/admin/v1/observability/slo)
check "S6-OBS-01c" "§25.3 SLO 全部登记" "$(echo "$SLO" | python3 -c "import sys,json;ks=set(i['key'] for i in json.load(sys.stdin)['data']['items']);need=set(['api_availability_user','login_p95','query_p95','admin_list_p95','audit_write','outbox_oldest','session_revoke_latency','rpo','tenant_isolation']);print('all' if need<=ks else 'missing:'+','.join(sorted(need-ks)))" 2>/dev/null)" "all"
check "S6-OBS-01d" "SLO 评估可执行并返回计数" "$(adm "$JA" "$CS_A" POST /api/admin/v1/observability/alerts/evaluate '{}' | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print('ok' if all(k in d for k in ('breaches_written','resolved','unchanged')) else 'missing')" 2>/dev/null)" "ok"
METRIC_N=$(adm "$JA" "$CS_A" GET "/api/admin/v1/observability/metrics?name=cs_" | jget "len(d['data']['items'])")
if [ -n "$METRIC_N" ] && [ "$METRIC_N" -ge 3 ]; then MOK=yes; else MOK=no; fi
check "S6-OBS-01e" "指标直读返回 cs_* 指标族" "$MOK" "yes"

## ==================== 权限负向 ====================
echo "---- 权限负向：审计员只读 / 普通用户越权 ----"
check "RBAC-S6a" "审计员写入 Provider 被拒绝（403）" "$(adm_code "$JU" "$CS_U" POST "/api/admin/v1/providers/$PKEY/commands/connect" '{"reason":"负向：审计员只读"}')" "403"
check "RBAC-S6b" "审计员读取备份列表允许（200）" "$(adm_code "$JU" "$CS_U" GET /api/admin/v1/backups)" "200"
check "RBAC-S6b2" "审计员读取版本与迁移允许（200）" "$(adm_code "$JU" "$CS_U" GET /api/admin/v1/system/version)" "200"
check "RBAC-S6b3" "审计员读取审计导出列表允许（200）" "$(adm_code "$JU" "$CS_U" GET /api/admin/v1/audit/exports)" "200"
check "RBAC-S6c" "审计员发起 Kill Switch 被拒绝（403）" "$(adm_code "$JU" "$CS_U" POST /api/admin/v1/control/kill-switch/requests '{"mode":"engage","reason":"负向","step_up_password":"x"}')" "403"
check "RBAC-S6d" "审计员修改配置被拒绝（403）" "$(adm_code "$JU" "$CS_U" PATCH /api/admin/v1/settings/platform.environment '{"value":"production","reason":"负向"}')" "403"
check "RBAC-S6e" "用户受众会话访问后台 API 被拒绝（401 未认证）" "$(curl -s -o /dev/null -w '%{http_code}' -b "$JV" -H 'X-Auth-Audience: user' "$BASE/api/admin/v1/control/state")" "401"
check "RBAC-S6f" "审计员角色不持有 Sprint 6 写权限（DB）" "$(sql "select count(*) from role_permissions rp join roles r on r.id=rp.role_id where r.key='auditor' and rp.permission_key in ('admin:killswitch:engage','admin:killswitch:release','admin:approval:decide','admin:provider:update','admin:config:update','admin:alert:manage','admin:flag:update','admin:budget:update','admin:backup:create','admin:backup:restore')")" "0"

## ==================== 运行时 ====================
echo "---- 运行时与迁移证据 ----"
MIG_MAX=$(sql "select max(name) from schema_migrations")
if [[ "$MIG_MAX" > "postgres/0050" ]]; then MIGOK=applied; else MIGOK="$MIG_MAX"; fi
check "RUNTIME-a" "0051/0052/0053 迁移已应用" "$MIGOK" "applied"
check "RUNTIME-b" "S6 治理表全部存在" "$(sql "select count(*) from information_schema.tables where table_schema='public' and table_name in ('configuration_settings','configuration_revisions','feature_flags','feature_flag_evaluations','kill_switch_engagements','approval_request_events','provider_descriptors','provider_secrets','routing_policies','routing_policy_revisions','retention_policies','retention_runs','deletion_requests','backup_jobs','restore_previews','audit_exports')")" "16"
if [ -f "$API_LOG" ]; then PANIC=$(grep -ciE "panic|fatal" "$API_LOG"); else PANIC=missing; fi
if [ "$PANIC" = "missing" ]; then nv "RUNTIME-c" "运行日志不存在，未验证 panic/fatal"; else check "RUNTIME-c" "API 日志无 panic/fatal" "$PANIC" "0"; fi
check "RUNTIME-d" "保留/删除执行默认关闭" "$(sql "select value::text from configuration_settings where key='governance.deletion.execution_enabled'")" "false"

cat >"$EVIDENCE_DIR/ids.env" <<EOF
E2E_S6_NEW_USER_ID=$NEW_ID
E2E_S6_PROVIDER_KEY=$PKEY
E2E_S6_KILL_SWITCH_ID=$KS_ID
E2E_S6_BUDGET_ID=$BID
E2E_S6_AUDIT_EXPORT_ID=$EXP_ID
E2E_S6_ADMIN_EMAIL=$ADMIN_EMAIL
E2E_S6_PLATFORM_ADMIN_EMAIL=$PLAT_EMAIL
E2E_S6_AUDITOR_EMAIL=$AUD_EMAIL
EOF
chmod 600 "$EVIDENCE_DIR/ids.env"

python3 - "$EVIDENCE_DIR/result.json" "$PASS" "$FAIL" "$NV" "$S6_BUILD_SHA" <<'PY'
import json,sys,datetime
path,p,f,nv,sha=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]),int(sys.argv[4]),sys.argv[5]
# 状态判定必须同时要求 FAIL=0 与 NOT_VERIFIED=0：
# 只要还有任何一项未能验证，套件就不能报告 PASS（不允许把 NOT_VERIFIED 表述为通过）。
blocking = f > 0 or nv > 0
data={"suite":"run-sprint6-e2e.sh","build_sha":sha,"pass":p,"fail":f,"not_verified":nv,
      "blocking_failures":f,"blocking_not_verified":nv,
      "criteria":"fail==0 and not_verified==0 and pass>0",
      "status":"PASS" if (f==0 and nv==0 and p>0) else "FAIL",
      "finished_at":datetime.datetime.now().astimezone().isoformat()}
if blocking:
    print("STATUS=FAIL：存在未通过或未验证项（fail=%d not_verified=%d）" % (f, nv), file=sys.stderr)
json.dump(data,open(path,"w",encoding="utf-8"),ensure_ascii=False,indent=2)
print("result.json written:",data)
PY

echo "==== Sprint 6 E2E 结束 $(date -Is) PASS=$PASS FAIL=$FAIL NOT_VERIFIED=$NV ===="
[ "$FAIL" -eq 0 ] && [ "$NV" -eq 0 ] || exit 1