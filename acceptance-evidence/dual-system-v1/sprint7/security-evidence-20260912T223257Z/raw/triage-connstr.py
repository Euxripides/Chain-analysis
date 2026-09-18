#!/usr/bin/env python3
"""Triage the connection-string hits: report VALUE HASH + length + context shape, never the value."""
import re, sys, hashlib, json, os
ROOT = sys.argv[1]
RX = re.compile(rb'(?i)((postgres|postgresql|clickhouse|nats|redis|mongodb)://)([^\s:/@\x22\x27]{1,40}):([^\s@/\x22\x27]{1,60})@')
files = ['backend/bin/api','backend/bin/worker','backend/bin/migrate','deploy/.env.example']
for rel in files:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p): print(rel, 'MISSING'); continue
    data = open(p,'rb').read()
    for m in RX.finditer(data):
        scheme, _, user, pwd = m.group(1), m.group(2), m.group(3), m.group(4)
        h = hashlib.sha256(pwd).hexdigest()[:16]
        pre = data[max(0,m.start()-90):m.start()]
        pre = re.sub(rb'[\x00-\x1f]', b'.', pre)
        print('%-26s scheme=%-24s user=%-14s pwd_len=%-3d pwd_sha256_16=%s' % (
            rel, scheme.decode(), user.decode('utf-8','replace'), len(pwd), h))
        print('      context_before(masked): %s' % pre.decode('utf-8','replace')[-90:])
    print()
print('=== distinct password hashes across all artifacts ===')
seen = {}
for rel in files:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p): continue
    for m in RX.finditer(open(p,'rb').read()):
        h = hashlib.sha256(m.group(4)).hexdigest()[:16]
        seen.setdefault(h, set()).add(rel)
for h, fs in seen.items():
    print('  %s -> %s' % (h, sorted(fs)))
