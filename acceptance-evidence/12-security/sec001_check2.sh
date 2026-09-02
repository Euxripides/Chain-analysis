#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== token_metadata_overrides schema ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\d token_metadata_overrides" | head -15
echo "=== any override rows ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT * FROM token_metadata_overrides LIMIT 10;"
echo "=== U5DT symbol current ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT symbol, name, decimals FROM token_metadata WHERE contract_address='0xefc391a6e4a1c0775d845c4ced7bfcaa653e6a6e';"
