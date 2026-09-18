#!/usr/bin/env bash
# udl01-v6-crosscheck-negative-calibration.sh — 对 U-DL-01 v6 的**新增 U5 判据**做负向校准。
#
# 目的：证明 U5 门禁不是"只改注释"——把权威轮的证据复制一份并逐项篡改，crosscheck 必须非 0，
# 且失败项必须精确落在对应的 U5 判据上；未篡改的正向对照必须仍然 0/50。
# 原轮证据一字不改（只读复制）。
set -uo pipefail
R="${R:-/home/euripides/projects/blockchain-platform}"
SRC="${SRC:-$(ls -1dt "$R"/acceptance-evidence/dual-system-v1/sprint7/udl01-worker-isolation-v6-* | head -1)}"
XC="$R/scripts/s7/udl01-crosscheck-v6.py"
U5G="$R/scripts/s7/udl01-u5-gate-v6.py"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/udl01-v6-crosscheck-negcal-$TS}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
LOG="$EV/negcal.log"
echo "evidence=$EV src=$SRC" | tee "$LOG"
echo "src_summary_sha=$(sha256sum "$SRC/udl01-summary.json" | cut -d" " -f1)" | tee -a "$LOG"
echo "crosscheck_sha=$(sha256sum "$XC" | cut -d" " -f1)" | tee -a "$LOG"
FAILS=0
bad(){ FAILS=$((FAILS+1)); echo "NEGCAL_FAIL $1 :: $2" | tee -a "$LOG"; }
good(){ echo "NEGCAL_PASS $1 :: $2" | tee -a "$LOG"; }

prep(){
  local name="$1"
  cp -a "$SRC" "$EV/$name"
  echo "$SRC/udl01-summary.json" > "$EV/$name.src-summary-path"
}
run_xc(){
  local name="$1"
  python3 "$XC" "$EV/$name" > "$EV/$name.xc-summary.json" 2> "$EV/$name.xc.err"
  XC_RC=$?
}
failed_has(){ python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(any(r.get('check')==sys.argv[2] for r in (d.get('failed') or [])))" "$1" "$2"; }

echo "=== C0 正向对照（未篡改副本必须 50/0） ==="
prep C0-untouched
run_xc C0-untouched
if [ "$XC_RC" != 0 ]; then bad C0 "未篡改副本 crosscheck rc=$XC_RC != 0"; fi
python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print('total=%s failed=%s' % (d.get('checks_total'), d.get('checks_failed')))" "$EV/C0-untouched.xc-summary.json" | tee -a "$LOG"
python3 -c "import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if (d.get('checks_total')==50 and d.get('checks_failed')==0) else 1)" "$EV/C0-untouched.xc-summary.json" && good C0 "未篡改副本 50/0" || bad C0 "未篡改副本不是 50/0"

echo "=== C1 篡改 bundle 绑定：page_chunk_budget_ms_hits=0 ==="
prep C1-budget-hits-zero
python3 - "$EV/C1-budget-hits-zero/bundle-binding.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p, encoding="utf-8"))
d["page_chunk_budget_ms_hits"] = 0
json.dump(d, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
python3 - "$EV/C1-budget-hits-zero/uc-desktop/udl01-b-evidence.json" <<'PY'
import json, sys
p = sys.argv[1]
json.dump(json.load(open(p, encoding="utf-8")), open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
run_xc C1-budget-hits-zero
if [ "$XC_RC" = 0 ]; then bad C1 "篡改 page_chunk_budget_ms_hits 后 crosscheck 仍为 0"; fi
[ "$(failed_has "$EV/C1-budget-hits-zero.xc-summary.json" "bundle-page-chunk-has-budget-unit")" = "True" ] && good C1 "失败项命中 bundle-page-chunk-has-budget-unit" || bad C1 "失败项未命中 bundle-page-chunk-has-budget-unit"

echo "=== C2 篡改超时文案为 0 秒 ==="
prep C2-zero-second-text
python3 - "$EV/C2-zero-second-text/uc-desktop/udl01-b-evidence.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p, encoding="utf-8"))
d["noticeText"] = "udl01-big.xlsx 在 0 秒内没有完成，解析 Worker 已被终止（未导入任何地址）。"
json.dump(d, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
run_xc C2-zero-second-text
if [ "$XC_RC" = 0 ]; then bad C2 "篡改 0 秒文案后 crosscheck 仍为 0"; fi
[ "$(failed_has "$EV/C2-zero-second-text.xc-summary.json" "b-budget-no-zero-seconds")" = "True" ] && good C2 "失败项命中 b-budget-no-zero-seconds" || bad C2 "失败项未命中 b-budget-no-zero-seconds"

echo "=== C3 篡改进度弹窗预算缺失 ==="
prep C3-progress-empty
python3 - "$EV/C3-progress-empty/uc-desktop/udl01-c-evidence.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p, encoding="utf-8"))
d["progressText"] = ""
json.dump(d, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
run_xc C3-progress-empty
if [ "$XC_RC" = 0 ]; then bad C3 "progressText 置空后 crosscheck 仍为 0"; fi
[ "$(failed_has "$EV/C3-progress-empty.xc-summary.json" "c-progress-budget-no-zero-seconds")" = "True" ] && good C3 "失败项命中 c-progress-budget-no-zero-seconds" || bad C3 "失败项未命中 c-progress-budget-no-zero-seconds"

echo "=== C4 u5-gate 对篡改轮必须非 0 ==="
python3 "$U5G" "$EV/C3-progress-empty.xc-summary.json" > "$EV/C4-u5gate-tampered.txt" 2>&1
U5RC=$?
if [ "$U5RC" = 0 ]; then bad C4 "u5-gate 对篡改轮仍为 0"; else good C4 "u5-gate 对篡改轮非 0（rc=$U5RC）"; fi
python3 "$U5G" "$EV/C0-untouched.xc-summary.json" > "$EV/C4-u5gate-clean.txt" 2>&1
U5RC2=$?
if [ "$U5RC2" != 0 ]; then bad C4 "u5-gate 对正向对照非 0"; else good C4 "u5-gate 对正向对照为 0"; fi

echo "src_summary_sha_after=$(sha256sum "$SRC/udl01-summary.json" | cut -d" " -f1)" | tee -a "$LOG"
echo "fails=$FAILS" | tee -a "$LOG"
if [ "$FAILS" != 0 ]; then echo "UDL01_V6_CROSSCHECK_NEGCAL_VERDICT=FAIL fails=$FAILS"; exit 1; fi
echo "UDL01_V6_CROSSCHECK_NEGCAL_VERDICT=PASS cases=4 fails=0"
exit 0
