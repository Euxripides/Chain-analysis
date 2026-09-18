#!/usr/bin/env bash
# round-probe: read-only reconnaissance of VM frontend test/tooling state.
# frozen; never hot-edited.
set -uo pipefail
export PATH=/usr/local/go/bin:/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
P=/home/euripides/projects/blockchain-platform
cd "$P/frontend" || exit 90
echo "=== pwd ==="; pwd
echo "=== node/pnpm ==="; node -v; pnpm -v
echo "=== xlsx version ==="
node -e "const p=require('$P/frontend/node_modules/xlsx/package.json');console.log(p.name,p.version)" 2>&1
echo "=== playwright configs ==="
ls -la playwright*.ts playwright*.mjs playwright*.js 2>&1
echo "=== tests dir ==="
ls -la tests 2>&1 | head -30
echo "=== e2e dir ==="
ls -la e2e 2>&1 | head -40
echo "=== frontend scripts in package.json ==="
node -e "const p=require('$P/frontend/package.json');console.log(JSON.stringify(p.scripts,null,1));console.log('DEPS',JSON.stringify(p.devDependencies,null,1))" 2>&1
echo "=== playwright browsers ==="
ls ~/.cache/ms-playwright 2>&1 | head
echo "=== nginx-live-qa ==="
ls -la nginx-live-qa.mjs 2>&1
echo "=== who listens 8448/8088/8080 ==="
ss -ltnp 2>/dev/null | grep -E '8448|8088|8080|4173|5173' || true
