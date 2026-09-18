#!/bin/bash
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"; R="/home/euripides/projects/blockchain-platform"; I="$EV/iac"; N="$I/normalized"
mkdir -p "$N"
exec > "$I/iac-normalized.log" 2>&1
set -x

echo "=== [N0] byte-identical copies into an evidence-local dir (repo NOT modified) ==="
cp -p "$R/deploy/compose.yaml" "$N/docker-compose.yml"
cp -p "$R/deploy/compose.yaml" "$N/docker-compose.yaml.orig"
sha256sum "$R/deploy/compose.yaml" "$N/docker-compose.yml"
cmp "$R/deploy/compose.yaml" "$N/docker-compose.yml" && echo "CMP_IDENTICAL=yes"

echo
echo "=== [N1] trivy config on normalized compose ==="
docker run --rm -v "$N:/w:ro" -v "$I:/out" aquasec/trivy:latest config /w \
  --checks-bundle-repository docker.m.daocloud.io/aquasec/trivy-checks:2 \
  --timeout 10m --format json --output /out/trivy-config-compose-normalized.json 2>&1 | tail -5
echo "TRIVY_NORM_JSON_EXIT=$?"
docker run --rm -v "$N:/w:ro" aquasec/trivy:latest config /w \
  --checks-bundle-repository docker.m.daocloud.io/aquasec/trivy-checks:2 --timeout 10m 2>&1 | tail -40

echo
echo "=== [N2] checkov on normalized compose ==="
docker run --rm -v "$N:/w:ro" bridgecrew/checkov:latest -d /w -o json --compact > "$I/checkov-compose-normalized.json" 2>"$I/checkov-compose-normalized.stderr.txt"
echo "CHECKOV_NORM_EXIT=$?"
cat "$I/checkov-compose-normalized.json" | head -c 600; echo
echo "--- stderr ---"; tail -5 "$I/checkov-compose-normalized.stderr.txt"

echo
echo "=== [N3] checkov explicit framework list ==="
docker run --rm bridgecrew/checkov:latest --list-frameworks 2>&1 | head -20 || true

echo
echo "=== [N4] checkov secrets: inspect CKV_SECRET_6 source lines (masked) ==="
python3 - <<'PYEOF'
import json, re, os
p = os.environ['I'] + '/checkov-secrets.json'
d = json.load(open(p))
if isinstance(d, dict): d = [d]
for blk in d:
    for f in ((blk.get('results') or {}).get('failed_checks') or []):
        fp = f.get('file_path'); ln = (f.get('file_line_range') or [0])[0]
        full = os.path.join(os.environ['R'], fp.lstrip('/'))
        try:
            line = open(full, encoding='utf-8', errors='replace').read().splitlines()[ln-1]
            m = re.search(r'[A-Za-z0-9+/]{16,}={0,2}', line)
            masked = (m.group(0)[:4] + '*'*(max(0,len(m.group(0))-8)) + m.group(0)[-4:]) if m else '-'
            key = re.match(r'\s*"([^"]+)"\s*:', line)
            print('%-22s %-60s line=%-5s json_key=%-28s masked_b64=%s' % (f.get('check_id'), fp, ln, key.group(1) if key else '-', masked))
        except Exception as e:
            print(f.get('check_id'), fp, ln, 'READ_ERR', e)
PYEOF
export I R
echo
echo "=== [N5] full KICS findings table ==="
python3 - <<'PYEOF'
import json, os
d = json.load(open(os.environ['I'] + '/results.json'))
print('kics_version=%s files_scanned=%s lines_scanned=%s files_parsed=%s queries_total=%s total=%s' % (
  d.get('kics_version'), d.get('files_scanned'), d.get('lines_scanned'), d.get('files_parsed'), d.get('queries_total'), d.get('total_counter')))
for q in (d.get('queries') or []):
    print('[%s] %s (%s)' % (q.get('severity'), q.get('query_name'), q.get('query_id')))
    for f in (q.get('files') or []):
        lines = [l.get('line') for l in (f.get('line') or [])]
        print('    %s  lines=%s' % (f.get('file_name'), lines))
PYEOF
echo "IAC_NORM_DONE=1"
