#!/usr/bin/env bash
set +e
echo "=== VM frontend bundle age ==="
ls -la /home/euripides/projects/blockchain-platform/frontend/dist/assets/
echo "=== api-control route in VM bundle? ==="
grep -c 'api-control' /home/euripides/projects/blockchain-platform/frontend/dist/assets/*.js 2>/dev/null || echo 0
echo "=== data-explorer route in VM bundle? ==="
grep -c 'data-explorer' /home/euripides/projects/blockchain-platform/frontend/dist/assets/*.js 2>/dev/null || echo 0
echo "=== local dist bundles ==="
ls "E:\Code\远程虚拟机\harness\blockchain-platform\frontend\dist\assets" 2>/dev/null | head
