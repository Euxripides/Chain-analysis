#!/usr/bin/env bash
set +e
echo "=== processes ==="
pgrep -af 'bin/api'; pgrep -af 'bin/worker'
echo "=== health ==="
curl -sS http://127.0.0.1:8080/health/ready
echo ""
echo "=== containers ==="
docker ps --format '{{.Names}}	{{.Status}}' | grep deploy
echo "=== non-terminal jobs last check ==="
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -c "SELECT count(*) FROM download_jobs WHERE status IN ('RUNNING','PAUSED','QUEUED');"
