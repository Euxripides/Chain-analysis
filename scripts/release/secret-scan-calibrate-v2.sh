#!/usr/bin/env bash
# secret-scan-calibrate-v2.sh — secret-scan.sh 的正负向校准（修正 v1 的 set -u 展开缺陷）。
#
# v1（secret-scan-calibrate.sh）真实缺陷：run_case 用 "OUT_JSON=... cmd" 前缀赋值，
# 该变量不会留在 shell 中；随后 set -u 下在 python3 调用里展开 $OUT_JSON → "unbound variable"，
# 校准在 N2 之前中断。v1 的运行输出保留在 /tmp/secret-scan-calibration-*（未删除）。
#
# 目的：证明扫描器 fail-closed，而不是"恰好这次输出 0 就叫干净"。
#   N1 干净产物 + 完整 env                 → 期望 rc=0（CLEAN）
#   N2 产物中**故意嵌入随机 dummy secret**  → 期望 rc=1（必须非 0）
#   N3 env 缺必需键                        → 期望 rc=2（解析不完整时拒绝给 0）
#   N4 产物不可读                          → 期望 rc=1
# 约束：dummy 值随机生成，绝不使用任何生产/真实凭据；不改生产二进制/服务；不删除任何原有文件。
set -uo pipefail

SCAN="${SCAN:-$(dirname "$(readlink -f "$0")")/secret-scan.sh}"
OUTDIR="${OUTDIR:-/tmp/secret-scan-calibration-v2-$(date -u +%Y%m%dT%H%M%SZ)}"
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
run_case() { # run_case <name> <expect_rc> <envfile> <artifact...>
  local name="$1" exp="$2" envf="$3"; shift 3
  OUT_JSON="$OUTDIR/$name.json"
  MIN_PATTERNS=3
  "$SCAN" "$envf" "$@" > "$OUTDIR/$name.out" 2>&1
  local rc=$?
  if [ "$rc" = "$exp" ]; then echo "CASE $name rc=$rc (expected $exp) OK"
  else echo "CASE $name rc=$rc (expected $exp) **CALIBRATION FAIL**"; FAIL=1; fi
  echo "  out=$OUTDIR/$name.out json=$OUTDIR/$name.json"
}

run_case N1_clean_complete_env        0 "$ENV_OK"  "$CLEAN"
run_case N2_dummy_secret_embedded     1 "$ENV_OK"  "$DIRTY"
run_case N3_env_missing_required_key  2 "$ENV_BAD" "$CLEAN"
run_case N4_artifact_unreadable       1 "$ENV_OK"  "$D/does-not-exist.bin"

SCAN_SHA="$(sha256sum "$SCAN" | cut -d' ' -f1)"
echo "scanner_sha256=$SCAN_SHA"
echo "calibration_out=$OUTDIR"
python3 - "$OUTDIR" "$SCAN_SHA" "$FAIL" <<'PY' > "$OUTDIR/RESULT.json"
import json,sys,os,glob
outdir, scan_sha, failed = sys.argv[1], sys.argv[2], int(sys.argv[3])
cases=[]
for f in sorted(glob.glob(os.path.join(outdir,"N*.json"))):
    try: cases.append(json.load(open(f)))
    except Exception as e: cases.append({"file":os.path.basename(f),"parse_error":str(e)})
print(json.dumps({"scanner_sha256":scan_sha,"cases":cases,"calibration_failed":bool(failed),
                  "case_count":len(cases)}, indent=2))
PY
cat "$OUTDIR/RESULT.json"
exit "$FAIL"
