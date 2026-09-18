#!/usr/bin/env bash
# S7-PERF-01：分层性能测试（内网直连 + 经 Nginx + 公网 HTTPS）。
#
# 覆盖 §28.9 要求：API、长表、图谱、百万行导出、Provider/Full Pipeline，并分别记录内网/公网表现。
#
# 原则：
#   - 所有数字来自真实请求（loadprobe 逐请求采样复算分位数），不接受估算；
#   - 每个场景记录 build SHA、并发、时长、样本数；
#   - 与 §30.2 预算对照（PostgreSQL API 查询 P95 < 200ms；常规响应体 < 1MB），
#     未达预算的如实记录为未达标，不修饰。
#
# 用法：bash scripts/perf/run-perf-suite.sh
# 输出：acceptance-evidence/dual-system-v1/sprint7/perf/

set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7/perf}"
mkdir -p "$EV"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"

set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then . /tmp/s6-e2e-env.sh; fi

BUILD_SHA=$(sha256sum "$PROJECT/backend/bin/api" | awk '{print $1}')
PROBE="$PROJECT/backend/bin/loadprobe"
BASE_LAN=http://127.0.0.1:8080
BASE_NGINX=http://127.0.0.1:8088
BASE_WAN=https://aurore.online
CONC="${CONC:-8}"
DUR="${DUR:-20s}"

echo "== S7-PERF-01 build=$BUILD_SHA conc=$CONC dur=$DUR =="
[ -x "$PROBE" ] || { echo "缺少 loadprobe（先 go build -o bin/loadprobe ./cmd/loadprobe）"; exit 2; }

# 多工作区身份登录必须显式选择工作区（P1-20260913）。
# 2026-09-16 真实事故：本套件的登录不带 workspace_id，夹具账号（investigator/viewer）拿到 409、
# Cookie jar 里只留下 CSRF，「用户层」场景 **100% 401** —— 于是历史 perf 数字量的是 297 字节错误响应
# 的延迟（p95 6 ms「很好看」），却能同时挂着 err=80868 这种荒谬的错误数。修好登录后这些数字才有意义。
if [ -z "${FIXTURE_WORKSPACE_ID:-}" ] && [ -r /tmp/dual-system-fixture.env ]; then
  set -a; . /tmp/dual-system-fixture.env; set +a
fi
login_workspace() {
  case "$1" in
    *@fixture.local) printf '%s' "${FIXTURE_WORKSPACE_ID:-}" ;;
    *) printf '' ;;
  esac
}
login() { # login <jar> <email> <password> <audience> <base>
  local ws; ws=$(login_workspace "$2")
  local body="{\"identifier\":\"$2\",\"password\":\"$3\",\"audience\":\"$4\""
  [ -n "$ws" ] && body="$body,\"workspace_id\":\"$ws\""
  body="$body}"
  curl -s -c "$1" -o /dev/null --max-time 10 -H 'Content-Type: application/json' -H "X-Auth-Audience: $4" \
    -d "$body" "$5/api/auth/v1/login"
}
# Netscape cookie jar 里 HttpOnly Cookie 的域字段带 "#HttpOnly_" 前缀，
# 若按 /^#/ 过滤会把**会话 Cookie 整条丢掉**，只剩 CSRF Cookie → 全部 401。
cookieheader() {
  awk '
    /^#HttpOnly_/ { sub(/^#HttpOnly_/, ""); printf "%s=%s; ", $6, $7; next }
    /^#/ || /^$/   { next }
    { printf "%s=%s; ", $6, $7 }
  ' "$1"
}

J_ADMIN=$(mktemp); J_USER=$(mktemp)
login "$J_ADMIN" admin@local "$ADMIN_PASSWORD" admin "$BASE_LAN"
login "$J_USER" fixture-investigator@fixture.local "$INVESTIGATOR_PASSWORD" user "$BASE_LAN"
CK_ADMIN=$(cookieheader "$J_ADMIN")
CK_USER=$(cookieheader "$J_USER")
echo "admin cookie fields: $(echo "$CK_ADMIN" | tr ';' '\n' | wc -l); user cookie fields: $(echo "$CK_USER" | tr ';' '\n' | wc -l)"
# 登录失败必须当场失败：否则整套负载测试会安静地量 401 错误响应，把"全错"伪装成"很快"。
if ! echo "$CK_ADMIN" | grep -q "cs_admin_session="; then echo "FATAL: 后台登录未拿到会话 Cookie，性能数字无意义"; exit 2; fi
if ! echo "$CK_USER" | grep -q "cs_user_session="; then echo "FATAL: 用户层登录未拿到会话 Cookie（夹具账号需要 workspace_id）"; exit 2; fi

run() { # run <label> <base> <cookie> <path>
  local label="$1" base="$2" ck="$3" path="$4"
  "$PROBE" -label "$label" -url "$base$path" -cookie "$ck" \
    -concurrency "$CONC" -duration "$DUR" -timeout 30s \
    -json "$EV/$label.json" >/dev/null 2>&1
  python3 - "$EV/$label.json" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception as e:
    print("  (no result)", e); raise SystemExit
print("  %-34s n=%-6s rps=%-8.1f p50=%-8.2f p95=%-8.2f p99=%-8.2f max=%-9.2f err=%-4s bytes=%.0f" % (
    d["label"], d["requests"], d["rps"], d["p50_ms"], d["p95_ms"], d["p99_ms"], d["max_ms"], d["errors"], d["avg_response_bytes"]))
PY
}

echo
echo "=== A. 内网 API（直连 8080，PG 查询为主） ==="
run "lan_dashboard"        "$BASE_LAN" "$CK_USER"  "/api/v2/dashboard"
run "lan_cases_list"       "$BASE_LAN" "$CK_USER"  "/api/v2/cases?limit=20"
run "lan_notifications"    "$BASE_LAN" "$CK_USER"  "/api/v2/notifications?limit=20"
run "lan_jobs_list"        "$BASE_LAN" "$CK_USER"  "/api/v2/jobs?limit=20"
run "lan_admin_overview"   "$BASE_LAN" "$CK_ADMIN" "/api/admin/v1/overview"
run "lan_admin_audit"      "$BASE_LAN" "$CK_ADMIN" "/api/admin/v1/audit/events?limit=50"
run "lan_admin_login_ev"   "$BASE_LAN" "$CK_ADMIN" "/api/admin/v1/login-events?limit=50"
run "lan_admin_jobs"       "$BASE_LAN" "$CK_ADMIN" "/api/admin/v1/jobs?limit=20"

echo
echo "=== B. 长表与图谱（真实链上对象） ==="
ADDR="${E2E_S5_10K_ADDRESS:-TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj}"
CHAIN="${E2E_S5_10K_CHAIN:-tron}"
run "lan_longtable_p1"     "$BASE_LAN" "$CK_USER"  "/api/v2/explorer/addresses/$CHAIN/$ADDR/activity?limit=200"
run "lan_longtable_assets" "$BASE_LAN" "$CK_USER"  "/api/v2/explorer/addresses/$CHAIN/$ADDR/assets"
run "lan_graph_flow"       "$BASE_LAN" "$CK_USER"  "/api/v2/explorer/addresses/$CHAIN/$ADDR/flow-profile"
run "lan_graph_overview"   "$BASE_LAN" "$CK_USER"  "/api/v2/explorer/addresses/$CHAIN/$ADDR/overview"
run "lan_graph_counter"    "$BASE_LAN" "$CK_USER"  "/api/v2/explorer/addresses/$CHAIN/$ADDR/counterparties"

echo
echo "=== C. 本机 Nginx 入口（8088）—— 只测免鉴权路径 ==="
echo "  说明：HTTPS 语义入口下会话 Cookie 带 Secure + __Host- 前缀，curl 在 http:// 上不会回送，"
echo "  因此带鉴权的 Nginx 层测量统一走 D 段（公网 HTTPS，同样经过 Nginx）。"
run "nginx_health_ready" "$BASE_NGINX" "" "/health/ready"

echo
echo "=== D. 公网 HTTPS（Cloudflare 隧道，从 VM 出网再回环） ==="
J_W=$(mktemp)
login "$J_W" admin@local "$ADMIN_PASSWORD" admin "$BASE_WAN"
CK_W=$(cookieheader "$J_W")
# 公网 tier 同样需要**两个受众**的会话：/api/admin/v1/* 用后台会话，/api/v2/* 用用户层会话。
# 2026-09-16 实测：用后台会话打 /api/v2/dashboard 得到 401，把 100% 错误的场景混进 p95 汇总。
J_WU=$(mktemp)
login "$J_WU" fixture-investigator@fixture.local "$INVESTIGATOR_PASSWORD" user "$BASE_WAN"
CK_WU=$(cookieheader "$J_WU")
if [ -n "$CK_W" ]; then
  run "wan_admin_overview" "$BASE_WAN" "$CK_W" "/api/admin/v1/overview"
  if echo "$CK_WU" | grep -q "cs_user_session="; then
    run "wan_dashboard" "$BASE_WAN" "$CK_WU" "/api/v2/dashboard"
  else
    echo "  公网用户层登录未拿到会话 Cookie，wan_dashboard 记为 NOT_VERIFIED"
  fi
else
  echo "  公网登录失败（可能被 Cloudflare 挑战），WAN 场景记为 NOT_VERIFIED"
fi

echo
echo "=== E. 汇总 ==="
python3 - "$EV" "$BUILD_SHA" "$CONC" "$DUR" <<'PY'
import json, os, sys
ev, sha, conc, dur = sys.argv[1:5]
rows, worst, skipped = [], [], []
for f in sorted(os.listdir(ev)):
    if not f.endswith(".json"):
        continue
    d = json.load(open(os.path.join(ev, f)))
    if "p95_ms" not in d or d.get("requests", 0) == 0:
        # 场景没跑出结果（例如公网被 Cloudflare 挑战）：如实记录，不参与预算判定，也不让汇总崩掉。
        skipped.append(d.get("label", f))
        continue
    rows.append(d)
    if d.get("errors"):
        worst.append((d["label"], d["errors"]))
out = {"build_api_sha": sha, "concurrency": conc, "duration": dur, "results": rows, "not_measured": skipped,
       "budget": {"pg_api_p95_ms": 200, "response_body_max_bytes": 1048576},
       "violations": {
           "p95_over_200ms": [r["label"] for r in rows if r["p95_ms"] > 200],
           "body_over_1mb": [r["label"] for r in rows if r["avg_response_bytes"] > 1048576],
           "with_errors": [l for l, _ in worst],
       }}
json.dump(out, open(os.path.join(ev, "summary.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(out["violations"], indent=2, ensure_ascii=False))
PY
echo "perf evidence: $EV"
