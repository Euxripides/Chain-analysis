#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
cp /tmp/r9-src/frontend/apps/user-console/src/pages/DownloadNewPage.tsx "$V/frontend/apps/user-console/src/pages/DownloadNewPage.tsx"
echo "=== grep leftover groupByChain refs (must be 0) ==="
grep -c "groupByChain()" "$V/frontend/apps/user-console/src/pages/DownloadNewPage.tsx" || true
echo "=== tsc ==="
cd "$V/frontend" && npx tsc -b 2>&1 | head -40; echo "tsc_rc=${PIPESTATUS[0]}"
echo "=== pull gofmt'd backend file back to a staging path for local sync ==="
sha256sum "$V/backend/internal/api/sprint5_downloads_import.go"
cp "$V/backend/internal/api/sprint5_downloads_import.go" /tmp/r9-import-gofmt.go
echo "=== done ==="
