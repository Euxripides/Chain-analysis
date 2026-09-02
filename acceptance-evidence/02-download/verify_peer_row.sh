#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
P=0x6808c73d7e9f1e1905c0618bb47bd6947ed4a6ae
echo "=== coverage for peer ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, row_count, raw_bytes, committed_at FROM dataset_coverage WHERE subject='$P';"
echo "=== raw object in minio ==="
docker exec deploy-minio-1 sh -c "mc alias set local http://127.0.0.1:9000 \"$MINIO_ROOT_USER\" \"$MINIO_ROOT_PASSWORD\" >/dev/null 2>&1; mc find local/raw --name '*.json.gz' | grep a41c1bc32c5020574c24db78cb823ea61c8bd9cc937 | head -3"
