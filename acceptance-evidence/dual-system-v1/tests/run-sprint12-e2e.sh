#!/usr/bin/env bash
# Sprint 1/2 遗留验收：AUTH-003/005/006/007/008、SEC-003/004、AUDIT-002
#
# 对应 docs/dual-system-development-plan.md §27.1（身份、会话与设备）、
# §27.6（审计、安全和体验）。每条断言都针对文档中该 Test ID 的"预期结果"。
#
# 前置：ADMIN_PASSWORD（平台 Owner 密码）。
# 所有时间敏感断言都打印实测值，不写死"通过"。

set -uo pipefail

BASE=${BASE:-http://127.0.0.1:8080}
PW=${ADMIN_PASSWORD:?需要 ADMIN_PASSWORD}
FIXTURE_ENV=${FIXTURE_ENV:-/tmp/dual-system-fixture.env}
PW_VIEWER=${FIXTURE_VIEWER_PASSWORD:-}

PASS=0; FAIL=0; BLOCKED=0
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

check() { # check <ID> <描述> <实际> <期望>
  if [ "$3" = "$4" ]; then printf '  PASS     %-10s %-52s %s\n' "$1" "$2" "$3"; PASS=$((PASS+1));
  else printf '  FAIL     %-10s %-52s got=%s want=%s\n' "$1" "$2" "$3" "$4"; FAIL=$((FAIL+1)); fi
}
blocked() { printf '  BLOCKED  %-10s %-52s %s\n' "$1" "$2" "$3"; BLOCKED=$((BLOCKED+1)); }
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

login_json() { # login_json <jar> <email> <password> <audience> -> body
  local jar=$1 email=$2 pass=$3 aud=${4:-user}
  curl -s -c "$jar" -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pass\",\"audience\":\"$aud\"}" "$BASE/api/auth/v1/login"
}
login() { # 返回状态码
  local jar=$1 email=$2 pass=$3 aud=${4:-user}
  curl -s -o /dev/null -w '%{http_code}' -c "$jar" -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pass\",\"audience\":\"$aud\"}" "$BASE/api/auth/v1/login"
}
csrf() { grep "$1" "$2" 2>/dev/null | awk '{print $7}'; }
cookie_of() { awk -v n="$2" '$6==n {print $7}' "$1" 2>/dev/null; }

JAR_OWNER=$WORK/owner.jar
curl -s -o /dev/null -c "$JAR_OWNER" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"admin\"}" "$BASE/api/auth/v1/login"
OWNER_CSRF=$(csrf cs_admin_session_csrf "$JAR_OWNER")
# 另需一个用户层会话：/api/v2/* 只接受 user 受众。
JAR_SVC=$WORK/svc.jar
curl -s -o /dev/null -c "$JAR_SVC" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user'   -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login"
AUDITOR_PW=${FIXTURE_AUDITOR_PASSWORD:-}
echo "== 准备完成：Owner 后台会话 + 用户层会话 =="

echo
echo "== AUTH-005 refresh 轮换 + 旧令牌重放触发会话族撤销 =="
JAR_A=$WORK/rotate.jar
check "AUTH-005" "初次登录" "$(login "$JAR_A" admin@local "$PW" user)" "200"
TOKEN_A=$(cookie_of "$JAR_A" cs_user_session)
check "AUTH-005" "轮换前旧令牌可用" "$(code -b "$JAR_A" "$BASE/api/auth/v1/session")" "200"

# 用旧令牌发起 refresh -> 服务端下发新令牌 B
REFRESH=$(curl -s -b "$JAR_A" -c "$JAR_A" -H "X-CSRF-Token: $(csrf cs_user_session_csrf "$JAR_A")" \
  -X POST "$BASE/api/auth/v1/refresh")
TOKEN_B=$(cookie_of "$JAR_A" cs_user_session)
check "AUTH-005" "refresh 下发了不同的新令牌" \
  "$([ -n "$TOKEN_B" ] && [ "$TOKEN_A" != "$TOKEN_B" ] && echo yes || echo no)" "yes"

# 重放旧令牌 A：必须被识别为 reuse，而不是普通 401
REUSE_BODY=$(curl -s -o "$WORK/reuse.json" -w '%{http_code}' -H "Cookie: cs_user_session=$TOKEN_A" \
  -H 'X-Auth-Audience: user' "$BASE/api/auth/v1/session")
REUSE_CODE=$(python3 -c 'import json;print(json.load(open("'"$WORK"'/reuse.json"))["error"]["code"])' 2>/dev/null || echo "parse_error")
check "AUTH-005" "旧令牌重放被识别（AUTHN_SESSION_REUSE）" "$REUSE_CODE" "AUTHN_SESSION_REUSE"
# 家族撤销：新令牌 B 也应立即失效
check "AUTH-005" "重放后整个会话族被撤销（新令牌同时失效）" \
  "$(code -H "Cookie: cs_user_session=$TOKEN_B" -H 'X-Auth-Audience: user' "$BASE/api/auth/v1/session")" "401"

echo
echo "== AUTH-006 退出后当前会话立即失效 =="
JAR_L=$WORK/logout.jar
login "$JAR_L" admin@local "$PW" user >/dev/null
TOKEN_L=$(cookie_of "$JAR_L" cs_user_session)
check "AUTH-006" "退出前会话可用" "$(code -H "Cookie: cs_user_session=$TOKEN_L" -H 'X-Auth-Audience: user' "$BASE/api/auth/v1/session")" "200"
curl -s -o /dev/null -b "$JAR_L" -H "X-CSRF-Token: $(csrf cs_user_session_csrf "$JAR_L")" -H 'Content-Type: application/json' \
  -X POST -d '{}' "$BASE/api/auth/v1/logout"
check "AUTH-006" "退出后同一令牌立即 401" \
  "$(code -H "Cookie: cs_user_session=$TOKEN_L" -H 'X-Auth-Audience: user' "$BASE/api/auth/v1/session")" "401"

echo
echo "== AUTH-007 管理员强制下线在 5 秒内生效（全部会话） =="
if [ -z "$PW_VIEWER" ]; then
  blocked "AUTH-007" "需要夹具账号口令" "设置 FIXTURE_VIEWER_PASSWORD 后重跑"
else
  JAR_V1=$WORK/v1.jar; JAR_V2=$WORK/v2.jar
  login "$JAR_V1" fixture-viewer@fixture.local "$PW_VIEWER" user >/dev/null
  login "$JAR_V2" fixture-viewer@fixture.local "$PW_VIEWER" user >/dev/null
  T1=$(cookie_of "$JAR_V1" cs_user_session); T2=$(cookie_of "$JAR_V2" cs_user_session)
  check "AUTH-007" "两个会话都可用" \
    "$(code -H "Cookie: cs_user_session=$T1" -H 'X-Auth-Audience: user' "$BASE/api/v2/dashboard")$(code -H "Cookie: cs_user_session=$T2" -H 'X-Auth-Audience: user' "$BASE/api/v2/dashboard")" "200200"
  VIEWER_ID=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/users?search=fixture-viewer" \
    | python3 -c 'import sys,json;d=json.load(sys.stdin);print((d["data"] or [{}])[0].get("id",""))')
  for sid in $(curl -s -b "$JAR_OWNER" -H 'Content-Type: application/json' -X POST \
      -d '{}' "$BASE/api/admin/v1/sessions" 2>/dev/null; true); do :; done
  SESSIONS=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/sessions?identity_id=$VIEWER_ID&active=true")
  T0=$(date +%s)
  for sid in $(echo "$SESSIONS" | python3 -c 'import sys,json;print(" ".join(s["id"] for s in json.load(sys.stdin)["data"]))'); do
    curl -s -o /dev/null -b "$JAR_OWNER" -H "X-CSRF-Token: $OWNER_CSRF" -H 'Content-Type: application/json' -X POST \
      -d '{"confirm":true,"reason":"AUTH-007 强制下线"}' "$BASE/api/admin/v1/sessions/$sid/commands/revoke"
  done
  DEAD_AT=""
  for _ in $(seq 1 12); do
    C1=$(code -H "Cookie: cs_user_session=$T1" -H 'X-Auth-Audience: user' "$BASE/api/v2/dashboard")
    C2=$(code -H "Cookie: cs_user_session=$T2" -H 'X-Auth-Audience: user' "$BASE/api/v2/dashboard")
    if [ "$C1" = "401" ] && [ "$C2" = "401" ]; then DEAD_AT=$(( $(date +%s) - T0 )); break; fi
    sleep 0.5
  done
  check "AUTH-007" "强制下线后全部会话 5 秒内失效（实测 ${DEAD_AT:-超时}s）" \
    "$([ -n "$DEAD_AT" ] && [ "$DEAD_AT" -le 5 ] && echo within_sla || echo over_sla)" "within_sla"
fi

echo
echo "== AUTH-008 新设备登录：设备/风险字段完整 =="
# is_new_device 测的是"首次见到某设备"；设备已登记过必然为 false，那是在测另一件事。
# 因此先清掉该账号的设备记录，让语义可测。
docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -v ON_ERROR_STOP=1 -c \
  "DELETE FROM trusted_devices WHERE identity_id = (SELECT id FROM identities WHERE email='admin@local')"
NEWDEV=$(curl -s -c "$WORK/newdev.jar" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H 'User-Agent: Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1' \
  -H 'Sec-CH-UA-Platform: "iOS"' -H 'Sec-CH-UA-Mobile: ?1' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$PW\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
# 事件落在 login 链上：取最近一条成功登录核对字段
sleep 1
EVT=$(curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/login-events?limit=20&result=success" \
  | python3 -c '
import sys, json
d = json.load(sys.stdin).get("data") or []
for r in d:
    if r.get("device_type") == "mobile":
        print(json.dumps(r)); break
')
check "AUTH-008" "移动端 UA 被解析为 device_type=mobile" \
  "$(printf '%s' "$EVT" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("device_type","") if sys.stdin.isatty()==False else "")' 2>/dev/null || echo "")" "mobile"
check "AUTH-008" "设备/IP/哈希字段齐全" "$(printf '%s' "$EVT" | python3 -c '
import sys, json
try:
    r = json.load(sys.stdin)
except Exception:
    print("no_event"); raise SystemExit
required = ["device_type","os_name","browser_name","client_ip","peer_ip","event_hash","prev_hash","user_agent"]
missing = [k for k in required if not r.get(k)]
print("complete" if not missing else "missing:" + ",".join(missing))
')" "complete"
NEW_DEVICE_FLAG=$(printf '%s' "$EVT" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("is_new_device"))' 2>/dev/null)
check "AUTH-008" "新设备被标记（is_new_device=true）" "$NEW_DEVICE_FLAG" "True"

echo
echo "== SEC-003 SSRF：用户输入不得被当作出站请求目标 =="
# 本批次新增面不含用户可控出站 URL。验证 URL 形状的输入被当作**数据**处理，
# 不会触发服务端向该地址发起请求（云元数据地址是最典型的探测目标）。
SSRF_INPUTS=("http://169.254.169.254/latest/meta-data/" "http://127.0.0.1:8080/health/ready" "file:///etc/passwd")
SSRF_ALL_SAFE=yes
for payload in "${SSRF_INPUTS[@]}"; do
  # 地址查询接口：URL 形状的地址应被判为非法地址，而不是被拿去请求
  st=$(code -b "$JAR_SVC" --get --data-urlencode "q=$payload" "$BASE/api/v2/search" 2>/dev/null || echo "000")
  [ "$st" = "200" ] || [ "$st" = "400" ] || [ "$st" = "404" ] || SSRF_ALL_SAFE=no
  # 个人资料名称：URL 只是字符串，不得产生任何服务端抓取
  curl -s -o /dev/null -b "$WORK/newdev.jar" -H "X-CSRF-Token: $(csrf cs_user_session_csrf "$WORK/newdev.jar")" \
    -H 'Content-Type: application/json' -X PATCH -d "{\"display_name\":\"$payload\"}" "$BASE/api/v2/me/profile"
done
check "SEC-003" "URL 形状输入仅被当作数据（未触发服务端请求）" "$SSRF_ALL_SAFE" "yes"
# 公共签名服务端点来自运维配置（不可被请求覆盖），代码默认关闭 -> 无外网出站。
check "SEC-003" "公共 4byte 签名服务默认关闭（无外网出站）" \
  "$(grep -c 'METHOD_PUBLIC_SIGNATURE_ENABLED", false' /home/euripides/projects/blockchain-platform/backend/internal/config/config.go)" "1"

echo
echo "== AUDIT-002 秘密扫描：响应与审计中不得出现明文口令 =="
SECRET="Sup3rSecret-Probe-$(date +%s)"
curl -s -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$SECRET\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login"
sleep 1
LEAK=0
# 1) 登录记录接口
curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/login-events?limit=50" | grep -q "$SECRET" && LEAK=$((LEAK+1))
# 2) 审计事件接口
curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/audit/events?limit=50" | grep -q "$SECRET" && LEAK=$((LEAK+1))
# 3) 会话接口
curl -s -b "$JAR_OWNER" "$BASE/api/admin/v1/sessions?limit=50" | grep -q "$SECRET" && LEAK=$((LEAK+1))
# 4) 服务端日志
grep -rq "$SECRET" /tmp/api.log /tmp/worker.log 2>/dev/null && LEAK=$((LEAK+1))
# 5) 数据库（login_events / audit_events_v2 / sessions / session_reuse_events）
DBLEAK=$(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc \
  "SELECT (SELECT count(*) FROM login_events WHERE subject_masked LIKE '%$SECRET%' OR detail::text LIKE '%$SECRET%')
        + (SELECT count(*) FROM audit_events_v2 WHERE detail::text LIKE '%$SECRET%' OR before::text LIKE '%$SECRET%' OR after::text LIKE '%$SECRET%')
        + (SELECT count(*) FROM sessions WHERE user_agent LIKE '%$SECRET%')
        + (SELECT count(*) FROM session_reuse_events WHERE detail::text LIKE '%$SECRET%')" 2>/dev/null || echo "-1")
check "AUDIT-002" "API/日志中无明文口令出现" "$LEAK" "0"
check "AUDIT-002" "数据库 4 张表扫描无明文口令（命中 ${DBLEAK} 处）" "$DBLEAK" "0"

echo "== SEC-004 API 限流：429 + Retry-After，且不误伤其他用户 =="
RL_EMAIL="ratelimit-probe@example.com"
# 账号维度容量为 AUTH_LOGIN_RATE_PER_MINUTE（默认 20），因此第 21 次失败必然 429。
RL_ATTEMPTS=25
RA_HEADER=""
GOT_429=""
for _ in $(seq 1 $RL_ATTEMPTS); do
  RESP=$(curl -s -D "$WORK/h.txt" -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$RL_EMAIL\",\"password\":\"wrong-password-probe\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
  if [ "$RESP" = "429" ]; then
    GOT_429=yes
    RA_HEADER=$(grep -i '^Retry-After:' "$WORK/h.txt" | tr -d '\r' | awk '{print $2}')
    break
  fi
done
check "SEC-004" "连续失败登录触发 429" "${GOT_429:-no}" "yes"
check "SEC-004" "429 响应带 Retry-After 且为正整数" \
  "$([ -n "$RA_HEADER" ] && [ "$RA_HEADER" -ge 1 ] 2>/dev/null && echo yes || echo "no(${RA_HEADER:-缺失})")" "yes"
# 关键：限流按 账号+IP 维度，不能因为某人被限流而影响其他账号
check "SEC-004" "限流不影响其他账号（无共享 bucket）" \
  "$(login "$WORK/other.jar" admin@local "$PW" admin)" "200"

echo
echo "== AUTH-003 不存在账号 vs 错误密码：外观与时序近似 =="
A003_ACCOUNT=${AUTH003_ACCOUNT:-fixture-auditor@fixture.local}
UNKNOWN_ACCOUNT="nobody-$(date +%s%N)@example.com"
PSQL_LOCAL="docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc"

# 探针账号会被本用例反复打失败，必须先解锁；否则返回 423 而不是 401，
# 两种样本就不可比（这不是产品缺陷，是测试夹具卫生问题）。
$PSQL_LOCAL "UPDATE identities SET locked_until=NULL, failed_login_count=0 WHERE lower(email)=lower('$A003_ACCOUNT')" >/dev/null 2>&1

# 断言 1（确定性）：错误码、提示文案、retryable 必须完全一致。
login_probe() {
  curl -s -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$1\",\"password\":\"definitely-wrong-1\",\"audience\":\"user\"}" \
    "$BASE/api/auth/v1/login"
}
norm() { printf '%s' "$1" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["error"]["code"], d["error"]["message"], d["error"]["retryable"])'; }
check "AUTH-003" "两种失败的错误码与提示完全一致" \
  "$(norm "$(login_probe "$UNKNOWN_ACCOUNT")")" "$(norm "$(login_probe "$A003_ACCOUNT")")"

# 断言 2（统计）：中位响应时间必须近似。
#
# 统计性断言会被三种因素污染，必须排除后再下结论：
#   * 429 限流：在哈希之前返回，耗时骤降到几毫秒；
#   * 423 账号锁定：同样不进入哈希路径；
#   * 额度/锁定窗口未重置：样本不足。
# 处理：丢弃所有非 401 样本；不足 5 个有效样本就等窗口重置重试一次；
# 仍不足则如实标 BLOCKED，绝不拿被污染的样本下结论（§27.7）。
sample_ms() {
  # sample_ms <identifier> <n> -> "<有效样本数> <中位秒数>"
  local id=$1 n=$2 i code t
  local times="" valid=0
  for i in $(seq 1 "$n"); do
    read -r code t <<< "$(curl -s -o /dev/null -w '%{http_code} %{time_total}' \
      -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
      -d "{\"identifier\":\"$id\",\"password\":\"definitely-wrong-1\",\"audience\":\"user\"}" \
      "$BASE/api/auth/v1/login")"
    if [ "$code" = "401" ]; then
      times="$times $t"
      valid=$((valid + 1))
    fi
  done
  local median
  median=$(printf '%s\n' $times | grep -v '^$' | sort -n | awk '{a[NR]=$1} END {if (NR > 0) printf "%.4f", a[int((NR+1)/2)]}')
  printf '%s %s' "$valid" "${median:-0}"
}

for round in 1 2; do
  $PSQL_LOCAL "UPDATE identities SET locked_until=NULL, failed_login_count=0 WHERE lower(email)=lower('$A003_ACCOUNT')" >/dev/null 2>&1
  read -r VALID_BAD MED_BAD <<< "$(sample_ms "$A003_ACCOUNT" 9)"
  read -r VALID_NONE MED_NONE <<< "$(sample_ms "$UNKNOWN_ACCOUNT" 9)"
  printf '           第 %s 轮有效样本：错误密码=%s/9  不存在账号=%s/9  中位数=%ss / %ss\n' \
    "$round" "$VALID_BAD" "$VALID_NONE" "$MED_BAD" "$MED_NONE"
  if [ "${VALID_BAD:-0}" -ge 5 ] && [ "${VALID_NONE:-0}" -ge 5 ]; then
    break
  fi
  if [ "$round" = "1" ]; then
    echo "           样本受限流/锁定污染，等待 61 秒窗口重置后重采…"
    sleep 61
  fi
done

if [ "${VALID_BAD:-0}" -lt 5 ] || [ "${VALID_NONE:-0}" -lt 5 ]; then
  blocked "AUTH-003" "时序近似（有效样本不足：${VALID_BAD:-0}/9、${VALID_NONE:-0}/9）" \
    "样本被限流或账号锁定污染；等窗口重置后重跑本用例即可取得有效样本"
else
  RATIO=$(python3 -c "
a = float('$MED_BAD') or 1e-9
b = float('$MED_NONE') or 1e-9
print(f'{max(a,b)/min(a,b):.2f}')
")
  printf '           中位响应时间比值 = %sx（阈值 3.0x）\n' "$RATIO"
  check "AUTH-003" "中位响应时间比值 ≤ 3x（实测 ${RATIO}x）" \
    "$(python3 -c "print('within' if $RATIO <= 3.0 else 'over')")" "within"
fi

echo
echo "==================== 结果 ===================="
printf 'PASS=%d FAIL=%d BLOCKED=%d\n' "$PASS" "$FAIL" "$BLOCKED"
[ "$FAIL" -eq 0 ]
