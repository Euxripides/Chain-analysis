#!/usr/bin/env bash
set -uo pipefail
V=/home/euripides/projects/blockchain-platform
ROUND="$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z"
REL="$V/acceptance-evidence/dual-system-v1/sprint7/release/uc-download-import-20260913T021911Z"
OUT=/tmp/r9-evidence-text.tgz
rm -f "$OUT"
LIST=/tmp/r9-evidence-list.txt
find "$ROUND" "$REL" -type f \( -name '*.txt' -o -name '*.json' -o -name '*.md' -o -name '*.log' -o -name '*.rc' -o -name '*.sha256' -o -name '*.mjs' -o -name '*.sh' -o -name '*.ts' -o -name '*.tsx' -o -name '*.py' \) -not -path '*/test-results/*' -not -path '*/dist/*' -not -name '*.build' | sort > "$LIST"
wc -l "$LIST"
tar czf "$OUT" -T "$LIST" 2>/dev/null
ls -la "$OUT"
echo "tar_sha256=$(sha256sum "$OUT" | cut -d' ' -f1)"
echo "=== manifest of what is inside (relative) ==="
sed "s#^$V/##" "$LIST" | tee /tmp/r9-evidence-list-rel.txt | head -60
echo "TOTAL_FILES=$(wc -l < "$LIST")"
