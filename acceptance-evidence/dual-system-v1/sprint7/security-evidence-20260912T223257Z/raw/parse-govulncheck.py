#!/usr/bin/env python3
"""Parse govulncheck JSON stream -> deduplicated, reachability-classified finding table."""
import json, sys, os, collections

D, OUT = sys.argv[1], sys.argv[2]
src = os.path.join(D, 'govulncheck.json')

def stream(path):
    dec = json.JSONDecoder(); buf = open(path, encoding='utf-8').read()
    i, n = 0, len(buf)
    while i < n:
        while i < n and buf[i] in ' \t\r\n': i += 1
        if i >= n: break
        obj, j = dec.raw_decode(buf, i); yield obj; i = j

config = None; osvs = {}; findings = []; modules = []
for obj in stream(src):
    if 'config' in obj: config = obj['config']
    elif 'osv' in obj:
        o = obj['osv']; osvs[o.get('id')] = o
    elif 'finding' in obj:
        f = obj['finding']
        findings.extend(f if isinstance(f, list) else [f])
    elif 'SBOM' in obj:
        modules = obj['SBOM'].get('modules', [])

def classify(trace):
    """govulncheck trace entries: function => symbol reachable; package => package; module => module."""
    if not trace: return 'unknown'
    if any(e.get('function') for e in trace): return 'symbol'
    if any(e.get('package') for e in trace): return 'package'
    return 'module'

def affected_versions(o, module_name):
    """Return (module_path, introduced, fixed) from OSV affected ranges, own-module first."""
    best = None
    for a in (o.get('affected') or []):
        p = (a.get('package') or {}).get('name', '')
        intro = fixed = ''
        for r in (a.get('ranges') or []):
            for ev in (r.get('events') or []):
                if 'introduced' in ev and ev['introduced'] != '0' and not intro: intro = ev['introduced']
                if 'fixed' in ev and not fixed: fixed = ev['fixed']
        cand = (p, intro, fixed)
        if module_name and p.split('/')[:3] == module_name.split('/')[:3]: return cand
        if best is None: best = cand
    return best or ('', '', '')

agg = collections.OrderedDict()
for f in findings:
    oid = f.get('osv'); o = osvs.get(oid, {})
    rec = agg.setdefault(oid, {
        'osv_id': oid, 'aliases': o.get('aliases') or [], 'summary': (o.get('summary') or '')[:200],
        'reachability': 'unknown', 'traces': 0, 'module': '', 'found_version': '', 'fixed_version': '',
        'example_trace': '', 'go_fixed_version': f.get('fixed_version', ''),
    })
    rec['traces'] += 1
    t = classify(f.get('trace') or [])
    order = {'symbol': 3, 'package': 2, 'module': 1, 'unknown': 0}
    if order.get(t, 0) > order.get(rec['reachability'], 0): rec['reachability'] = t
    mp, intro, fixed = affected_versions(o, rec['module'])
    if mp: rec['module'] = mp
    if intro and not rec['found_version']: rec['found_version'] = intro
    if fixed and not rec['fixed_version']: rec['fixed_version'] = fixed
    if not rec['example_trace']:
        for e in (f.get('trace') or []):
            pos = e.get('position') or {}
            if pos.get('filename'):
                rec['example_trace'] = pos['filename'] + ':' + str(pos.get('line', '')); break

rows = list(agg.values())
by = collections.Counter(r['reachability'] for r in rows)
out = {
    'scanner': config,
    'module_count': len(modules),
    'raw_finding_entries': len(findings),
    'distinct_vulnerabilities': len(rows),
    'reachability_breakdown_distinct': dict(by),
    'osv_record_count': len(osvs),
    'vulnerabilities': rows,
}
json.dump(out, open(OUT, 'w', encoding='utf-8'), indent=2, ensure_ascii=False)
print(json.dumps({k: v for k, v in out.items() if k != 'vulnerabilities'}, indent=2))
print()
print('%-16s %-9s %-12s %-32s %-10s %-10s %s' % ('OSV','REACH','MODULE','','FOUND','FIXED','ALIASES'))
for r in sorted(rows, key=lambda x: (-order.get(x['reachability'],0), x['osv_id'])):
    print('%-16s %-9s %-32s %-10s %-10s %s' % (r['osv_id'], r['reachability'], r['module'][:32], r['found_version'], r['fixed_version'], ','.join(r['aliases'])[:55]))
