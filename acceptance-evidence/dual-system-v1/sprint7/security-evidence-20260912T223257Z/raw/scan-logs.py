#!/usr/bin/env python3
"""AUDIT-002 runtime-log secret scan. Counts plaintext password/token/key/cookie occurrences in logs.
Emits file:line (or byte offset) + masked value. Never prints a raw secret value."""
import os, re, sys, json, gzip, subprocess, datetime, collections

ROOT = sys.argv[1]; OUTDIR = sys.argv[2]
os.makedirs(OUTDIR, exist_ok=True)

LOG_PATTERNS = [
 ('url_query_token',   re.compile(r'(?i)[?&](token|access_token|api_?key|apikey|key|password|passwd|pwd|secret|auth|session|sessionid|sid)=([^&\s"\'#]{3,})')),
 ('authorization_hdr', re.compile(r'(?i)\bauthorization\b\s*[:=]\s*(bearer|basic|token)?\s*([A-Za-z0-9._\-+/=]{8,})')),
 ('bearer_token',      re.compile(r'(?i)\bbearer\s+([A-Za-z0-9._\-+/=]{12,})')),
 ('set_cookie',        re.compile(r'(?i)\bset-cookie\b\s*:\s*([^\r\n;=]{1,60})=([^;\r\n]{4,})')),
 ('cookie_pair',       re.compile(r'(?i)\bcookie\b\s*[:=]\s*([^\r\n;=]{1,60})=([^;\s\r\n"]{6,})')),
 ('json_password',     re.compile(r'(?i)"(password|passwd|pwd|secret|token|api_?key|access_?key|client_?secret)"\s*:\s*"([^"]{3,})"')),
 ('kv_password',       re.compile(r'(?i)\b(password|passwd|pwd|secret|token|api_?key|access_?key|client_?secret)\b\s*[:=]\s*"?([^\s",;]{6,})')),
 ('aws_key',           re.compile(r'\b(AKIA|ASIA)[0-9A-Z]{16}\b')),
 ('jwt',               re.compile(r'\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b')),
 ('pg_conn',           re.compile(r'(?i)\b(postgres|postgresql)://[^\s:]+:([^\s@]{3,})@')),
 ('private_key',       re.compile(r'-----BEGIN [A-Z ]*PRIVATE KEY-----')),
]
PLACEHOLDER = re.compile(r'(?i)^(\x24\{?[a-z0-9_]+\}?|<[^>]*>|x{3,}|\*{3,}|\.{3,}|-+|changeme|your[_-]?|placeholder|example|dummy|fake|redact|todo|none|null|undefined|true|false|test|sample|demo|bearer|basic|\{\{.*\}\})$')

def mask(v):
    v = v.strip()
    if len(v) <= 4: return '*' * len(v)
    return v[:2] + '*' * max(3, len(v) - 4) + v[-2:]

def classify(v):
    v = v.strip().strip('"').strip("'")
    if not v: return 'EMPTY'
    if PLACEHOLDER.match(v): return 'PLACEHOLDER'
    if re.fullmatch(r'[\d\.\-/:]+', v): return 'NUMERIC_OR_PATH'
    if len(v) < 6: return 'TOO_SHORT'
    if re.search(r'(?i)(%[0-9a-f]{2}|redacted|masked|\*\*\*)', v): return 'ALREADY_MASKED'
    return 'PLAINTEXT_CANDIDATE'

def scan_text(path, label, out_hits, max_bytes=None, gz=False):
    opener = gzip.open if gz else open
    n = 0; matched = 0
    with opener(path, 'rt', encoding='utf-8', errors='replace') as fh:
        for i, line in enumerate(fh, 1):
            n += 1
            if len(line) > 8000: line = line[:8000]
            for rule, rx in LOG_PATTERNS:
                m = rx.search(line)
                if not m: continue
                matched += 1
                groups = [g for g in m.groups() if g]
                val = groups[-1] if groups else m.group(0)
                out_hits.append({'source': label, 'path': path, 'line': i, 'rule': rule,
                                 'classification': classify(val), 'masked': mask(val)})
            if max_bytes and n % 100000 == 0: pass
    return n, matched

hits = []; file_stats = []
targets = []

NGINX = '/var/log/nginx'
if os.path.isdir(NGINX):
    for fn in sorted(os.listdir(NGINX)):
        if 'blockchain-platform' in fn:
            targets.append((os.path.join(NGINX, fn), 'nginx-log', fn.endswith('.gz')))
for base, lbl in [(os.path.join(ROOT, 'backend', 'logs'), 'backend-log'),
                  (os.path.join(ROOT, 'frontend', 'logs'), 'frontend-log'),
                  (os.path.join(ROOT, 'outputs'), 'app-output-log')]:
    if os.path.isdir(base):
        for fn in sorted(os.listdir(base)):
            if fn.endswith('.log'):
                targets.append((os.path.join(base, fn), lbl, False))

for path, label, gz in targets:
    try:
        size = os.path.getsize(path)
        n, matched = scan_text(path, label, hits, gz=gz)
        file_stats.append({'path': path, 'label': label, 'bytes': size, 'lines_scanned': n, 'raw_matches': matched})
    except Exception as e:
        file_stats.append({'path': path, 'label': label, 'error': repr(e)[:120]})

# docker container logs for the 4 in-scope platform containers
docker_targets = ['deploy-postgres-1', 'deploy-clickhouse-1', 'deploy-minio-1', 'deploy-nats-1']
for c in docker_targets:
    try:
        r = subprocess.run(['docker', 'logs', '--tail', '20000', c], capture_output=True, text=True, timeout=120)
        txt = (r.stdout or '') + (r.stderr or '')
        lines = txt.splitlines()
        dh = []
        for i, line in enumerate(lines, 1):
            if len(line) > 8000: line = line[:8000]
            for rule, rx in LOG_PATTERNS:
                m = rx.search(line)
                if not m: continue
                groups = [g for g in m.group() and m.groups() if g]
                val = groups[-1] if groups else m.group(0)
                dh.append({'source': 'docker-log', 'path': 'docker logs ' + c, 'line': i, 'rule': rule,
                           'classification': classify(val), 'masked': mask(val)})
        hits.extend(dh)
        file_stats.append({'path': 'docker logs ' + c, 'label': 'docker-log', 'bytes': len(txt),
                           'lines_scanned': len(lines), 'raw_matches': len(dh)})
    except Exception as e:
        file_stats.append({'path': 'docker logs ' + c, 'label': 'docker-log', 'error': repr(e)[:120]})

by_class = collections.Counter(h['classification'] for h in hits)
by_rule = collections.Counter(h['rule'] for h in hits)
by_source = collections.Counter(h['source'] for h in hits)
plain = [h for h in hits if h['classification'] == 'PLAINTEXT_CANDIDATE']

report = {
 'generated_utc': datetime.datetime.now(datetime.UTC).isoformat(),
 'scanner': 'custom log-secret scanner (raw/scan-logs.py) — gitleaks/trufflehog unavailable',
 'sources_scanned': file_stats,
 'total_raw_matches': len(hits),
 'matches_by_classification': dict(by_class),
 'matches_by_rule': dict(by_rule),
 'matches_by_source': dict(by_source),
 'plaintext_candidate_count': len(plain),
 'plaintext_candidates': plain[:400],
 'all_matches_sample': hits[:1500],
}
json.dump(report, open(os.path.join(OUTDIR, 'secret-scan-logs.json'), 'w', encoding='utf-8'), indent=2, ensure_ascii=False)

print('sources=%d raw_matches=%d' % (len(file_stats), len(hits)))
print('by_classification:', dict(by_class))
print('by_rule:', dict(by_rule))
print('by_source:', dict(by_source))
print()
print('--- per-source raw matches ---')
for f in file_stats:
    print('  %-60s bytes=%-12s lines=%-9s raw=%s' % (f['path'][-60:], f.get('bytes','-'), f.get('lines_scanned','-'), f.get('raw_matches', f.get('error','-'))))
print()
print('--- PLAINTEXT_CANDIDATE (masked) ---')
for h in plain[:40]:
    print('%s:%s [%s] %s' % (h['path'], h['line'], h['rule'], h['masked']))
