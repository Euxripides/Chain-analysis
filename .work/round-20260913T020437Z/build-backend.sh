#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BK="$V/.work/r9-src-backup-$TS"
mkdir -p "$BK"
FILES="backend/internal/api/sprint5_downloads.go backend/internal/api/handlers.go backend/internal/api/userv2.go frontend/apps/user-console/src/pages/DownloadNewPage.tsx"
echo "=== backup pre-edit originals (additive) ==="
for f in $FILES; do mkdir -p "$BK/$(dirname "$f")"; cp -p "$V/$f" "$BK/$f"; done
echo "backup=$BK"
echo "=== verify uploaded sha matches ==="
for f in $FILES backend/internal/api/sprint5_downloads_import.go; do
  printf '%s  %s\n' "$(sha256sum "/tmp/r9-src/$f" | cut -d' ' -f1)" "$f"
done
echo "=== diff summary of uploaded vs live (line counts only) ==="
for f in $FILES; do
  printf '%s  +%s/-%s\n' "$f" "$(diff "/tmp/r9-src/$f" "$V/$f" | grep -c '^>')" "$(diff "/tmp/r9-src/$f" "$V/$f" | grep -c '^<')"
done
echo "=== install ==="
for f in $FILES; do cp "/tmp/r9-src/$f" "$V/$f"; done
cp /tmp/r9-src/backend/internal/api/sprint5_downloads_import.go "$V/backend/internal/api/sprint5_downloads_import.go"
echo "=== gofmt check on new/changed files ==="
gofmt -l "$V/backend/internal/api/sprint5_downloads_import.go" "$V/backend/internal/api/sprint5_downloads.go" "$V/backend/internal/api/handlers.go" "$V/backend/internal/api/userv2.go"
echo "=== go build ./... ==="
cd "$V/backend" && go build ./... 2>&1 | head -40; echo "build_rc=${PIPESTATUS[0]}"
echo "=== go vet ./internal/api/... ==="
cd "$V/backend" && go vet ./internal/api/... 2>&1 | head -40; echo "vet_rc=${PIPESTATUS[0]}"
echo "=== go test ./internal/api/... (compile+run) ==="
cd "$V/backend" && go test ./internal/api/... 2>&1 | tail -25; echo "test_rc=${PIPESTATUS[0]}"
echo "=== done ==="
