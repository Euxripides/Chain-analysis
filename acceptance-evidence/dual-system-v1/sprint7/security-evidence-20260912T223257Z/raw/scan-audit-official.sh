#!/bin/bash
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"; D="$EV/deps"
exec > "$D/official-registry-audit.log" 2>&1
set -x
export NVM_DIR="$HOME/.nvm"; [ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
cd /home/euripides/projects/blockchain-platform/frontend

REG="https://registry.npmjs.org"
echo "=== PROVENANCE ==="
echo "host=$(hostname)"
echo "node=$(node -v)  pnpm=$(pnpm -v)  npm=$(npm -v)"
echo "registry_override=$REG"
echo "cwd=$(pwd)"
echo "lockfile_sha256_before=$(sha256sum pnpm-lock.yaml | cut -d' ' -f1)"
echo "package_json_sha256_before=$(sha256sum package.json | cut -d' ' -f1)"
echo "cmd: CI=true pnpm audit --json --registry=$REG"
echo "start_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo

echo "=== [A] pnpm audit --json --registry (official) ==="
CI=true pnpm audit --json --registry="$REG" > "$D/official-registry-pnpm-audit.json" 2> "$D/official-registry-pnpm-audit.stderr.txt"
EC=$?
echo "OFFICIAL_AUDIT_JSON_EXIT=$EC"
echo "end_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "json_bytes=$(wc -c < "$D/official-registry-pnpm-audit.json")"
echo "--- stderr (raw) ---"; cat "$D/official-registry-pnpm-audit.stderr.txt"

echo
echo "=== [B] pnpm audit (table) --registry (official) ==="
CI=true pnpm audit --registry="$REG" > "$D/official-registry-pnpm-audit.txt" 2>&1
echo "OFFICIAL_AUDIT_TABLE_EXIT=$?"

echo
echo "=== [C] pnpm audit --prod --json --registry (official, prod only) ==="
CI=true pnpm audit --prod --json --registry="$REG" > "$D/official-registry-pnpm-audit-prod.json" 2> "$D/official-registry-pnpm-audit-prod.stderr.txt"
echo "OFFICIAL_AUDIT_PROD_EXIT=$?"

echo
echo "=== INTEGRITY AFTER ==="
echo "lockfile_sha256_after=$(sha256sum pnpm-lock.yaml | cut -d' ' -f1)"
echo "package_json_sha256_after=$(sha256sum package.json | cut -d' ' -f1)"
echo "node_modules_untouched_check=see hashes above (must match _before)"
echo "OFFICIAL_AUDIT_DONE=1"
