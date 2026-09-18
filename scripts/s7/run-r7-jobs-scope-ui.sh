#!/usr/bin/env bash
# run-r7-jobs-scope-ui.sh — R7 真实浏览器回归（真实登录后旧 V1 /jobs 行数 == API == DB）。
#
# 登录入口修正（根监控只读核实）：用户控制台 BrowserRouter basename=/app-v2，
# 登录页是 /app-v2/login；根 /login 是旧 V1 SPA fallback，匿名也能渲染旧 shell，不能作为已登录证据。
# SQL 绑定：psql 的 :'var' 插值只在 stdin/文件模式生效，-c 不做插值，故经管道送入并加 -i。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/jobs-scope-r7-ui-$TS}"
mkdir -p "$EV"; chmod 700 "$EV"
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
DBIDS="$(echo "SELECT string_agg(id::text, chr(44)) FROM download_jobs WHERE organization_id = :'org'::uuid AND workspace_id = :'ws'::uuid" | docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v org="$S7JS_ORG_ID" -v ws="$S7JS_WS_S7JS_A")"
FORBID="$(echo "SELECT string_agg(job_no, chr(44)) FROM download_jobs WHERE organization_id = :'org'::uuid AND workspace_id = :'ws'::uuid" | docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v org="$S7JS_ORG_ID" -v ws="$S7JS_WS_S7JS_B")"
[ -n "$DBIDS" ] || { echo "DB baseline empty - refusing to run R7 (no baseline, no false pass)"; exit 2; }
echo "s7js_org=$S7JS_ORG_ID"
echo "db_ids=$DBIDS"
echo "forbidden_job_nos=$FORBID"
cd "$R/frontend"
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R7_OUT="$EV" R7_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R7_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R7_EXPECTED_ROWS=3 R7_DB_IDS="$DBIDS" R7_FORBIDDEN_JOB_NOS="$FORBID"
node node_modules/@playwright/test/cli.js test tests/dual-system-r7-jobs-scope.spec.ts \
  --config playwright.dual.config.ts --project=user-desktop --project=user-mobile --reporter=list \
  > "$EV/playwright.log" 2>&1
RC=$?
echo "playwright_rc=$RC"
tail -n 22 "$EV/playwright.log"
echo "EVIDENCE=$EV"
ls -la "$EV"
[ -f "$EV/r7-ui-evidence.json" ] && head -c 1400 "$EV/r7-ui-evidence.json"
exit "$RC"
