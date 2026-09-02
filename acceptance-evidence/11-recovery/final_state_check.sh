#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== leftover RUNNING/PAUSED jobs ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT id, job_no, status, created_at FROM download_jobs WHERE status IN ('RUNNING','PAUSED','QUEUED','PAUSE_REQUESTED','RESUMING','CANCEL_REQUESTED') AND created_at > now() - interval '6 hours';"
echo "=== shards not terminal ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT job_id, dataset, status, from_block, to_block FROM download_shards WHERE status NOT IN ('COMPLETED','CANCELLED','FAILED','SKIPPED');"
echo "=== display_name state ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT display_name, primary_case_id FROM workspace_addresses WHERE address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd';"
echo "=== cases/tags ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT name FROM cases; SELECT name FROM tags;"
echo "=== processes ==="
pgrep -af 'bin/(api|worker)'
echo "=== containers ==="
docker ps --format '{{.Names}}	{{.Status}}' | grep deploy
echo "=== health ==="
curl -sS http://127.0.0.1:8080/health/ready
echo ""
echo "=== manual settings ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT kill_switch, manual_mode, manual_reason, manual_until FROM api_control_settings WHERE id=1;"
