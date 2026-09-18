set -uo pipefail
cd /home/euripides/projects/blockchain-platform
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
echo "=== FACTS: did strict-calibration pre-exist? ==="
echo "-- current EVID top-level entries with birth/change times --"
stat -c '%w  %n' "$EVID" 2>/dev/null
for d in "$EVID"/*/; do stat -c 'birth=%w mtime=%y  %n' "$d"; done
echo "-- strict-calibration contents (fixture dirs, no rm performed by this inspection) --"
ls -la "$EVID/strict-calibration" | head -20
echo
echo "=== C1 positive failures (first 25) ==="
python3 -c "
import json
d=json.load(open('$EVID/strict-calibration/C1_positive.json'))
print('ok=',d['ok'],'failures=',len(d['failures']))
for f in d['failures'][:25]: print(' -',f)
print('--- tables ---')
for k,v in d['tables'].items(): print(k,v)
"
echo
echo "=== raw C1 stdout tail ==="
tail -5 "$EVID/strict-calibration/C1_positive.out"
