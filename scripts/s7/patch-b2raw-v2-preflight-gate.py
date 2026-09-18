#!/usr/bin/env python3
"""Exactly-once patch: replace the invented preflight PG-call floor with per-item accounting.

Before:  a magic floor of >= 5 calls, which was wrong (the preflight phase issues exactly 3) and
         would also have accepted a run in which a required query silently did not happen.
After:   exactly three recorded calls, each already proven rc=0 by the pg_bad check; a missing
         query changes the count and fails. The non-no-op calibration is therefore PRESERVED and
         made stricter, not removed.
"""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()
old = 'check P0 "preflight: PG helper really executed (no silent no-op)" "$([ "$HC_PG_TOTAL" -ge 5 ] && echo yes || echo no)" "yes"'
new = (
    '# The preflight phase issues EXACTLY three queries: the export census, the in-flight census and\n'
    '# the same-parameter count. Binding the count to that number keeps the no-silent-no-op check\n'
    '# (a skipped query changes the count) and replaces the earlier invented floor of 5.\n'
    'check P0 "preflight: exactly three pre-census PG queries recorded (n=$HC_PG_TOTAL)" "$HC_PG_TOTAL" "3"\n'
    'check P0 "preflight: every pre-census PG query returned rc=0" "$HC_PG_BAD" "0"'
)
count = src.count(old)
print("anchor_count=" + str(count))
if count != 1:
    print("PATCH_REFUSED anchor must appear exactly once")
    sys.exit(3)
before = hashlib.sha256(src.encode("utf-8")).hexdigest()
patched = src.replace(old, new)
after = hashlib.sha256(patched.encode("utf-8")).hexdigest()
open(path, "w", encoding="utf-8").write(patched)
print("sha256_before=" + before)
print("sha256_after=" + after)
print("PATCH_APPLIED")
