#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== check mutations from role test ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT id, name, limit_usd FROM api_control_budgets WHERE name='role-test';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT selector, method_name, source, verified FROM method_signatures WHERE method_name LIKE 'accTest%';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT chain, contract_address, source, verified FROM contract_abis WHERE contract_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT * FROM token_metadata_overrides WHERE contract_address='0xefc391a6e4a1c0775d845c4ced7bfcaa653e6a6e';"
echo "=== cleanup test mutations ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "DELETE FROM api_control_budgets WHERE name='role-test';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "DELETE FROM method_signatures WHERE method_name LIKE 'accTest%';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "DELETE FROM contract_abis WHERE contract_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "DELETE FROM token_metadata_overrides WHERE contract_address='0xefc391a6e4a1c0775d845c4ced7bfcaa653e6a6e';"
echo "=== verify cleanup ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT count(*) AS leftover_budgets FROM api_control_budgets WHERE name='role-test'; SELECT count(*) AS leftover_sigs FROM method_signatures WHERE method_name LIKE 'accTest%';"
