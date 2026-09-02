#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== pre-existing paused job detail ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT job_no, status, from_block, to_block, datasets, resolved_at FROM download_jobs WHERE id='ff9a5743-8d0c-42f4-a5ff-1bf7ce10d187';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, status, from_block, to_block, downloaded_rows, page_no FROM download_shards WHERE job_id='ff9a5743-8d0c-42f4-a5ff-1bf7ce10d187';"
