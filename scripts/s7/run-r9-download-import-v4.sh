#!/usr/bin/env bash
# run-r9-download-import-v4.sh — U-DL-01 导入验收 v4：真实浏览器 + 真实服务端 + 真实 DB 核对。
#
# 本轮（《新建数据下载页面全量整改实施规范》§12）修正 driver 侧的真实缺陷：
#   旧版在命令行又传了 --reporter=list --reporter=json。命令行 reporter **覆盖**
#   playwright.r9v4.config.ts 里的 reporter，JSON 只写 stdout，playwright-report.json
#   从不生成，run-matrix 全项 MISSING（引擎侧假失败）。
#   现在只在 config 声明 reporter；runner 对报告做 fail-closed 检查，且 run-matrix.rc 进最终 gate。
#
# 判据（§12.3 / §12.4）：
#   1) playwright-report.json 存在、非空、可解析、suites 非空；
#   2) stats 与设计一致（expected=7 / unexpected=0 / skipped=7 / flaky=0，共 14 个 project×test）；
#   3) 每个 project×test 都能映射到 passed/skipped/failed；
#   4) expected run 项不得 MISSING；expected excluded 项必须 skipped；
#   5) fixtures rc / 夹具自检 rc / playwright rc / report-check rc / matrix rc 全 0，且日志无 Traceback。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-v4-$TS}"
REMED="${REMED:-$R/frontend/acceptance-evidence/download-flowline-remediation}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
GATE="$EV/gate.txt"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"
echo "started_utc=$TS" | tee -a "$EV/run-meta.txt"

echo "=== P0 preflight（缺项即失败，不生成绿色假象） ==="
PRE_MISSING=0
for k in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW S7JS_INVESTIGATOR_WS S7JS_ORG_ID; do
  if [ ! -f /home/euripides/.s7jobscope-creds.env ]; then echo "MISSING creds file"; PRE_MISSING=1; break; fi
done
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW S7JS_INVESTIGATOR_WS; do
  eval "val=$$v"
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

echo "=== P1 fixtures（多条目 ZIP + OOXML 必需条目 + 三个安全分支） ==="
cd "$R/frontend" || exit 90
set -o pipefail
node scripts/r9-make-fixtures-v4.mjs "$FIX" "$ADDRESS" "$CHAIN" "$CASE_NAME" "$TAG_NAME" 2>&1 | tee "$EV/fixtures-manifest.json"
FIXRC=${PIPESTATUS[0]}
echo "fixtures_rc=$FIXRC" | tee -a "$EV/run-meta.txt"
for f in "$FIX"/*; do sha256sum "$f"; done > "$EV/fixtures-sha256.txt"
cat "$EV/fixtures-sha256.txt"

echo "=== P1b 夹具机器自检（§11.5，fail-closed） ==="
SELF_RC=1
python3 - "$FIX/r9-07-fixture-selfcheck.json" > "$EV/p1b-fixture-selfcheck.txt" 2>&1 <<'PY'
import json, os, sys
path = sys.argv[1]
problems = []
if not os.path.exists(path) or os.path.getsize(path) == 0:
    print("FIXTURE_SELFCHECK_MISSING " + path)
    sys.exit(1)
data = json.load(open(path, encoding="utf-8"))
if data.get("ok") is not True:
    problems.append("selfcheck.ok != true: %r" % (data.get("ok"),))
intents = {row.get("file"): row for row in (data.get("bomb_intents") or [])}
for f in ("bomb-size.xlsx", "bomb-ratio.xlsx", "bomb-lie.xlsx"):
    row = intents.get(f)
    if row is None:
        problems.append("缺少夹具意图记录：" + f)
        continue
    if row.get("structure_ok") is not True:
        problems.append(f + " ZIP/OOXML 结构自检未通过")
    if row.get("intent_matches_central_directory") is not True:
        problems.append(f + " 意图与中央目录字段不一致")
for row in (data.get("fixtures") or []):
    if row.get("under_max_file_bytes") is not True:
        problems.append("文件超过 16MiB 上限：" + str(row.get("file")))
    if row.get("zip_magic") is False or row.get("eocd_present") is False:
        problems.append("ZIP 魔数 / EOCD 自检未通过：" + str(row.get("file")))
    if row.get("required_ooxml_entries") is False:
        problems.append("缺少 OOXML 必需条目：" + str(row.get("file")))
print(json.dumps({"ok": not problems, "problems": problems,
                  "bomb_intents": data.get("bomb_intents"),
                  "self_check": path}, ensure_ascii=False, indent=2))
sys.exit(1 if problems else 0)
PY
SELF_RC=$?
echo "fixture_selfcheck_rc=$SELF_RC" | tee -a "$EV/run-meta.txt"
head -c 1200 "$EV/p1b-fixture-selfcheck.txt"

echo "=== P2 real browser run（reporter 只在 config 里声明，命令行不得覆盖） ==="
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R9_OUT="$EV" R9_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R9_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R9_ADDRESS="$ADDRESS" R9_CHAIN="$CHAIN" R9_FIXTURES="$FIX"
export R9_MISSING_CASE="$CASE_NAME" R9_MISSING_TAG="$TAG_NAME"
node node_modules/@playwright/test/cli.js test tests/r9-uc-download-import-v4.spec.ts \
  --config playwright.r9v4.config.ts \
  --project=uc-desktop \
  --project=uc-mobile \
  > "$EV/playwright.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 40 "$EV/playwright.log"

echo "=== P3 报告 fail-closed 检查（存在 / 非空 / 可解析 / suites / stats） ==="
REPORT_RC=1
python3 - "$EV/playwright-report.json" > "$EV/p3-report-check.txt" 2>&1 <<'PY'
import json, os, sys
path = sys.argv[1]
problems = []
if not os.path.exists(path) or os.path.getsize(path) == 0:
    print(json.dumps({"ok": False, "problems": ["playwright-report.json 缺失或为空"], "path": path}, ensure_ascii=False))
    sys.exit(1)
try:
    rep = json.load(open(path, encoding="utf-8"))
except Exception as exc:  # noqa: BLE001
    print(json.dumps({"ok": False, "problems": ["JSON 不可解析: %r" % (exc,)]}, ensure_ascii=False))
    sys.exit(1)
suites = rep.get("suites") or []
if not suites:
    problems.append("suites 为空")
stats = rep.get("stats") or {}
design = {"expected": 7, "unexpected": 0, "skipped": 7, "flaky": 0}
for key, want in design.items():
    if stats.get(key) != want:
        problems.append("stats.%s=%r != 设计值 %r" % (key, stats.get(key), want))
allowed = ("passed", "skipped", "failed")
observed = {}
def walk(node, hint):
    project = node.get("title") if node.get("title") in ("uc-desktop", "uc-mobile") else hint
    for spec in node.get("specs") or []:
        title = spec.get("title", "") or ""
        for item in spec.get("tests") or []:
            proj = item.get("projectName") or project
            results = item.get("results") or [{}]
            status = results[-1].get("status")
            if proj:
                observed[(proj, title)] = status
    for child in node.get("suites") or []:
        walk(child, project)
for suite in suites:
    walk(suite, None)
bad = {("%s/%s" % k): v for k, v in observed.items() if v not in allowed}
if bad:
    problems.append("存在不能映射为 passed/skipped/failed 的 project×test：%r" % (bad,))
if len(observed) != 14:
    problems.append("project×test 组合数 %d != 14（2 project × 7 用例）" % len(observed))
print(json.dumps({"ok": not problems, "problems": problems, "stats": stats,
                  "observed_count": len(observed),
                  "observed": {"%s/%s" % k: v for k, v in sorted(observed.items())}},
                 ensure_ascii=False, indent=2))
sys.exit(1 if problems else 0)
PY
REPORT_RC=$?
echo "report_check_rc=$REPORT_RC" | tee -a "$EV/run-meta.txt"

echo "=== P4 project × test 矩阵判定（机器 JSON，expected run 不得 MISSING） ==="
python3 - "$EV" <<'PY' | tee "$EV/run-matrix.txt"
import json, sys, os
ev = sys.argv[1]
desktop = {"R9-01","R9-02","R9-03","R9-04","R9-05","R9-07"}
mobile = {"R9-06"}
tests = ["R9-01","R9-02","R9-03","R9-04","R9-05","R9-06","R9-07"]
report_path = os.path.join(ev, "playwright-report.json")
actual = {}
report_error = None
try:
    rep = json.load(open(report_path, encoding="utf-8"))
    def walk(suite, proj_hint):
        proj = suite.get("title") if suite.get("title") in ("uc-desktop", "uc-mobile") else proj_hint
        for spec in suite.get("specs", []) or []:
            title = spec.get("title", "") or ""
            key = next((c for c in tests if title.startswith(c)), None)
            if key is None:
                continue
            for t in spec.get("tests", []) or []:
                p = t.get("projectName") or proj
                st = t.get("status")
                if not st or st == "expected":
                    results = t.get("results") or [{}]
                    st = results[-1].get("status")
                if p:
                    actual[(p, key)] = st
        for child in suite.get("suites", []) or []:
            walk(child, proj)
    for s in rep.get("suites", []) or []:
        walk(s, None)
except Exception as e:
    report_error = repr(e)
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
        if got not in ("passed", "skipped", "failed"):
            ok = False
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
       "report_error": report_error,
       "tests": rows, "summary": summary}
json.dump(out, open(os.path.join(ev, "run-matrix.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(summary, indent=2, ensure_ascii=False))
sys.exit(1 if (mismatch or report_error or summary["actual_failed_or_missing"]) else 0)
PY
MIX=$?
echo "$MIX" > "$EV/run-matrix.rc"

echo "=== P5 DB cross-check of the job the UI really created ==="
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

echo "=== P6 retry evidence ==="
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

echo "=== P7 R9-07 证据与 self-check 归档（§21 证据目录） ==="
mkdir -p "$REMED/r9"
for f in playwright-report.json run-matrix.json; do
  [ -f "$EV/$f" ] && cp -f "$EV/$f" "$REMED/r9/$f"
done
[ -f "$EV/uc-desktop/r9-07-evidence.json" ] && cp -f "$EV/uc-desktop/r9-07-evidence.json" "$REMED/r9/r9-07-evidence.json"
[ -f "$FIX/r9-07-fixture-selfcheck.json" ] && cp -f "$FIX/r9-07-fixture-selfcheck.json" "$REMED/r9/r9-07-fixture-selfcheck.json"
cp -f "$EV/run-meta.txt" "$REMED/r9/run-meta.txt" 2>/dev/null || true
echo "archived_to=$REMED/r9" | tee -a "$EV/run-meta.txt"

echo "=== P8 最终 gate（fail-closed） ==="
TB=0
for f in "$EV/playwright.log" "$EV/p1b-fixture-selfcheck.txt" "$EV/p3-report-check.txt" "$EV/run-matrix.txt"; do
  [ -f "$f" ] || continue
  if grep -q "Traceback (most recent call last)" "$f"; then TB=1; echo "traceback_found_in=$f"; fi
done
GATE_FAILS=""
gate_chk(){ [ "$2" -ne 0 ] && GATE_FAILS="$GATE_FAILS $1(rc=$2)"; return 0; }
gate_chk fixtures "$FIXRC"
gate_chk fixture-selfcheck "$SELF_RC"
gate_chk playwright "$RC"
gate_chk report-check "$REPORT_RC"
gate_chk run-matrix "$MIX"
[ "$TB" -eq 0 ] || GATE_FAILS="$GATE_FAILS traceback-found"
{
  echo "fixtures_rc=$FIXRC"
  echo "fixture_selfcheck_rc=$SELF_RC"
  echo "playwright_rc=$RC"
  echo "report_check_rc=$REPORT_RC"
  echo "run_matrix_rc=$MIX"
  echo "traceback_found=$TB"
  echo "gate_failures=$(echo $GATE_FAILS | tr -s ' ' | sed 's/^ *//')"
} >> "$GATE"
cat "$GATE" | tee -a "$EV/run-meta.txt"
echo "EVIDENCE=$EV" | tee -a "$EV/run-meta.txt"
if [ -n "$GATE_FAILS" ]; then
  echo "R9_V4_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"
  exit 1
fi
echo "R9_V4_VERDICT=PASS playwright_rc=$RC report_check_rc=$REPORT_RC run_matrix_rc=$MIX fixtures_rc=$FIXRC" | tee -a "$EV/run-meta.txt"
exit 0
