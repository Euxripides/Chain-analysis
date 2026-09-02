#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== PEER3 shard pagination ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT page_no, page_size, page_token, page_state, downloaded_rows FROM download_shards WHERE job_id='dbdf5970-a1d7-40a4-9072-85c78229db3c';"
echo "=== LABUBU shard pagination ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT page_no, page_size, downloaded_rows FROM download_shards WHERE job_id='4e4d3012-86cf-4bdb-abdb-7e529108ad54';"
