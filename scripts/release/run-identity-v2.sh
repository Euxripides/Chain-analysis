#!/usr/bin/env bash
set -uo pipefail
cd /home/euripides/projects/blockchain-platform || exit 90
E=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
python3 scripts/release/strict-identity-proof-v2.py "$E/pre-content" "$E/post-content" "$E/pre-content/columns-PRE.json" "$E/strict-identity-v2.json" > "$E/strict-identity-v2.out" 2>&1
echo "identity_v2_rc=$?"
python3 - "$E/strict-identity-v2.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
print("ok=",d["ok"],"failures=",len(d["failures"]))
for k,v in d["tables"].items():
    print(" %-16s raw_identical=%s pre=%s post=%s preserved=%s/%s appended=%s utf8=%s/%s empty_proven=%s" % (
        k, v.get("raw_bytes_identical"), v.get("pre_rows"), v.get("post_rows"),
        v.get("pre_rows_byte_preserved"), v.get("pre_pk_count"), v.get("appended_pk_count"),
        v.get("pre_utf8_valid"), v.get("post_utf8_valid"), v.get("pre_empty_dump_proven_valid")))
for f in d["failures"][:10]:
    print(" FAIL", f)
PY
