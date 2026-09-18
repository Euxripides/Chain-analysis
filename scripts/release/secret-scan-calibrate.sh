#!/usr/bin/env bash
# secret-scan-calibrate.sh — 对 secret-scan.sh 的**正负向校准**。
#
# 目的：证明扫描器具备 fail-closed 能力，而不是"恰好这次输出 0 就叫干净"。
#   N1 干净产物 + 完整 env            → 期望 rc=0（CLEAN）
#   N2 干净产物 + **故意嵌入 dummy secret** → 期望 rc=1（HIT，必须非 0）
#   N3 env 缺必需键                   → 期望 rc=2（读取/解析不完整时拒绝给 0）
#   N4 产物不可读                     → 期望 rc=1
# 约束：dummy 值一律**随机生成**，绝不使用任何生产/真实凭据；不修改生产二进制或服务；不删除任何原有文件。
set -uo pipefail

SCAN="${SCAN:-$(dirname "$(readlink -f "$0")")/secret-scan.sh}"
OUTDIR="${OUTDIR:-/tmp/secret-scan-calibration-$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$OUTDIR"; chmod 700 "$OUTDIR"
umask 077

[ -x "$SCAN" ] || { echo "scanner not executable: $SCAN" >&2; exit 2; }

D="$OUTDIR/work"; mkdir -p "$D"; chmod 700 "$D"
ENV_OK="$D/env-ok.env"; ENV_BAD="$D/env-missing-key.env"
CLEAN="$D/artifact-clean.bin"; DIRTY="$D/artifact-dirty.bin"

# 随机 dummy 凭据（仅本次校准使用，不打印）
P1="$(openssl rand -hex 24)"; C1="$(openssl rand -hex 24)"; M1="$(openssl rand -hex 24)"
{
  echo "POSTGRES_PASSWORD=$P1"
  echo "CLICKHOUSE_PASSWORD=$C1"
  echo "MINIO_ROOT_PASSWORD=$M1"
} > "$ENV_OK"
chmod 600 "$ENV_OK"
{ echo "POSTGRES_PASSWORD=$P1"; echo "CLICKHOUSE_PASSWORD=$C1"; } > "$ENV_BAD"   # 缺 MINIO_ROOT_PASSWORD
chmod 600 "$ENV_BAD"

head -c 200000 /dev/urandom > "$CLEAN"
{ cat "$CLEAN"; printf '%s' "$M1"; } > "$DIRTY"     # 嵌入 1 个 dummy 值

FAIL=0
run_case() { # run_case <name> <expect_rc> <expect_json_verdict> <envfile> <artifact...>
  local name="$1" exp="$2" expv="$3" envf="$4"; shift 4
  OUT_JSON="$OUTDIR/$name.json" MIN_PATTERNS=3 "$SCAN" "$envf" "$@" > "$OUTDIR/$name.out" 2>&1
  local rc=$?
  local v="$(python3 -c "import json,sys;print(json.load(open('$OUT_JSON')).get('verdict','-'))" 2>/dev/null || echo no-json)"
  if [ "$rc" = "$exp" ]; then echo "CASE $name rc=$rc (expected $exp) OK verdict=$v"
  else echo "CASE $name rc=$rc (expected $exp) **CALIBRATION FAIL** verdict=$v"; FAIL=1; fi
  echo "  out=$OUTDIR/$name.out json=$OUT_JSON"
}

run_case N1_clean_complete_env   0 CLEAN "$ENV_OK" "$CLEAN"
run_case N2_dummy_secret_embedded 1 LEAK_OR_ERROR "$ENV_OK" "$DIRTY"
run_case N3_env_missing_required_key 2 "" "$ENV_BAD" "$CLEAN"
run_case N4_artifact_unreadable  1 LEAK_OR_ERROR "$ENV_OK" "$D/does-not-exist.bin"

echo "scanner_sha256=$(sha256sum "$SCAN" | cut -d' ' -f1)"
echo "calibration_out=$OUTDIR"
{
  printf '{\n  "scanner_sha256": "%s",\n  "cases": [\n' "$(sha256sum "$SCAN" | cut -d' ' -f1)"
  n=0
  for f in "$OUTDIR"/N*.json; do
    [ -f "$f" ] || continue
    [ "$n" -gt 0 ] && printf ',\n'
    printf '    %s' "$(python3 -c "import json;d=json.load(open('$f'));print(json.dumps({'case':'$(basename $f .json)','rc':d.get('rc'),'verdict':d.get('verdict'),'hits':d.get('hits'),'patterns':d.get('patterns_checked')}))" 2>/dev/null || echo '{}')"
    n=$((n+1))
  done
  printf '\n  ],\n  "calibration_failed": %s,\n  "checked_utc": "%s"\n}\n' "$FAIL" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "$OUTDIR/RESULT.json"
cat "$OUTDIR/RESULT.json"
exit "$FAIL"
