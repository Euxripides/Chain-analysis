#!/usr/bin/env python3
"""§23.8 'build products contain no secret' check.
For EVERY secret value in deploy/.env, test byte-substring presence in each build product.
Reports PRESENT/ABSENT only — never the value."""
import os, re, sys, hashlib, json, datetime

ROOT = sys.argv[1]; OUTDIR = sys.argv[2]
os.makedirs(OUTDIR, exist_ok=True)
KV = re.compile(rb'(?m)^\s*([A-Z0-9_]{3,64})\s*=\s*(.+?)\s*$')
SECRETKEY = re.compile(rb'(?i)(PASSWORD|SECRET|TOKEN|API_?KEY|ACCESS_?KEY|_DSN|_URL|COOKIE_SECRET|PRIVATE)')
SKIPVAL = re.compile(rb'(?i)^(\$|<|true$|false$|https?://[a-z0-9\.\-]+/?$|[\d\.\-:/]+$)')

# collect secret values from live env
cands = []
for rel in ['deploy/.env', 'backend/.env']:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p): continue
    for m in KV.finditer(open(p, 'rb').read()):
        k, v = m.group(1), m.group(2).strip().strip(b'"').strip(b"'")
        if not SECRETKEY.search(k): continue
        if SKIPVAL.match(v) or len(v) < 8: continue
        cands.append((rel, k.decode(), v))
# also DSN-embedded passwords
RX = re.compile(rb'(?i)(postgres|postgresql|clickhouse|nats|redis|mongodb)://([^\s:/@\x22\x27]{1,40}):([^\s@/\x22\x27]{6,})@')
for rel in ['deploy/.env', 'backend/.env']:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p): continue
    for m in RX.finditer(open(p, 'rb').read()):
        cands.append((rel, m.group(1).decode().upper() + '_DSN_PASSWORD', m.group(3)))

# dedupe by value hash
uniq = {}
for rel, k, v in cands:
    uniq.setdefault(hashlib.sha256(v).hexdigest(), {'keys': [], 'value': v})
    if k not in uniq[hashlib.sha256(v).hexdigest()]['keys']: uniq[hashlib.sha256(v).hexdigest()]['keys'].append(k)

# build products
products = ['backend/bin/api', 'backend/bin/worker', 'backend/bin/migrate']
for base in ['frontend/apps/admin-console/dist', 'frontend/apps/user-console/dist', 'frontend/dist']:
    b = os.path.join(ROOT, base)
    if not os.path.isdir(b): continue
    for dp, dn, fns in os.walk(b):
        for fn in fns: products.append(os.path.relpath(os.path.join(dp, fn), ROOT))

blobs = {}
for rel in products:
    p = os.path.join(ROOT, rel)
    try: blobs[rel] = open(p, 'rb').read()
    except Exception: pass

rows = []
for h, info in uniq.items():
    v = info['value']
    where = sorted([rel for rel, b in blobs.items() if v in b])
    rows.append({'value_sha256_16': h[:16], 'value_len': len(v), 'env_keys': info['keys'],
                 'present_in_build_products': where, 'count': len(where)})

rows.sort(key=lambda r: -r['count'])
out = {'generated_utc': datetime.datetime.now(datetime.UTC).isoformat(),
       'method': 'byte-substring presence test of every deploy/.env secret value inside each build product; values never printed',
       'build_products_scanned': len(blobs), 'distinct_secret_values_tested': len(rows),
       'secrets_found_in_build_products': sum(1 for r in rows if r['count']),
       'results': rows}
json.dump(out, open(os.path.join(OUTDIR, 'build-product-secret-check.json'), 'w', encoding='utf-8'), indent=2, ensure_ascii=False)

print('build products scanned      :', len(blobs))
print('distinct secret values tried:', len(rows))
print('VALUES FOUND IN BUILD PRODUCTS:', sum(1 for r in rows if r['count']))
print()
for r in rows:
    if r['count']:
        print('  [!] sha16=%s len=%-3d keys=%s' % (r['value_sha256_16'], r['value_len'], ','.join(r['env_keys'])))
        for w in r['present_in_build_products']: print('        -> %s' % w)
print()
print('  [ok] values NOT in any build product:', sum(1 for r in rows if not r['count']))
for r in rows:
    if not r['count']: print('        sha16=%s len=%-3d keys=%s' % (r['value_sha256_16'], r['value_len'], ','.join(r['env_keys'])))
