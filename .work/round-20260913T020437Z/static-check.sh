#!/usr/bin/env bash
set -uo pipefail
export PATH=/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
echo "=== listeners 3000/3001/3002 ==="
ss -ltnp 2>/dev/null | grep -E ':300[0-2]' || true
echo "=== processes ==="
ps -eo pid,ppid,cmd | grep -E "vite|serve|http-server|preview" | grep -v grep | head -20
echo "=== user-console dist contents ==="
ls -la "$V/frontend/apps/user-console/dist" | head
echo "=== done ==="
