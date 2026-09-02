#!/usr/bin/env bash
set +e
echo "=== VM binary info ==="
ls -la /home/euripides/projects/blockchain-platform/backend/bin/ 2>/dev/null
ls -la /home/euripides/projects/blockchain-platform/backend/ | head
echo "=== VM git state ==="
cd /home/euripides/projects/blockchain-platform && git log -1 --oneline 2>/dev/null && git status --short 2>/dev/null | head -5
echo "=== VM backend binary strings version ==="
strings /home/euripides/projects/blockchain-platform/backend/bin/api 2>/dev/null | grep -iE 'parquet|jsonl' | head -5
