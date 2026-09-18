#!/bin/bash
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"; R="/home/euripides/projects/blockchain-platform"
S="$EV/sbom"; mkdir -p "$S"
exec > "$S/sbom-run.log" 2>&1
set -x
SYFT="anchore/syft:latest"
echo "=== SYFT PROVENANCE ==="
date -u +%Y-%m-%dT%H:%M:%SZ
docker image inspect $SYFT --format '{{json .RepoDigests}}|{{.Id}}|{{.Created}}'
docker run --rm $SYFT version

echo
echo "=== [1] frontend source SBOM (cyclonedx-json) ==="
docker run --rm -v "$R/frontend:/w:ro" -v "$S:/out" $SYFT dir:/w \
  --exclude './node_modules/**' \
  -o cyclonedx-json=/out/frontend.cdx.json
echo "FRONTEND_CDX_EXIT=$?"
docker run --rm -v "$R/frontend:/w:ro" -v "$S:/out" $SYFT dir:/w \
  --exclude './node_modules/**' \
  -o spdx-json=/out/frontend.spdx.json
echo "FRONTEND_SPDX_EXIT=$?"

echo
echo "=== [2] backend source SBOM (cyclonedx-json) ==="
docker run --rm -v "$R/backend:/w:ro" -v "$S:/out" $SYFT dir:/w \
  -o cyclonedx-json=/out/backend.cdx.json
echo "BACKEND_CDX_EXIT=$?"
docker run --rm -v "$R/backend:/w:ro" -v "$S:/out" $SYFT dir:/w \
  -o spdx-json=/out/backend.spdx.json
echo "BACKEND_SPDX_EXIT=$?"

echo
echo "=== [3] deploy config SBOM ==="
docker run --rm -v "$R/deploy:/w:ro" -v "$S:/out" $SYFT dir:/w -o cyclonedx-json=/out/deploy.cdx.json
echo "DEPLOY_CDX_EXIT=$?"

echo
echo "=== ARTIFACT BYTE COUNTS ==="
ls -la "$S"
echo "SBOM_DONE=1"
