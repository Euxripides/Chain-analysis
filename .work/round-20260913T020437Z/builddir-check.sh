#!/usr/bin/env bash
set -uo pipefail
V=/home/euripides/projects/blockchain-platform
D="$V/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z"
echo "=== build dir ==="
ls -la "$D/build-20260913T015005Z" | head -20
echo "=== build script ==="
for f in "$D/build-20260913T015005Z"/*; do
  case "$f" in *.sh) echo "--- $f"; cat "$f";; esac
done
echo "=== deploy dir ==="
ls -la "$D/deploy-20260913T015815Z" | head -20
echo "=== done ==="
