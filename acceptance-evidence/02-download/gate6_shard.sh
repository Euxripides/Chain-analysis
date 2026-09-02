#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== shard details ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, status, downloaded_rows, page_no, page_token, page_state FROM download_shards WHERE job_id='b06faabe-802b-4c39-a94a-52a5bbc86b09';"
echo "=== manifest parts ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT raw_object FROM download_shards WHERE job_id='b06faabe-802b-4c39-a94a-52a5bbc86b09';"
echo "=== decode page token ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -A -c "SELECT page_token FROM download_shards WHERE job_id='b06faabe-802b-4c39-a94a-52a5bbc86b09';" | base64 -d 2>/dev/null | head -c 300
