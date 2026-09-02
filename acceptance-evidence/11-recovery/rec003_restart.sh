#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== shard state after worker exit ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, downloaded_rows, page_no, lease_owner, leased_until, retries, error FROM download_shards WHERE job_id='61c6e511-b9c2-4452-905d-2dcb5f85cea9';"
echo "=== job state ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status FROM download_jobs WHERE id='61c6e511-b9c2-4452-905d-2dcb5f85cea9';"
echo "=== restart worker with same env ==="
cd /home/euripides/projects/blockchain-platform/backend
set -a; source /home/euripides/projects/blockchain-platform/deploy/.env; set +a
nohup ./bin/worker >> /tmp/worker.log 2>&1 &
echo "worker restart pid=$!"
sleep 5
pgrep -af 'bin/worker'
