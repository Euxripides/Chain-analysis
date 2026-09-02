#!/usr/bin/env bash
set +e
echo "=== worker log last 15 ==="
tail -n 15 /tmp/worker.log
echo "=== reaper code search ==="
grep -rn 'reaper|Reaper|lease' /home/euripides/projects/blockchain-platform/backend/cmd/worker/main.go | head -10
grep -rn 'reaper|Reaper' /home/euripides/projects/blockchain-platform/backend/internal/download/*.go | head -10
