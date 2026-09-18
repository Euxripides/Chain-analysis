#!/usr/bin/env python3
"""Fetch OSV records for the exact vulnerability IDs govulncheck reported; extract authoritative severity."""
import json, sys, os, urllib.request, urllib.error, datetime

D, OUT = sys.argv[1], sys.argv[2]
parsed = json.load(open(os.path.join(D, 'govulncheck-parsed.json'), encoding='utf-8'))
ids = [v['osv_id'] for v in parsed['vulnerabilities']]

os.makedirs(OUT, exist_ok=True)
log = []
records = {}
for vid in ids:
    url = 'https://api.osv.dev/v1/vulns/' + vid
    try:
        req = urllib.request.Request(url, headers={'User-Agent': 'security-evidence/1.0'})
        with urllib.request.urlopen(req, timeout=30) as r:
            body = r.read().decode('utf-8')
            code = r.status
        open(os.path.join(OUT, vid + '.json'), 'w', encoding='utf-8').write(body)
        records[vid] = json.loads(body)
        log.append('%s HTTP %s bytes=%d' % (vid, code, len(body)))
    except Exception as e:
        log.append('%s FETCH_FAILED %r' % (vid, e))

def sev(rec):
    ds = rec.get('database_specific') or {}
    out = {'database_specific_severity': ds.get('severity'), 'cvss': []}
    for s in (rec.get('severity') or []):
        out['cvss'].append({'type': s.get('type'), 'score': s.get('score')})
    for a in (rec.get('affected') or []):
        d2 = a.get('database_specific') or {}
        if d2.get('severity'): out.setdefault('per_affected', []).append(d2['severity'])
    return out

table = []
for vid in ids:
    rec = records.get(vid)
    if not rec:
        table.append({'osv_id': vid, 'severity': 'FETCH_FAILED'}); continue
    s = sev(rec)
    table.append({
        'osv_id': vid,
        'aliases': rec.get('aliases') or [],
        'summary': (rec.get('summary') or '')[:200],
        'severity': s.get('database_specific_severity') or 'UNKNOWN',
        'cvss': s.get('cvss'),
        'published': rec.get('published'),
        'modified': rec.get('modified'),
    })

json.dump({'fetched_utc': datetime.datetime.utcnow().isoformat() + 'Z', 'source': 'https://api.osv.dev/v1/vulns/<ID>', 'log': log, 'severity_table': table},
          open(os.path.join(D, 'osv-severity-table.json'), 'w', encoding='utf-8'), indent=2, ensure_ascii=False)
print('\n'.join(log))
print()
for t in table:
    print('%-16s %-10s %s' % (t['osv_id'], t.get('severity'), ','.join(t.get('aliases') or [])[:50]))
