#!/usr/bin/env bash
# =====================================================================
# P0 回归：旧兼容面 /api/v1 未认证读 + 租户/受众作用域
#   scripts/s7/p0-legacy-v1-negative-tests.sh
#
# 覆盖 acceptance-audit.md 第 3 条与用户明确要求：
#   未认证 / Viewer / 跨组织 / 无 evidence 权限的 admin / GPT Actions 正负向。
# 只读：全部为 GET/POST 探测到**不改变业务状态**的端点；不写库、不删数据。
# 需要凭证时从环境读取（P0_ADMIN_PASSWORD / P0_VIEWER_PASSWORD / 夹具口令），
# 缺失的维度记为 BLOCKED 并**使整体 rc 非 0**（不允许用缺失冒充通过）。
#
# 退出码：0 = 全部通过；1 = 存在 FAIL 或 BLOCKED；2 = 前置错误。
# =====================================================================
set -uo pipefail
B="${P0_BASE:-http://127.0.0.1:8088}"
EV="${P0_EV:-/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/p0-legacy-v1}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
R="${P0_PROJECT:-/home/euripides/projects/blockchain-platform}"
mkdir -p "$EV" || exit 2
LOG="$EV/p0-negative-$TS.log"
: > "$LOG"
PASS=0; FAIL=0; BLOCKED=0
say()  { echo "$*" | tee -a "$LOG"; }
pass() { PASS=$((PASS+1)); say "  PASS  $*"; }
fail() { FAIL=$((FAIL+1)); say "  FAIL  $*"; }
blk()  { BLOCKED=$((BLOCKED+1)); say "  BLOCKED $*"; }

code() { curl -s -o "$EV/.body.$$" -w '%{http_code}' --max-time 20 "$@" 2>/dev/null; }
expect_code() { # expect_code <want> <label> <curl args...>
  local want="$1" label="$2"; shift 2
  local got; got="$(code "$@")"
  if [ "$got" = "$want" ]; then pass "$label -> $got"; else fail "$label -> got=$got want=$want"; fi
  rm -f "$EV/.body.$$"
}

say "==== P0 匿名泄露路径回归 $TS  base=$B ===="
say "范围声明（必须与结论一起引用）：本脚本验证的是**匿名访问路径的关闭**与**公开白名单契约**，"
say "以及已认证受众/权限的**有限负向**。它**不构成**完整 P0（多租户对象级隔离）验收："
say "多 org/workspace 的 job/report/export/status/ticket/canvas/investigation 对象负向、受限 Actions key 跨 org、"
say "审计 authorization.object_denied 与 request/trace 绑定、后台调查 runtime 的 dataset 默认组织回退、"
say "前端 admin audience 适配等仍未验证。"
say "二进制：$(sha256sum "$(systemctl --user show -p MainPID --value blockchain-api.service | xargs -I{} readlink -f /proc/{}/exe 2>/dev/null)" 2>/dev/null | cut -c1-64)"

# ---------- 1) 未认证：旧面读路由必须 401 ----------
say ""
say "== 1) 未认证读（修复前 200，现必须 401）=="
for p in /api/v1/cases /api/v1/investigations "/api/v1/download-jobs?limit=1" \
         /api/v1/api-control/overview /api/v1/api-control/provider-accounts \
         "/api/v1/api-control/requests?limit=1" /api/v1/admin/planning-decisions \
         "/api/v1/admin/ankr/benchmarks?limit=1" "/api/v1/data-explorer/addresses?limit=1" \
         /api/v1/coverage /api/v1/coverage/stats /api/v1/tags /api/v1/providers/ankr/status \
         /api/v1/count-oracle/status /api/v1/api-control/costs /api/v1/api-control/budgets \
         /api/v1/api-control/alerts /api/v1/tokens/enrichment /api/v1/methods/enrichment \
         /api/v1/admin/methods/backfill /api/v1/admin/token-metadata/backfill; do
  expect_code 401 "unauth GET $p" "$B$p"
done

# ---------- 2) 未认证响应体不得含租户字段 ----------
say ""
say "== 2) 未认证响应体不得泄露租户字段 =="
body="$(curl -s --max-time 20 "$B/api/v1/cases")"
case "$body" in
  *organization_id*|*workspace_id*) fail "未认证响应体出现 organization_id/workspace_id" ;;
  *) pass "未认证响应体无租户字段（$(echo -n "$body" | wc -c) 字节）" ;;
esac

# ---------- 3) 公开白名单必须保持 200 ----------
say ""
say "== 3) 公开白名单（契约必须保持）=="
for p in /api/v1/me /api/v1/capabilities /api/v1/chains/capabilities \
         /api/v1/methods/search /openapi.json /health/ready; do
  expect_code 200 "public GET $p" "$B$p"
done

# ---------- 4) 伪造头不得提权 ----------
say ""
say "== 4) 伪造 X-User-* 不得提权 =="
expect_code 401 "forged X-User-* /api/v1/cases" -H 'X-User-Id: 1' -H 'X-User-Role: owner' "$B/api/v1/cases"
expect_code 401 "forged X-User-* /api/v2/cases" -H 'X-User-Id: 1' -H 'X-User-Role: owner' "$B/api/v2/cases"
expect_code 401 "forged X-User-* /api/admin/v1/jobs" -H 'X-User-Id: 1' -H 'X-User-Role: owner' "$B/api/admin/v1/jobs"

# ---------- 5) GPT Actions 契约（独立 API Key，不得被 session 规则误伤）----------
say ""
say "== 5) GPT Actions 独立 API Key 契约 =="
expect_code 401 "Actions 无 Key" "$B/api/v1/address/overview?chain=ethereum&address=0x0000000000000000000000000000000000000000"
expect_code 401 "Actions 伪造 Key" -H 'Authorization: Bearer not-a-real-key' "$B/api/v1/address/overview?chain=ethereum&address=0x0000000000000000000000000000000000000000"
AK="${GPT_ACTIONS_API_KEY:-}"
if [ -z "$AK" ] && [ -f /home/euripides/projects/blockchain-platform/deploy/.env ]; then
  AK="$(grep -E '^GPT_ACTIONS_API_KEY=' /home/euripides/projects/blockchain-platform/deploy/.env | head -1 | cut -d= -f2- | tr -d '"')"
fi
if [ -n "$AK" ]; then
  c="$(code -H "Authorization: Bearer $AK" "$B/api/v1/address/overview?chain=ethereum&address=0x0000000000000000000000000000000000000000")"
  case "$c" in 200|404|503|422) pass "Actions 主 Key 正向（$c，非 401）" ;; 401) fail "Actions 主 Key 被 session 规则误伤（401）" ;; *) say "  INFO  Actions 主 Key -> $c" ;; esac
  rm -f "$EV/.body.$$"
else
  blk "未找到 GPT_ACTIONS_API_KEY：Actions 正向未验证"
fi

# ---------- 6) 真实租户隔离：两组织 × 真实对象 × 正负向对照 ----------
say ""
say "== 6) 真实租户隔离（org=default 的 owner vs org=acme 的 viewer/investigator）=="
# shellcheck disable=SC1091
[ -f /tmp/s6-e2e-env.sh ] && . /tmp/s6-e2e-env.sh
AE="${P0_ADMIN_EMAIL:-${ADMIN_EMAIL:-admin@local}}"
VE="${P0_VIEWER_EMAIL:-${VIEW_EMAIL:-fixture-viewer@fixture.local}}"
IE="${P0_INV_EMAIL:-${INV_EMAIL:-fixture-investigator@fixture.local}}"
AU="${P0_AUDITOR_EMAIL:-${AUDITOR_EMAIL:-fixture-auditor@fixture.local}}"
AP="${P0_ADMIN_PASSWORD:-${ADMIN_PASSWORD:-}}"
VP="${P0_VIEWER_PASSWORD:-${VIEWER_PASSWORD:-}}"
IP="${P0_INV_PASSWORD:-${INVESTIGATOR_PASSWORD:-}}"
AUP="${P0_AUDITOR_PASSWORD:-${AUDITOR_PASSWORD:-}}"

# DB 只读查询所需的连接参数（取自部署 .env；只用于 SELECT）
if [ -z "${POSTGRES_USER:-}" ] && [ -f "$R/deploy/.env" ]; then
  set -a; # shellcheck disable=SC1091
  . "$R/deploy/.env"; set +a
fi
PGQ() { docker exec -e "PGPASSWORD=${POSTGRES_PASSWORD:-}" deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain_platform}" -Atc "$1" 2>/dev/null; }

# --- 真实对象夹具：从库里取"确实存在且属于某组织"的 case / investigation ---
CASE_DEFAULT="$(PGQ "select c.id::text from cases c join organizations o on o.id=c.organization_id where o.slug='default' order by c.created_at desc limit 1")"
CASE_ACME="$(PGQ "select c.id::text from cases c join organizations o on o.id=c.organization_id where o.slug='acme' order by c.created_at desc limit 1")"
INV_ACME="$(PGQ "select inv.id::text from investigations inv join cases c on c.id=inv.case_id join organizations o on o.id=c.organization_id where o.slug='acme' order by inv.created_at desc limit 1")"
EID_ACME="$(PGQ "select ie.id::text from investigation_evidence ie join investigations inv on inv.id=ie.investigation_id join cases c on c.id=inv.case_id join organizations o on o.id=c.organization_id where o.slug='acme' order by ie.created_at desc limit 1")"

python3 - "$EV/tenant-fixtures-$TS.json" "$CASE_DEFAULT" "$CASE_ACME" "$INV_ACME" "$EID_ACME" <<'PY'
import json, sys
p, cd, ca, ia, ea = sys.argv[1:6]
json.dump({"case_org_default": cd, "case_org_acme": ca, "investigation_org_acme": ia,
           "evidence_org_acme": ea,
           "note": "均为库中真实存在且带 organization_id 的对象；用于证明跨组织隔离而不是'不存在 ID 404'"},
          open(p, "w"), indent=2, ensure_ascii=False)
PY
say "  夹具：default case=$CASE_DEFAULT ; acme case=$CASE_ACME ; acme inv=$INV_ACME ; acme evidence=$EID_ACME"
[ -n "$CASE_DEFAULT" ] && [ -n "$CASE_ACME" ] || fail "无法取得跨组织真实对象夹具（default/acme 各需至少 1 个 case）"
[ -n "$CASE_DEFAULT" ] && [ -n "$CASE_ACME" ] || { say "  跳过跨组织矩阵（缺夹具）"; }

# --- 角色权限集合取证（不得用超级 admin 冒称"无权限主体"）---
PGQ "select json_agg(t) from (select r.key as role, p.key as permission, p.sensitive from roles r join role_permissions rp on rp.role_id=r.id join permissions p on p.key=rp.permission_key where r.key in ('owner','viewer','investigator','auditor') order by 1,2) t" > "$EV/role-permissions-$TS.json"
AU_HAS_EVIDENCE_READ="$(PGQ "select count(*) from roles r join role_permissions rp on rp.role_id=r.id where r.key='auditor' and rp.permission_key='evidence:read'")"
AU_HAS_EVIDENCE_SENS="$(PGQ "select count(*) from roles r join role_permissions rp on rp.role_id=r.id where r.key='auditor' and rp.permission_key='evidence:sensitive:read'")"
say "  权限取证：auditor evidence:read=$AU_HAS_EVIDENCE_READ evidence:sensitive:read=$AU_HAS_EVIDENCE_SENS（$EV/role-permissions-$TS.json）"

login() { # login <jar> <email> <pass> <aud>
  curl -s -o /dev/null -w '%{http_code}' --max-time 20 -c "$1" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $4" \
    -d "{\"identifier\":\"$2\",\"password\":\"$3\",\"audience\":\"$4\"}" "$B/api/auth/v1/login"
}
csrf_of() { awk '!/^#/ && NF>=7 && $6 ~ /csrf/ {v=$7} END{if(v!="")print v}' "$1"; }
errcode() { python3 -c "import json,sys;d=json.load(open(sys.argv[1]));print(((d.get('error') or {}).get('code') or ''))" "$1" 2>/dev/null; }
req() { # req <jar> <aud> <method> <path> [extra...]  -> GOT / BODYF
  local jar="$1" aud="$2" m="$3" p="$4"; shift 4
  BODYF="$EV/.body.$$"
  GOT="$(curl -s -o "$BODYF" -w '%{http_code}' --max-time 25 -b "$jar" \
        -H "X-Auth-Audience: $aud" -X "$m" "$@" "$B$p" 2>/dev/null)"
}
# 正向可用性必须严格断言：非期望码一律 FAIL（不允许 INFO 放行）
must() { # must <want-csv> <label> <got>
  case ",$1," in *",$3,"*) pass "$2 -> $3" ;; *) fail "$2 -> got=$3 want=$1" ;; esac
}

JARD="$EV/.jard.$$"; JARA="$EV/.jara.$$"; JARI="$EV/.jari.$$"

if [ -n "$AP" ] && [ -n "$CASE_DEFAULT" ]; then
  c="$(login "$JARD" "$AE" "$AP" user)"
  [ "$c" = "200" ] || blk "owner(default) 登录失败 HTTP $c"
  if [ "$c" = "200" ]; then
    pass "owner(default) 登录 200（$AE / audience=user）"
    req "$JARD" user GET "/api/v2/cases/$CASE_DEFAULT"
    must 200 "正向：owner 读**本组织真实 case**" "$GOT"
    req "$JARD" user GET "/api/v2/cases/$CASE_ACME"
    must 403,404 "跨组织：owner(default) 读 **acme 真实 case**" "$GOT"
    req "$JARD" user GET "/api/v2/investigations/$INV_ACME"
    must 403,404 "跨组织：owner(default) 读 **acme 真实 investigation**" "$GOT"
    rm -f "$BODYF"
  fi
else
  blk "缺少 owner 凭证或 default 组织 case 夹具：跨组织正向/负向未验证"
fi

if [ -n "$VP" ] && [ -n "$CASE_ACME" ]; then
  c="$(login "$JARA" "$VE" "$VP" user)"
  if [ "$c" = "200" ]; then
    pass "viewer(acme) 登录 200（$VE）"
    req "$JARA" user GET "/api/v2/cases/$CASE_ACME"
    must 200 "正向：viewer 读**本组织真实 case**（证明 404 不是能力缺失）" "$GOT"
    req "$JARA" user GET "/api/v2/cases/$CASE_DEFAULT"
    must 403,404 "跨组织：viewer(acme) 读 **default 真实 case**" "$GOT"
    TOK="$(curl -s -o /dev/null -b "$JARA" -c "$JARA" "$B/api/v1/me"; csrf_of "$JARA")"
    if [ -n "$TOK" ]; then
      req "$JARA" user POST /api/v2/cases -H 'Content-Type: application/json' -H "X-CSRF-Token: $TOK" -d '{"name":"p0-probe-should-be-denied"}'
      EC="$(errcode "$BODYF")"
      case "$GOT:$EC" in
        403:csrf_failed) fail "Viewer 创建案件被 CSRF 拦下（未证明权限拒绝）" ;;
        403:*) pass "Viewer 创建案件 -> 403（error.code=$EC，权限拒绝）" ;;
        401) fail "Viewer -> 401（§27 要求 403）" ;;
        *) fail "Viewer 创建案件 -> got=$GOT code=$EC" ;;
      esac
    else
      blk "viewer 未取得 CSRF 令牌，权限 403 与 CSRF 403 无法区分"
    fi
    rm -f "$BODYF"
  else
    blk "viewer(acme) 登录失败 HTTP $c"
  fi
else
  blk "缺少 viewer 凭证或 acme 组织 case 夹具"
fi

if [ -n "$IP" ] && [ -n "$INV_ACME" ]; then
  c="$(login "$JARI" "$IE" "$IP" user)"
  if [ "$c" = "200" ]; then
    pass "investigator(acme) 登录 200（$IE）"
    req "$JARI" user GET "/api/v2/investigations/$INV_ACME"
    must 200 "正向：investigator 读**本组织真实 investigation**" "$GOT"
    req "$JARI" user GET "/api/v2/cases/$CASE_DEFAULT"
    must 403,404 "跨组织：investigator(acme) 读 **default 真实 case**" "$GOT"
    rm -f "$BODYF"
  else
    blk "investigator(acme) 登录失败 HTTP $c"
  fi
fi

# --- auditor：无 evidence:read 时读正文必须 403，且必须由**该主体自己的权限集合**支撑 ---
if [ -n "$AUP" ] && [ -n "$EID_ACME" ]; then
  if [ "$AU_HAS_EVIDENCE_READ" = "0" ]; then
    c="$(login "$JARI" "$AU" "$AUP" user)"
    if [ "$c" = "200" ]; then
      pass "auditor(acme) 登录 200（$AU）；实测其角色**不含** evidence:read"
      req "$JARI" user GET "/api/v2/evidence/$EID_ACME"
      must 403 "auditor（无 evidence:read）读 **真实 evidence 正文**" "$GOT"
      EC="$(errcode "$BODYF")"; say "  INFO  error.code=$EC"
      rm -f "$BODYF"
    else
      blk "auditor 登录失败 HTTP $c"
    fi
  else
    say "  INFO  auditor 角色实际**包含** evidence:read（$AU_HAS_EVIDENCE_READ）——该负向不适用，不用它冒称无权限主体"
    blk "没有可证实的'无 evidence 权限'主体：审计负向未验证"
  fi
else
  blk "缺少 auditor 凭证或 acme evidence 夹具：无 evidence 权限负向未验证"
fi

# --- 审计绑定：跨组织拒绝必须落 authorization 审计并带 request/trace ---
ACOLS="$(PGQ "select string_agg(column_name, ',') from information_schema.columns where table_name='audit_events_v2'")"
say ""
say "== 7) 拒绝事件审计绑定 =="
case "$ACOLS" in
  *request_id*|*trace_id*)
    ROW="$(PGQ "select action||'|'||coalesce(request_id::text,'')||'|'||coalesce(trace_id::text,'') from audit_events_v2 where action like 'authorization%' or action like '%denied%' order by seq desc limit 1")"
    if [ -n "$ROW" ]; then
      IFS='|' read -r ACT REQ TRC <<< "$ROW"
      [ -n "$REQ" ] && pass "最近拒绝事件 $ACT 带 request_id" || blk "最近拒绝事件 $ACT 的 request_id 为空"
      [ -n "$TRC" ] && pass "最近拒绝事件 $ACT 带 trace_id" || blk "最近拒绝事件 $ACT 的 trace_id 为空"
    else
      blk "audit_events_v2 中找不到 authorization/denied 事件"
    fi ;;
  *) blk "audit_events_v2 列结构不含 request_id/trace_id（实测列：$ACOLS）" ;;
esac

rm -f "$JARD" "$JARA" "$JARI" "$EV/.body.$$" 2>/dev/null || true

say ""
say "==== 结果：PASS=$PASS FAIL=$FAIL BLOCKED=$BLOCKED （$TS）===="
printf '{"passed":%d,"failed":%d,"blocked":%d,"ts":"%s","base":"%s","log":"%s","scope":"anonymous-path-closure + limited authenticated negatives; NOT full tenant-isolation acceptance","not_covered":["cross-org object negatives for job/report/export/status/ticket/canvas/investigation","restricted GPT Actions key cross-org","authorization.object_denied audit bound to request/trace","investigation runtime dataset default-org fallback","frontend admin audience adaptation"],"api_sha":"%s"}\n' \
  "$PASS" "$FAIL" "$BLOCKED" "$TS" "$B" "$LOG" "$(sha256sum "$R/backend/bin/api" 2>/dev/null | cut -d' ' -f1)" > "$EV/result-$TS.json"
say "result: $EV/result-$TS.json"
if [ "$FAIL" -gt 0 ] || [ "$BLOCKED" -gt 0 ]; then
  say "整体判定：FAIL/BLOCKED（不允许以缺失维度冒充通过）" >&2
  exit 1
fi
say "整体判定：PASS"
exit 0
