#!/usr/bin/env bash
# run-r9-download-import.sh — U-DL-01 导入验收：真实浏览器 + 真实服务端 + 真实 DB 核对。
# 冻结后不再修改。旧失败证据一律保留，不覆盖任何既有目录。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-v2-$TS}"
mkdir -p "$EV"; chmod 700 "$EV"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV"

set -a; . /home/euripides/.s7jobscope-creds.env; set +a
WS="$S7JS_INVESTIGATOR_WS"
CHAIN="${R9_CHAIN:-bsc}"
ADDRESS="${R9_ADDRESS:-0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649}"
CASE_NAME="${R9_MISSING_CASE:-R9-MISSING-CASE-20260913}"
TAG_NAME="${R9_MISSING_TAG:-R9-MISSING-TAG-20260913}"
FIX="$EV/fixtures"
mkdir -p "$FIX"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1; }

echo "=== P0 pre-state: 目标工作区必须没有该案件/标签，避免'已存在'假通过 ==="
{
  echo "workspace_id=$WS chain=$CHAIN address=$ADDRESS case_name=$CASE_NAME tag_name=$TAG_NAME"
  echo "cases_in_ws=$(printf "SELECT count(*) FROM cases WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$CASE_NAME" | pgq)"
  echo "tags_in_ws=$(printf "SELECT count(*) FROM tags WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$TAG_NAME" | pgq)"
  echo "case_links_before=$(printf "SELECT count(*) FROM address_case_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
  echo "tag_links_before=$(printf "SELECT count(*) FROM address_tag_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
  echo "jobs_before=$(printf "SELECT count(*) FROM download_jobs WHERE workspace_id='%s';\n" "$WS" | pgq)"
} | tee "$EV/p0-pre-state.txt"

echo "=== P1 fixtures (真实 SheetJS 0.20.3 写盘) ==="
cd "$R/frontend" || exit 90
node scripts/r9-make-fixtures-v2.mjs "$FIX" "$ADDRESS" "$CHAIN" "$CASE_NAME" "$TAG_NAME" | tee "$EV/fixtures-manifest.json"
ls -la "$FIX" | tee -a "$EV/fixtures-manifest.json"
for f in "$FIX"/*; do sha256sum "$f"; done > "$EV/fixtures-sha256.txt"
cat "$EV/fixtures-sha256.txt"

echo "=== P2 real browser run ==="
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R9_OUT="$EV" R9_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R9_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R9_ADDRESS="$ADDRESS" R9_CHAIN="$CHAIN" R9_FIXTURES="$FIX"
export R9_MISSING_CASE="$CASE_NAME" R9_MISSING_TAG="$TAG_NAME"
node node_modules/@playwright/test/cli.js test tests/r9-uc-download-import-v2.spec.ts \
  --config playwright.r9v2.config.ts --project=uc-desktop --project=uc-mobile --reporter=list \
  > "$EV/playwright.log" 2>&1
RC=$?
echo "playwright_rc=$RC" | tee "$EV/playwright.rc"
tail -n 40 "$EV/playwright.log"

echo "=== P3 DB cross-check of the job the UI really created ==="
JOB=$(python3 -c "
import json,sys
try:
    d=json.load(open('$EV/uc-desktop/r9-01-evidence.json'))
    print(d['create_response'].get('job_id') or '')
except Exception:
    print('')
")
echo "job_id=$JOB" | tee "$EV/p3-db-crosscheck.txt"
if [ -n "$JOB" ]; then
  {
    printf "download_subjects_for_job=%s\n" "$(printf "SELECT count(*) FROM download_subjects WHERE job_id='%s' AND subject='%s';\n" "$JOB" "$ADDRESS" | pgq)"
    printf "job_row=%s\n" "$(printf "SELECT job_no||'|'||status||'|'||workspace_id::text FROM download_jobs WHERE id='%s';\n" "$JOB" | pgq)"
    printf "case_links_after=%s\n" "$(printf "SELECT count(*) FROM address_case_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
    printf "tag_links_after=%s\n" "$(printf "SELECT count(*) FROM address_tag_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
    printf "cases_named_created=%s\n" "$(printf "SELECT count(*) FROM cases WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$CASE_NAME" | pgq)"
    printf "tags_named_created=%s\n" "$(printf "SELECT count(*) FROM tags WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$TAG_NAME" | pgq)"
  } | tee -a "$EV/p3-db-crosscheck.txt"
else
  echo "NO JOB ID CAPTURED - DB cross-check cannot be claimed" | tee -a "$EV/p3-db-crosscheck.txt"
fi

echo "=== P4 r9-05 retry evidence (job ids must be identical) ==="
python3 - <<PY | tee "$EV/p4-retry-evidence.txt"
import json
try:
    d=json.load(open('$EV/uc-desktop/r9-05-evidence.json'))
    print('first_status', d['first']['status'], 'first_job', d['first']['body'].get('job_id'))
    print('second_status', d['second']['status'], 'second_job', d['second']['body'].get('job_id'))
    print('same_job', d['first']['body'].get('job_id') == d['second']['body'].get('job_id'))
except Exception as e:
    print('r9-05 evidence unavailable:', e)
PY

echo "EVIDENCE=$EV"
exit "$RC"
