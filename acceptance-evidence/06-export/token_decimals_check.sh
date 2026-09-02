#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== token_metadata for U5DT ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT chain, contract_address, symbol, name, decimals, metadata_source, verified, logo_object_key FROM token_metadata WHERE contract_address='0xefc391a6e4a1c0775d845c4ced7bfcaa653e6a6e';"
echo "=== decimals column type ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\d token_metadata" | grep -i decimal
