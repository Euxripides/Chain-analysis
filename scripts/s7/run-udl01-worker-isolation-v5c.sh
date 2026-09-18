#!/usr/bin/env bash
# run-udl01-worker-isolation-v5c.sh — U-DL-01：隔离解析 Worker / 有界超时 / 可取消 / 大文件 / 恶意 ZIP 真实验收轮。
#
# 判据与 R10 v7 同构（根已认可的 fail-closed 纪律）：
#   PASS 只在 playwright rc=0 ∧ report-check rc=0 ∧ crosscheck rc=0 ∧ summary-render rc=0
#        ∧ bundle 绑定冻结稳定 ∧ 无 Traceback 时打印；每个 post-processing 命令的 rc 都进 gate.txt。
# 本轮**不部署**：部署已在 udl01-deploy-user-console.sh（PASS=16/FAIL=1，失败项是脚本自身的 chunk 发现缺陷）
# 完成，本脚本只做"服务端实际服务的字节 == 磁盘 == 预期"的绑定校验。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/udl01-worker-isolation-v5c-$TS}"
XC="${XC:-$R/scripts/s7/udl01-crosscheck-v5b.py}"
D="$R/frontend/apps/user-console"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee -a "$EV/runner.console.log") 2>&1
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "crosscheck_sha256=$(sha256sum "$XC" | cut -d' ' -f1)" | tee -a "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"
echo "started_utc=$TS" | tee -a "$EV/run-meta.txt"
GATE="$EV/gate.txt"
gate_note(){ printf '%s\n' "$1" >> "$GATE"; }

api_pid(){ pgrep -f "$R/backend/bin/api\$" | head -1; }
worker_pid(){ pgrep -f "$R/backend/bin/worker\$" | head -1; }

echo "=== P0a 冻结基线（api/worker 身份，运行前后必须一致） ==="
API_PID_B="$(api_pid)"; WORKER_PID_B="$(worker_pid)"
API_SHA_B="$(sha256sum "/proc/$API_PID_B/exe" | cut -d' ' -f1)"
WORKER_SHA_B="$(sha256sum "/proc/$WORKER_PID_B/exe" | cut -d' ' -f1)"
echo "api_pid=$API_PID_B sha=$API_SHA_B" | tee -a "$EV/run-meta.txt"
echo "worker_pid=$WORKER_PID_B sha=$WORKER_SHA_B" | tee -a "$EV/run-meta.txt"
{ echo "api_sha_before=$API_SHA_B"; echo "worker_sha_before=$WORKER_SHA_B"; } >> "$GATE"

echo "=== P0b0 配置自检（v5b 轮真实缺陷：config 的 testMatch 静默没改到，Playwright 跑了旧 spec） ==="
CONF="$R/frontend/playwright.udl01v5c.config.ts"
grep -q "udl01-worker-isolation-v5b" "$CONF" || { echo "CONFIG_TESTMATCH_MISMATCH"; exit 4; }
[ -f "$R/frontend/tests/udl01-worker-isolation-v5b.spec.ts" ] || { echo "SPEC_V5B_MISSING"; exit 4; }
echo "config_testmatch_ok=1" | tee -a "$EV/run-meta.txt"

echo "=== P0b readiness + 登录夹具 ==="
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
echo "ready_attempts=$i body=$B" | tee -a "$EV/run-meta.txt"
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW; do
  eval "val=\$$v"; [ -n "${val:-}" ] || { echo "PREFLIGHT_MISSING $v"; exit 3; }
done

echo "=== P0c bundle 绑定（服务端实际服务的字节，本轮不部署） ==="
DISK_INDEX_SHA="$(sha256sum "$D/dist/index.html" | cut -d' ' -f1)"
SERVED_INDEX_SHA="$(curl -s -k https://127.0.0.1:8448/app-v2/ | sha256sum | cut -d' ' -f1)"
PAGE_CHUNK="$(ls "$D/dist/assets" | grep -E '^DownloadNewPage-.*\.js$' | head -1)"
WORKER_CHUNK="$(ls "$D/dist/assets" | grep -E '^import-parse\.worker-.*\.js$' | head -1)"
grep -q "$WORKER_CHUNK" "$D/dist/assets/$PAGE_CHUNK" && REFS=true || REFS=false
W_HTTP="$(curl -s -k -o "$EV/served-worker-chunk.js" -w '%{http_code}' "https://127.0.0.1:8448/app-v2/assets/$WORKER_CHUNK")"
W_DISK_SHA="$(sha256sum "$D/dist/assets/$WORKER_CHUNK" | cut -d' ' -f1)"
W_SERVED_SHA="$(sha256sum "$EV/served-worker-chunk.js" | cut -d' ' -f1)"
CSP="$(curl -s -k -D - -o /dev/null https://127.0.0.1:8448/app-v2/ | grep -ci 'content-security-policy')"
python3 - "$EV/bundle-binding.json" "$SERVED_INDEX_SHA" "$DISK_INDEX_SHA" "$PAGE_CHUNK" "$WORKER_CHUNK" "$W_HTTP" "$W_DISK_SHA" "$W_SERVED_SHA" "$REFS" "$CSP" <<'PY'
import json, sys
(out, served, disk, page, worker, http, dsha, ssha, refs, csp) = sys.argv[1:11]
json.dump({"served_index_sha": served, "disk_index_sha": disk, "page_chunk": page, "worker_chunk": worker,
           "worker_chunk_http": http, "worker_chunk_disk_sha": dsha, "worker_chunk_served_sha": ssha,
           "page_chunk_references_worker": refs == "true", "csp_headers": int(csp)},
          open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
cat "$EV/bundle-binding.json" | tee -a "$EV/run-meta.txt"

echo "=== P1 真实夹具（v5b：200k 行 .xls + 60k 行 .xlsx + 6 类恶意 ZIP + 正常对照） ==="
mkdir -p "$EV/fixtures"
node "$R/frontend/scripts/r9-make-fixtures-v5c.mjs" "$EV/fixtures" 200000 60000 > "$EV/p1-fixtures.log" 2>&1
FIXRC=$?
echo "fixtures_rc=$FIXRC" | tee -a "$EV/run-meta.txt"
gate_note "fixtures_rc=$FIXRC"
tail -3 "$EV/p1-fixtures.log"
ls -l "$EV/fixtures" | awk '{print $5, $9}' | grep udl01 || true

echo "=== P2 真实浏览器（隔离解析 / 超时 / 取消 / 恶意 ZIP / 回归） ==="
cd "$R/frontend" || exit 90
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export UDL01_OUT="$EV" UDL01_EMAIL="$S7JS_INVESTIGATOR_EMAIL" UDL01_PASSWORD="$S7JS_INVESTIGATOR_PW" UDL01_FIXTURES="$EV/fixtures"
node node_modules/@playwright/test/cli.js test --config playwright.udl01v5c.config.ts > "$EV/playwright.console.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 30 "$EV/playwright.console.log"

echo "=== P3 冻结复核 + report 校验 ==="
API_PID_A="$(api_pid)"; WORKER_PID_A="$(worker_pid)"
API_SHA_A="$(sha256sum "/proc/$API_PID_A/exe" 2>/dev/null | cut -d' ' -f1)"
WORKER_SHA_A="$(sha256sum "/proc/$WORKER_PID_A/exe" 2>/dev/null | cut -d' ' -f1)"
FREEZE_STABLE=0
[ "$API_SHA_A" = "$API_SHA_B" ] && [ "$WORKER_SHA_A" = "$WORKER_SHA_B" ] && [ "$API_PID_A" = "$API_PID_B" ] && FREEZE_STABLE=1
{ echo "api_sha_after=$API_SHA_A"; echo "worker_sha_after=$WORKER_SHA_A"; echo "freeze_stable=$FREEZE_STABLE"; } >> "$GATE"
REPORT_RC=0
if [ ! -s "$EV/playwright-report.json" ]; then echo REPORT_MISSING; REPORT_RC=1; else
python3 - "$EV/playwright-report.json" > "$EV/p3-report-check.txt" 2>&1 <<'PY'
import json, sys
rep = json.load(open(sys.argv[1], encoding="utf-8"))
s = rep.get("stats") or {}
print("suites=%d expected=%s unexpected=%s skipped=%s flaky=%s" % (len(rep.get("suites") or []), s.get("expected"), s.get("unexpected"), s.get("skipped"), s.get("flaky")))
sys.exit(0 if (rep.get("suites") and s.get("expected") == 5 and s.get("unexpected") == 0 and s.get("skipped") == 0) else 1)
PY
REPORT_RC=$?; fi
cat "$EV/p3-report-check.txt"
gate_note "report_check_rc=$REPORT_RC"

echo "=== P4 机器核验 ==="
python3 "$XC" "$EV" > "$EV/udl01-summary.json" 2> "$EV/udl01-crosscheck.err"
XRC=$?
gate_note "crosscheck_rc=$XRC"
echo "crosscheck_rc=$XRC" | tee -a "$EV/run-meta.txt"

echo "=== P5 summary 渲染（rc 进 gate） ==="
python3 - "$EV/udl01-summary.json" > "$EV/p5-summary-render.out" 2>&1 <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
print("verdict=%s checks_total=%s checks_failed=%s" % (d.get("verdict"), d.get("checks_total"), d.get("checks_failed")))
for row in (d.get("failed") or [])[:40]:
    print("FAILED: %s -> %s" % (row.get("check"), json.dumps(row.get("detail"), ensure_ascii=False)[:240]))
for row in (d.get("matrix") or []):
    o = row.get("observed") or {}
    print("matrix %s: status=%s results=%s dur=%s ev_bytes=%s ok=%s" % (row.get("test"), o.get("status"), o.get("result_statuses"), o.get("result_durations"), row.get("evidence_bytes"), row.get("ok")))
PY
SUMMARY_RC=$?
cat "$EV/p5-summary-render.out"
gate_note "summary_render_rc=$SUMMARY_RC"

echo "=== P6 最终 gate（fail-closed） ==="
sleep 1
TB=0
for f in "$EV/runner.console.log" "$EV/playwright.console.log" "$EV/udl01-crosscheck.err" "$EV/p3-report-check.txt" "$EV/p5-summary-render.out" "$EV/p1-fixtures.log"; do
  [ -f "$f" ] || continue
  if grep -q "Traceback (most recent call last)" "$f"; then TB=1; echo "traceback_found_in=$f"; fi
done
gate_note "traceback_found=$TB"
GATE_FAILS=""
gate_chk(){ [ "$2" -ne 0 ] && GATE_FAILS="$GATE_FAILS $1(rc=$2)"; return 0; }
gate_chk playwright "$RC"
gate_chk report-check "$REPORT_RC"
gate_chk crosscheck "$XRC"
gate_chk summary-render "$SUMMARY_RC"
gate_chk fixtures "$FIXRC"
[ "$FREEZE_STABLE" -eq 1 ] || GATE_FAILS="$GATE_FAILS freeze-unstable"
[ "$TB" -eq 0 ] || GATE_FAILS="$GATE_FAILS traceback-found"
echo "gate_failures=$(echo $GATE_FAILS | tr -s ' ' | sed 's/^ *//')" >> "$GATE"
cat "$GATE" | tee -a "$EV/run-meta.txt"
if [ -n "$GATE_FAILS" ]; then echo "UDL01_V5C_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"; exit 1; fi
echo "UDL01_V5C_VERDICT=PASS playwright_rc=$RC report_rc=$REPORT_RC crosscheck_rc=$XRC summary_render_rc=$SUMMARY_RC fixtures_rc=$FIXRC freeze_stable=$FREEZE_STABLE traceback=$TB" | tee -a "$EV/run-meta.txt"
exit 0
