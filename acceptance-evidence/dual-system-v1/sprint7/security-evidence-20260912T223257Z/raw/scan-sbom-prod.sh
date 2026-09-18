#!/bin/bash
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"; R="/home/euripides/projects/blockchain-platform"; S="$EV/sbom"
exec > "$S/prod-sbom-run.log" 2>&1
set -x
STAGE=/tmp/prod-sbom-stage
rm -rf "$STAGE"; mkdir -p "$STAGE"
# hardlink ONLY the current production artifacts + module manifests (no backups, no rollback dirs)
ln "$R/backend/bin/api"     "$STAGE/api"
ln "$R/backend/bin/worker"  "$STAGE/worker"
ln "$R/backend/bin/migrate" "$STAGE/migrate"
ln "$R/backend/go.mod"      "$STAGE/go.mod"
ln "$R/backend/go.sum"      "$STAGE/go.sum"
echo "=== staged (hardlinks; same inode as originals) ==="
ls -la "$STAGE"
sha256sum "$STAGE/api" "$STAGE/worker" "$STAGE/migrate" "$STAGE/go.mod" "$STAGE/go.sum"

echo
echo "=== syft on the production-only staging dir ==="
docker run --rm -v "$STAGE:/w:ro" -v "$S:/out" anchore/syft:latest dir:/w \
  -o cyclonedx-json=/out/backend-prod.cdx.json
echo "PROD_CDX_EXIT=$?"
docker run --rm -v "$STAGE:/w:ro" -v "$S:/out" anchore/syft:latest dir:/w \
  -o spdx-json=/out/backend-prod.spdx.json
echo "PROD_SPDX_EXIT=$?"

echo
echo "=== component counts ==="
python3 - <<'PYEOF'
import json, collections, os
S=os.environ['S'] if 'S' in os.environ else None
PYEOF
python3 -c "
import json, collections
base='$S'
d=json.load(open(base+'/backend-prod.cdx.json'))
comps=d.get('components') or []
print('PROD cdx components:', len(comps))
print('by type:', dict(collections.Counter(c.get('type') for c in comps)))
paths=collections.Counter()
for c in comps:
    for p in (c.get('properties') or []):
        if p.get('name')=='syft:location:0:path': paths[p.get('value')]+=1
print('by source path:')
for k,v in paths.most_common(10): print('   %-28s %d' % (k, v))
names=sorted(set(c.get('name') for c in comps))
print('distinct package names:', len(names))
for n in names:
    if any(k in n for k in ('excelize','chi','pgx','crypto')): print('   KEY:', n)
"
echo "PROD_SBOM_DONE=1"
