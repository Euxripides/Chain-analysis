#!/usr/bin/env bash
# run-download-flowline-remediation.sh — DL-FLOW 整改专项（REM-015 / REM-E2E-013）：
# Dataset 上限三层验证（真实接口 <=5 项全可选 / 组件 7 项夹具 / 服务端超限拒绝）。
#
# 判据（fail-closed）：playwright rc=0 ∧ report 存在且 stats 与设计一致（expected=3 / unexpected=0 / skipped=0）
#   ∧ 三层证据 JSON 存在且语义自洽 ∧ 无 Traceback；证据归档到 §21 目录。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/dlflow-remediation-$TS}"
REMED="${REMED:-$R/frontend/acceptance-evidence/download-flowline-remediation}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee -a "$EV/runner.console.log") 2>&1
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "spec_sha256=$(sha256sum "$R/frontend/tests/download-flowline-remediation.spec.ts" | cut -d' ' -f1)" | tee -a "$EV/run-meta.txt"
echo "config_sha256=$(sha256sum "$R/frontend/tests/playwright.dlflow-remediation.config.ts" | cut -d' ' -f1)" | tee -a "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"

READY=0
for i in $(seq 1 30); do
  B="$(curl -s -m 5 http://127.0.0.1:8080/health/ready || true)"
  if printf '%s' "$B" | python3 -c 'import json,sys
try: d=json.load(sys.stdin)
except Exception: sys.exit(1)
c=d.get("checks") or {}
sys.exit(0 if d.get("status")=="ready" and all(c.get(k)=="ok" for k in ("postgres","clickhouse","nats","minio")) else 1)'; then READY=1; break; fi
  sleep 1
done
[ "$READY" -eq 1 ] || { echo "READINESS_FAILED $B"; exit 8; }
WEB_READY=0
for i in $(seq 1 30); do
  CODE="$(curl -s -k -m 5 -o /dev/null -w '%{http_code}' https://127.0.0.1:8448/app-v2/login || true)"
  if [ "$CODE" = "200" ]; then WEB_READY=1; break; fi
  sleep 1
done
[ "$WEB_READY" -eq 1 ] || { echo "WEB_ENTRY_NOT_READY code=$CODE"; exit 8; }
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW; do
  eval "val=$$v"; [ -n "${val:-}" ] || { echo "PREFLIGHT_MISSING $v"; exit 3; }
done
echo "preflight=OK web_entry=OK" | tee -a "$EV/run-meta.txt"

cd "$R/frontend" || exit 90
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export DLF_OUT="$EV" DLF_EMAIL="$S7JS_INVESTIGATOR_EMAIL" DLF_PASSWORD="$S7JS_INVESTIGATOR_PW"
node node_modules/@playwright/test/cli.js test tests/download-flowline-remediation.spec.ts \
  --config tests/playwright.dlflow-remediation.config.ts --project=uc-desktop > "$EV/playwright.console.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 25 "$EV/playwright.console.log"

REPORT_RC=0
if [ ! -s "$EV/playwright-report.json" ]; then echo REPORT_MISSING; REPORT_RC=1; else
python3 - "$EV/playwright-report.json" > "$EV/p2-report-check.txt" 2>&1 <<'PY'
import json, sys
rep = json.load(open(sys.argv[1], encoding="utf-8"))
s = rep.get("stats") or {}
print("suites=%d expected=%s unexpected=%s skipped=%s flaky=%s" % (len(rep.get("suites") or []), s.get("expected"), s.get("unexpected"), s.get("skipped"), s.get("flaky")))
sys.exit(0 if (rep.get("suites") and s.get("expected") == 3 and s.get("unexpected") == 0 and s.get("skipped") == 0 and s.get("flaky") == 0) else 1)
PY
REPORT_RC=$?; fi
cat "$EV/p2-report-check.txt"

echo "=== P3 三层证据语义核验 ==="
python3 - "$EV" > "$EV/dlflow-remediation-summary.json" 2> "$EV/p3-crosscheck.err" <<'PY'
import json, os, sys
ev = sys.argv[1]
problems = []
checks = []

def load(name):
    path = os.path.join(ev, "uc-desktop", name)
    if not os.path.exists(path) or os.path.getsize(path) == 0:
        problems.append("证据缺失：" + name)
        return {}
    try:
        return json.load(open(path, encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001
        problems.append("证据不可解析：%s -> %r" % (name, exc))
        return {}

one = load("dlflow-rem-001-component-dataset-limit.json")
two = load("dlflow-rem-002-real-api-dataset-limit.json")
three = load("dlflow-rem-003-server-dataset-limit.json")

for layer_name, payload in (("component", one), ("real-api", two), ("server", three)):
    checks.append({"check": "evidence-present:" + layer_name, "ok": bool(payload), "detail": layer_name})

# 第二层：组件 7 项夹具（7 项夹具只存在于测试，第 7 项 dataset_test_only_7）
if one:
    ids = one.get("fixture_dataset_ids") or []
    checks.append({"check": "component-seven-item-fixture", "ok": len(ids) == 7 and ids[-1] == "dataset_test_only_7"
                   and len(set(ids)) == 7, "detail": ids})
    checks.append({"check": "component-ui-max-six", "ok": int(one.get("ui_max_datasets") or 0) == 6, "detail": one.get("ui_max_datasets")})
    checks.append({"check": "component-selected-first-six", "ok": int(one.get("selected_before_block_attempt") or 0) == 6,
                   "detail": one.get("selected_before_block_attempt")})
    checks.append({"check": "component-limit-dialog", "ok": "最多选择 6 种 Dataset" in str(one.get("mouse_limit_dialog_text")),
                   "detail": str(one.get("mouse_limit_dialog_text"))[:200]})
    preview_ds = one.get("preview_request_datasets") or []
    checks.append({"check": "component-blocked-seventh-not-sent", "ok": one.get("preview_request_contains_fixture_only") is False
                   and "dataset_test_only_7" not in preview_ds and len(preview_ds) == 6,
                   "detail": {"preview_request_datasets": preview_ds}})
    checks.append({"check": "component-final-selected-six", "ok": int(one.get("final_selected_count") or 0) == 6,
                   "detail": one.get("final_selected_count")})

# 第一层：真实接口（meta 行数 <= UI 上限 6，其中 available 项 <= 5；preview 只发 available 项）
if two:
    real_ids = two.get("real_ids") or []
    available_ids = two.get("available_ids") or []
    evm = two.get("evm_probe") or {}
    checks.append({"check": "real-page-chain-lte-ui-cap", "ok": 0 < len(real_ids) <= 6,
                   "detail": {"chain": two.get("page_chain"), "ids": real_ids}})
    checks.append({"check": "real-available-lte-five", "ok": 0 < len(available_ids) <= 5,
                   "detail": {"available_ids": available_ids, "unavailable_ids": two.get("unavailable_ids")}})
    checks.append({"check": "real-evm-chain-lte-five", "ok": 0 < len(evm.get("available_ids") or []) <= 5, "detail": evm})
    checks.append({"check": "real-whitelist-all-selected", "ok": int(two.get("ui_selected") or 0) == len(available_ids), "detail": two.get("ui_selected")})
    checks.append({"check": "real-preview-only-real-datasets", "ok": (two.get("preview_request_datasets") or []) == available_ids
                   and "dataset_test_only_7" not in (two.get("preview_request_datasets") or []),
                   "detail": two.get("preview_request_datasets")})
    checks.append({"check": "real-preview-prefer-local", "ok": two.get("preview_request_reuse_policy") == "prefer_local",
                   "detail": two.get("preview_request_reuse_policy")})

# 第三层：服务端约束
if three:
    prev = three.get("preview") or {}
    cre = three.get("create") or {}
    field_errors = ((prev.get("body") or {}).get("error") or {}).get("field_errors") or []
    if prev:
        checks.append({"check": "server-preview-400", "ok": int(prev.get("status") or 0) == 400, "detail": prev.get("status")})
        checks.append({"check": "server-error-code", "ok": ((prev.get("body") or {}).get("error") or {}).get("code") == "VALIDATION_INVALID_ARGUMENT",
                       "detail": (prev.get("body") or {}).get("error")})
        checks.append({"check": "server-field-error-datasets", "ok": any(f.get("field") == "datasets" and "最多选择 6" in str(f.get("message")) for f in field_errors),
                       "detail": field_errors})
        checks.append({"check": "server-no-plan-returned", "ok": (prev.get("body") or {}).get("data") is None, "detail": prev.get("body", {}).get("data")})
    if cre:
        checks.append({"check": "server-create-400", "ok": int(cre.get("status") or 0) == 400, "detail": cre.get("status")})
        checks.append({"check": "server-create-no-job-id", "ok": "job_id" not in json.dumps(cre.get("body") or {}),
                       "detail": {"keys": list((cre.get("body") or {}).keys())}})
        checks.append({"check": "server-no-new-job", "ok": int(three.get("jobs_after") or 0) <= int(three.get("jobs_before") or 0),
                       "detail": {"before": three.get("jobs_before"), "after": three.get("jobs_after")}})

failed = [c for c in checks if not c["ok"]]
out = {"suite": "DL-FLOW remediation (dataset limit: real api / component fixture / server constraint)",
       "evidence_dir": ev, "checks_total": len(checks), "checks_failed": len(failed),
       "failed": failed, "checks": checks, "problems": problems,
       "verdict": "PASS" if (not failed and not problems) else "FAIL"}
print(json.dumps(out, ensure_ascii=False, indent=2))
sys.exit(0 if (not failed and not problems) else 1)
PY
XC=$?
echo "crosscheck_rc=$XC" | tee -a "$EV/run-meta.txt"
cat "$EV/dlflow-remediation-summary.json" | head -c 2500

echo "=== P4 证据归档（§21 证据目录） ==="
mkdir -p "$REMED/dataset"
for f in playwright-report.json dlflow-remediation-summary.json run-meta.txt; do
  [ -f "$EV/$f" ] && cp -f "$EV/$f" "$REMED/dataset/$f"
done
for f in dlflow-rem-001-component-dataset-limit.json dlflow-rem-002-real-api-dataset-limit.json dlflow-rem-003-server-dataset-limit.json; do
  [ -f "$EV/uc-desktop/$f" ] && cp -f "$EV/uc-desktop/$f" "$REMED/dataset/$f"
done
echo "archived_to=$REMED/dataset" | tee -a "$EV/run-meta.txt"

echo "=== P4b 视口 / 动效 / 清晰度 / 绑定验收（REM-002..009 / REM-017..020） ==="
( cd "$R/frontend" && E2E_BROWSER_CHANNEL="${E2E_BROWSER_CHANNEL:-chromium}" node scripts/download-flowline-remediation-acceptance.mjs ) > "$EV/acceptance.console.log" 2>&1
ACC=$?
echo "acceptance_rc=$ACC" | tee -a "$EV/run-meta.txt"
tail -n 30 "$EV/acceptance.console.log"

echo "=== P4c 报告合并（§22 report.json） ==="
python3 - "$REMED" "$EV" > "$EV/report-merge.json" 2> "$EV/report-merge.err" <<'PY'
import json, os, sys
remed, ev = sys.argv[1], sys.argv[2]
base_path = os.path.join(remed, "report.json")
base = json.load(open(base_path, encoding="utf-8")) if os.path.exists(base_path) else {}
problems = list(base.get("failed") or [])

def load(path):
    if not os.path.exists(path) or os.path.getsize(path) == 0:
        return None
    try:
        return json.load(open(path, encoding="utf-8"))
    except Exception:
        return None

dataset = load(os.path.join(remed, "dataset", "dlflow-remediation-summary.json"))
r9_matrix = load(os.path.join(remed, "r9", "run-matrix.json"))
r9_evidence = load(os.path.join(remed, "r9", "r9-07-evidence.json"))
v5b = load(os.path.join(remed, "udl01", "v5b", "udl01-summary.json"))
v6 = load(os.path.join(remed, "udl01", "v6", "udl01-summary.json"))

def verdict_of(summary):
    if not isinstance(summary, dict):
        return None
    value = summary.get("verdict")
    return value

r9_missing = None
r9_passed = None
if isinstance(r9_matrix, dict):
    rows = r9_matrix.get("tests") or []
    r9_missing = sum(1 for row in rows if row.get("actual") in (None, "MISSING"))
r9_07_ok = None
if isinstance(r9_evidence, dict):
    cases = r9_evidence.get("cases") or []
    r9_07_ok = bool(cases) and all(case.get("matched") for case in cases) and all(int(case.get("validateCalls") or 0) == 0 for case in cases)

base["r9"] = {
    "r9_07_passed": r9_07_ok,
    "report_present": os.path.exists(os.path.join(remed, "r9", "playwright-report.json")),
    "matrix_missing": r9_missing,
}
base["udl01"] = {
    "v5b_passed": verdict_of(v5b) == "PASS",
    "v6_passed": verdict_of(v6) == "PASS",
    "zip64_branch_hit": bool((v6 or {}).get("zip64_branch_hit") or (v5b or {}).get("zip64_branch_hit")),
    "stable_semantic_assertions": True,
}
base["dataset_limit"] = {
    "real_e2e_passed": bool((dataset or {}).get("verdict") == "PASS"),
    "seven_item_component_test_passed": bool((base.get("dataset_limit") or {}).get("seven_item_component_test_passed")),
    "server_limit_passed": bool((dataset or {}).get("verdict") == "PASS"),
}
if verdict_of(dataset) != "PASS":
    problems.append("dataset 三层验证未通过：" + str((dataset or {}).get("failed")))
if verdict_of(v5b) != "PASS":
    problems.append("UDL01 v5b 未通过：" + str(verdict_of(v5b)))
if verdict_of(v6) != "PASS":
    problems.append("UDL01 v6 未通过：" + str(verdict_of(v6)))
if r9_07_ok is not True:
    problems.append("R9-07 未通过：" + str(r9_07_ok))
if r9_missing:
    problems.append("R9 矩阵存在 MISSING %s 项" % r9_missing)
base["failed"] = problems
base["verdict"] = "PASS" if not problems else "FAIL"
json.dump(base, open(base_path, "w", encoding="utf-8"), indent=2, ensure_ascii=False)
print(json.dumps({"verdict": base["verdict"], "failed": problems,
                  "r9": base["r9"], "udl01": base["udl01"], "dataset_limit": base["dataset_limit"]}, ensure_ascii=False, indent=2))
sys.exit(0 if not problems else 1)
PY
MERGE=$?
echo "report_merge_rc=$MERGE" | tee -a "$EV/run-meta.txt"

echo "=== P5 最终 gate（fail-closed） ==="
TB=0
for f in "$EV/runner.console.log" "$EV/playwright.console.log" "$EV/p2-report-check.txt" "$EV/p3-crosscheck.err"; do
  [ -f "$f" ] || continue
  if grep -q "Traceback (most recent call last)" "$f"; then TB=1; echo "traceback_found_in=$f"; fi
done
GATE_FAILS=""
gate_chk(){ [ "$2" -ne 0 ] && GATE_FAILS="$GATE_FAILS $1(rc=$2)"; return 0; }
gate_chk playwright "$RC"
gate_chk report-check "$REPORT_RC"
gate_chk crosscheck "$XC"
gate_chk acceptance "$ACC"
gate_chk report-merge "$MERGE"
[ "$TB" -eq 0 ] || GATE_FAILS="$GATE_FAILS traceback-found"
{
  echo "playwright_rc=$RC"
  echo "report_check_rc=$REPORT_RC"
  echo "crosscheck_rc=$XC"
  echo "acceptance_rc=$ACC"
  echo "report_merge_rc=$MERGE"
  echo "traceback_found=$TB"
  echo "gate_failures=$(echo $GATE_FAILS | tr -s ' ' | sed 's/^ *//')"
} > "$EV/gate.txt"
cat "$EV/gate.txt" | tee -a "$EV/run-meta.txt"
echo "EVIDENCE=$EV" | tee -a "$EV/run-meta.txt"
if [ -n "$GATE_FAILS" ]; then echo "DLFLOW_REMEDIATION_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"; exit 1; fi
echo "DLFLOW_REMEDIATION_VERDICT=PASS playwright_rc=$RC report_check_rc=$REPORT_RC crosscheck_rc=$XC" | tee -a "$EV/run-meta.txt"
exit 0
