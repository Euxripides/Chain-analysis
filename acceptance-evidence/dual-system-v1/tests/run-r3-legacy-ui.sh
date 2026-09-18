#!/usr/bin/env bash
# R-3 旧 V1 前端（/api-control、/system）admin 受众缺陷 —— 真实浏览器验收 runner。
#
# 约定：
#   - 必须在 **HTTPS 回环验收入口**（默认 https://127.0.0.1:8448）上执行：
#     nginx 对 /api 写死 X-Forwarded-Proto https，后端下发 Secure/__Host- Cookie，
#     http 源下 Playwright APIRequestContext 会丢弃 Cookie 造成假失败；
#   - 证据目录一律新建（时间戳命名），已存在即拒绝执行，绝不覆盖历史证据；
#   - 真实口令只从 /tmp/s6-e2e-env.sh 与 deploy/.env 读取，不写入证据；
#   - 审计证据走**容器内已授权的只读 psql**（docker exec），不扩大任何账号权限。
#
# 用法（VM）：
#   bash acceptance-evidence/dual-system-v1/tests/run-r3-legacy-ui.sh
# 输出：acceptance-evidence/dual-system-v1/sprint7/ui-r3-<UTC 时间戳>/

set -o pipefail
PROJECT="$PROJECT"
[ -n "$PROJECT" ] || PROJECT=/home/euripides/projects/blockchain-platform
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$R3_OUT"
if [ -z "$OUT" ]; then OUT="$PROJECT/acceptance-evidence/dual-system-v1/sprint7/ui-r3-$TS"; fi

if [ -e "$OUT" ]; then
  echo "证据目录已存在，拒绝覆盖：$OUT"
  exit 9
fi
mkdir -p "$OUT"

export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR"
[ -n "$XDG_RUNTIME_DIR" ] || XDG_RUNTIME_DIR="/run/user/$(id -u)"
export XDG_RUNTIME_DIR
set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then set -a; . /tmp/s6-e2e-env.sh; set +a; fi

E2E_CHROMIUM_PATH="$E2E_CHROMIUM_PATH"
[ -n "$E2E_CHROMIUM_PATH" ] || E2E_CHROMIUM_PATH=/usr/bin/chromium-browser
E2E_BASE_URL="$E2E_BASE_URL"
[ -n "$E2E_BASE_URL" ] || E2E_BASE_URL=https://127.0.0.1:8448
export E2E_CHROMIUM_PATH E2E_BASE_URL R3_OUT="$OUT"

[ -x "$E2E_CHROMIUM_PATH" ] || { echo "缺少 Chromium：$E2E_CHROMIUM_PATH"; exit 2; }

# ---------- 入口前置检查 ----------
PREFLIGHT="$OUT/entry-preflight.txt"
{
  echo "entry=$E2E_BASE_URL"
  echo "checked_at=$(date -Is)"
  case "$E2E_BASE_URL" in
    https://*) echo "scheme=https OK" ;;
    *) echo "scheme=NOT-https"; echo "结论：拒绝执行（Secure Cookie 会被 Playwright APIRequestContext 丢弃）。"; exit 3 ;;
  esac
  curl -sk -o /dev/null -w "health_live=%{http_code}\n" "$E2E_BASE_URL/health/live" || { echo "结论：入口不可达"; exit 2; }
  echo "--- 关键入口探测（真实状态码）---"
  for u in / /admin/login /api-control /system /health/ready; do
    printf '%-16s %s\n' "$u" "$(curl -sk -o /dev/null -w '%{http_code}' "$E2E_BASE_URL$u")"
  done
  echo "--- 静态资源归属（V1 SPA 由 vite:3000 提供）---"
  curl -sk "$E2E_BASE_URL/" | grep -o '<title>[^<]*</title>' || true
  echo "--- 监听归属 ---"
  ss -ltn | grep -E ':8448|:8088|:3000|:3001|:3002|:8080' || true
} 2>&1 | tee "$PREFLIGHT"
grep -q "scheme=https OK" "$PREFLIGHT" || { echo "入口前置检查失败，终止"; exit 3; }

# ---------- 部署身份（未经核实不得写入结论）----------
IDENT="$OUT/deployment-identity.txt"
{
  echo "captured_at=$(date -Is)"
  echo "project=$PROJECT"
  echo "--- systemctl --user show -p ExecStart ---"
  systemctl --user show -p ExecStart -p ActiveState -p SubState -p MainPID blockchain-api.service blockchain-worker.service 2>&1
  echo "--- 前端服务 ---"
  systemctl --user show -p ExecStart -p MainPID blockchain-vite.service blockchain-admin-console.service 2>&1 | grep -E 'ExecStart|MainPID'
  echo "--- /proc/<PID>/exe ---"
  for u in blockchain-api.service blockchain-worker.service; do
    p=$(systemctl --user show -p MainPID --value "$u")
    echo "$u MainPID=$p exe=$(readlink -f /proc/$p/exe 2>&1)"
  done
  echo "--- sha256(binary on disk) ---"
  sha256sum "$PROJECT/backend/bin/api" "$PROJECT/backend/bin/worker"
  echo "--- sha256(/proc/<PID>/exe) ---"
  ap=$(systemctl --user show -p MainPID --value blockchain-api.service)
  wp=$(systemctl --user show -p MainPID --value blockchain-worker.service)
  sha256sum "/proc/$ap/exe" "/proc/$wp/exe" 2>&1
  echo "--- 前端产物的真实 sha256（随本轮源码一起记录）---"
  sha256sum "$PROJECT/frontend/src/api/client.ts" \
            "$PROJECT/frontend/src/App.tsx" \
            "$PROJECT/frontend/src/auth/AdminSession.tsx" \
            "$PROJECT/frontend/src/components/BackendLoginDialog.tsx" \
            "$PROJECT/frontend/src/pages/SystemPage.tsx" \
            "$PROJECT/frontend/apps/admin-console/src/App.tsx" \
            "$PROJECT/frontend/apps/admin-console/src/pages/LoginPage.tsx" \
            "$PROJECT/frontend/apps/admin-console/src/lib/returnTo.ts" 2>&1
  echo "--- 管理后台静态产物（vite preview 直接读盘）---"
  ls -la "$PROJECT/frontend/apps/admin-console/dist/assets" 2>&1 | tail -n 5
} 2>&1 | tee "$IDENT"

# ---------- 真实凭证（只进环境，不进证据）----------
E2E_R3_ADMIN_EMAIL="$E2E_R3_ADMIN_EMAIL"
[ -n "$E2E_R3_ADMIN_EMAIL" ] || E2E_R3_ADMIN_EMAIL=admin@local
E2E_R3_ADMIN_PASSWORD="$E2E_R3_ADMIN_PASSWORD"
[ -n "$E2E_R3_ADMIN_PASSWORD" ] || E2E_R3_ADMIN_PASSWORD="$ADMIN_PASSWORD"
E2E_R3_USER_EMAIL="$E2E_R3_USER_EMAIL"
[ -n "$E2E_R3_USER_EMAIL" ] || E2E_R3_USER_EMAIL=fixture-investigator@fixture.local
E2E_R3_USER_PASSWORD="$E2E_R3_USER_PASSWORD"
[ -n "$E2E_R3_USER_PASSWORD" ] || E2E_R3_USER_PASSWORD="$INVESTIGATOR_PASSWORD"
export E2E_R3_ADMIN_EMAIL E2E_R3_ADMIN_PASSWORD E2E_R3_USER_EMAIL E2E_R3_USER_PASSWORD

ADMIN_LEN=$(printf %s "$E2E_R3_ADMIN_PASSWORD" | wc -c)
USER_LEN=$(printf %s "$E2E_R3_USER_PASSWORD" | wc -c)
{
  echo "admin_email=$E2E_R3_ADMIN_EMAIL admin_password_len=$ADMIN_LEN"
  echo "user_email=$E2E_R3_USER_EMAIL user_password_len=$USER_LEN"
} > "$OUT/credentials-preflight.txt"
[ "$ADMIN_LEN" -gt 0 ] || { echo "缺少后台账号口令（ADMIN_PASSWORD），终止"; exit 4; }
[ "$USER_LEN" -gt 0 ] || { echo "缺少用户层账号口令（INVESTIGATOR_PASSWORD），终止"; exit 4; }

AUDIT_FROM="$(date -Is)"

# ---------- 真实浏览器执行 ----------
SPECS="$SPECS"
[ -n "$SPECS" ] || SPECS="tests/r3-legacy-ui-audience.spec.ts tests/r3-legacy-ui-negative.spec.ts tests/r3-nav-return-to.spec.ts tests/r3-chart-axes.spec.ts"

cd "$PROJECT/frontend"
PLAYWRIGHT_JSON_OUTPUT_NAME="$OUT/playwright-report.json" \
  node node_modules/@playwright/test/cli.js test --config playwright.r3.config.ts \
  $SPECS --reporter=list,json >"$OUT/playwright.log" 2>&1
RC=$?
tail -n 25 "$OUT/playwright.log"

AUDIT_TO="$(date -Is)"
sleep 3

# ---------- 审计证据（容器内已授权的只读采集）----------
python3 "$PROJECT/acceptance-evidence/dual-system-v1/tests/r3-collect-audit.py" "$OUT" "$AUDIT_FROM" "$AUDIT_TO"
AUDIT_RC=$?
echo "audit_collector_rc=$AUDIT_RC"

# ---------- 汇总（判据以 JSON reporter + 审计采集状态为准）----------
python3 "$PROJECT/acceptance-evidence/dual-system-v1/tests/r3-summarize.py" "$OUT" "$RC"
SUM_RC=$?

if [ "$RC" -ne 0 ] || [ "$SUM_RC" -ne 0 ]; then
  echo "R-3 RESULT: FAIL (playwright_rc=$RC summarize_rc=$SUM_RC audit_rc=$AUDIT_RC)"
  exit 1
fi
echo "R-3 RESULT: PASS"
exit 0
