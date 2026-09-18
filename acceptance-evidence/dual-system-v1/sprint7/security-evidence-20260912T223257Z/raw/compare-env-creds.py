#!/usr/bin/env python3
"""Compare credential VALUE HASHES between live .env files and the values embedded in build artifacts.
Prints only hashes/lengths — never raw values."""
import os, re, sys, hashlib

ROOT = sys.argv[1]
TARGET = {'8690ac3034f20c0f': 'clickhouse DSN password (embedded in api/worker/migrate + .env.example)',
          '8cafc51c210a2240': 'postgres DSN password (embedded in api/worker/migrate + .env.example)'}
RX = re.compile(rb'(?i)(postgres|postgresql|clickhouse|nats|redis|mongodb)://([^\s:/@\x22\x27]{1,40}):([^\s@/\x22\x27]{1,60})@')
KV = re.compile(rb'(?m)^\s*([A-Z0-9_]{3,64})\s*=\s*(.+?)\s*$')

envs = ['deploy/.env', 'backend/.env', 'frontend/.env.local']
print('=== live .env DSN password hashes ===')
for rel in envs:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p): print('%-22s MISSING' % rel); continue
    data = open(p,'rb').read()
    print('%-22s bytes=%d' % (rel, len(data)))
    for m in RX.finditer(data):
        pwd = m.group(3)
        h = hashlib.sha256(pwd).hexdigest()[:16]
        flag = '  <== MATCHES ARTIFACT/EXAMPLE' if h in TARGET else ''
        print('    %-14s user=%-14s pwd_len=%-3d sha16=%s%s' % (m.group(1).decode(), m.group(2).decode('utf-8','replace'), len(pwd), h, flag))

print()
print('=== named secret keys in live .env (value hash + length only) ===')
SECRETKEYS = re.compile(rb'(?i)(PASSWORD|SECRET|TOKEN|API_?KEY|ACCESS_?KEY|COOKIE|SESSION)')
for rel in envs:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p): continue
    print('---', rel)
    for m in KV.finditer(open(p,'rb').read()):
        k, v = m.group(1), m.group(2).strip(b'"').strip(b"'")
        if not SECRETKEYS.search(k): continue
        if v.startswith(b'$') or v.startswith(b'<'): cls = 'TEMPLATE_REF'
        elif len(v) < 8: cls = 'SHORT'
        else: cls = 'VALUE'
        h = hashlib.sha256(v).hexdigest()[:12] if v else '-'
        mark = '  <== SAME AS EMBEDDED/EXAMPLE' if h in ('8cafc51c210a'[:12], '8690ac3034f2'[:12]) or hashlib.sha256(v).hexdigest()[:16] in TARGET else ''
        print('    %-34s len=%-4d cls=%-12s sha12=%s%s' % (k.decode(), len(v), cls, h, mark))
