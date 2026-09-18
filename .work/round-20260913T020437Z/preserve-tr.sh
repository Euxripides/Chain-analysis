#!/usr/bin/env bash
# r9-preserve-testresults.sh — 只读复制当前 frontend/test-results 到唯一证据目录，保留源目录。
set -uo pipefail
V=/home/euripides/projects/blockchain-platform
EV2="$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-v2-20260913T022357Z"
ARCH="$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/playwright-testresults-archive"
TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$EV2/test-results-copied-$TS" "$ARCH"
echo "=== source listing ==="
ls -la "$V/frontend/test-results" | head
echo "=== copy to EVID2 (read-only copy; source kept) ==="
cp -a "$V/frontend/test-results/." "$EV2/test-results-copied-$TS/"
echo "=== copy to stable archive (so a later Playwright wipe cannot destroy it) ==="
cp -a "$V/frontend/test-results" "$ARCH/test-results-$TS"
find "$ARCH" -type f | sort
echo "=== EVID2 copied file count ==="
find "$EV2/test-results-copied-$TS" -type f | wc -l
find "$EV2/test-results-copied-$TS" -type f | sort | head -30
echo "=== sha256 of copied artefacts ==="
find "$EV2/test-results-copied-$TS" -type f -exec sha256sum {} \; | sort -k2 > "$EV2/test-results-copied-$TS.sha256.txt"
wc -l "$EV2/test-results-copied-$TS.sha256.txt"
echo "=== HONEST RECORD: v1 playwright artefacts ==="
{
  echo "checked_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "v1_evidence_dir=$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-20260913T022255Z"
  echo "v1_dir_contents_below (only these survived):"
  find "$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-20260913T022255Z" -type f | sort
  echo "v1_playwright_default_outputdir_had: trace.zip + test-failed-*.png + error-context.md for 6 failed tests"
  echo "current frontend/test-results contains ONLY v2 run dirs => v1 trace/screenshot/error-context were wiped by Playwright's default outputDir cleanup at the start of the v2 run."
  echo "NOT RECOVERED, NOT RECONSTRUCTED. The v1 playwright.log (runner-captured) and the v1 uc-desktop/r9-05-evidence.json did survive and are listed above."
} | tee "$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/EVIDENCE-LOSS-RECORD.txt"
echo "=== done ==="
