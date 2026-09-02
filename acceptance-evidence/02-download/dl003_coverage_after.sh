#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
A=0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
echo "=== coverage after DL-003 (benchmark) ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, row_count, committed_at FROM dataset_coverage WHERE subject='$A' ORDER BY dataset, from_block;"
