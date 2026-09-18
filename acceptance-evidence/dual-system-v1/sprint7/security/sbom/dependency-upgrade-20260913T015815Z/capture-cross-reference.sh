#!/usr/bin/env bash
# READ-ONLY cross-reference capture for the 2026-09-13 dependency-upgrade evidence root.
# Reads only hashes/versions/listings (never a secret file, never a creds file).
# ADDITIVE: writes one new file into the scoped evidence dir, refuses to overwrite it.
set -u
PROJ=/home/euripides/projects/blockchain-platform
REF="$PROJ/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z"
OUT="$PROJ/acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z/cross-reference-dependency-remediation.txt"

if [ -e "$OUT" ]; then
  echo "FATAL REFUSING TO OVERWRITE EXISTING PATH: $OUT"
  exit 3
fi

{
  echo "### CROSS-REFERENCE to the dependency-upgrade remediation evidence root  utc=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo "### reference root = $REF"
  echo "### This file is a read-only capture (names/sizes/hashes/versions only)."
  echo
  if [ ! -d "$REF" ]; then
    echo "REFERENCE ROOT NOT PRESENT ON THIS HOST."
    echo "NOTE: as of this run the reference root exists ONLY on the acceptance VM;"
    echo "      it is not present in the Windows git worktree of this repository."
    exit 0
  fi
  echo "--- top-level listing (names, sizes, mtimes; no content of unknown files) ---"
  ls -la "$REF"
  echo
  echo "--- subdirectory names ---"
  find "$REF" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort
  echo
  echo "--- key hash/version files (small text files only) ---"
  for f in 00-go-version.txt 01-baseline-sha.txt 03-after-sha.txt 06-go-directive.txt 08-go-mod-verify.txt 10-current-sha.txt 11-go-directive-verify.txt; do
    if [ -f "$REF/$f" ]; then
      echo "== $f (sha256 $(sha256sum "$REF/$f" | cut -d' ' -f1)) =="
      cat "$REF/$f"
      echo
    else
      echo "== $f : not present =="
      echo
    fi
  done
  echo "--- live check: does the recorded base go.mod/go.sum digest still hold? ---"
  sha256sum "$PROJ/backend/go.mod" "$PROJ/backend/go.sum"
  echo "(compare with 01-baseline-sha.txt / 10-current-sha.txt above)"
} > "$OUT" 2>&1
echo "XREF_WRITTEN=$OUT"
wc -l "$OUT"
