#!/usr/bin/env python3
"""Exactly-once corrections to the B2 raw-only UI runner (v2).

1. Report gate: Playwright's JSON report carries the human title on the SPEC, not on each test
   entry, so the previous extraction produced empty titles and the gate could never see them.
2. Console title checks: the console legitimately repeats a title (test list + failure list), so an
   exact count of 1 was wrong; the check now asserts "mentioned at least once".
"""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()
edits = [
    (
        '        for t in spec.get("tests") or []:\n            titles.append(t.get("title", ""))',
        '        titles.append(spec.get("title", ""))',
    ),
    (
        'check P2 "console mentions B2RAW-01" "$(grep -c \'B2RAW-01\' "$EV/playwright.console.log" || true)" "1"',
        'check P2 "console mentions B2RAW-01" "$([ "$(grep -c \'B2RAW-01\' "$EV/playwright.console.log" || true)" -ge 1 ] && echo yes || echo no)" "yes"',
    ),
    (
        'check P2 "console mentions B2RAW-02" "$(grep -c \'B2RAW-02\' "$EV/playwright.console.log" || true)" "1"',
        'check P2 "console mentions B2RAW-02" "$([ "$(grep -c \'B2RAW-02\' "$EV/playwright.console.log" || true)" -ge 1 ] && echo yes || echo no)" "yes"',
    ),
]
before = hashlib.sha256(src.encode("utf-8")).hexdigest()
for old, new in edits:
    count = src.count(old)
    print("anchor_count=" + str(count) + " anchor=" + old.splitlines()[0][:70])
    if count != 1:
        print("PATCH_REFUSED anchor must appear exactly once")
        sys.exit(3)
    src = src.replace(old, new)
after = hashlib.sha256(src.encode("utf-8")).hexdigest()
open(path, "w", encoding="utf-8").write(src)
print("sha256_before=" + before)
print("sha256_after=" + after)
print("PATCH_APPLIED")
