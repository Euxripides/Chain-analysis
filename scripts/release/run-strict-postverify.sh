#!/usr/bin/env bash
set -uo pipefail
cd /home/euripides/projects/blockchain-platform || exit 90
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
echo "=== strict content check v2 on REAL pre/post ==="
python3 scripts/release/strict-content-check-v2.py "$EVID/pre-content" "$EVID/post-content" "$EVID/pre-content/columns-PRE.json" "$EVID/strict-content-final.json" > "$EVID/strict-content-final.out" 2>&1
echo "strict_content_rc=$?"
python3 -c "
import json;d=json.load(open('$EVID/strict-content-final.json'))
print('ok=',d['ok'],'failures=',len(d['failures']))
for k,v in d['tables'].items(): print(' ',k,v)
"
echo "=== strict identity proof ==="
python3 scripts/release/strict-identity-proof.py "$EVID/pre-content" "$EVID/post-content" "$EVID/pre-content/columns-PRE.json" "$EVID/strict-identity-final.json" > "$EVID/strict-identity-final.out" 2>&1
echo "identity_rc=$?"
python3 -c "
import json;d=json.load(open('$EVID/strict-identity-final.json'))
print('ok=',d['ok'],'failures=',len(d['failures']))
for k,v in d['tables'].items():
    print(' ',k,'raw_identical=',v.get('raw_identical'),'pre_rows=',v.get('pre_rows'),'post_rows=',v.get('post_rows'),
          'preserved=',v.get('pre_rows_byte_preserved'),'/',v.get('pre_pk_count'),'appended=',v.get('appended_pk_count'),
          'utf8=',v.get('pre_utf8_valid'),v.get('post_utf8_valid'))
for f in d['failures'][:10]: print('  FAIL',f)
"
