#!/usr/bin/env bash
# run-r9-download-import-v3.sh — U-DL-01 导入验收 v3：真实浏览器 + 真实服务端 + 真实 DB 核对。
# 冻结后不再修改。v1/v2 文件与其证据一律保留。
#
# 与 v2 的差别：
#   - preflight：缺少凭据/地址直接非零退出，绝不把 skip 当绿色；
#   - outputDir 落在当轮唯一 EVID（Playwright 每次运行会清空 outputDir）；
#   - 产出机器可读 playwright-report.json + run-matrix.json（每个 project×test 组合的预期与实际）；
#   - 预期/实际不一致（尤其"应跑却 skip"）→ 非零退出。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-v3-$TS}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"

echo "=== P0 preflight（缺项即失败，不生成绿色假象） ==="
PRE_MISSING=0
for k in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW S7JS_INVESTIGATOR_WS S7JS_ORG_ID; do
  if [ ! -f /home/euripides/.s7jobscope-creds.env ]; then echo "MISSING creds file"; PRE_MISSING=1; break; fi
done
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW S7JS_INVESTIGATOR_WS; do
  eval "val=\$$v"
  if [ -z "${val:-}" ]; then echo "PREFLIGHT_MISSING $v"; PRE_MISSING=1; fi
done
CHAIN="${R9_CHAIN:-bsc}"
ADDRESS="${R9_ADDRESS:-0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649}"
[ -z "$ADDRESS" ] && { echo "PREFLIGHT_MISSING R9_ADDRESS"; PRE_MISSING=1; }
if [ "$PRE_MISSING" -ne 0 ]; then echo "PREFLIGHT_FAILED - refusing to run (no green from missing inputs)" | tee -a "$EV/run-meta.txt"; exit 3; fi
echo "preflight=OK chain=$CHAIN address=$ADDRESS" | tee -a "$EV/run-meta.txt"

WS="$S7JS_INVESTIGATOR_WS"
CASE_NAME="${R9_MISSING_CASE:-R9-MISSING-CASE-20260913}"
TAG_NAME="${R9_MISSING_TAG:-R9-MISSING-TAG-20260913}"
FIX="$EV/fixtures"; mkdir -p "$FIX"
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1; }

{
  echo "workspace_id=$WS chain=$CHAIN address=$ADDRESS case_name=$CASE_NAME tag_name=$TAG_NAME"
  echo "cases_in_ws=$(printf "SELECT count(*) FROM cases WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$CASE_NAME" | pgq)"
  echo "tags_in_ws=$(printf "SELECT count(*) FROM tags WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$TAG_NAME" | pgq)"
  echo "case_links_before=$(printf "SELECT count(*) FROM address_case_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
  echo "tag_links_before=$(printf "SELECT count(*) FROM address_tag_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
  echo "jobs_before=$(printf "SELECT count(*) FROM download_jobs WHERE workspace_id='%s';\n" "$WS" | pgq)"
} | tee "$EV/p0-pre-state.txt"

echo "=== P1 fixtures ==="
cd "$R/frontend" || exit 90
node scripts/r9-make-fixtures-v3.mjs "$FIX" "$ADDRESS" "$CHAIN" "$CASE_NAME" "$TAG_NAME" | tee "$EV/fixtures-manifest.json"
for f in "$FIX"/*; do sha256sum "$f"; done > "$EV/fixtures-sha256.txt"
cat "$EV/fixtures-sha256.txt"

echo "=== P2 real browser run ==="
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R9_OUT="$EV" R9_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R9_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R9_ADDRESS="$ADDRESS" R9_CHAIN="$CHAIN" R9_FIXTURES="$FIX"
export R9_MISSING_CASE="$CASE_NAME" R9_MISSING_TAG="$TAG_NAME"
node node_modules/@playwright/test/cli.js test tests/r9-uc-download-import-v3.spec.ts \
  --config playwright.r9v3.config.ts --project=uc-desktop --project=uc-mobile --reporter=list \
  > "$EV/playwright.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 40 "$EV/playwright.log"

echo "=== P3 project × test 矩阵判定（机器 JSON） ==="
python3 - "$EV" <<'PY' | tee "$EV/run-matrix.txt"
import json, sys, os
ev = sys.argv[1]
desktop = {"R9-01","R9-02","R9-03","R9-04","R9-05","R9-07"}
mobile = {"R9-06"}
tests = ["R9-01","R9-02","R9-03","R9-04","R9-05","R9-06","R9-07"]
report_path = os.path.join(ev, "playwright-report.json")
actual = {}
try:
    rep = json.load(open(report_path))
    for suite in rep.get("suites", []):
        for spec in suite.get("specs", []):
            for t in spec.get("tests", []):
                proj = t.get("projectName")
                title = spec.get("title", "")
                key = next((c for c in tests if title.startswith(c)), None)
                if key is None:
                    continue
                actual[(proj, key)] = t.get("status") or (t.get("results") or [{}])[0].get("status")
except Exception as e:
    print("REPORT_UNREADABLE", e)
rows = []
mismatch = []
for proj, runs in (("uc-desktop", desktop), ("uc-mobile", mobile)):
    for key in tests:
        expected = "run" if key in runs else "excluded-by-project-design"
        got = actual.get((proj, key), "MISSING")
        if expected == "run":
            ok = got == "passed"
        else:
            ok = got == "skipped"
        rows.append({"project": proj, "test": key, "expected": expected, "actual": got, "ok": ok})
        if not ok:
            mismatch.append({"project": proj, "test": key, "expected": expected, "actual": got})
summary = {
    "expected_run": sum(1 for r in rows if r["expected"] == "run"),
    "expected_excluded": sum(1 for r in rows if r["expected"].startswith("excluded")),
    "actual_passed": sum(1 for r in rows if r["actual"] == "passed"),
    "actual_skipped": sum(1 for r in rows if r["actual"] == "skipped"),
    "actual_failed_or_missing": sum(1 for r in rows if r["actual"] not in ("passed", "skipped")),
    "mismatches": mismatch,
}
out = {"scope_note": "R9-05 只证明 API 幂等；UI 多链第二组故障重试需独立故障注入夹具，未在本轮覆盖。",
       "breakpoints_note": "本轮只覆盖 1440x900 与 390x844 两个断点；§27 要求的全部 4 断点仍未闭合。",
       "tests": rows, "summary": summary}
json.dump(out, open(os.path.join(ev, "run-matrix.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(summary, indent=2, ensure_ascii=False))
sys.exit(1 if mismatch or summary["actual_failed_or_missing"] else 0)
PY
MIX=$?
echo "$MIX" > "$EV/run-matrix.rc"

echo "=== P4 DB cross-check of the job the UI really created ==="
JOB=$(python3 -c "
import json
try:
    d=json.load(open('$EV/uc-desktop/r9-01-evidence.json'))
    print(d['create_response'].get('job_id') or '')
except Exception:
    print('')
")
echo "job_id=$JOB" | tee "$EV/p4-db-crosscheck.txt"
if [ -n "$JOB" ]; then
  {
    printf "download_subjects_for_job=%s\n" "$(printf "SELECT count(*) FROM download_subjects WHERE job_id='%s' AND subject='%s';\n" "$JOB" "$ADDRESS" | pgq)"
    printf "job_row=%s\n" "$(printf "SELECT job_no||'|'||status||'|'||workspace_id::text FROM download_jobs WHERE id='%s';\n" "$JOB" | pgq)"
    printf "case_links_after=%s\n" "$(printf "SELECT count(*) FROM address_case_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
    printf "tag_links_after=%s\n" "$(printf "SELECT count(*) FROM address_tag_links WHERE workspace_id='%s' AND normalized_address='%s';\n" "$WS" "$ADDRESS" | pgq)"
    printf "cases_named_created=%s\n" "$(printf "SELECT count(*) FROM cases WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$CASE_NAME" | pgq)"
    printf "tags_named_created=%s\n" "$(printf "SELECT count(*) FROM tags WHERE workspace_id='%s' AND name='%s';\n" "$WS" "$TAG_NAME" | pgq)"
  } | tee -a "$EV/p4-db-crosscheck.txt"
else
  echo "NO JOB ID CAPTURED - DB cross-check cannot be claimed" | tee -a "$EV/p4-db-crosscheck.txt"
fi

echo "=== P5 retry evidence ==="
python3 - <<PY | tee "$EV/p5-retry-evidence.txt"
import json
try:
    d=json.load(open('$EV/uc-desktop/r9-05-evidence.json'))
    print('first_status', d['first']['status'], 'first_job', d['first']['body'].get('job_id'))
    print('second_status', d['second']['status'], 'second_job', d['second']['body'].get('job_id'))
    print('same_job', d['first']['body'].get('job_id') == d['second']['body'].get('job_id'))
except Exception as e:
    print('r9-05 evidence unavailable:', e)
PY

echo "EVIDENCE=$EV" | tee -a "$EV/run-meta.txt"
echo "matrix_rc=$MIX playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
[ "$RC" -ne 0 ] && exit "$RC"
[ "$MIX" -ne 0 ] && exit 4
exit 0
