#!/usr/bin/env bash
# §7.3 功能开关**真实请求路径**验收（2026-09-16 轮）。
#
# 断言的不是"后台能试算"，而是：
#   1. 关掉开关之后，**真实业务请求**按开关被拒绝（503 FEATURE_DISABLED + 稳定 error code）；
#   2. 该次求值在 feature_flag_evaluations 里留下**真实流量**的记录（subject=调用者、reason、revision），
#      而不是只有后台试算写的那一条；
#   3. 重新打开后真实请求恢复；
#   4. 安全与恢复路径（Kill Switch 审批/执行/恢复）**不受开关影响** —— 能力开关不能变成安全开关。
#
# 运行（VM）：bash run-sprint7-featureflags.sh
# 输出：acceptance-evidence/dual-system-v1/tests/sprint7/qa/featureflags/
set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7/qa/featureflags}"
mkdir -p "$EV"
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then set -a; . /tmp/s6-e2e-env.sh; set +a; fi
if [ -f /tmp/dual-system-fixture.env ]; then set -a; . /tmp/dual-system-fixture.env; set +a; fi

BASE=${BASE:-http://127.0.0.1:8080}
PG="docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA"
PASS=0; FAIL=0
log(){ echo "$*" | tee -a "$EV/run.log"; }
ok(){ PASS=$((PASS+1)); log "  PASS $1"; }
bad(){ FAIL=$((FAIL+1)); log "  FAIL $1"; }
chk(){ if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1 got=$2 want=$3"; fi; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
code(){ curl -s -o /tmp/ff-body.json -w "%{http_code}" "$@"; }

# ---- 登录（后台 Owner + 用户层调查员）----
login(){ # login <jar> <email> <pw> <aud> [ws]
  local jar=$1 email=$2 pw=$3 aud=$4 ws=${5:-}
  local body="{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\""
  [ -n "$ws" ] && body="$body,\"workspace_id\":\"$ws\""
  body="$body}"
  curl -s -c "$jar" -H "Content-Type: application/json" -H "X-Auth-Audience: $aud" -d "$body" "$BASE/api/auth/v1/login"
}
JA=/tmp/ff-owner.jar; JI=/tmp/ff-inv.jar; rm -f "$JA" "$JI"
OWNER=$(login "$JA" admin@local "$ADMIN_PASSWORD" admin)
chk "SETUP Owner 后台登录" "$(echo "$OWNER" | jget 'd["success"]')" "True"
INV=$(login "$JI" fixture-investigator@fixture.local "$INVESTIGATOR_PASSWORD" user "$FIXTURE_WORKSPACE_ID")
chk "SETUP 调查员用户层登录" "$(echo "$INV" | jget 'd["success"]')" "True"
CSRFA=$(awk '$6=="cs_admin_session_csrf"{print $7}' "$JA" | tail -1)
CSRFI=$(awk '$6=="cs_user_session_csrf"{print $7}' "$JI" | tail -1)
OWNER_ID=$(echo "$OWNER" | jget 'd["data"]["user"]["id"]')
INV_ID=$(echo "$INV" | jget 'd["data"]["user"]["id"]')
log "owner=$OWNER_ID investigator=$INV_ID"

flag_revision(){ $PG -c "select revision from feature_flags where key='$1'"; }
# set_flag 必须写成**单行** curl：多行续行在无引号包裹时会被 shell 拆成多条命令
# （2026-09-16 首次运行的真实教训：PATCH 从未执行，于是"关掉开关"根本没发生，
#  后续 503 断言全部落空）。
set_flag(){ # set_flag <key> <enabled> <percentage> <reason>
  local key=$1 enabled=$2 pct=$3 reason=$4
  local rev; rev=$(flag_revision "$key")
  curl -s -b "$JA" -H "X-CSRF-Token: $CSRFA" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "If-Match: $rev" -X PATCH -d "{\"enabled\":$enabled,\"percentage\":$pct,\"reason\":\"$reason\"}" "$BASE/api/admin/v1/feature-flags/$key" -o /tmp/ff-patch.json -w '%{http_code}'
}
eval_rows(){ # eval_rows <flag> <subject_id> <since_iso>
  $PG -c "select count(*) from feature_flag_evaluations where flag_key='$1' and subject_id='$2' and evaluated_at > '$3'"
}

log "==== §7.3 功能开关真实请求路径验收 $(date -Is) ===="

# ---- 用例 A：user.explorer_v2 关掉之后，真实 Explorer 请求被拒绝 ----
log "== A. user.explorer_v2 =="
ADDR=${E2E_S5_ADDRESS:-TU585SoBMd3hp9WaLc7yctSbR9fSmXsm32}
URL="$BASE/api/v2/explorer/addresses/tron/$ADDR/overview"
C=$(code -b "$JI" -H 'X-Auth-Audience: user' "$URL"); chk "A1 开关开启时真实 Explorer 请求 200" "$C" "200"
MARK=$(date -u +%Y-%m-%dT%H:%M:%SZ)
P=$(set_flag user.explorer_v2 false 0 "验收：关闭 Explorer v2"); chk "A2 关闭开关的 PATCH 返回 200" "$P" "200"
C=$(code -b "$JI" -H 'X-Auth-Audience: user' "$URL")
chk "A3 开关关闭后真实请求 503" "$C" "503"
EC=$(python3 -c "import json;print(json.load(open('/tmp/ff-body.json'))['error']['code'])" 2>/dev/null || echo "?")
chk "A4 错误码是稳定的 FEATURE_DISABLED" "$EC" "FEATURE_DISABLED"
FLAGKEY=$(python3 -c "import json;print(json.load(open('/tmp/ff-body.json'))['error']['details']['flag'])" 2>/dev/null || echo "?")
chk "A5 错误详情带回 flag key" "$FLAGKEY" "user.explorer_v2"
ROWS=$(eval_rows user.explorer_v2 "$INV_ID" "$MARK")
chk "A6 真实流量在 feature_flag_evaluations 留下拒绝记录" "$([ "${ROWS:-0}" -ge 1 ] && echo yes || echo no)" "yes"
REASON=$($PG -c "select reason from feature_flag_evaluations where flag_key='user.explorer_v2' and subject_id='$INV_ID' order by evaluated_at desc limit 1")
chk "A7 记录里的 reason 来自真实求值" "$REASON" "disabled"
P=$(set_flag user.explorer_v2 true 100 "验收：恢复 Explorer v2"); chk "A8 恢复开关的 PATCH 返回 200" "$P" "200"
C=$(code -b "$JI" -H 'X-Auth-Audience: user' "$URL"); chk "A9 恢复后真实请求重新 200" "$C" "200"

# ---- 用例 B：admin.alert_center 关掉之后，后台告警中心接口被拒绝 ----
log "== B. admin.alert_center =="
AURL="$BASE/api/admin/v1/alerts"
C=$(code -b "$JA" -H 'X-Auth-Audience: admin' "$AURL"); chk "B1 开关开启时告警列表 200" "$C" "200"
MARK=$(date -u +%Y-%m-%dT%H:%M:%SZ)
P=$(set_flag admin.alert_center false 0 "验收：关闭告警中心"); chk "B2 关闭开关的 PATCH 返回 200" "$P" "200"
C=$(code -b "$JA" -H 'X-Auth-Audience: admin' "$AURL"); chk "B3 开关关闭后告警列表 503" "$C" "503"
EC=$(python3 -c "import json;print(json.load(open('/tmp/ff-body.json'))['error']['code'])" 2>/dev/null || echo "?")
chk "B4 错误码 FEATURE_DISABLED" "$EC" "FEATURE_DISABLED"
ROWS=$(eval_rows admin.alert_center "$OWNER_ID" "$MARK")
chk "B5 真实流量留下评估记录" "$([ "${ROWS:-0}" -ge 1 ] && echo yes || echo no)" "yes"

# ---- 用例 C：安全与恢复路径不受开关影响 ----
log "== C. 安全路径不被开关影响 =="
RURL="$BASE/api/admin/v1/control/kill-switch/requests"
C=$(code -b "$JA" -H 'X-Auth-Audience: admin' -X POST -H "X-CSRF-Token: $CSRFA" -H "Content-Type: application/json" -d '{}' "$RURL")
if [ "$C" = "503" ]; then bad "C1 发起 Kill Switch 请求不得被开关拒绝（got 503）"; else ok "C1 发起 Kill Switch 请求仍可到达鉴权/校验层（$C，不是 503）"; fi
EC=$(python3 -c "import json;print(json.load(open('/tmp/ff-body.json')).get('error',{}).get('code'))" 2>/dev/null || echo "-")
chk "C2 该响应的错误码不是 FEATURE_DISABLED" "$([ "$EC" = "FEATURE_DISABLED" ] && echo yes || echo no)" "no"
APURL="$BASE/api/admin/v1/approvals"
C=$(code -b "$JA" -H 'X-Auth-Audience: admin' "$APURL"); chk "C3 审批中心在开关关闭时仍可用" "$C" "200"
P=$(set_flag admin.alert_center true 100 "验收：恢复告警中心"); chk "C4 恢复告警中心开关" "$P" "200"
C=$(code -b "$JA" -H 'X-Auth-Audience: admin' "$AURL"); chk "C5 恢复后告警列表重新 200" "$C" "200"

log "==== 结果：PASS=$PASS FAIL=$FAIL ===="
python3 - "$EV" "$PASS" "$FAIL" <<'PY'
import json, sys
ev, p, f = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
json.dump({"passed": p, "failed": f, "status": "PASS" if f == 0 and p > 0 else "FAIL",
           "criteria": "failed==0 and passed>0", "suite": "featureflags-real-path"},
          open(ev + "/result.json", "w"), ensure_ascii=False, indent=2)
PY
[ "$FAIL" -eq 0 ] || exit 1
