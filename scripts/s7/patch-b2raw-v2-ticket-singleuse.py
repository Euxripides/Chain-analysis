#!/usr/bin/env python3
"""Exactly-once additions: single-use ticket reuse must be rejected, and the consumed ticket's own
request/trace ids must bind to the ticket download audit."""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()
old = '''  TICKET_BYTES=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'bytes','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true' order by seq desc limit 1")
  check P9 "UI ticket audit bytes == UI file bytes" "$TICKET_BYTES" "$UI_BYTES"'''
new = '''  TICKET_BYTES=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'bytes','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true' order by seq desc limit 1")
  check P9 "UI ticket audit bytes == UI file bytes" "$TICKET_BYTES" "$UI_BYTES"
  TICKET_URL=$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
data=d['ticket_response']['body'].get('data') or {}
print(data.get('url') or '')
" "$UI_JSON")
  nonempty P9 "$TICKET_URL" "ticket url captured from the real envelope"
  REUSE_CODE=$(httpc ticket-reuse "$EV/ticket-reuse.json" "$BASE$TICKET_URL")
  check P9 "re-using a consumed one-time ticket is rejected (410)" "$REUSE_CODE" "410"
  CONSUMED_TID=$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
rows=[r for r in d.get('observed_download_responses',[]) if '/api/v2/exports/download/' in r['url']]
print(rows[-1]['trace_id'] if rows and rows[-1].get('trace_id') else '')
" "$UI_JSON")
  if [ -n "$CONSUMED_TID" ]; then
    TICKET_AUDIT_TID=$(pgq "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true' order by seq desc limit 1")
    check P9 "ticket download audit trace_id == the consumed ticket response trace id" "$TICKET_AUDIT_TID" "$CONSUMED_TID"
  fi
  check P9 "no curl helper call failed during the ticket checks" "$(helper_counts | awk '{print $4}')" "0"'''
edits = [(old, new)]
before = hashlib.sha256(src.encode("utf-8")).hexdigest()
for o, n in edits:
    count = src.count(o)
    print("anchor_count=" + str(count))
    if count != 1:
        print("PATCH_REFUSED anchor must appear exactly once")
        sys.exit(3)
    src = src.replace(o, n)
after = hashlib.sha256(src.encode("utf-8")).hexdigest()
open(path, "w", encoding="utf-8").write(src)
print("sha256_before=" + before)
print("sha256_after=" + after)
print("PATCH_APPLIED")
