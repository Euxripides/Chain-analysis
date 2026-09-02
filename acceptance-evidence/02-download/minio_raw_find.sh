#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
docker exec deploy-minio-1 sh -c "mc alias set local http://127.0.0.1:9000 \"$MINIO_ROOT_USER\" \"$MINIO_ROOT_PASSWORD\" >/dev/null 2>&1; mc find local/blockchain-raw --name 'manifest.json.gz' | tail -5"
echo "=== search a41c1bc3 prefix ==="
docker exec deploy-minio-1 sh -c "mc find local/blockchain-raw --name '*' | grep a41c1bc32c5020574c24db78cb823ea61c8bd9cc937 | head -5"
