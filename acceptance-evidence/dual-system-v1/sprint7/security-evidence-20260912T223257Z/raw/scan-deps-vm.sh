#!/bin/bash
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"
D="$EV/deps"
mkdir -p "$D"
exec > "$D/vm-deps-run.log" 2>&1
set -x

export NVM_DIR="$HOME/.nvm"; [ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
export PATH="/usr/local/go/bin:$PATH"
GO=/usr/local/go/bin/go
GV=/tmp/gtools/govulncheck

echo "=== HOST / TOOL VERSIONS ==="
echo "host: $(hostname)  date_utc: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "node: $(node -v)  pnpm: $(pnpm -v)  npm: $(npm -v)"
echo "npm registry: $(npm config get registry)"
$GO version
$GV -version 2>&1 || echo "GOVULNCHECK_MISSING"

echo
echo "=== PRE INTEGRITY ==="
sha256sum /home/euripides/projects/blockchain-platform/backend/go.mod /home/euripides/projects/blockchain-platform/backend/go.sum /home/euripides/projects/blockchain-platform/frontend/pnpm-lock.yaml /home/euripides/projects/blockchain-platform/frontend/package.json

echo
echo "=== [1] pnpm audit --json ==="
cd /home/euripides/projects/blockchain-platform/frontend
CI=true pnpm audit --json > "$D/pnpm-audit.json" 2> "$D/pnpm-audit.stderr.txt"
echo "PNPM_AUDIT_JSON_EXIT=$?"
CI=true pnpm audit > "$D/pnpm-audit.txt" 2>&1
echo "PNPM_AUDIT_TABLE_EXIT=$?"

echo
echo "=== [2] go list -m all ==="
cd /home/euripides/projects/blockchain-platform/backend
GOFLAGS=-mod=readonly $GO list -m all > "$D/go-list-m-all.txt" 2> "$D/go-list-m-all.stderr.txt"
echo "GO_LIST_EXIT=$?  lines=$(wc -l < "$D/go-list-m-all.txt")"
GOFLAGS=-mod=readonly $GO list -m -json all > "$D/go-list-m-all.json" 2>/dev/null
echo "module_count_json=$(grep -c '"Path"' "$D/go-list-m-all.json")"

echo
echo "=== [3] govulncheck ==="
$GV -version > "$D/govulncheck-version.txt" 2>&1
$GV ./... > "$D/govulncheck.txt" 2> "$D/govulncheck.stderr.txt"
echo "GOVULNCHECK_EXIT=$?"
$GV -show verbose ./... > "$D/govulncheck-verbose.txt" 2>&1
echo "GOVULNCHECK_VERBOSE_EXIT=$?"
$GV -format json ./... > "$D/govulncheck.json" 2> "$D/govulncheck-json.stderr.txt"
echo "GOVULNCHECK_JSON_EXIT=$?"

echo
echo "=== POST INTEGRITY ==="
sha256sum /home/euripides/projects/blockchain-platform/backend/go.mod /home/euripides/projects/blockchain-platform/backend/go.sum /home/euripides/projects/blockchain-platform/frontend/pnpm-lock.yaml /home/euripides/projects/blockchain-platform/frontend/package.json
echo "VM_DEPS_DONE=1"
