#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== method_signatures for transfer & removeLiquidity... ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT selector, method_name, source, verified, occurrence_count, updated_at FROM method_signatures WHERE selector IN ('0xa9059cbb','0xaf2979eb') ORDER BY selector, verified DESC;"
echo "=== source distribution in method_signatures ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT source, verified, count(*) FROM method_signatures GROUP BY source, verified ORDER BY count(*) DESC;"
