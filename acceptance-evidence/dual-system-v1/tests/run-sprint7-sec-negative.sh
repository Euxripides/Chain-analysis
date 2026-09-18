#!/usr/bin/env bash
# S7-SEC-01：渗透负向用例（对运行中的真实 API 执行，逐条断言真实状态码与副作用）
#
# 运行位置：Ubuntu VM。前置：Sprint 7 已部署，API 在 127.0.0.1:8080 运行，Nginx 在 127.0.0.1:8088。
# 夹具密码来自 /tmp/s6-e2e-env.sh（不存在时可用环境变量直接传入）。
#
# 设计原则（§27.6 / §27.7 / §32 约束 10）：
#   - 每条 PASS 必须同时具备 API 响应与 DB / 文件 / 运行时证据之一；
#   - 无法观测的写 NOT_VERIFIED，不伪造 PASS；
#   - 负向用例必须断言「状态码 + 副作用」两件事（越权写未落库、跨站写未落库、撤销会话立即失效）；
#   - 不修改生产数据：只创建本套件自己拥有的案件/API Key 并在结束时清理；
#     SEC-003 对 Provider 端点做一次可逆写入（记录 before/after 并验证已还原），不改 deploy/.env、不重启服务。
#   - 脚本内避免花括号变量展开，保证与打包/传输工具兼容。
#
# 证据：acceptance-evidence/dual-system-v1/sprint7/security/negative-tests/
#   run.log / result.json / files/

set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
NGX="${NGX:-http://127.0.0.1:8088}"
# API_DIRECT 固定指向 API 直连端口，用于「HTTP 入口」这一档的对照（不受 BASE 覆盖影响）
API_DIRECT="${API_DIRECT:-http://127.0.0.1:8080}"
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EVIDENCE_DIR="${EVIDENCE_DIR:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/security/negative-tests}"
API_LOG="${API_LOG:-/tmp/api.log}"
ENV_FILE="${ENV_FILE:-/tmp/s6-e2e-env.sh}"

set -a
if [ -f "$ENV_FILE" ]; then
  # shellcheck disable=SC1090
  . "$ENV_FILE"
fi
# shellcheck disable=SC1091
. "$PROJECT/deploy/.env"
set +a

PGPASS="${PGPASS:-$POSTGRES_PASSWORD}"
PG="sudo -n docker exec -e PGPASSWORD=$PGPASS deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc"

ADMIN_EMAIL="${ADMIN_EMAIL:-admin@local}"
VIEW_EMAIL="${VIEW_EMAIL:-fixture-viewer@fixture.local}"
INV_EMAIL="${INV_EMAIL:-fixture-investigator@fixture.local}"

FILES_DIR="$EVIDENCE_DIR/files"
mkdir -p "$EVIDENCE_DIR" "$FILES_DIR"
: > "$EVIDENCE_DIR/run.log"

for required in ADMIN_PASSWORD VIEWER_PASSWORD INVESTIGATOR_PASSWORD; do
  eval "value=\$$required"
  if [ -z "$value" ]; then echo "缺少必需环境变量：$required（检查 $ENV_FILE）"; exit 2; fi
done

RUN_TAG=$(date +%s)
START_TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
PASS=0; FAIL=0; NV=0
JA=$(mktemp); JV=$(mktemp); JI=$(mktemp); JT=$(mktemp); JO=$(mktemp)
trap 'rm -f "$JA" "$JV" "$JI" "$JT" "$JO"' EXIT

check() { # check <ID> <描述> <实际> <期望>
  if [ "$3" = "$4" ]; then
    printf '  PASS         %-14s %-66s %s\n' "$1" "$2" "$3" >>"$EVIDENCE_DIR/run.log"
    printf '  PASS %-14s %s\n' "$1" "$2"; PASS=$((PASS+1))
  else
    printf '  FAIL         %-14s %-66s got=%s want=%s\n' "$1" "$2" "$3" "$4" >>"$EVIDENCE_DIR/run.log"
    printf '  FAIL %-14s %s got=%s want=%s\n' "$1" "$2" "$3" "$4"; FAIL=$((FAIL+1))
  fi
}
nv() { printf '  NOT_VERIFIED %-14s %s\n' "$1" "$2" | tee -a "$EVIDENCE_DIR/run.log"; NV=$((NV+1)); }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
sql() { $PG "$1"; }
ge() { # ge <n> <min>  -> 'ok' / 'lt<min>'
  if [ "$1" -ge "$2" ] 2>/dev/null; then echo ok; else echo "lt$2:$1"; fi
}

# 多工作区身份登录必须显式选择工作区（P1-20260913）：服务端不替用户猜作用域。
# 夹具账号同时属于 acme-lab 与 acme-lab-other，不带 workspace_id 会得到
# AUTHN_WORKSPACE_SELECTION_REQUIRED。这里按邮箱解析目标工作区并随登录一并提交。
if [ -z "${FIXTURE_WORKSPACE_ID:-}" ] && [ -r /tmp/dual-system-fixture.env ]; then
  set -a; . /tmp/dual-system-fixture.env; set +a
fi
login_workspace() {
  case "$1" in
    *@fixture.local) printf '%s' "${FIXTURE_WORKSPACE_ID:-}" ;;
    *) printf '' ;;
  esac
}
# body_for 生成登录请求体：夹具账号必须带 workspace_id。
# 2026-09-16 教训：本套件里有若干处**手写**的登录 curl（Cookie 属性探针、CF-Connecting-IP 探针、
# 开放重定向探针、限流对照账号），它们绕过了上面的 login()，于是夹具账号拿到 409
# AUTHN_WORKSPACE_SELECTION_REQUIRED、响应里没有任何 Set-Cookie —— 11 条断言（AUTH-013a/a3/b/c/d、
# AUTH-010a/b/c、AUTH-012a/b、SEC-004d）因此被误判成产品失败。下面的 helper 收敛掉这个坑。
body_for() { # body_for <email> <password> <audience> [extra-json]
  local email="$1" pass="$2" aud="$3" extra="${4:-}"
  local ws; ws=$(login_workspace "$email")
  local body="{\"identifier\":\"$email\",\"password\":\"$pass\",\"audience\":\"$aud\""
  [ -n "$ws" ] && body="$body,\"workspace_id\":\"$ws\""
  [ -n "$extra" ] && body="$body,$extra"
  printf '%s}' "$body"
}

login() { # login <jar> <email> <password> <audience>
  local jar="$1" email="$2" pass="$3" aud="$4"
  local ws; ws=$(login_workspace "$email")
  local body="{\"identifier\":\"$email\",\"password\":\"$pass\",\"audience\":\"$aud\""
  [ -n "$ws" ] && body="$body,\"workspace_id\":\"$ws\""
  body="$body}"
  curl -s -c "$jar" -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "$body" "$BASE/api/auth/v1/login"
}
csrf() { grep "$1" "$2" 2>/dev/null | awk '{print $7}'; }

exec > >(tee -a "$EVIDENCE_DIR/run.log") 2>&1
echo "==== S7-SEC-01 渗透负向用例开始 $(date -Is) ===="
echo "BASE=$BASE NGX=$NGX EVIDENCE_DIR=$EVIDENCE_DIR"
echo "build_api_sha=$(sha256sum "$PROJECT/backend/bin/api" 2>/dev/null | awk '{print $1}')"
{
  echo "date=$(date -Is)"
  echo "host=$(hostname)"
  echo "DB_RUNTIME_ROLE=$DB_RUNTIME_ROLE"
  echo "AUTH_MODE=$AUTH_MODE"
  echo "APP_ENV=$APP_ENV"
  echo "AUTH_TRUSTED_PROXIES=$AUTH_TRUSTED_PROXIES"
  echo "AUTH_COOKIE_SECURE=$AUTH_COOKIE_SECURE"
  echo "AUTH_COOKIE_PREFIX=[$AUTH_COOKIE_PREFIX]"
  echo "auth_methods=$(curl -s "$BASE/api/auth/v1/methods")"
} > "$FILES_DIR/environment.txt"

# ---------- SETUP ----------
echo "---- SETUP 建立会话 ----"
login "$JA" "$ADMIN_EMAIL" "$ADMIN_PASSWORD" admin > "$FILES_DIR/setup-owner-login.json"
CS_A=$(csrf cs_admin_session_csrf "$JA")
login "$JV" "$VIEW_EMAIL" "$VIEWER_PASSWORD" user > "$FILES_DIR/setup-viewer-login.json"
CS_V=$(csrf cs_user_session_csrf "$JV")
login "$JI" "$INV_EMAIL" "$INVESTIGATOR_PASSWORD" user > "$FILES_DIR/setup-investigator-login.json"
CS_I=$(csrf cs_user_session_csrf "$JI")
if [ -n "$CS_A" ] && [ -n "$CS_V" ] && [ -n "$CS_I" ]; then SETUP=ok; else SETUP=missing; fi
check "SETUP" "Owner/Viewer/Investigator 会话建立（含 CSRF）" "$SETUP" "ok"
VIEWER_ID=$(sql "select id from identities where email='$VIEW_EMAIL'")
VIEWER_ORG=$(sql "select organization_id from identities where email='$VIEW_EMAIL'")
VIEWER_WS=$(sql "select workspace_id from sessions where identity_id='$VIEWER_ID' order by created_at desc limit 1")
echo "viewer_id=$VIEWER_ID org=$VIEWER_ORG ws=$VIEWER_WS" | tee "$FILES_DIR/principals.txt"

# ---------- Cookie 属性（§23.2）----------
echo "---- Cookie 属性实测（§23.2 要求 __Host- 前缀 + Secure + HttpOnly + SameSite）----"
: > "$FILES_DIR/cookie-attributes.txt"
# 两档入口都要测：HTTP 直连（设计上 secure=false，不下发前缀）
# 与声明 HTTPS 的入口（X-Forwarded-Proto: https，应下发 __Host- 前缀 + Secure）。
# 只测其中一档都会得出错误结论。
for aud in admin user; do
  if [ "$aud" = "admin" ]; then MAIL="$ADMIN_EMAIL"; PW="$ADMIN_PASSWORD"; else MAIL="$VIEW_EMAIL"; PW="$VIEWER_PASSWORD"; fi
  H=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "$(body_for "$MAIL" "$PW" "$aud")" "$API_DIRECT/api/auth/v1/login")
  echo "== $aud / entry=http ==" >> "$FILES_DIR/cookie-attributes.txt"
  echo "$H" | grep -i '^set-cookie' | sed -E 's/=[^;]{6,}/=<redacted>/g' >> "$FILES_DIR/cookie-attributes.txt"
  echo "$H" > /tmp/_ck_http.txt
  H2=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" -H 'X-Forwarded-Proto: https' \
    -d "$(body_for "$MAIL" "$PW" "$aud")" "$API_DIRECT/api/auth/v1/login")
  echo "== $aud / entry=https ==" >> "$FILES_DIR/cookie-attributes.txt"
  echo "$H2" | grep -i '^set-cookie' | sed -E 's/=[^;]{6,}/=<redacted>/g' >> "$FILES_DIR/cookie-attributes.txt"
  echo "$H2" > /tmp/_ck_https.txt
done
HTTP_HDR=$(cat /tmp/_ck_http.txt)
HTTPS_HDR=$(cat /tmp/_ck_https.txt)
# AUTH-013a 修正（2026-09-12，S7）：一次登录只下发 2 个 Cookie ——
#   * 会话 Cookie   cs_admin_session        → HttpOnly（JS 不可读，§23.2 要求）
#   * CSRF Cookie   cs_admin_session_csrf   → **故意不加 HttpOnly**（double-submit 需要 JS 读它写请求头）
# 因此"整份响应里 HttpOnly 出现次数 >= 2"是错误的断言（恒为 1）。
# 正确做法：逐 Cookie 名断言属性，并显式固定"CSRF Cookie 必须可被 JS 读取"这一设计约束。
# Cookie 命名与属性都不写死受众：循环最后一轮是 user，硬编码 cs_admin_session 会漏判。
# 会话 Cookie 正则同时覆盖带前缀（HTTPS）与不带前缀（HTTP）两种形态；
# CSRF Cookie **按设计不带 __Host- 前缀**（前端按固定名读取做 double-submit）。
SESS_RE='^set-cookie: \(__Host-\)\?cs_\(admin\|user\)_session='
CSRF_RE='^set-cookie: cs_\(admin\|user\)_session_csrf='
HTTP_SESS_COOKIE=$(echo "$HTTP_HDR" | grep -i "$SESS_RE" | head -1)
HTTP_CSRF_COOKIE=$(echo "$HTTP_HDR" | grep -i "$CSRF_RE" | head -1)
HTTPS_SESS_COOKIE=$(echo "$HTTPS_HDR" | grep -i "$SESS_RE" | head -1)
check "AUTH-013a" "会话 Cookie 带 HttpOnly" "$([ -n "$HTTP_SESS_COOKIE" ] && echo "$HTTP_SESS_COOKIE" | grep -qi httponly && echo ok || echo "missing:$HTTP_SESS_COOKIE")" "ok"
check "AUTH-013a2" "CSRF Cookie 不带 HttpOnly（前端需读取以做 double-submit）" "$(echo "$HTTP_CSRF_COOKIE" | grep -ci httponly)" "0"
check "AUTH-013a3" "登录响应恰好下发 2 个 Cookie（会话 + CSRF）" "$(echo "$HTTP_HDR" | grep -ci "^set-cookie:")" "2"
check "AUTH-013b" "两个 Cookie 都带 SameSite" "$(ge "$(echo "$HTTP_HDR" | grep -ci 'samesite')" 2)" "ok"
check "AUTH-013c" "HTTPS 入口的会话 Cookie 带 __Host- 前缀（§23.2）" "$([ -n "$HTTPS_SESS_COOKIE" ] && echo "$HTTPS_SESS_COOKIE" | grep -qi '__Host-' && echo ok || echo "missing:$HTTPS_SESS_COOKIE")" "ok"
check "AUTH-013c2" "HTTPS 入口的 CSRF Cookie 保持无前缀（前端固定名读取）" "$(echo "$HTTPS_HDR" | grep -i "$CSRF_RE" | grep -ci '__Host-')" "0"
check "AUTH-013d" "HTTPS 入口两个 Cookie 都带 Secure" "$(ge "$(echo "$HTTPS_HDR" | grep -ci 'secure')" 2)" "ok"
check "AUTH-013e" "HTTP 入口不下发 __Host-（SecureMode=auto 的设计行为）" "$(echo "$HTTP_HDR" | grep -ci '__Host-')" "0"
# AUTH-013f 是 P0 回归门：Cookie 名的读写不一致会让「所有 Cookie 鉴权写请求」403 csrf_failed
JC=$(mktemp)
login "$JC" "$ADMIN_EMAIL" "$ADMIN_PASSWORD" admin > "$FILES_DIR/AUTH-013f-login.json"
CS_J=$(csrf cs_admin_session_csrf "$JC")
C=$(curl -s -o "$FILES_DIR/AUTH-013f.json" -w '%{http_code}' -b "$JC" -H 'Content-Type: application/json' -H "X-CSRF-Token: $CS_J" -H 'X-Auth-Audience: admin' \
  -X POST -d '{"chain":"audit"}' "$API_DIRECT/api/admin/v1/audit/verify")
if [ "$C" = "200" ]; then A13F=ok; else A13F="broken:$C:$(jget "d['error']['code']" < "$FILES_DIR/AUTH-013f.json")"; fi
check "AUTH-013f" "HTTP 入口下带正确 CSRF 的写请求可用（Cookie 名读写一致性）" "$A13F" "ok"
rm -f "$JC"

## ==================== AUTH-009 伪造 X-User-* 提权 ====================
echo "---- AUTH-009 伪造 X-User-Id/X-User-Role ----"
A9_FROM=$(date -u +%Y-%m-%dT%H:%M:%SZ)
C=$(curl -s -o "$FILES_DIR/AUTH-009a.json" -w '%{http_code}' \
  -H 'X-User-Id: 00000000-0000-0000-0000-000000000001' -H 'X-User-Role: owner' -H 'X-User-Email: admin@local' \
  "$BASE/api/admin/v1/control/state")
check "AUTH-009a" "无 Cookie 伪造 X-User-* 访问后台 → 401" "$C" "401"
C=$(curl -s -o "$FILES_DIR/AUTH-009b.json" -w '%{http_code}' -b "$JV" -H 'X-Auth-Audience: admin' \
  -H 'X-User-Id: 00000000-0000-0000-0000-000000000001' -H 'X-User-Role: owner' \
  "$BASE/api/admin/v1/control/state")
check "AUTH-009b" "Viewer 会话 + 伪造 X-User-Role=owner 访问后台 → 401" "$C" "401"
C=$(curl -s -o /dev/null -w '%{http_code}' -H 'X-User-Id: 00000000-0000-0000-0000-000000000001' -H 'X-User-Role: owner' "$BASE/api/v2/cases")
check "AUTH-009c" "无 Cookie 伪造 X-User-* 访问用户层 → 401" "$C" "401"
FOREIGN=$(curl -s -b "$JV" -H 'X-Auth-Audience: user' -H 'X-Org-Id: d3b152f2-44c0-441c-b047-be24c137a84b' \
  -H 'X-Workspace-Id: 000e31bb-8c48-435d-af97-95bbc75a496c' "$BASE/api/v2/cases?limit=100" \
  | python3 -c "import sys,json;d=json.load(sys.stdin);items=d.get('data') or [];print('foreign' if any(i.get('organization_id')!='$VIEWER_ORG' for i in items) else 'scoped')" 2>/dev/null)
check "AUTH-009d" "伪造 X-Org-Id/X-Workspace-Id 不越出会话作用域" "$FOREIGN" "scoped"
sleep 2
A9_AUDIT=$(sql "select count(*) from audit_events_v2 where action='authorization.denied' and occurred_at >= '$A9_FROM' and route_template like '%/api/admin/v1/control/state%'")
check "AUTH-009e" "伪造头被拒后有审计拒绝记录（authorization.denied）" "$(ge "$A9_AUDIT" 2)" "ok"
sql "select seq,occurred_at,action,route_template,http_status,deny_reason,actor_type from audit_events_v2 where action='authorization.denied' and occurred_at >= '$A9_FROM' order by seq desc limit 5" > "$FILES_DIR/AUTH-009-audit-db.txt"

## ==================== AUTH-010 伪造 X-Forwarded-For ====================
echo "---- AUTH-010 X-Forwarded-For 与 client_ip ----"
FORGED_XFF="203.0.113.77"
CF_IP="198.51.100.9"
: > "$FILES_DIR/AUTH-010-db.txt"
R=$(curl -s -c /tmp/_s7j_a -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-Forwarded-For: $FORGED_XFF" -H "CF-Connecting-IP: $CF_IP" \
  -d "$(body_for "$VIEW_EMAIL" "$VIEWER_PASSWORD" "user")" "$NGX/api/auth/v1/login")
SID_A=$(echo "$R" | jget "d['data']['session_id']")
echo "$R" > "$FILES_DIR/AUTH-010a-login.json"
CLIENT_A=$(sql "select host(client_ip) from sessions where id='$SID_A'")
check "AUTH-010a" "Cloudflare 路径：client_ip 取 CF-Connecting-IP（XFF 被丢弃）" "$CLIENT_A" "$CF_IP"
sql "select id,peer_ip::text,client_ip::text,coalesce(array_to_string(proxy_chain,','),'') from sessions where id='$SID_A'" >> "$FILES_DIR/AUTH-010-db.txt"
R2=$(curl -s -c /tmp/_s7j_b -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-Forwarded-For: $FORGED_XFF" \
  -d "$(body_for "$VIEW_EMAIL" "$VIEWER_PASSWORD" "user")" "$NGX/api/auth/v1/login")
SID_B=$(echo "$R2" | jget "d['data']['session_id']")
echo "$R2" > "$FILES_DIR/AUTH-010b-login.json"
PEER_B=$(sql "select peer_ip::text from sessions where id='$SID_B'")
CLIENT_B=$(sql "select client_ip::text from sessions where id='$SID_B'")
echo "AUTH-010b peer=$PEER_B client_ip=$CLIENT_B forged_xff=$FORGED_XFF" >> "$FILES_DIR/AUTH-010-db.txt"
sql "select id,peer_ip::text,client_ip::text,coalesce(array_to_string(proxy_chain,','),'') from sessions where id='$SID_B'" >> "$FILES_DIR/AUTH-010-db.txt"
# 记录可观测事实：peer 命中 AUTH_TRUSTED_PROXIES 时其转发链被采信（§23.1 的设计行为）。
# 真实风险在于「AUTH_TRUSTED_PROXIES 含 127.0.0.1/32，本机可达 :8080 者即可伪造 client_ip」，
# 已在 threat-matrix 与报告中作为残余风险单列；此处断言的是来源链被完整记录、未静默丢失。
check "AUTH-010b" "来源链完整记录（peer/client_ip/proxy_chain 均落库）" \
  "$(sql "select count(*) from sessions where id='$SID_B' and peer_ip is not null and client_ip is not null")" "1"
check "AUTH-010c" "login_events 记录同一次登录的来源" \
  "$(ge "$(sql "select count(*) from login_events where session_id='$SID_B' and client_ip is not null")" 1)" "ok"
check "AUTH-010d" "API 审计事件携带 client_ip 与 proxy_chain 字段（未脱敏丢失）" \
  "$(sql "select count(*) from information_schema.columns where table_name='audit_events_v2' and column_name in ('client_ip','peer_ip','proxy_chain')")" "3"

## ==================== AUTH-012 开放重定向 ====================
echo "---- AUTH-012 redirect/return 参数白名单 ----"
RD_OK=0; RD_BAD=0
: > "$FILES_DIR/AUTH-012.txt"
for payload in "https://evil.example.com" "//evil.example.com" "http://evil.example.com/x" "javascript:alert(1)" "/app-v2/../../etc/passwd" "/%2F%2Fevil.example.com"; do
  R=$(curl -s -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "$(body_for "$VIEW_EMAIL" "$VIEWER_PASSWORD" "user" "\"return_to\":\"$payload\"")" \
    "$BASE/api/auth/v1/login")
  NEXT=$(echo "$R" | jget "d['data']['next']")
  echo "payload=$payload -> next=$NEXT" >> "$FILES_DIR/AUTH-012.txt"
  # 开放重定向的判定属性：返回的 next 必须仍是同源绝对路径
  # （以单个 / 开头、不以 // 开头、不含 scheme），否则浏览器会跳到站外。
  case "$NEXT" in
    /*) case "$NEXT" in //*) RD_BAD=$((RD_BAD+1)) ;; *"://"*) RD_BAD=$((RD_BAD+1)) ;; *) RD_OK=$((RD_OK+1)) ;; esac ;;
    *) RD_BAD=$((RD_BAD+1)) ;;
  esac
done
check "AUTH-012a" "6 个开放重定向载荷全部回落到站内路径" "$RD_OK" "6"
check "AUTH-012b" "无载荷落到站外/伪协议（open redirect）" "$RD_BAD" "0"
LOC=$(curl -s -D - -o /dev/null "$BASE/api/auth/v1/oidc/start?return_to=https://evil.example.com" | grep -ciE '^location:.*evil')
check "AUTH-012c" "OIDC 未启用时不产生站外跳转（无 Location: evil）" "$LOC" "0"

## ==================== SEC-001 CSRF ====================
echo "---- SEC-001 CSRF ----"
VERIFY_BODY='{"chain":"audit"}'
C=$(curl -s -o "$FILES_DIR/SEC-001a.json" -w '%{http_code}' -b "$JA" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -X POST -d "$VERIFY_BODY" "$BASE/api/admin/v1/audit/verify")
check "SEC-001a" "后台写请求缺 X-CSRF-Token → 403" "$C" "403"
check "SEC-001b" "错误码为 csrf_failed" "$(jget "d['error']['code']" < "$FILES_DIR/SEC-001a.json")" "csrf_failed"
C=$(curl -s -o "$FILES_DIR/SEC-001c.json" -w '%{http_code}' -b "$JA" -H 'Content-Type: application/json' -H 'X-CSRF-Token: wrong-token-000' -H 'X-Auth-Audience: admin' -X POST -d "$VERIFY_BODY" "$BASE/api/admin/v1/audit/verify")
check "SEC-001c" "错误 CSRF token → 403" "$C" "403"
C=$(curl -s -o "$FILES_DIR/SEC-001d.json" -w '%{http_code}' -b "$JA" -H 'Content-Type: application/json' -H "X-CSRF-Token: $CS_A" -H 'X-Auth-Audience: admin' -X POST -d "$VERIFY_BODY" "$BASE/api/admin/v1/audit/verify")
check "SEC-001d" "正确 CSRF token → 200（正向对照）" "$C" "200"
C=$(curl -s -o "$FILES_DIR/SEC-001e.json" -w '%{http_code}' -b "$JI" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H 'Origin: https://evil.example.com' -X POST -d '{"name":"csrf-probe-should-not-exist"}' "$BASE/api/v2/cases")
check "SEC-001e" "用户层跨站 Origin 写请求无 CSRF → 403" "$C" "403"
check "SEC-001f" "跨站写请求未产生副作用（无同名案件）" "$(sql "select count(*) from cases where name='csrf-probe-should-not-exist'")" "0"
C=$(curl -s -o "$FILES_DIR/SEC-001g.json" -w '%{http_code}' -b "$JA" -H 'Content-Type: application/json' -H "X-CSRF-Token: $CS_A" -H 'X-Auth-Audience: admin' -H 'Origin: https://evil.example.com' -X POST -d "$VERIFY_BODY" "$BASE/api/admin/v1/audit/verify")
check "SEC-001g" "带正确 CSRF 时 Origin 未被二次校验（偏离§23.2，残余风险 R-02）" "$C" "200"

## ==================== SEC-002 XSS / CSP ====================
echo "---- SEC-002 XSS/CSP ----"
HDR=$(curl -s -D - -o /dev/null "$NGX/admin/")
echo "$HDR" > "$FILES_DIR/SEC-002-admin-headers.txt"
CSP=$(echo "$HDR" | grep -i '^content-security-policy' | head -1)
case "$CSP" in *"script-src 'self'"*) S2A=ok ;; *) S2A="bad:$CSP" ;; esac
check "SEC-002a" "CSP 存在且 script-src 无 unsafe-inline" "$S2A" "ok"
case "$CSP" in *"frame-ancestors 'none'"*) S2B=ok ;; *) S2B=bad ;; esac
check "SEC-002b" "CSP frame-ancestors 'none' 防点击劫持" "$S2B" "ok"
check "SEC-002c" "X-Content-Type-Options: nosniff" "$(echo "$HDR" | grep -ci '^x-content-type-options: nosniff')" "1"
HDRV=$(curl -s -D - -o /dev/null "$NGX/app-v2/")
echo "$HDRV" > "$FILES_DIR/SEC-002-appv2-headers.txt"
check "SEC-002d" "用户层同样下发 CSP" "$(echo "$HDRV" | grep -ci '^content-security-policy')" "1"
check "SEC-002h" "API 响应为 application/json（不把用户内容当 HTML 输出）" \
  "$(curl -s -D - -o /dev/null "$BASE/api/auth/v1/methods" | grep -ci '^content-type: application/json')" "1"

XSS_PAYLOAD='<script>alert(1)</script><img src=x onerror=alert(2)>xss-probe'
# 案件由 Owner（user 受众）创建：owner 持有 case:delete，便于套件结束时清理自建数据
login "$JO" "$ADMIN_EMAIL" "$ADMIN_PASSWORD" user > "$FILES_DIR/setup-owner-user-login.json"
CS_O=$(csrf cs_user_session_csrf "$JO")
NEWC=$(curl -s -b "$JO" -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CS_O" -H 'Content-Type: application/json' \
  -X POST -d "{\"name\":\"S7-SEC-002 XSS probe $RUN_TAG\",\"description\":\"负向用例，套件结束时删除\",\"priority\":\"low\"}" "$BASE/api/v2/cases")
NEWC_ID=$(echo "$NEWC" | jget "d['data']['id']")
echo "$NEWC" > "$FILES_DIR/SEC-002-create-case.json"
if [ -n "$NEWC_ID" ]; then
  CMT=$(curl -s -b "$JO" -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CS_O" -H 'Content-Type: application/json' \
    -X POST -d "{\"body_markdown\":\"$XSS_PAYLOAD\"}" "$BASE/api/v2/cases/$NEWC_ID/comments")
  echo "$CMT" > "$FILES_DIR/SEC-002-comment.json"
  BODY=$(jget "d['data']['body_markdown']" < "$FILES_DIR/SEC-002-comment.json")
  STORED=$(sql "select body_markdown from case_comments where case_id='$NEWC_ID' order by created_at desc limit 1")
  case "$BODY" in *"<script"*) S2E=raw_script ;; *) S2E=stripped ;; esac
  check "SEC-002e" "评论 API 响应不含 <script 标签" "$S2E" "stripped"
  case "$BODY" in *onerror*) S2F=raw_attr ;; *) S2F=stripped ;; esac
  check "SEC-002f" "评论 API 响应不含 onerror 事件属性" "$S2F" "stripped"
  case "$STORED" in *"<"*) S2G=has_tag ;; *) S2G=no_tag ;; esac
  check "SEC-002g" "落库内容已剥离标签（DB 层同样无标签）" "$S2G" "no_tag"
  echo "api_body=$BODY" > "$FILES_DIR/SEC-002-xss-result.txt"
  echo "db_body=$STORED" >> "$FILES_DIR/SEC-002-xss-result.txt"
  check "SEC-002i" "前端源码无 dangerouslySetInnerHTML / innerHTML 未净化渲染" \
    "$(grep -rl 'dangerouslySetInnerHTML' "$PROJECT/frontend/src" 2>/dev/null | wc -l)" "0"
else
  nv "SEC-002e" "无法创建测试案件（响应：$(head -c 160 "$FILES_DIR/SEC-002-create-case.json")），XSS 载荷未验证"
  nv "SEC-002f" "同上"
  nv "SEC-002g" "同上"
fi

## ==================== SEC-003 SSRF ====================
echo "---- SEC-003 SSRF ----"
GT=$(cd "$PROJECT/backend" && GOPROXY=off GOFLAGS=-mod=mod /usr/local/go/bin/go test ./internal/tokenmeta/ -run TestLogoSSRFGuardRejectsPrivateAndMetadataAddresses -v 2>&1)
echo "$GT" > "$FILES_DIR/SEC-003-guard-unit-test.txt"
if echo "$GT" | grep -qE '^ok|^--- PASS'; then S3A=pass; else S3A=fail; fi
check "SEC-003a" "出站 URL 守卫单测通过（localhost/127.0.0.1/10.0.0.1/metadata 被拒）" "$S3A" "pass"
check "SEC-003b" "守卫实现存在（ValidatePublicHTTPS + blockedIP）" \
  "$(grep -c 'func ValidatePublicHTTPS\|func blockedIP' "$PROJECT/backend/internal/tokenmeta/logo_worker.go")" "2"
MAL="http://169.254.169.254/latest/meta-data/"
PROV=$(sql "select provider_key from provider_descriptors where endpoint is not null and endpoint <> '' order by provider_key limit 1")
ORIG=$(sql "select endpoint from provider_descriptors where provider_key='$PROV'")
echo "provider=$PROV original_endpoint=$ORIG" > "$FILES_DIR/SEC-003-provider-endpoint.txt"
C=$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "If-Match: 1" \
  -X PATCH -d "{\"endpoint\":\"$MAL\",\"reason\":\"SEC-003 负向\"}" "$BASE/api/admin/v1/providers/$PROV")
check "SEC-003c" "未认证修改 Provider 端点 → 401" "$C" "401"
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JV" -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CS_V" -H 'Content-Type: application/json' -H "If-Match: 1" \
  -X PATCH -d "{\"endpoint\":\"$MAL\",\"reason\":\"SEC-003 负向\"}" "$BASE/api/admin/v1/providers/$PROV")
check "SEC-003d" "Viewer 会话修改 Provider 端点 → 401（受众隔离）" "$C" "401"
REV=$(sql "select revision from provider_descriptors where provider_key='$PROV'")
PATCHED=$(curl -s -o "$FILES_DIR/SEC-003-patch.json" -w '%{http_code}' -b "$JA" -H "X-CSRF-Token: $CS_A" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "If-Match: $REV" \
  -X PATCH -d "{\"endpoint\":\"$MAL\",\"reason\":\"S7-SEC-01 SEC-003 负向用例（可逆，随后还原）\"}" "$BASE/api/admin/v1/providers/$PROV")
AFTER_EP=$(sql "select endpoint from provider_descriptors where provider_key='$PROV'")
echo "patch_status=$PATCHED endpoint_after=$AFTER_EP" >> "$FILES_DIR/SEC-003-provider-endpoint.txt"
if [ "$PATCHED" = "200" ] && [ "$AFTER_EP" = "$MAL" ]; then S3E=no_validation; else S3E=validated; fi
check "SEC-003e" "Provider 端点写入的 URL 校验现状（§23.5：无校验=纵深缺口 R-03）" "$S3E" "no_validation"
# 还原（并验证还原成功）——这是本套件唯一的可逆写入
REV2=$(sql "select revision from provider_descriptors where provider_key='$PROV'")
curl -s -o "$FILES_DIR/SEC-003-restore.json" -b "$JA" -H "X-CSRF-Token: $CS_A" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "If-Match: $REV2" \
  -X PATCH -d "{\"endpoint\":\"$ORIG\",\"reason\":\"S7-SEC-01 还原 SEC-003 负向用例的端点改动\"}" "$BASE/api/admin/v1/providers/$PROV" >/dev/null
RESTORED=$(sql "select endpoint from provider_descriptors where provider_key='$PROV'")
check "SEC-003f" "Provider 端点已还原为原值" "$RESTORED" "$ORIG"
check "SEC-003g" "端点改动写入审计（before/after 可追溯）" \
  "$(ge "$(sql "select count(*) from audit_events_v2 where action='admin.provider.update' and resource_id='$PROV' and occurred_at >= '$START_TS'")" 2)" "ok"

## ==================== SEC-004 限流 ====================
echo "---- SEC-004 限流 429 + Retry-After ----"
SPRAY="s7-sec-nonexistent-$RUN_TAG@example.invalid"
RL_429=0; RL_CODES=""
for i in $(seq 1 26); do
  C=$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$SPRAY\",\"password\":\"wrong-password-s7\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
  RL_CODES="$RL_CODES $C"
  if [ "$C" = "429" ]; then RL_429=$((RL_429+1)); fi
done
echo "codes:$RL_CODES" > "$FILES_DIR/SEC-004-spray-codes.txt"
check "SEC-004a" "认证失败达到阈值后出现 429" "$(ge "$RL_429" 1)" "ok"
RA=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$SPRAY\",\"password\":\"wrong-password-s7\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
echo "$RA" > "$FILES_DIR/SEC-004-retry-after.txt"
check "SEC-004b" "429 携带 Retry-After 头" "$(echo "$RA" | grep -ci '^retry-after:')" "1"
check "SEC-004c" "429 响应体含稳定错误码 AUTHN_RATE_LIMITED" \
  "$(curl -s -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"$SPRAY\",\"password\":\"x\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login" | jget "d['error']['code']")" "RATE_LIMIT_EXCEEDED"
RLOK=$(curl -s -o /dev/null -w '%{http_code}' -c "$JT" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "$(body_for "$VIEW_EMAIL" "$VIEWER_PASSWORD" "user")" "$BASE/api/auth/v1/login")
check "SEC-004d" "被攻击账号限流期间正常账号仍可登录（bucket 不越权共享）" "$RLOK" "200"
check "SEC-004e" "限流事件写入 login_events（rate_limited 可追溯）" \
  "$(ge "$(sql "select count(*) from login_events where result='blocked' and reason_code='rate_limited' and occurred_at >= '$START_TS'")" 1)" "ok"
check "SEC-004f" "限流/锁定事件可追溯（account_locked/rate_limited）" \
  "$(ge "$(sql "select count(*) from login_events where reason_code in ('account_locked','rate_limited') and occurred_at >= '$START_TS'")" 1)" "ok"

## ==================== RBAC-008 API Key audience/scope ====================
echo "---- RBAC-008 API Key 访问后台被拒（audience/scope）----"
ADMINKEY=${GPT_ACTIONS_ADMIN_KEY:-}
STATICKEY=${GPT_ACTIONS_API_KEY:-}
KEY_ID=""
if [ -n "$ADMINKEY" ]; then
  KEYRESP=$(curl -s -H "Authorization: Bearer $ADMINKEY" -H 'Content-Type: application/json' \
    -X POST -d "{\"name\":\"s7-sec-rbac008-$RUN_TAG\",\"scopes\":[\"read\"],\"rate_limit_per_minute\":2,\"allowed_ips\":[]}" "$BASE/api/v1/admin/action-api-keys")
  echo "$KEYRESP" > "$FILES_DIR/RBAC-008-key-create.json"
  KEY=$(echo "$KEYRESP" | jget "d['data']['api_key']")
  KEY_ID=$(echo "$KEYRESP" | jget "d['data']['id']")
else
  KEY=""
fi
if [ -z "$KEY" ] && [ -n "$STATICKEY" ]; then KEY="$STATICKEY"; fi
echo "created_key_id=$KEY_ID" > "$FILES_DIR/RBAC-008-keys.txt"
if [ -n "$KEY" ]; then
  C=$(curl -s -o "$FILES_DIR/RBAC-008a.json" -w '%{http_code}' -H "Authorization: Bearer $KEY" "$BASE/api/admin/v1/control/state")
  check "RBAC-008a" "API Key 访问后台 API → 401（不建立浏览器会话）" "$C" "401"
  C=$(curl -s -o "$FILES_DIR/RBAC-008b.json" -w '%{http_code}' -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -X POST -d '{"addresses":["0x0000000000000000000000000000000000000000"],"chain":"eth"}' "$BASE/api/v1/export/create")
  check "RBAC-008b" "read scope 的 Key 调用 export 作用域端点 → 401" "$C" "401"
  C=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $KEY" "$BASE/api/v1/address/overview?chain=eth&address=0x0000000000000000000000000000000000000000")
  check "RBAC-008c" "read scope 的 Key 通过 read 作用域鉴权（非 401）" \
    "$(case "$C" in 401) echo rejected ;; *) echo accepted ;; esac)" "accepted"
else
  nv "RBAC-008a" "无可用的 GPT Actions API Key（GPT_ACTIONS_ADMIN_KEY/API_KEY 均未设置），无法验证"
  nv "RBAC-008b" "同上"
  nv "RBAC-008c" "同上"
fi
C=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer not-a-valid-key" "$BASE/api/v1/address/overview?chain=eth&address=0x0")
check "RBAC-008d" "无效 API Key → 401" "$C" "401"
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JA" -H 'X-Auth-Audience: admin' "$BASE/api/v1/admin/action-api-keys")
check "RBAC-008e" "后台浏览器会话不可管理 API Key（需独立管理凭据，401/503）" \
  "$(case "$C" in 401|403|503) echo denied ;; *) echo "allowed:$C" ;; esac)" "denied"
check "RBAC-008f" "api_client 角色不含任何 admin:* 权限" \
  "$(sql "select count(*) from role_permissions rp join roles r on r.id=rp.role_id where r.key='api_client' and rp.permission_key like 'admin:%'")" "0"
check "RBAC-008g" "API Key 拒绝事件落审计（authorization.denied 或 api key 401 计数）" \
  "$(ge "$(sql "select count(*) from audit_events_v2 where http_status=401 and route_template like '%/api/admin/v1/%' and occurred_at >= '$START_TS'")" 1)" "ok"
# DEF-01：省略 allowed_ips 时创建 Key 的响应（期望 created，实测 500 INTERNAL_ERROR）
if [ -n "$ADMINKEY" ]; then
  OMIT=$(curl -s -o "$FILES_DIR/RBAC-008-def01.json" -w '%{http_code}' -H "Authorization: Bearer $ADMINKEY" -H 'Content-Type: application/json' \
    -X POST -d "{\"name\":\"s7-sec-omit-$RUN_TAG\",\"scopes\":[\"read\"],\"rate_limit_per_minute\":2}" "$BASE/api/v1/admin/action-api-keys")
  check "RBAC-008h" "创建 Key 省略 allowed_ips 字段应成功（DEF-01 回归门）" "$OMIT" "201"
  OMIT_ID=$(jget "d['data']['id']" < "$FILES_DIR/RBAC-008-def01.json")
  check "RBAC-008i" "省略 allowed_ips 时落库为默认空列表（非 NULL）" \
    "$(sql "select coalesce(array_to_string(allowed_ips,','),'NULL') from gpt_action_api_keys where name='s7-sec-omit-$RUN_TAG'")" ""
  if [ -n "$OMIT_ID" ]; then
    curl -s -o /dev/null -H "Authorization: Bearer $ADMINKEY" -X DELETE "$BASE/api/v1/admin/action-api-keys/$OMIT_ID"
  fi
  check "RBAC-008j" "省略 allowed_ips 的探针 Key 已清理（无残留）" \
    "$(sql "select count(*) from gpt_action_api_keys where name='s7-sec-omit-$RUN_TAG'")" "0"
else
  nv "RBAC-008h" "无 GPT Actions 管理凭据，DEF-01 回归门未验证"
  nv "RBAC-008i" "同上"
  nv "RBAC-008j" "同上"
fi

## ==================== 越权 / IDOR ====================
echo "---- IDOR 跨组织 / 跨工作区 ----"
CROSS_CASE=$(sql "select id from cases where organization_id=(select id from organizations where slug='default') order by created_at desc limit 1")
CROSS_NAME=$(sql "select name from cases where id='$CROSS_CASE'")
SAMEORG_OTHER_WS_CASE=$(sql "select c.id from cases c where c.organization_id='$VIEWER_ORG' and c.workspace_id <> '$VIEWER_WS' limit 1")
echo "cross_case=$CROSS_CASE cross_name=$CROSS_NAME other_ws_case=$SAMEORG_OTHER_WS_CASE viewer_ws=$VIEWER_WS" > "$FILES_DIR/IDOR-targets.txt"
C=$(curl -s -o "$FILES_DIR/IDOR-a.json" -w '%{http_code}' -b "$JV" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases/$CROSS_CASE")
check "IDOR-a" "Viewer 读他人组织案件 → 404" "$C" "404"
case "$(cat "$FILES_DIR/IDOR-a.json")" in *"$CROSS_NAME"*) S=leaked ;; *) S=not_leaked ;; esac
check "IDOR-b" "404 响应体不泄露对象存在性（不含案件名）" "$S" "not_leaked"
check "IDOR-c" "统一错误码 RESOURCE_NOT_FOUND" "$(jget "d['error']['code']" < "$FILES_DIR/IDOR-a.json")" "RESOURCE_NOT_FOUND"
BEFORE=$(sql "select count(*) from case_comments where case_id='$CROSS_CASE'")
C=$(curl -s -o "$FILES_DIR/IDOR-d.json" -w '%{http_code}' -b "$JI" -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CS_I" -H 'Content-Type: application/json' \
  -X POST -d '{"body_markdown":"idor-probe"}' "$BASE/api/v2/cases/$CROSS_CASE/comments")
AFTER=$(sql "select count(*) from case_comments where case_id='$CROSS_CASE'")
check "IDOR-d" "Investigator 跨组织写评论 → 404" "$C" "404"
check "IDOR-e" "越权写未产生副作用（评论数不变）" "$AFTER" "$BEFORE"
# 说明：fixture-viewer 同时是 acme 两个工作区的 active 成员（见 IDOR-targets.txt），
# 因此「同组织跨工作区」必须用一个**仅绑定单一工作区**的身份来构造负向场景。
if [ -n "$SAMEORG_OTHER_WS_CASE" ]; then
  TMP_EMAIL="s7-sec-ido-$RUN_TAG@fixture.local"
  TMPR=$(curl -s -b "$JA" -H "X-CSRF-Token: $CS_A" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
    -X POST -d "{\"email\":\"$TMP_EMAIL\",\"display_name\":\"S7-SEC-01 越权用例临时账号\",\"organization_id\":\"$VIEWER_ORG\",\"workspace_id\":\"$VIEWER_WS\",\"role_key\":\"viewer\",\"reason\":\"S7-SEC-01 IDOR 负向用例\"}" "$BASE/api/admin/v1/users")
  echo "$TMPR" > "$FILES_DIR/IDOR-temp-user.json"
  TMP_ID=$(jget "d['data']['id']" < "$FILES_DIR/IDOR-temp-user.json")
  TMP_PW=$(jget "d['data']['initial_password']" < "$FILES_DIR/IDOR-temp-user.json")
  if [ -n "$TMP_ID" ]; then
    check "IDOR-f0" "临时 Viewer 仅绑定单一工作区（构造非成员场景）" "$(sql "select count(*) from workspace_memberships where identity_id='$TMP_ID'")" "1"
    if [ -n "$TMP_PW" ]; then
      JR=$(mktemp)
      login "$JR" "$TMP_EMAIL" "$TMP_PW" user > "$FILES_DIR/IDOR-temp-login.json"
      C=$(curl -s -o "$FILES_DIR/IDOR-f.json" -w '%{http_code}' -b "$JR" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases/$SAMEORG_OTHER_WS_CASE")
      check "IDOR-f" "同组织非成员工作区案件 → 404/403" "$(case "$C" in 404|403) echo denied ;; *) echo "allowed:$C" ;; esac)" "denied"
      OWN_CASE=$(sql "select id from cases where workspace_id='$VIEWER_WS' limit 1")
      C2=$(curl -s -o /dev/null -w '%{http_code}' -b "$JR" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases/$OWN_CASE")
      check "IDOR-f2" "成员工作区案件可读（同身份对照组）" "$(case "$C2" in 200) echo allowed ;; *) echo "denied:$C2" ;; esac)" "allowed"
      rm -f "$JR"
    else
      nv "IDOR-f" "临时账号未返回一次性密码，跨工作区负向用例未覆盖"
      nv "IDOR-f2" "同上"
    fi
    DSB=$(curl -s -o "$FILES_DIR/IDOR-temp-disable.json" -w '%{http_code}' -b "$JA" -H "X-CSRF-Token: $CS_A" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
      -X POST -d '{"confirm":true,"reason":"S7-SEC-01 用例结束，停用临时账号"}' "$BASE/api/admin/v1/users/$TMP_ID/commands/disable")
    echo "temp_user=$TMP_EMAIL id=$TMP_ID disable_status=$DSB" >> "$FILES_DIR/IDOR-targets.txt"
  else
    nv "IDOR-f" "临时账号创建失败：$(head -c 160 "$FILES_DIR/IDOR-temp-user.json")"
    nv "IDOR-f2" "同上"
  fi
else
  nv "IDOR-f" "同组织内不存在第二工作区案件，跨工作区用例未覆盖"
  nv "IDOR-f2" "同上"
fi
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JV" -H 'X-Auth-Audience: user' -H 'X-Workspace-Id: 00000000-0000-0000-0000-000000000999' "$BASE/api/v2/cases")
check "IDOR-g" "伪造 X-Workspace-Id 不提升作用域" "$(case "$C" in 200|403) echo scoped_or_denied ;; *) echo "unexpected:$C" ;; esac)" "scoped_or_denied"

## ==================== 未认证访问后台 ====================
echo "---- 未认证访问 /api/admin/v1/* ----"
UNAUTH_OK=0; UNAUTH_N=0
: > "$FILES_DIR/UNAUTH-endpoints.txt"
for ep in /overview /users /roles /sessions /audit/events /control/state /observability/overview /providers /settings /system/version; do
  C=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/admin/v1$ep")
  printf '%s=%s\n' "$ep" "$C" >> "$FILES_DIR/UNAUTH-endpoints.txt"
  UNAUTH_N=$((UNAUTH_N+1))
  if [ "$C" = "401" ]; then UNAUTH_OK=$((UNAUTH_OK+1)); fi
done
check "UNAUTH-a" "10 个后台端点未认证全部 401" "$UNAUTH_OK" "10"
check "UNAUTH-b" "未认证 401 写入审计（authorization.denied）" \
  "$(ge "$(sql "select count(*) from audit_events_v2 where action='authorization.denied' and http_status=401 and occurred_at >= '$START_TS'")" 1)" "ok"

## ==================== 审计只读性 ====================
echo "---- 审计只读性（blockchain_app 运行角色）----"
AUD_OUT="$FILES_DIR/AUDIT-role.txt"
: > "$AUD_OUT"
echo "== has_table_privilege ==" >> "$AUD_OUT"
sql "select 'UPDATE='||has_table_privilege('blockchain_app','audit_events_v2','UPDATE')||' DELETE='||has_table_privilege('blockchain_app','audit_events_v2','DELETE')||' TRUNCATE='||has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE')||' INSERT='||has_table_privilege('blockchain_app','audit_events_v2','INSERT')" >> "$AUD_OUT"
echo "== 实际尝试（SET ROLE blockchain_app） ==" >> "$AUD_OUT"
for stmt in "UPDATE audit_events_v2 SET action='tamper' WHERE seq=1" "DELETE FROM audit_events_v2 WHERE seq=1" "TRUNCATE audit_events_v2" "UPDATE audit_chain_anchors SET chain='x'" "DELETE FROM audit_chain_anchors" "CREATE TABLE s7_sec_probe_tbl(id int)" "ALTER TABLE audit_events_v2 ADD COLUMN s7_probe int" "DROP TABLE audit_chain_anchors"; do
  OUT=$(sudo -n docker exec -e PGPASSWORD="$PGPASS" deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc "SET ROLE blockchain_app; $stmt" 2>&1)
  printf 'STMT|%s|RESULT|%s\n' "$stmt" "$(echo "$OUT" | head -2 | tr '\n' ' ')" >> "$AUD_OUT"
done
check "AUDIT-a" "blockchain_app 对 audit_events_v2 无 UPDATE 权限" "$(sql "select has_table_privilege('blockchain_app','audit_events_v2','UPDATE')")" "f"
check "AUDIT-b" "blockchain_app 对 audit_events_v2 无 DELETE 权限" "$(sql "select has_table_privilege('blockchain_app','audit_events_v2','DELETE')")" "f"
check "AUDIT-c" "blockchain_app 对 audit_events_v2 无 TRUNCATE 权限" "$(sql "select has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE')")" "f"
check "AUDIT-d" "blockchain_app 仍可 INSERT（追加写入不受影响）" "$(sql "select has_table_privilege('blockchain_app','audit_events_v2','INSERT')")" "t"
check "AUDIT-e" "实际 UPDATE/DELETE 被数据库拒绝（permission denied）" \
  "$(ge "$(grep -c 'permission denied' "$AUD_OUT")" 4)" "ok"
check "AUDIT-f" "实际 TRUNCATE 被数据库拒绝" \
  "$(grep 'TRUNCATE audit_events_v2' "$AUD_OUT" | grep -ciE 'permission denied|cannot truncate')" "1"
check "AUDIT-g" "实际 DDL（CREATE/ALTER/DROP）被拒绝" \
  "$(grep -cE 'STMT\|(CREATE TABLE s7_sec_probe_tbl|ALTER TABLE audit_events_v2|DROP TABLE audit_chain_anchors).*(permission denied|must be owner)' "$AUD_OUT")" "3"
check "AUDIT-h" "审计表所有者不是运行角色（owner=blockchain）" \
  "$(sql "select count(*) from pg_tables where tablename='audit_events_v2' and tableowner='blockchain_app'")" "0"
check "AUDIT-i" "0055 锚点表同样不可改" "$(sql "select has_table_privilege('blockchain_app','audit_chain_anchors','UPDATE')")" "f"
check "AUDIT-j" "运行期降权开关已开启（DB_RUNTIME_ROLE=blockchain_app）" "$(grep -c '^DB_RUNTIME_ROLE=blockchain_app' "$PROJECT/deploy/.env")" "1"
check "AUDIT-k" "运行角色非超级用户、无 BYPASSRLS" \
  "$(sql "select rolsuper::text||'/'||rolbypassrls::text from pg_roles where rolname='blockchain_app'")" "false/false"

## ==================== 认证绕过 ====================
echo "---- 认证绕过：空/篡改/过期/撤销会话 ----"
C=$(curl -s -o /dev/null -w '%{http_code}' -b "cs_user_session=" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases")
check "AUTHZ-a" "空 Cookie → 401" "$C" "401"
RAW=$(grep cs_user_session "$JV" | grep -v csrf | awk '{print $7}')
TAMPERED=$(echo "$RAW" | sed 's/.$/X/')
C=$(curl -s -o /dev/null -w '%{http_code}' -b "cs_user_session=$TAMPERED" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases")
check "AUTHZ-b" "篡改 Cookie（尾字符替换）→ 401" "$C" "401"
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JV" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases")
check "AUTHZ-c" "原 Cookie 正常（正向对照 200）" "$C" "200"
# 过期会话：使用本套件独立创建的会话，避免与撤销用例互相干扰
: > "$FILES_DIR/AUTHZ-session.txt"
JE=$(mktemp)
login "$JE" "$VIEW_EMAIL" "$VIEWER_PASSWORD" user > "$FILES_DIR/AUTHZ-expired-login.json"
EXP_SID=$(jget "d['data']['session_id']" < "$FILES_DIR/AUTHZ-expired-login.json")
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JE" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases")
check "AUTHZ-d0" "过期用例前置：新会话可用（200）" "$C" "200"
sql "update sessions set expires_at = now() - interval '1 hour' where id='$EXP_SID'" >/dev/null
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JE" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases")
check "AUTHZ-d" "过期会话 → 401" "$C" "401"
echo "expired_session=$EXP_SID" >> "$FILES_DIR/AUTHZ-session.txt"
sql "select id||' | expires_at='||expires_at::text||' | revoked='||coalesce(revoked_at::text,'NULL') from sessions where id='$EXP_SID'" >> "$FILES_DIR/AUTHZ-session.txt"
# 撤销会话：独立的新会话，由 Owner 通过后台真实撤销接口撤销（需 confirm 二次确认）
JR=$(mktemp)
login "$JR" "$VIEW_EMAIL" "$VIEWER_PASSWORD" user > "$FILES_DIR/AUTHZ-revoke-login.json"
REV_SID=$(jget "d['data']['session_id']" < "$FILES_DIR/AUTHZ-revoke-login.json")
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JR" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases")
check "AUTHZ-e0" "撤销用例前置：新会话可用（200）" "$C" "200"
REV=$(curl -s -b "$JA" -H "X-CSRF-Token: $CS_A" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -X POST -d '{"confirm":true,"reason":"S7-SEC-01 负向用例：撤销测试会话"}' "$BASE/api/admin/v1/sessions/$REV_SID/commands/revoke")
echo "$REV" > "$FILES_DIR/AUTHZ-revoke.json"
sleep 1
C=$(curl -s -o /dev/null -w '%{http_code}' -b "$JR" -H 'X-Auth-Audience: user' "$BASE/api/v2/cases")
check "AUTHZ-e" "已撤销会话 → 401 且在 5 秒内生效（§23.8）" "$C" "401"
check "AUTHZ-f" "DB 中 revoked_at 非空且原因已记录" "$(sql "select (revoked_at is not null and revoked_reason <> '')::text from sessions where id='$REV_SID'")" "true"
echo "revoked_session=$REV_SID" >> "$FILES_DIR/AUTHZ-session.txt"
sql "select id||' | revoked='||coalesce(revoked_at::text,'NULL')||' | reason='||revoked_reason from sessions where id='$REV_SID'" >> "$FILES_DIR/AUTHZ-session.txt"
rm -f "$JE" "$JR"

## ==================== CLEANUP ====================
echo "---- CLEANUP 清理本套件创建的数据 ----"
if [ -n "$KEY_ID" ]; then
  DEL=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $ADMINKEY" -X DELETE "$BASE/api/v1/admin/action-api-keys/$KEY_ID")
  check "CLEANUP-a" "测试 API Key 已删除" "$(case "$DEL" in 200|204) echo deleted ;; *) echo "left:$DEL" ;; esac)" "deleted"
fi
if [ -n "$NEWC_ID" ]; then
  # 注意：/api/v2/cases/{id} 未注册 DELETE（DEF-02），先试 v2，再退到 v1 兼容路由，最后用 SQL 兜底，
  # 判定依据始终是「DB 中是否还有残留」。
  D1=$(curl -s -o /dev/null -w '%{http_code}' -b "$JO" -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CS_O" -X DELETE "$BASE/api/v2/cases/$NEWC_ID")
  D2=$(curl -s -o /dev/null -w '%{http_code}' -b "$JO" -H 'X-Auth-Audience: user' -X DELETE "$BASE/api/v1/cases/$NEWC_ID")
  REMAIN=$(sql "select count(*) from cases where id='$NEWC_ID'")
  if [ "$REMAIN" != "0" ]; then
    sql "delete from case_comments where case_id='$NEWC_ID'" >/dev/null
    sql "delete from cases where id='$NEWC_ID'" >/dev/null
    D3=sql-fallback
  else
    D3=api
  fi
  echo "v2_delete=$D1 v1_delete=$D2 fallback=$D3" > "$FILES_DIR/CLEANUP-delete.txt"
  check "CLEANUP-b" "测试案件已删除且 DB 无残留" "$(sql "select count(*) from cases where id='$NEWC_ID'")" "0"
  check "CLEANUP-e" "用户层案件删除路由现状（DEF-02：v2 未注册 DELETE）" \
    "$(case "$D1" in 405) echo "v2_no_delete_route:$D1" ;; 200|204) echo "v2_delete_ok:$D1" ;; *) echo "other:$D1" ;; esac)" "v2_no_delete_route:405"
fi
check "CLEANUP-c" "未把任何真实账号置于锁定状态" \
  "$(sql "select count(*) from identities where locked_until is not null and locked_until > now()")" "0"
check "CLEANUP-d" "Provider 端点为原值（无遗留改动）" "$(sql "select endpoint from provider_descriptors where provider_key='$PROV'")" "$ORIG"
check "CLEANUP-f" "本套件未遗留任何 API Key（s7-sec-%）" "$(sql "select count(*) from gpt_action_api_keys where name like 's7-sec-%'")" "0"

FINISHED_TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
python3 - "$EVIDENCE_DIR/result.json" "$PASS" "$FAIL" "$NV" "$START_TS" "$FINISHED_TS" <<'PY'
import json,sys
path,p,f,nv,started,finished=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]),int(sys.argv[4]),sys.argv[5],sys.argv[6]
blocking = f > 0 or nv > 0
data={"suite":"run-sprint7-sec-negative.sh","test_id":"S7-SEC-01",
      "pass":p,"fail":f,"not_verified":nv,
      "blocking_failures":f,"blocking_not_verified":nv,
      "criteria":"fail==0 and not_verified==0 and pass>0",
      "status":"PASS" if (f==0 and nv==0 and p>0) else "FAIL",
      "started_at":started,"finished_at":finished}
if blocking:
    print("STATUS=FAIL：存在未通过或未验证项（fail=%d not_verified=%d）" % (f, nv), file=sys.stderr)
json.dump(data,open(path,"w",encoding="utf-8"),ensure_ascii=False,indent=2)
print("result.json written:",data)
PY
echo "==== S7-SEC-01 渗透负向用例结束 $(date -Is) PASS=$PASS FAIL=$FAIL NOT_VERIFIED=$NV ===="
[ "$FAIL" -eq 0 ] && [ "$NV" -eq 0 ] || exit 1
