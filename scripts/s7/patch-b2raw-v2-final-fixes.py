#!/usr/bin/env python3
"""Final runner corrections: deployed API identity, real audit column names, authenticated reuse probe."""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()
edits = [
    (
        "EXPECT_API_SHA=20346f3e01f11496caee2d72d3715c57ced2e40f93fd12e148870e3cff11811a",
        "EXPECT_API_SHA=3f7cc3a727b467ad5956a9f3f235015c5ab45cc6ef99929b3cb1472af0c85334",
    ),
    (
        'KROUTE=$(pgq "SET ROLE blockchain_app; select coalesce(route,\'\')||\' \'||coalesce(method,\'\') from audit_events_v2 where action=\'export.completed\' and resource_id=\'$EXPORT_ID\' order by seq desc limit 1")',
        'KROUTE=$(pgq "SET ROLE blockchain_app; select coalesce(route_template,\'\')||\' \'||coalesce(http_method,\'\') from audit_events_v2 where action=\'export.completed\' and resource_id=\'$EXPORT_ID\' order by seq desc limit 1")',
    ),
    (
        'REUSE_CODE=$(httpc ticket-reuse "$EV/ticket-reuse.json" "$BASE$TICKET_URL")',
        'REUSE_CODE=$(httpc ticket-reuse "$EV/ticket-reuse.json" -b "$COOKIE" -H "X-Auth-Audience: user" "$BASE$TICKET_URL")',
    ),
]
before = hashlib.sha256(src.encode("utf-8")).hexdigest()
for old, new in edits:
    c = src.count(old)
    print("anchor_count=" + str(c) + " :: " + old[:70])
    if c != 1:
        print("PATCH_REFUSED")
        sys.exit(3)
    src = src.replace(old, new)
open(path, "w", encoding="utf-8").write(src)
print("sha256_before=" + before)
print("sha256_after=" + hashlib.sha256(src.encode("utf-8")).hexdigest())
print("PATCH_APPLIED")
