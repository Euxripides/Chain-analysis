#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT id, job_no, status, created_at, source FROM download_jobs WHERE status IN ('RUNNING','PAUSED','QUEUED');"
