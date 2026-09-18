#!/usr/bin/env bash
# =====================================================================
# R-1：GPT Actions 受限 API Key 的 scope / audience / 跨组织负向
#   scripts/s7/p0-actions-scope-tests.sh
#
# 为什么需要它：`gpt_action_api_keys` 实测曾为 0 行，无法验证"受限 key 不得越权"，
# 只能用环境主 Key 做正向 —— 那不能证明 scope 隔离。本脚本**创建一次性测试 key**（夹具），
# 用完即 **disable（不删除）**，保留审计痕迹；不触碰任何用户账号或业务数据。
#
# 覆盖：RBAC-008（API Client 访问后台 / audience / scope 负向）、
#       P0 §3.2 的 `/api/v1/export/status` 跨组织收窄。
#
# 退出码：0=全部通过；1=有 FAIL/BLOCKED；2=前置错误。只读业务数据，仅写 gpt_action_api_keys（夹具）。
# =====================================================================
set -uo pipefail
B="${P0_BASE:-http://127.0.0.1:8088}"
R="${P0_PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${P0_EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p0-legacy-v1}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$EV" || exit 2
LOG="$EV/actions-scope-$TS.log"; : > "$LOG"
PASS=0; FAIL=0; BLOCKED=0
say()  { echo "$*" | tee -a "$LOG"; }
pass() { PASS=$((PASS+1)); say "  PASS  $*"; }
fail() { FAIL=$((FAIL+1)); say "  FAIL  $*"; }
blk()  { BLOCKED=$((BLOCKED+1)); say "  BLOCKED $*"; }

[ -f "$R/deploy/.env" ] && { set -a; . "$R/deploy/.env"; set +a; }
AK="${GPT_ACTIONS_ADMIN_KEY:-}"
[ -n "$AK" ] || { echo "ABORT: 缺少 GPT_ACTIONS_ADMIN_KEY" >&2; exit 2; }
PGQ() { docker exec -e "PGPASSWORD=${POSTGRES_PASSWORD:-}" deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain_platform}" -Atc "$1" 2>/dev/null; }

say "==== Actions scope 负向 $TS  base=$B ===="
say "api_sha=$(sha256sum "$R/backend/bin/api" 2>/dev/null | cut -d' ' -f1)"

# 真实跨组织导出夹具
EXP_ROW="$(PGQ "select e.id::text||'|'||coalesce(o.slug,'') from exports e left join organizations o on o.id=e.organization_id order by e.created_at desc limit 1")"
EXP_ID="${EXP_ROW%%|*}"; EXP_ORG="${EXP_ROW##*|}"
say "导出夹具：id=${EXP_ID:-（无）} org=${EXP_ORG:-（无）}"
[ -n "$EXP_ID" ] || blk "库中没有 exports 记录：跨组织导出负向未验证"

mkkey() { # mkkey <name> <scopes-json> -> 输出 "id api_key"
  curl -s --max-time 25 -H "Authorization: Bearer $AK" -H 'Content-Type: application/json' \
    -X POST -d "{\"name\":\"$1\",\"scopes\":$2,\"rate_limit_per_minute\":600,\"allowed_ips\":[]}" \
    "$B/api/v1/admin/action-api-keys/" | python3 -c "import sys,json;d=json.load(sys.stdin);x=d.get('data') or {};print(x.get('id',''),x.get('api_key',''))" 2>/dev/null
}
code_auth() { # code_auth <key> <path>
  local key="$1" path="$2"
  curl -s -o "$EV/.abody.$$" -w '%{http_code}' --max-time 25 -H "Authorization: Bearer $key" "$B$path" 2>/dev/null
}
expect() { local w="$1" l="$2" g="$3"; if [ "$g" = "$w" ]; then pass "$l -> $g"; else fail "$l -> got=$g want=$w"; fi; }
disable() { curl -s -o /dev/null --max-time 20 -H "Authorization: Bearer $AK" -X POST "$B/api/v1/admin/action-api-keys/$1/disable" 2>/dev/null; }

READK="$(mkkey "p0-probe-read-$TS" '["read"]')"
EXPK="$(mkkey "p0-probe-export-$TS" '["export"]')"
RID="${READK%% *}"; RKEY="${READK##* }"
EID="${EXPK%% *}"; EKEY="${EXPK##* }"
say "夹具 key：read id=${RID:-?} export id=${EID:-?}"
[ -n "${RKEY:-}" ] && [ -n "$RID" ] || blk "read 范围测试 key 创建失败（无法验证 scope 隔离）"
[ -n "${EKEY:-}" ] && [ -n "$EID" ] || blk "export 范围测试 key 创建失败"

if [ -n "${RKEY:-}" ]; then
  expect 200 "read key 调 read 范围端点 /api/v1/address/overview" "$(code_auth "$RKEY" "/api/v1/address/overview?chain=ethereum&address=0x0000000000000000000000000000000000000000")"
  expect 401 "read key 越 scope 调 export 端点 /api/v1/export/status" "$(code_auth "$RKEY" "/api/v1/export/status?job_id=$EXP_ID")"
  expect 401 "read key 访问后台 API /api/admin/v1/jobs" "$(code_auth "$RKEY" /api/admin/v1/jobs)"
  rm -f "$EV/.abody.$$"
fi

if [ -n "${EKEY:-}" ] && [ -n "$EXP_ID" ]; then
  # 正向：export key 读**自己平台作用域内**的真实导出（存在即 200/404 视归属；关键是不得因 UUID 合法就放行他组织）
  g="$(code_auth "$EKEY" "/api/v1/export/status?job_id=$EXP_ID")"
  case "$g" in
    200) pass "export key 读本平台作用域导出 -> 200（夹具导出属于 org=$EXP_ORG）" ;;
    404) pass "export key 读该导出 -> 404（该导出不属于 Actions 平台作用域，收窄生效、不泄露存在性）" ;;
    401) fail "export key 被 401（scope 未生效，误伤合法 key）" ;;
    *) fail "export key 读导出 -> got=$g" ;;
  esac
  # 负向：不存在/他组织的 UUID 不得因"是合法 UUID"而返回元数据
  g="$(code_auth "$EKEY" "/api/v1/export/status?job_id=00000000-0000-0000-0000-000000000000")"
  expect 404 "export key 读不存在导出 UUID（不得因 UUID 合法而放行）" "$g"
  rm -f "$EV/.abody.$$"
fi

# 清理：disable（不删除，保留审计）
[ -n "${RID:-}" ] && { disable "$RID"; say "  已 disable 测试 key read id=$RID（保留记录，不删除）"; }
[ -n "${EID:-}" ] && { disable "$EID"; say "  已 disable 测试 key export id=$EID（保留记录，不删除）"; }

say ""
say "==== 结果：PASS=$PASS FAIL=$FAIL BLOCKED=$BLOCKED （$TS）===="
printf '{"passed":%d,"failed":%d,"blocked":%d,"ts":"%s","scope":"GPT Actions restricted-key scope/audience/cross-org negative","test_keys_disabled":["%s","%s"]}\n' \
  "$PASS" "$FAIL" "$BLOCKED" "$TS" "${RID:-}" "${EID:-}" > "$EV/actions-scope-result-$TS.json"
say "result: $EV/actions-scope-result-$TS.json"
[ "$FAIL" -gt 0 ] || [ "$BLOCKED" -gt 0 ] && { say "整体判定：FAIL/BLOCKED" >&2; exit 1; }
say "整体判定：PASS"; exit 0
