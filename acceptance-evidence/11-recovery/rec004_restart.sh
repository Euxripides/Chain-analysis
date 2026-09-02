#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
cd /home/euripides/projects/blockchain-platform/backend
set -a; source /home/euripides/projects/blockchain-platform/deploy/.env; set +a
nohup ./bin/worker >> /tmp/worker.log 2>&1 &
echo "restarted pid=$!"
sleep 4
pgrep -af 'bin/worker'
echo "=== shard state ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, downloaded_rows, page_no, lease_owner, leased_until, retries FROM download_shards WHERE job_id='f7f1121c-e20e-4d95-85b0-f42919ff42b3';"
