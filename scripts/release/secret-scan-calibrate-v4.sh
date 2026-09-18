#!/usr/bin/env bash
# secret-scan-calibrate-v4.sh — secret-scan-v3.sh 的正负向校准（wrapper 自行记账，不靠 glob 子 JSON 猜例数）。
#
# 保留的历史失败（不热改、不删除）：
#   calibrate v1（tmp 输出保留）：set -u 展开未导出的 OUT_JSON → unbound variable，N2 前中断。
#   calibrate v2（tmp 输出保留）：普通赋值未 export → 子扫描器不写 JSON；且 case_count=0 仍报 calibration_failed=false。
#   calibrate v3（tmp 输出保留）：把"每个 case 都必须有子 JSON"当断言，而 early-exit 的负向 case
#       （必需键缺失时 die 2）**按设计不会写 JSON**，导致真实正确的负向行为被误判失败；且只看
#       子 JSON case_count，未对每个固定 case 做独立记账。
#
# v4 断言模型：
#   * 6 个固定 case，每个**恰好记账一次**（缺一即 FAIL）；
#   * wrapper 自己记录 case/expected_rc/actual_rc/logpath/assertion，不看子 JSON 猜例数；
#   * early-exit（rc=2）是**正确**的负向证据，只要求 stderr 日志非空并含可判定文本；
#   * 新增 C5（短 required 值必须仍被扫描）与 C6（DSN 百分号编码口令必须按解码后真实字节扫描）。
# 约束：全部 dummy 值随机/非生产；不改生产二进制、不重启任何服务、不删除原有文件。
set -uo pipefail

SCAN="${SCAN:-$(dirname "$(readlink -f "$0")")/secret-scan-v3.sh}"
OUTDIR="${OUTDIR:-/tmp/secret-scan-calibration-v4-$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$OUTDIR/cases"; chmod 700 "$OUTDIR" "$OUTDIR/cases"
umask 077

[ -x "$SCAN" ] || { echo "scanner not executable: $SCAN" >&2; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "openssl required" >&2; exit 2; }

D="$OUTDIR/work"; mkdir -p "$D"; chmod 700 "$D"
ENV_OK="$D/env-ok.env"; ENV_BAD="$D/env-missing-key.env"
ENV_SHORT="$D/env-short-required.env"; ENV_DSNENC="$D/env-dsn-urlencoded.env"
CLEAN="$D/artifact-clean.bin"; DIRTY="$D/artifact-dirty.bin"
SHORT_ART="$D/artifact-short-embedded.bin"; DSN_ART="$D/artifact-dsn-decoded.bin"

P1="$(openssl rand -hex 24)"; C1="$(openssl rand -hex 24)"; M1="$(openssl rand -hex 24)"
SHORT="$(openssl rand -hex 2 | cut -c1-5)"          # 5 字符，低于 policy min=8
DSNPW='p@ss:w/rd#1'                                   # 含 DSN 需 URL 编码的字符
DSNENC="$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=""))' "$DSNPW")"

printf 'POSTGRES_PASSWORD=%s\nCLICKHOUSE_PASSWORD=%s\nMINIO_ROOT_PASSWORD=%s\n' "$P1" "$C1" "$M1" > "$ENV_OK"; chmod 600 "$ENV_OK"
printf 'POSTGRES_PASSWORD=%s\nCLICKHOUSE_PASSWORD=%s\n' "$P1" "$C1" > "$ENV_BAD"; chmod 600 "$ENV_BAD"
printf 'POSTGRES_PASSWORD=%s\nCLICKHOUSE_PASSWORD=%s\nMINIO_ROOT_PASSWORD=%s\n' "$SHORT" "$C1" "$M1" > "$ENV_SHORT"; chmod 600 "$ENV_SHORT"
printf 'POSTGRES_PASSWORD=%s\nCLICKHOUSE_PASSWORD=%s\nMINIO_ROOT_PASSWORD=%s\nDATABASE_URL=postgres://blockchain_api:%s@127.0.0.1:5433/db?sslmode=disable\n' \
  "$P1" "$C1" "$M1" "$DSNENC" > "$ENV_DSNENC"; chmod 600 "$ENV_DSNENC"

head -c 120000 /dev/urandom > "$CLEAN"
{ cat "$CLEAN"; printf '%s' "$M1"; } > "$DIRTY"
{ head -c 60000 /dev/urandom; printf '%s' "$SHORT"; head -c 60000 /dev/urandom; } > "$SHORT_ART"
{ head -c 60000 /dev/urandom; printf '%s' "$DSNPW"; head -c 60000 /dev/urandom; } > "$DSN_ART"

FAIL=0; ACCOUNTED=0
E_CLEAN=0; E_MISS=2; E_HIT=1

run_case() { # run_case <case> <expect_rc> <expect_kind> <stderr_must_contain> <envfile> <artifact...>
  local case="$1" exp="$2" kind="$3" needle="$4" envf="$5"; shift 5
  local log="$OUTDIR/cases/$case.log" json="$OUTDIR/cases/$case.json"
  OUT_JSON="$json"; MIN_PATTERNS=3; export OUT_JSON MIN_PATTERNS
  "$SCAN" "$envf" "$@" > "$log" 2>&1
  local rc=$?
  local assert=OK reason=""
  [ "$rc" = "$exp" ] || { assert=FAIL; reason="rc mismatch expected=$exp"; }
  [ -s "$log" ] || { assert=FAIL; reason="$reason empty-log"; }
  if [ -n "$needle" ] && ! grep -q -- "$needle" "$log"; then assert=FAIL; reason="$reason missing-stderr-needle('$needle')"; fi
  case "$kind" in
    json)  [ -s "$json" ] || { assert=FAIL; reason="$reason missing-json"; } ;;
    early) [ -s "$json" ] && { assert=FAIL; reason="$reason unexpected-json-on-early-exit"; } ;;
  esac
  {
    echo "case=$case"
    echo "expected_rc=$exp"
    echo "actual_rc=$rc"
    echo "kind=$kind"
    echo "logpath=$log"
    echo "jsonpath=$json"
    echo "assertion=$assert"
    echo "reason=$reason"
  } > "$OUTDIR/cases/$case.record.txt"
  ACCOUNTED=$((ACCOUNTED+1))
  [ "$assert" = "OK" ] || FAIL=1
  echo "CASE $case actual_rc=$rc expected_rc=$exp assertion=$assert $reason"
}

run_case C1_clean_complete_env          $E_CLEAN json  ""                    "$ENV_OK"     "$CLEAN"
run_case C2_dummy_secret_embedded       $E_HIT   json  ""                    "$ENV_OK"     "$DIRTY"
run_case C3_env_missing_required_key    $E_MISS  early "required credential keys missing" "$ENV_BAD" "$CLEAN"
run_case C4_artifact_unreadable         $E_HIT   json  ""                    "$ENV_OK"     "$D/nope.bin"
run_case C5_short_required_still_scanned $E_HIT  json  ""                    "$ENV_SHORT"  "$SHORT_ART"
run_case C6_dsn_urlencoded_decoded      $E_HIT   json  ""                    "$ENV_DSNENC" "$DSN_ART"

EXPECTED_CASES=6
if [ "$ACCOUNTED" -ne "$EXPECTED_CASES" ]; then
  echo "case accounting $ACCOUNTED != $EXPECTED_CASES"; FAIL=1
fi
# C5 必须真的命中，且 JSON 里出现 policy_warnings（短 required 不被静默过滤）
if [ -s "$OUTDIR/cases/C5_short_required_still_scanned.json" ]; then
  pw="$(python3 -c "import json;d=json.load(open('$OUTDIR/cases/C5_short_required_still_scanned.json'));print(len(d.get('policy_warnings',[])))" 2>/dev/null || echo 0)"
  [ "$pw" -ge 1 ] || { echo "C5 policy_warnings empty → short required may have been silently filtered"; FAIL=1; }
fi
if [ -s "$OUTDIR/cases/C6_dsn_urlencoded_decoded.json" ]; then
  forms="$(python3 -c "import json;d=json.load(open('$OUTDIR/cases/C6_dsn_urlencoded_decoded.json'));print(sum(1 for f in d.get('pattern_fingerprints',[]) if f.get('form')=='dsn-urldecoded'))" 2>/dev/null || echo 0)"
  [ "$forms" -ge 1 ] || { echo "C6 no dsn-urldecoded pattern recorded"; FAIL=1; }
fi

SCAN_SHA="$(sha256sum "$SCAN" | cut -d' ' -f1)"
echo "scanner_sha256=$SCAN_SHA"
echo "calibration_out=$OUTDIR"
OUTDIR="$OUTDIR" SCAN_SHA="$SCAN_SHA" FAIL="$FAIL" ACCOUNTED="$ACCOUNTED" EXPECTED_CASES="$EXPECTED_CASES" \
python3 - <<'PY' > "$OUTDIR/RESULT.json"
import json,os,glob
outdir=os.environ["OUTDIR"]
cases=[]
for f in sorted(glob.glob(os.path.join(outdir,"cases","*.record.txt"))):
    d={}
    for line in open(f):
        if "=" in line:
            k,v=line.rstrip("\n").split("=",1); d[k]=v
    cases.append(d)
doc={"scanner_sha256":os.environ["SCAN_SHA"],
     "expected_cases":int(os.environ["EXPECTED_CASES"]),
     "accounted_cases":int(os.environ["ACCOUNTED"]),
     "cases":cases,
     "calibration_failed":bool(int(os.environ["FAIL"])),
     "checked_utc":__import__("datetime").datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ")}
print(json.dumps(doc,indent=2,ensure_ascii=False))
PY
cat "$OUTDIR/RESULT.json"
exit "$FAIL"
