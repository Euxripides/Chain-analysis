#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
V=/home/euripides/projects/blockchain-platform
echo "=== buildinfo of deployed api ==="
go version -m "$V/backend/bin/api" 2>&1 | head -20
echo "=== dep-upgrade evidence build scripts ==="
ls "$V/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z/" 2>/dev/null | head -30
echo "=== any build script in that dir ==="
grep -l "api.build" "$V/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z/"* 2>/dev/null | head
echo "=== show build stage2 if present ==="
for f in "$V"/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z/*.sh "$V"/scripts/s7/*dep*stage2*; do
  [ -f "$f" ] && { echo "--- $f"; grep -n "go build" "$f" | head -10; }
done
echo "=== done ==="
