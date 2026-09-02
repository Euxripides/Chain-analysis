#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== raw object for PEER3 job dbdf5970 ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT raw_object, downloaded_rows, status, error FROM download_shards WHERE job_id='dbdf5970-a1d7-40a4-9072-85c78229db3c';"
echo "=== minio objects for that hash ==="
docker exec deploy-minio-1 mc find local/blockchain-raw --name '*' 2>/dev/null | grep -i 'dbdf|114147' | head -5
echo "=== all 2026/09 token_transfers raw dirs modified in last 30min ==="
docker exec deploy-minio-1 mc ls -r local/blockchain-raw/raw/ankr/bsc/token_transfers/2026/09 2>/dev/null | tail -8
