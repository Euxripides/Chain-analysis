#!/usr/bin/env bash
# READ-ONLY additive-only proof: confirm that every PRE-EXISTING file of the sbom evidence
# directory is unchanged, i.e. that this scope only added files.
set -u
P=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom
NEW="$P/dependency-upgrade-20260913T015815Z"
echo "=== utc=$(date -u '+%Y-%m-%dT%H:%M:%SZ') host=$(hostname) ==="
echo "=== the 12 pre-existing artifacts recorded in sbom-artifact-hashes.txt, re-hashed now ==="
cd "$P" || exit 1
sha256sum backend.cdx.json backend.spdx.json frontend.cdx.json frontend.spdx.json \
          syft-version.txt syft-image-info.txt commands.txt SUMMARY.md validation.txt \
          backend-scan-stability.txt SCAN-TARGET-CORRECTION.md sbom_report.py
echo
echo "=== pre-existing files (outside the new scope dir) modified after 2026-09-13 09:00:00 ==="
find "$P" -type f -not -path "$NEW/*" -newermt "2026-09-13 09:00:00" -print
echo "(nothing listed above => no pre-existing file was modified by this scope)"
echo
echo "=== newest mtimes among PRE-EXISTING files ==="
find "$P" -type f -not -path "$NEW/*" -printf '%T+ %p\n' | sort | tail -4
echo
echo "=== counts ==="
echo "pre-existing files in the parent sbom dir: $(find "$P" -type f -not -path "$NEW/*" | wc -l)"
echo "files in the new scope dir:                $(find "$NEW" -type f | wc -l)"
echo "=== every file of the new scope dir (relative) ==="
cd "$NEW" && find . -type f | sed 's|^\./||' | sort
