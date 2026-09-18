#!/usr/bin/env bash
# secret-scan-calibrate-v3.sh — secret-scan.sh 的正负向校准（修正 v1/v2 缺陷，并自证 case 覆盖）。
#
# 保留的失败事实（不热改、不删）：
#   v1 secret-scan-calibrate.sh：run_case 用 "OUT_JSON=... cmd" 前缀赋值后又用 set -u 展开 $OUT_JSON
#      → "unbound variable"，校准在 N2 前中断。（输出保留于 /tmp/secret-scan-calibration-*）
#   v2 secret-scan-calibrate-v2.sh：改为普通赋值但**未 export**，
#      scanner 子进程看不到 OUT_JSON → 不写每例 JSON；且 RESULT.json 在 case_count=0 时仍报
#      calibration_failed=false（**覆盖度自证缺口**）。（输出保留于 /tmp/secret-scan-calibration-v2-*）
# v3 修正：显式 export；并**强制要求 case_count == 期望例数**，否则校准自身判失败。
#
# 目的：证明扫描器 fail-closed，而不是"恰好这次输出 0 就叫干净"。
#   N1 干净产物 + 完整 env                 → rc=0（CLEAN）
#   N2 产物中故意嵌入随机 dummy secret      → rc=1（必须非 0）
#   N3 env 缺必需键                        → rc=2（解析不完整时拒绝给 0）
#   N4 产物不可读                          → rc=1
# 约束：dummy 值随机生成，绝不使用任何生产/真实凭据；不改生产二进制/服务；不删除任何原有文件。
set -uo pipefail

SCAN="${SCAN:-$(dirname "$(readlink -f "$0")")/secret-scan.sh}"
OUTDIR="${OUTDIR:-/tmp/secret-scan-calibration-v3-$(date -u +%Y%m%dT%H%M%SZ)}"
EXPECTED_CASES=4
mkdir -p "$OUTDIR"; chmod 700 "$OUTDIR"
umask 077

[ -x "$SCAN" ] || { echo "scanner not executable: $SCAN" >&2; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "openssl required" >&2; exit 2; }

D="$OUTDIR/work"; mkdir -p "$D"; chmod 700 "$D"
ENV_OK="$D/env-ok.env"; ENV_BAD="$D/env-missing-key.env"
CLEAN="$D/artifact-clean.bin"; DIRTY="$D/artifact-dirty.bin"

P1="$(openssl rand -hex 24)"; C1="$(openssl rand -hex 24)"; M1="$(openssl rand -hex 24)"
printf 'POSTGRES_PASSWORD=%s\nCLICKHOUSE_PASSWORD=%s\nMINIO_ROOT_PASSWORD=%s\n' "$P1" "$C1" "$M1" > "$ENV_OK"
chmod 600 "$ENV_OK"
printf 'POSTGRES_PASSWORD=%s\nCLICKHOUSE_PASSWORD=%s\n' "$P1" "$C1" > "$ENV_BAD"
chmod 600 "$ENV_BAD"
head -c 200000 /dev/urandom > "$CLEAN"
{ cat "$CLEAN"; printf '%s' "$M1"; } > "$DIRTY"

FAIL=0
run_case() {
  local name="$1" exp="$2" envf="$3"; shift 3
  OUT_JSON="$OUTDIR/$name.json"
  MIN_PATTERNS=3
  export OUT_JSON MIN_PATTERNS          # ← v2 的缺陷修复点
  "$SCAN" "$envf" "$@" > "$OUTDIR/$name.out" 2>&1
  local rc=$?
  if [ "$rc" = "$exp" ]; then echo "CASE $name rc=$rc (expected $exp) OK"
  else echo "CASE $name rc=$rc (expected $exp) **CALIBRATION FAIL**"; FAIL=1; fi
  if [ ! -s "$OUT_JSON" ]; then echo "CASE $name **MISSING JSON** $OUT_JSON"; FAIL=1; fi
}

run_case N1_clean_complete_env        0 "$ENV_OK"  "$CLEAN"
run_case N2_dummy_secret_embedded     1 "$ENV_OK"  "$DIRTY"
run_case N3_env_missing_required_key  2 "$ENV_BAD" "$CLEAN"
run_case N4_artifact_unreadable       1 "$ENV_OK"  "$D/does-not-exist.bin"

CASES="$(ls -1 "$OUTDIR"/N*.json 2>/dev/null | wc -l)"
[ "$CASES" -eq "$EXPECTED_CASES" ] || { echo "case coverage $CASES != $EXPECTED_CASES"; FAIL=1; }

SCAN_SHA="$(sha256sum "$SCAN" | cut -d' ' -f1)"
echo "scanner_sha256=$SCAN_SHA"
echo "calibration_out=$OUTDIR"
export OUTDIR SCAN_SHA
python3 - <<'PY' > "$OUTDIR/RESULT.json"
import json,os,glob
outdir=os.environ["OUTDIR"]; scan_sha=os.environ["SCAN_SHA"]
cases=[]
for f in sorted(glob.glob(os.path.join(outdir,"N*.json"))):
    try:
        d=json.load(open(f)); d["case"]=os.path.basename(f)[:-5]; cases.append(d)
    except Exception as e: cases.append({"file":os.path.basename(f),"parse_error":str(e)})
print(json.dumps({"scanner_sha256":scan_sha,"case_count":len(cases),"cases":cases}, indent=2))
PY
CASES_JSON="$(python3 -c "import json;print(json.load(open('$OUTDIR/RESULT.json'))['case_count'])" 2>/dev/null || echo 0)"
[ "$CASES_JSON" -eq "$EXPECTED_CASES" ] || { echo "RESULT.json case_count $CASES_JSON != $EXPECTED_CASES"; FAIL=1; }
python3 - "$OUTDIR/RESULT.json" "$FAIL" <<'PY'
import json,sys
p,failed=sys.argv[1],int(sys.argv[2])
d=json.load(open(p)); d["calibration_failed"]=bool(failed)
json.dump(d,open(p,"w"),indent=2)
PY
cat "$OUTDIR/RESULT.json"
exit "$FAIL"
