#!/usr/bin/env bash
# run-download-flowline.sh — 新建数据下载 Flowline 专项验收（真实浏览器 + 真实服务端 + 真实登录）。
#
# 覆盖《新建数据下载 Flowline 界面重构实施规范》§25.2 的 DL-FLOW-001 … DL-FLOW-010，
# 并产出机器可读的 run-matrix.json（每个 project×用例的预期与实际）。
#
# 与 run-r9-download-import-v4.sh 的差别：**不在命令行传 --reporter**，
# 让 playwright.dlflow.config.ts 里的 json outputFile 生效（否则 JSON 会打到 stdout，
# 报告文件永远不生成 —— r9 runner 的 P3 矩阵就是因此长期读不到报告）。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/download-flowline/run-$TS}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"

echo "=== P0 preflight（缺项即失败，不生成绿色假象） ==="
if [ ! -f /home/euripides/.s7jobscope-creds.env ]; then echo "PREFLIGHT_MISSING creds file"; exit 3; fi
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
if [ -z "${S7JS_INVESTIGATOR_EMAIL:-}" ] || [ -z "${S7JS_INVESTIGATOR_PW:-}" ]; then
  echo "PREFLIGHT_FAILED - refusing to run (no green from missing inputs)" | tee -a "$EV/run-meta.txt"
  exit 3
fi
CHAIN="${DLF_CHAIN:-bsc}"
ADDRESS="${DLF_ADDRESS:-0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649}"
echo "preflight=OK chain=$CHAIN address=$ADDRESS" | tee -a "$EV/run-meta.txt"

echo "=== P1 真实浏览器运行（uc-desktop + uc-mobile） ==="
cd "$R/frontend" || exit 90
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export DLF_OUT="$EV" DLF_EMAIL="$S7JS_INVESTIGATOR_EMAIL" DLF_PASSWORD="$S7JS_INVESTIGATOR_PW"
export DLF_ADDRESS="$ADDRESS" DLF_CHAIN="$CHAIN"
# 不在命令行传 --reporter：CLI 会覆盖 config，json 报告写不进文件，矩阵永远是 MISSING。
node node_modules/@playwright/test/cli.js test tests/download-flowline-redesign.spec.ts \
  --config playwright.dlflow.config.ts --project=uc-desktop --project=uc-mobile \
  > "$EV/playwright.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 40 "$EV/playwright.log"

echo "=== P2 project × 用例 矩阵判定（机器 JSON） ==="
python3 - "$EV" <<'PY' | tee "$EV/run-matrix.txt"
import json, sys, os
ev = sys.argv[1]
tests = ["DL-FLOW-001","DL-FLOW-002","DL-FLOW-003","DL-FLOW-004","DL-FLOW-005",
         "DL-FLOW-006","DL-FLOW-007","DL-FLOW-008","DL-FLOW-009","DL-FLOW-010"]
actual = {}
report = os.path.join(ev, "playwright-report.json")
rows, mismatch = [], []
try:
    rep = json.load(open(report))
    def walk(suite, hint):
        proj = suite.get("title") if suite.get("title") in ("uc-desktop", "uc-mobile") else hint
        for spec in suite.get("specs", []) or []:
            title = spec.get("title", "") or ""
            key = next((c for c in tests if c in title), None)
            if key is None:
                continue
            for t in spec.get("tests", []) or []:
                p = t.get("projectName") or proj
                st = t.get("status")
                if not st:
                    results = t.get("results") or [{}]
                    st = results[-1].get("status")
                # Playwright JSON 报告里的 status 是"相对预期"的结果：
                # expected = 通过 / unexpected = 失败 / skipped = 跳过 / flaky = 重试后通过。
                st = {"expected": "passed", "unexpected": "failed"}.get(st, st)
                if p:
                    actual[(p, key)] = st
        for child in suite.get("suites", []) or []:
            walk(child, proj)
    for s in rep.get("suites", []) or []:
        walk(s, None)
except Exception as e:
    print("REPORT_UNREADABLE", e)
for proj in ("uc-desktop", "uc-mobile"):
    for key in tests:
        got = actual.get((proj, key), "MISSING")
        ok = got == "passed"
        rows.append({"project": proj, "test": key, "actual": got, "ok": ok})
        if not ok:
            mismatch.append({"project": proj, "test": key, "actual": got})
summary = {
    "expected_run": len(rows),
    "actual_passed": sum(1 for r in rows if r["actual"] == "passed"),
    "actual_failed_or_missing": sum(1 for r in rows if r["actual"] != "passed"),
    "mismatches": mismatch,
}
json.dump({"tests": rows, "summary": summary}, open(os.path.join(ev, "run-matrix.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(summary, indent=2, ensure_ascii=False))
sys.exit(1 if mismatch else 0)
PY
MIX=$?
echo "$MIX" > "$EV/run-matrix.rc"
echo "EVIDENCE=$EV" | tee -a "$EV/run-meta.txt"
[ "$RC" -ne 0 ] && exit "$RC"
[ "$MIX" -ne 0 ] && exit 4
exit 0
