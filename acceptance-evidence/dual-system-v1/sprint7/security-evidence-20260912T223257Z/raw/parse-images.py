#!/usr/bin/env python3
"""Summarise trivy image JSON scans: severity counts, per-package vuln list, target provenance."""
import json, sys, os, glob, collections, datetime

IMGDIR, OUT = sys.argv[1], sys.argv[2]
refs = {
    'postgres-18': ('postgres:18', True),
    'clickhouse-clickhouse-server-26.7': ('clickhouse/clickhouse-server:26.7', True),
    'nats-2-alpine': ('nats:2-alpine', True),
    'minio-minio-latest': ('minio/minio:latest', False),
    'mobsf-latest': ('opensecurity/mobile-security-framework-mobsf:latest', False),
}
ORDER = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN']
out = {'generated_utc': datetime.datetime.now(datetime.UTC).isoformat(), 'images': []}

for key, (ref, in_scope) in refs.items():
    p = os.path.join(IMGDIR, key + '.json')
    rec = {'scan_key': key, 'image_ref': ref, 'in_project_release_scope': in_scope,
           'json_exists': os.path.exists(p), 'json_bytes': os.path.getsize(p) if os.path.exists(p) else 0}
    if not rec['json_exists'] or rec['json_bytes'] == 0:
        rec['scan_status'] = 'SCAN_FAILED_OR_INCOMPLETE'
        out['images'].append(rec); continue
    try:
        d = json.load(open(p, encoding='utf-8'))
    except Exception as e:
        rec['scan_status'] = 'JSON_PARSE_FAILED: %r' % e
        out['images'].append(rec); continue
    sev = collections.Counter(); pkgs = {}
    for res in (d.get('Results') or []):
        for v in (res.get('Vulnerabilities') or []):
            s = (v.get('Severity') or 'UNKNOWN').upper()
            sev[s] += 1
            pk = v.get('PkgName', '?')
            e = pkgs.setdefault(pk, {'pkg': pk, 'installed': v.get('InstalledVersion'), 'severity': s,
                                     'ids': [], 'fixed': v.get('FixedVersion') or None, 'title': (v.get('Title') or '')[:90]})
            if v.get('VulnerabilityID'): e['ids'].append(v['VulnerabilityID'])
            if ORDER.index(s) < ORDER.index(e['severity']): e['severity'] = s
    rec['scan_status'] = 'OK'
    rec['artifact_name'] = d.get('ArtifactName'); rec['artifact_type'] = d.get('ArtifactType')
    rec['metadata_os'] = ((d.get('Metadata') or {}).get('OS') or {}).get('Family')
    rec['metadata_repo_digests'] = (d.get('Metadata') or {}).get('RepoDigests')
    rec['metadata_image_id'] = (d.get('Metadata') or {}).get('ImageID')
    rec['metadata_repo_tags'] = (d.get('Metadata') or {}).get('RepoTags')
    rec['counts'] = {k: sev.get(k, 0) for k in ORDER}
    rec['counts_total'] = sum(sev.values())
    rec['result_targets'] = [{'target': r.get('Target'), 'class': r.get('Class'), 'type': r.get('Type'),
                              'vulns': len(r.get('Vulnerabilities') or [])} for r in (d.get('Results') or [])]
    rec['no_fix_available_count'] = sum(1 for e in pkgs.values() if not e['fixed'])
    rec['top_packages'] = sorted(pkgs.values(), key=lambda e: (ORDER.index(e['severity']), -len(e['ids'])))[:25]
    out['images'].append(rec)

json.dump(out, open(OUT, 'w', encoding='utf-8'), indent=2, ensure_ascii=False)
for i in out['images']:
    print('%-36s %-12s C=%-5s H=%-5s M=%-5s L=%-5s U=%-5s total=%-6s nofix=%s' % (
        i['image_ref'], i.get('scan_status'), i.get('counts', {}).get('CRITICAL','-'),
        i.get('counts', {}).get('HIGH','-'), i.get('counts', {}).get('MEDIUM','-'),
        i.get('counts', {}).get('LOW','-'), i.get('counts', {}).get('UNKNOWN','-'),
        i.get('counts_total','-'), i.get('no_fix_available_count','-')))
