#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
A=0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
echo "=== benchmark address coverage ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT chain, subject, dataset, from_block, to_block, status, provider, row_count, raw_bytes, committed_at FROM dataset_coverage WHERE subject='$A' ORDER BY dataset, from_block;"
echo "=== download_jobs for benchmark ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT id, job_no, status, from_block, to_block, datasets, created_at FROM download_jobs WHERE id IN (SELECT job_id FROM download_subjects WHERE subject='$A') ORDER BY created_at DESC LIMIT 10;"
echo "=== workspace addresses ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT * FROM workspace_addresses WHERE address='$A';"
