#!/usr/bin/env python3
"""Resolve authoritative severity for each Go vuln via its GHSA alias (OSV API GHSA records carry database_specific.severity + CVSS)."""
import json, sys, os, urllib.request, datetime

D, OUT = sys.argv[1], sys.argv[2]
os.makedirs(OUT, exist_ok=True)
parsed = json.load(open(os.path.join(D, 'govulncheck-parsed.json'), encoding='utf-8'))
log, table = [], []

def fetch(vid):
    url = 'https://api.osv.dev/v1/vulns/' + vid
    req = urllib.request.Request(url, headers={'User-Agent': 'security-evidence/1.0'})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.status, r.read().decode('utf-8')

for v in parsed['vulnerabilities']:
    alias = next((a for a in v['aliases'] if a.startswith('GHSA-')), None)
    sev, cvss, src = 'UNKNOWN', [], None
    if alias:
        try:
            code, body = fetch(alias)
            open(os.path.join(OUT, alias + '.json'), 'w', encoding='utf-8').write(body)
            rec = json.loads(body)
            src = alias
            sev = (rec.get('database_specific') or {}).get('severity') or 'UNKNOWN'
            for s in (rec.get('severity') or []):
                cvss.append({'type': s.get('type'), 'score': s.get('score')})
            log.append('%s -> %s HTTP %s severity=%s' % (v['osv_id'], alias, code, sev))
        except Exception as e:
            log.append('%s -> %s FETCH_FAILED %r' % (v['osv_id'], alias, e))
    else:
        log.append('%s -> NO GHSA ALIAS (Go DB has no severity field)' % v['osv_id'])
    table.append({
        'osv_id': v['osv_id'], 'alias_used': src, 'aliases': v['aliases'],
        'severity': sev, 'cvss': cvss,
        'module': v['module'], 'reachability': v['reachability'],
        'summary': v['summary'], 'fixed_version': v.get('go_fixed_version') or v.get('fixed_version'),
    })

out = {'fetched_utc': datetime.datetime.now(datetime.UTC).isoformat(),
       'source': 'https://api.osv.dev/v1/vulns/<GHSA-ID> (OSV mirrors GHSA severity)',
       'note': 'vuln.go.dev Go records carry no severity field; severity resolved via GHSA alias.',
       'log': log, 'severity_table': table}
json.dump(out, open(os.path.join(D, 'osv-severity-table.json'), 'w', encoding='utf-8'), indent=2, ensure_ascii=False)
print('\n'.join(log)); print()
for t in table:
    print('%-16s %-10s %-9s %-32s %s' % (t['osv_id'], t['severity'], t['reachability'], t['module'][:32], t['alias_used'] or '-'))
