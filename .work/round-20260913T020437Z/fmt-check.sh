#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
BK=$(ls -d "$V"/.work/r9-src-backup-* | tail -1)
echo "=== pre-edit backup line counts (must match the Windows originals) ==="
wc -l "$BK/frontend/apps/user-console/src/pages/DownloadNewPage.tsx" "$BK/backend/internal/api/sprint5_downloads.go" "$BK/backend/internal/api/handlers.go" "$BK/backend/internal/api/userv2.go"
echo "=== gofmt -w new file ==="
gofmt -w "$V/backend/internal/api/sprint5_downloads_import.go"
gofmt -l "$V/backend/internal/api/sprint5_downloads_import.go" && echo "gofmt clean"
sha256sum "$V/backend/internal/api/sprint5_downloads_import.go"
echo "=== go build after gofmt ==="
cd "$V/backend" && go build ./... 2>&1 | head -20; echo "build_rc=${PIPESTATUS[0]}"
echo "=== frontend typecheck ==="
cd "$V/frontend" && npx tsc -b 2>&1 | head -40; echo "tsc_rc=${PIPESTATUS[0]}"
echo "=== done ==="
