#!/usr/bin/env bash
set +e
echo "=== local env files ==="
ls -la /e/Code/远程虚拟机/harness/blockchain-platform/backend/.env /e/Code/远程虚拟机/harness/blockchain-platform/deploy/.env 2>/dev/null || echo "local env missing"
