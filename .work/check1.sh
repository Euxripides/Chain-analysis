#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
echo "active download jobs: $(PSQL "SELECT count(*) FROM download_jobs WHERE status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING')")"
PSQL "SELECT id||' '||status||' '||updated_at FROM download_jobs ORDER BY updated_at DESC LIMIT 5"
echo "units: api=$(systemctl --user is-active blockchain-api) worker=$(systemctl --user is-active blockchain-worker)"
echo "deploy script present: $(ls -la /tmp/deploywal.sh | awk '{print $5}') bytes"
