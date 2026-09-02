#!/usr/bin/env bash
set +e
echo "=== binary build times ==="
ls -la /home/euripides/projects/blockchain-platform/backend/bin/api /home/euripides/projects/blockchain-platform/backend/bin/worker
echo "=== process start times ==="
ps -o pid,lstart,cmd -p 1788976 1788978
echo "=== source contains fix ==="
grep -c 'rangeCoverageVerdict' /home/euripides/projects/blockchain-platform/backend/internal/download/runner.go
grep -c 'refusing silent 0-row completion' /home/euripides/projects/blockchain-platform/backend/internal/download/runner.go
echo "=== binary contains fix (strings) ==="
strings /home/euripides/projects/blockchain-platform/backend/bin/worker | grep -c 'refusing silent 0-row completion'
strings /home/euripides/projects/blockchain-platform/backend/bin/api | grep -c 'refusing silent 0-row completion'
echo "=== frontend api-control route present in source ==="
grep -rl 'api-control' /home/euripides/projects/blockchain-platform/frontend/src/ | head -3
echo "=== vite process ==="
cat /tmp/blockchain-platform-vite.pid
pgrep -af 'vite'
echo "=== sync marker ==="
ls -la /home/euripides/projects/blockchain-platform/backend/internal/download/range_guard_test.go
