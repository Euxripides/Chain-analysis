#!/usr/bin/env bash
set +e
echo "=== VM frontend dist files ==="
ls /home/euripides/projects/blockchain-platform/frontend/dist/assets/ 2>/dev/null | head
echo "=== VM secret scan ==="
grep -rEl 'ANKR_API_KEY|POSTGRES_PASSWORD|CLICKHOUSE_PASSWORD|MINIO_ROOT_PASSWORD|BEGIN (RSA|EC|PRIVATE) KEY' /home/euripides/projects/blockchain-platform/frontend/dist/ 2>/dev/null | head -5 || echo "no hits"
echo "=== VM source map check ==="
find /home/euripides/projects/blockchain-platform/frontend/dist -name '*.map' | head -3
echo "=== VM frontend index.html env embed check ==="
grep -o 'window.[A-Z_]*' /home/euripides/projects/blockchain-platform/frontend/dist/index.html | sort -u
