#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== minio buckets ==="
docker exec deploy-minio-1 sh -c "mc alias set local http://127.0.0.1:9000 \"$MINIO_ROOT_USER\" \"$MINIO_ROOT_PASSWORD\" >/dev/null 2>&1; mc ls local"
echo "=== minio env bucket names ==="
grep -iE 'MINIO_.*BUCKET|BUCKET' /home/euripides/projects/blockchain-platform/deploy/.env | cut -d= -f1
