#!/usr/bin/env bash
set -uo pipefail
V=/home/euripides/projects/blockchain-platform
EV1="$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-20260913T022255Z"
EV2="$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-v2-20260913T022357Z"
echo "=== default playwright outputDir (shared, wiped per run) ==="
ls -la "$V/frontend/test-results" 2>&1 | head -20
echo "=== count of test-results dirs now ==="
ls -1 "$V/frontend/test-results" 2>/dev/null | wc -l
echo "=== do v1 outputs still exist under EVID1? ==="
find "$EV1" -maxdepth 2 -type f | sort
echo "=== EVID2 files ==="
find "$EV2" -maxdepth 2 -type f | sort
echo "=== any r9 v1 trace/screenshot anywhere? ==="
find "$V" -name "*r9-uc-download-import-*" -not -path "*/node_modules/*" 2>/dev/null | head -20
echo "=== done ==="
