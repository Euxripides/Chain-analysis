#!/usr/bin/env python3
"""Append the client-disconnect regression test for the export download audit."""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()
addition = open(sys.argv[2], encoding="utf-8").read()
anchor = '	if len(sink.events) != 0 {\n\t\tt.Fatalf("no audit event may be recorded when the writer fails, got %d", len(sink.events))\n\t}\n}\n'
count = src.count(anchor)
print("anchor_count=" + str(count))
if count != 1 or src.count("TestDownloadFullAuditSurvivesClientDisconnect") != 0:
    print("PATCH_REFUSED (anchor must be unique and the new test must not exist yet)")
    sys.exit(3)
before = hashlib.sha256(src.encode("utf-8")).hexdigest()
src = src.replace(anchor, anchor + addition)
open(path, "w", encoding="utf-8").write(src)
print("sha256_before=" + before)
print("sha256_after=" + hashlib.sha256(src.encode("utf-8")).hexdigest())
print("PATCH_APPLIED")
