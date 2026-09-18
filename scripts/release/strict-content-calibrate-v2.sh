#!/usr/bin/env bash
# strict-content-calibrate-v2.sh
# v1 defects corrected here (v1 preserved on disk):
#   * v1 began with 'rm -rf "$CAL"' on a FIXED path. v2 uses a unique UTC+nonce dir
#     and fails closed if it already exists. No default deletion of evidence.
#   * v1 C3 hardcoded parts[12]; v2 resolves the downloaded_rows index from the
#     columns descriptor, picks a PK whose PRE value is > 0, and asserts the
#     mutation really produced a decrease.
#   * v1 accepted ANY non-zero exit; v2 requires rc==1 (rule failure) and a specific
#     rule id + object in the JSON, so a python crash (rc=3) cannot count as a pass.
set -uo pipefail
cd /home/euripides/projects/blockchain-platform || exit 90
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
CHECK=scripts/release/strict-content-check-v2.py
NONCE=$(head -c 8 /dev/urandom | od -An -tx1 | tr -d ' \n')
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
CAL="$EVID/strict-calibration-v2-$STAMP-$NONCE"
if [ -e "$CAL" ]; then echo "FATAL: calibration dir already exists, refusing: $CAL"; exit 90; fi
mkdir -p "$CAL" || exit 90
echo "calibration_dir=$CAL (newly created; nothing removed)"
PASSED=0; TOTAL=0
REAL_PRE="$EVID/pre-content"; REAL_POST="$EVID/post-content"
COLS="$REAL_PRE/columns-PRE.json"

mk(){
  d="$1"; mkdir -p "$d"
  for t in download_jobs download_shards checkpoints outbox_events platform_jobs backup_jobs job_event_log; do
    cp "$REAL_POST/$t.tsv.gz" "$d/$t.tsv.gz"
  done
}
# assert_case <name> <expected_rc> <regex-that-must-match-a-failure>
assert_case(){
  name="$1"; want_rc="$2"; want_re="$3"
  TOTAL=$((TOTAL+1))
  out="$CAL/$name.json"
  python3 "$CHECK" "$CUR_PRE" "$CUR_POST" "$CUR_COLS" "$out" > "$CAL/$name.out" 2>&1
  rc=$?
  res=$(python3 - "$out" "$want_rc" "$want_re" <<'PY'
import json,sys
out,want_rc,want_re=sys.argv[1],int(sys.argv[2]),sys.argv[3]
try: d=json.load(open(out))
except Exception as e: print("NOJSON:%s"%e); raise SystemExit(0)
if d.get("crashed"): print("CRASHED"); raise SystemExit(0)
fs=d.get("failures",[])
hit=[f for f in fs if want_re=="" or want_re in f]
if want_rc==0:
    print("OK" if (d.get("ok") and not fs) else "NOTOK:failures=%d"%len(fs))
else:
    print("OK" if hit else "NORULE:failures=%d first=%s"%(len(fs), fs[0] if fs else "-"))
PY
)
  if [ "$rc" -eq "$want_rc" ] && [ "$res" = "OK" ]; then
    echo "CAL2 $name: PASS rc=$rc verdict=$res"; PASSED=$((PASSED+1))
  else
    echo "CAL2 $name: FAIL rc=$rc (wanted $want_rc) verdict=$res"
  fi
}

# C1 positive
CUR_PRE="$REAL_PRE"; CUR_POST="$REAL_POST"; CUR_COLS="$COLS"
assert_case C1_positive 0 ""

# C2 row loss
mk "$CAL/c2_post"; CUR_PRE="$REAL_PRE"; CUR_POST="$CAL/c2_post"; CUR_COLS="$COLS"
python3 - "$CAL/c2_post/download_shards.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=[l for l in f.read().split(b'\n') if l]
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data[:1000])+b'\n')
PY
assert_case C2_row_loss 1 "R1 row loss download_shards"

# C3 progress regression with a REAL resolved index and a PRE>0 value
mk "$CAL/c3_post"; CUR_PRE="$REAL_PRE"; CUR_POST="$CAL/c3_post"; CUR_COLS="$COLS"
R3=$(python3 - "$COLS" "$REAL_PRE/download_shards.tsv.gz" "$CAL/c3_post/download_shards.tsv.gz" <<'PY'
import gzip,sys,json
cols=json.load(open(sys.argv[1]))['download_shards']['columns'].split(',')
pk=json.load(open(sys.argv[1]))['download_shards']['pk']
ci=cols.index('downloaded_rows'); pi=cols.index(pk)
def rows(p):
    with gzip.open(p,'rb') as f: return [l.split(b'\t') for l in f.read().split(b'\n') if l]
pre=rows(sys.argv[2]); post=rows(sys.argv[3])
target=None
for r in pre:
    try:
        if int(r[ci])>0: target=r[pi]; break
    except Exception: pass
print("resolved index downloaded_rows=%d pk=%s"%(ci,pk.decode() if isinstance(pk,bytes) else pk), file=sys.stderr)
if target is None: print("NOVALUE"); raise SystemExit(0)
n=0
for r in post:
    if r[pi]==target:
        r[ci]=str(int(r[ci])-1).encode(); n+=1
with gzip.open(sys.argv[3],'wb') as f: f.write(b'\n'.join(b'\t'.join(r) for r in post)+b'\n')
print(target.decode())
PY
)
echo "  C3 target_pk=$R3"
assert_case C3_progress_regression 1 "R4 progress regressed download_shards"

# C4 corrupt row
mk "$CAL/c4_post"; CUR_POST="$CAL/c4_post"
python3 - "$CAL/c4_post/checkpoints.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=[l for l in f.read().split(b'\n') if l]
data.insert(5,b'only-one-field')
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data)+b'\n')
PY
assert_case C4_corrupt_row 1 "S2 POST checkpoints row"

# C5 duplicate pk (post)
mk "$CAL/c5_post"; CUR_POST="$CAL/c5_post"
python3 - "$CAL/c5_post/outbox_events.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=[l for l in f.read().split(b'\n') if l]
data.append(data[0])
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data)+b'\n')
PY
assert_case C5_duplicate_pk_post 1 "S3 POST outbox_events duplicate pk"

# C6 descriptor missing a required table
mk "$CAL/c6_post"; CUR_POST="$CAL/c6_post"; CUR_COLS="$CAL/c6_legend.json"
python3 - "$COLS" "$CUR_COLS" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); d.pop('checkpoints',None); json.dump(d,open(sys.argv[2],'w'))
PY
assert_case C6_missing_table 1 "S1 table set mismatch"

# C7 empty descriptor
CUR_COLS="$CAL/c7_empty.json"; printf '{}' > "$CUR_COLS"
assert_case C7_empty_descriptor 1 "S1 columns descriptor empty"

# C8 missing dump
mk "$CAL/c8_post"; CUR_POST="$CAL/c8_post"; CUR_COLS="$COLS"; rm -f "$CAL/c8_post/download_jobs.tsv.gz"
assert_case C8_missing_dump 1 "S4 missing dump for download_jobs"

# C9 duplicate pk on the PRE side
mk "$CAL/c9_pre"; CUR_PRE="$CAL/c9_pre"; CUR_POST="$REAL_POST"
python3 - "$CAL/c9_pre/outbox_events.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=[l for l in f.read().split(b'\n') if l]
data.append(data[0])
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data)+b'\n')
PY
assert_case C9_duplicate_pk_pre 1 "S3 PRE outbox_events duplicate pk"

echo "CALIBRATION_V2_SUMMARY passed=$PASSED total=$TOTAL dir=$CAL"
{
  echo "strict_content_calibration_v2_utc=$STAMP"
  echo "checker=$CHECK sha256=$(sha256sum $CHECK | cut -d' ' -f1)"
  echo "calibration_dir=$CAL"
  echo "passed=$PASSED total=$TOTAL"
} > "$CAL/CALIBRATION-V2-SUMMARY.txt"
if [ "$PASSED" -ne "$TOTAL" ]; then echo "CALIBRATION_V2 FAILED"; exit 1; fi
echo "CALIBRATION_V2 OK"
exit 0
