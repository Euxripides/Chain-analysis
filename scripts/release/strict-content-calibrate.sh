#!/usr/bin/env bash
# strict-content-calibrate.sh
# Calibrates strict-content-check.py: positive case must pass (rc=0) and every
# negative fixture must FAIL with a real non-zero exit code.
set -uo pipefail
cd /home/euripides/projects/blockchain-platform || exit 90
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
CAL="$EVID/strict-calibration"
CHECK=scripts/release/strict-content-check.py
rm -rf "$CAL" 2>/dev/null
mkdir -p "$CAL"
PASSED=0; TOTAL=0
run_case(){
  name="$1"; expect="$2"; pre="$3"; post="$4"; cols="$5"
  TOTAL=$((TOTAL+1))
  out="$CAL/$name.json"
  python3 "$CHECK" "$pre" "$post" "$cols" "$out" > "$CAL/$name.out" 2>&1
  rc=$?
  nfail=$(python3 -c "
import json,sys
try:
    d=json.load(open('$out')); print(len(d.get('failures',[])))
except Exception: print('NA')")
  if [ "$expect" = "zero" ]; then
    if [ "$rc" -eq 0 ]; then echo "CAL $name: OK (rc=0 as expected) failures=$nfail"; PASSED=$((PASSED+1));
    else echo "CAL $name: BAD expected rc=0 got rc=$rc"; fi
  else
    if [ "$rc" -ne 0 ]; then echo "CAL $name: OK (rc=$rc non-zero as expected) failures=$nfail"; PASSED=$((PASSED+1));
    else echo "CAL $name: BAD expected non-zero got rc=0"; fi
  fi
}
mk(){ mkdir -p "$1"; cp "$2"/columns-PRE.json "$1"/ 2>/dev/null; for t in download_jobs download_shards checkpoints outbox_events platform_jobs backup_jobs job_event_log; do cp "$2/$t.tsv.gz" "$1/$t.tsv.gz"; done; }

REAL_PRE="$EVID/pre-content"; REAL_POST="$EVID/post-content"
# C1 positive
run_case C1_positive zero "$REAL_PRE" "$REAL_POST" "$REAL_PRE/columns-PRE.json"

# C2 row loss
F="$CAL/c2_post"; mk "$F" "$REAL_POST"
python3 - "$F/download_shards.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=f.read().split(b'\n')
data=[l for l in data if l!='']
data=data[:1000]
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data)+b'\n')
PY
run_case C2_row_loss nonzero "$REAL_PRE" "$F" "$REAL_PRE/columns-PRE.json"

# C3 progress regression (rewrite downloaded_rows to 0 on the first shard row)
F="$CAL/c3_post"; mk "$F" "$REAL_POST"
python3 - "$F/download_shards.tsv.gz" "$REAL_POST/download_shards.tsv.gz" <<'PY'
import gzip,sys
src,dst=sys.argv[2],sys.argv[1]
with gzip.open(src,'rb') as f: raw=f.read()
lines=raw.split(b'\n')
out=[]
done=False
for l in lines:
    if l==b'': continue
    parts=l.split(b'\t')
    if not done and len(parts)>12:
        parts[12]=b'0'   # downloaded_rows
        done=True
    out.append(b'\t'.join(parts))
with gzip.open(dst,'wb') as f: f.write(b'\n'.join(out)+b'\n')
PY
run_case C3_progress_regression nonzero "$REAL_PRE" "$F" "$REAL_PRE/columns-PRE.json"

# C4 corrupt row (wrong field count)
F="$CAL/c4_post"; mk "$F" "$REAL_POST"
python3 - "$F/checkpoints.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=f.read().split(b'\n')
data=[l for l in data if l!='']
data.insert(5, b'only-one-field')
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data)+b'\n')
PY
run_case C4_corrupt_row nonzero "$REAL_PRE" "$F" "$REAL_PRE/columns-PRE.json"

# C5 duplicate primary key
F="$CAL/c5_post"; mk "$F" "$REAL_POST"
python3 - "$F/outbox_events.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=[l for l in f.read().split(b'\n') if l!=b'']
data.append(data[0])
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data)+b'\n')
PY
run_case C5_duplicate_pk nonzero "$REAL_PRE" "$F" "$REAL_PRE/columns-PRE.json"

# C6 descriptor missing a required table
F="$CAL/c6_cols"; mk "$F" "$REAL_POST"
python3 - "$REAL_PRE/columns-PRE.json" "$F/legend.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); d.pop('checkpoints',None)
json.dump(d,open(sys.argv[2],'w'))
PY
run_case C6_missing_table nonzero "$REAL_PRE" "$F" "$F/legend.json"

# C7 empty descriptor
F="$CAL/c7_cols"; mk "$F" "$REAL_POST"
printf '{}' > "$F/empty.json"
run_case C7_empty_descriptor nonzero "$REAL_PRE" "$F" "$F/empty.json"

# C8 missing dump
F="$CAL/c8_post"; mk "$F" "$REAL_POST"
rm -f "$F/download_jobs.tsv.gz"
run_case C8_missing_dump nonzero "$REAL_PRE" "$F" "$REAL_PRE/columns-PRE.json"

# C9 duplicate PK inside the PRE side (must also fail)
F="$CAL/c9_pre"; mk "$F" "$REAL_PRE"
python3 - "$F/outbox_events.tsv.gz" <<'PY'
import gzip,sys
p=sys.argv[1]
with gzip.open(p,'rb') as f: data=[l for l in f.read().split(b'\n') if l!=b'']
data.append(data[0])
with gzip.open(p,'wb') as f: f.write(b'\n'.join(data)+b'\n')
PY
run_case C9_duplicate_pk_pre nonzero "$F" "$REAL_POST" "$REAL_PRE/columns-PRE.json"

echo "CALIBRATION_SUMMARY passed=$PASSED total=$TOTAL"
{
  echo "strict_content_calibration_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "checker=scripts/release/strict-content-check.py sha256=$(sha256sum $CHECK | cut -d' ' -f1)"
  echo "passed=$PASSED total=$TOTAL"
} > "$CAL/CALIBRATION-SUMMARY.txt"
if [ "$PASSED" -ne "$TOTAL" ]; then echo "CALIBRATION FAILED"; exit 1; fi
echo "CALIBRATION OK"
exit 0
