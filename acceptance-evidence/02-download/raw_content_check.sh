#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== raw part content check ==="
docker exec deploy-minio-1 mc cat local/blockchain-raw/raw/ankr/bsc/token_transfers/2026/09/a41c1bc32c5020574c24db78cb823ea61c8bd9cc937fab42e4539b2e3246332d/part-000001.json.gz 2>/dev/null | gunzip 2>/dev/null | head -c 900
echo ""
echo "=== manifest content ==="
docker exec deploy-minio-1 mc cat local/blockchain-raw/raw/ankr/bsc/token_transfers/2026/09/a41c1bc32c5020574c24db78cb823ea61c8bd9cc937fab42e4539b2e3246332d/manifest.json.gz 2>/dev/null | gunzip 2>/dev/null | head -c 900
