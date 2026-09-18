#!/usr/bin/env python3
"""Hash the in-scope release artifacts and scan build products for embedded secrets."""
import os, re, sys, json, hashlib, subprocess, datetime, collections

ROOT, OUTDIR = sys.argv[1], sys.argv[2]
os.makedirs(OUTDIR, exist_ok=True)

ARTIFACTS = [
 ('backend/bin/api', 'binary', 'backend executable'),
 ('backend/bin/worker', 'binary', 'backend worker executable'),
 ('backend/bin/migrate', 'binary', 'migration executable'),
 ('deploy/compose.yaml', 'iac', 'compose'),
 ('deploy/nginx-blockchain-platform.conf', 'iac', 'nginx site config'),
 ('deploy/monitoring/dual-system-alerts.yaml', 'iac', 'alert rules'),
 ('deploy/.env.example', 'config', 'env template'),
 ('frontend/pnpm-lock.yaml', 'lockfile', 'frontend lockfile'),
 ('backend/go.mod', 'lockfile', 'go module manifest'),
 ('backend/go.sum', 'lockfile', 'go module checksums'),
]
for i in range(1, 9):
    pass
for fn in sorted(os.listdir(os.path.join(ROOT, 'deploy'))):
    if fn.endswith('.service') or fn.endswith('.timer'):
        ARTIFACTS.append(('deploy/' + fn, 'systemd', 'systemd unit'))

def sha256(p):
    h = hashlib.sha256()
    with open(p, 'rb') as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()

inv = []
for rel, kind, desc in ARTIFACTS:
    p = os.path.join(ROOT, rel)
    if os.path.isfile(p):
        st = os.stat(p)
        inv.append({'path': rel, 'kind': kind, 'description': desc, 'bytes': st.st_size,
                    'mtime_utc': datetime.datetime.fromtimestamp(st.st_mtime, datetime.UTC).isoformat(),
                    'sha256': sha256(p)})
    else:
        inv.append({'path': rel, 'kind': kind, 'description': desc, 'missing': True})

# frontend build products
front = []
for rel in ['frontend/apps/admin-console/dist', 'frontend/apps/user-console/dist']:
    base = os.path.join(ROOT, rel)
    if not os.path.isdir(base): continue
    for dp, dn, fns in os.walk(base):
        for fn in sorted(fns):
            fp = os.path.join(dp, fn)
            front.append({'path': os.path.relpath(fp, ROOT), 'bytes': os.path.getsize(fp), 'sha256': sha256(fp)})

# deployed nginx conf on host
nginx_host = '/etc/nginx/sites-enabled/blockchain-platform.conf'
host_nginx = {'path': nginx_host, 'sha256': sha256(nginx_host), 'bytes': os.path.getsize(nginx_host)} if os.path.exists(nginx_host) else {'path': nginx_host, 'missing': True}

SECRET_RX = [
 ('aws_access_key_id', re.compile(rb'\b(AKIA|ASIA)[0-9A-Z]{16}\b')),
 ('private_key_block', re.compile(rb'-----BEGIN [A-Z ]*PRIVATE KEY-----')),
 ('jwt', re.compile(rb'\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b')),
 ('github_token', re.compile(rb'\bgh[pousr]_[A-Za-z0-9]{20,}\b')),
 ('slack_token', re.compile(rb'\bxox[baprs]-[A-Za-z0-9-]{10,}\b')),
 ('pg_conn_with_password', re.compile(rb'(?i)\b(postgres|postgresql)://[^\s:]{1,40}:([^\s@/]{6,})@')),
 ('clickhouse_conn_password', re.compile(rb'(?i)\bclickhouse://[^\s:]{1,40}:([^\s@/]{6,})@')),
 ('nats_conn_password', re.compile(rb'(?i)\bnats://[^\s:]{1,40}:([^\s@/]{6,})@')),
 ('minio_secret_literal', re.compile(rb'(?i)(MINIO_ROOT_PASSWORD|SECRET_KEY|ACCESS_KEY)\s*[:=]\s*[\x22\x27]([A-Za-z0-9+/=_-]{12,})[\x22\x27]')),
 ('bearer_literal', re.compile(rb'(?i)\bBearer\s+([A-Za-z0-9._\-]{24,})')),
]

def mask(b):
    try: s = b.decode('utf-8', 'replace')
    except Exception: s = repr(b)
    return (s[:2] + '*' * max(3, len(s) - 4) + s[-2:]) if len(s) > 4 else '*' * len(s)

scan_hits = []
scanned = []
targets = [a for a in inv if not a.get('missing') and a['kind'] in ('binary', 'iac', 'config', 'systemd')]
targets += [{'path': f['path'], 'kind': 'build-product', 'sha256': f['sha256'], 'bytes': f['bytes']} for f in front]
for t in targets:
    p = os.path.join(ROOT, t['path'])
    try:
        data = open(p, 'rb').read()
    except Exception as e:
        scanned.append({'path': t['path'], 'error': repr(e)[:80]}); continue
    n = 0
    for rule, rx in SECRET_RX:
        for m in rx.finditer(data):
            n += 1
            groups = [g for g in m.groups() if g]
            val = groups[-1] if groups else m.group(0)
            scan_hits.append({'path': t['path'], 'rule': rule, 'masked': mask(val),
                              'offset': m.start(), 'artifact_kind': t['kind']})
    scanned.append({'path': t['path'], 'kind': t['kind'], 'bytes': t['bytes'], 'sha256': t['sha256'], 'matches': n})

out = {
 'generated_utc': datetime.datetime.now(datetime.UTC).isoformat(),
 'host': os.uname().nodename,
 'release_artifact_inventory': inv,
 'frontend_build_products': front,
 'deployed_nginx_conf': host_nginx,
 'artifact_secret_scan': {
   'rules': [r for r, _ in SECRET_RX],
   'targets_scanned': len(scanned),
   'total_matches': len(scan_hits),
   'matches': scan_hits,
   'per_target': scanned,
 },
}
json.dump(out, open(os.path.join(OUTDIR, 'artifact-inventory.json'), 'w', encoding='utf-8'), indent=2, ensure_ascii=False)

print('inventory entries:', len(inv), ' frontend build products:', len(front))
print()
for a in inv:
    print('%-56s %-10s %s' % (a['path'], a.get('bytes', 'MISSING'), a.get('sha256', '')[:16]))
print()
print('frontend build products:')
for f in front: print('  %-56s %-10s %s' % (f['path'], f['bytes'], f['sha256'][:16]))
print()
print('deployed nginx:', host_nginx)
print()
print('ARTIFACT SECRET SCAN: targets=%d total_matches=%d' % (len(scanned), len(scan_hits)))
for h in scan_hits[:40]:
    print('   %s [%s] %s' % (h['path'], h['rule'], h['masked']))
