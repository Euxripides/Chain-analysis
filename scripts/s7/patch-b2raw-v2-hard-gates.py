#!/usr/bin/env python3
"""Exactly-once hard-gate corrections for the B2 raw-only UI runner (2026-09-13 review).

Root cause being fixed: the 135033Z round accepted a create evidence file whose export id was empty
(spec read body.id instead of body.data.id) and then kept issuing PG/HTTP work with an empty id,
producing "empty == empty" PASS lines. The runner must now stop at the first unusable artefact.
"""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()

check_old = 'check(){ if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi; }'
check_new = '''check(){
  # An empty value can never satisfy an expectation: "empty == empty" is not evidence, it is a
  # missing artefact. Callers that legitimately expect emptiness compare against a sentinel word.
  if [ -z "$3" ] && [ -z "$4" ]; then
    echo "  FAIL $1 :: $2 :: BOTH SIDES EMPTY (an empty-vs-empty comparison is never evidence)"
    FAIL=$((FAIL+1))
    return 0
  fi
  if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi
}'''

inflight_old = 'check P12 "no NEW in-flight export introduced by this round (pre-existing user jobs untouched)" "$NEW_INFLIGHT" ""'
inflight_new = 'check P12 "no NEW in-flight export introduced by this round (pre-existing user jobs untouched)" "$([ -z "$NEW_INFLIGHT" ] && echo none || echo "$NEW_INFLIGHT")" "none"'

hard_create = '''HARD_ID_OK=1
if [ -z "$EXPORT_ID" ]; then HARD_ID_OK=0; fi
if [ "$(printf '%s' "$EXPORT_ID" | grep -cE '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')" != "1" ]; then HARD_ID_OK=0; fi
if [ -z "$UI_CREATE_RID" ] || [ -z "$UI_CREATE_TID" ]; then HARD_ID_OK=0; fi
if [ "$PW_RC" != "0" ] || [ "$REPORT_RC" != "0" ]; then HARD_ID_OK=0; fi
if [ "$HARD_ID_OK" != "1" ]; then
  echo "HARD_ABORT: create evidence is not usable (export_id='$EXPORT_ID' request_id='$UI_CREATE_RID' trace_id='$UI_CREATE_TID' playwright_rc=$PW_RC report_rc=$REPORT_RC); no further request is issued"
  gate "hard_abort_unusable_create_evidence"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 8
fi

echo "=== P3 PG row for the UI-created export ==="'''

hard_pg = '''if [ "$STATUS" != "COMPLETED" ] || [ -z "$REF" ] || [ "$(printf '%s' "$PSHA" | grep -cE '^[0-9a-f]{64}$')" != "1" ] || [ "$(printf '%s' "$PROWS" | grep -cE '^[0-9]+$')" != "1" ]; then
  echo "HARD_ABORT: PG row unusable (status='$STATUS' object_ref='$REF' sha256='$PSHA' rows='$PROWS'); stopping before any download request"
  gate "hard_abort_unusable_pg_row"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 8
fi

echo "=== P4 audit chain: create + completed bound EXACTLY to the browser request ids ==="'''

detail_guard = '''if [ ! -s "$EV/detail.json" ]; then
  echo "HARD_ABORT: v2 detail response is empty; refusing to interpret it as data"
  gate "hard_abort_empty_detail"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 8
fi
DET_BUCKET=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('object_bucket'))" "$EV/detail.json")'''

edits = [
    (check_old, check_new),
    (inflight_old, inflight_new),
    ('echo "=== P3 PG row for the UI-created export ==="', hard_create),
    ('echo "=== P4 audit chain: create + completed bound EXACTLY to the browser request ids ==="', hard_pg),
    ('DET_BUCKET=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))[\'data\'].get(\'object_bucket\'))" "$EV/detail.json")', detail_guard),
]
before = hashlib.sha256(src.encode("utf-8")).hexdigest()
for old, new in edits:
    count = src.count(old)
    print("anchor_count=" + str(count) + " :: " + old.splitlines()[0][:80])
    if count != 1:
        print("PATCH_REFUSED anchor must appear exactly once")
        sys.exit(3)
    src = src.replace(old, new)
after = hashlib.sha256(src.encode("utf-8")).hexdigest()
open(path, "w", encoding="utf-8").write(src)
print("sha256_before=" + before)
print("sha256_after=" + after)
print("PATCH_APPLIED")
