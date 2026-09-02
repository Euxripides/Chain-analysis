#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== recent-range shard ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT raw_object, page_no, page_size, downloaded_rows, page_state FROM download_shards WHERE job_id='a87145a1-950c-419c-aeea-14ef13787061';"
