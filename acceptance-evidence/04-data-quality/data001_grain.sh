#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== DATA-001 grain checks (benchmark address) ==="
echo "--- token_transfers ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS fin, uniqExact(tx_hash, log_index, token_address) AS grain FROM $DB.token_transfers FINAL WHERE chain='bsc' AND (from_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd' OR to_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')"
echo "--- transactions ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS fin, uniqExact(tx_hash) AS grain FROM $DB.transactions FINAL WHERE chain='bsc' AND (from_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd' OR to_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd')"
echo "--- logs (address) ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS fin, uniqExact(tx_hash, log_index) AS grain FROM $DB.logs FINAL WHERE chain='bsc' AND address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd'"
echo "--- balances ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS fin, uniqExact(address, token_address) AS grain FROM $DB.address_balances FINAL WHERE chain='bsc' AND address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd'"
echo "=== global physical vs FINAL (top tables) ==="
for ds in transactions token_transfers logs nft_transfers; do
  phys=$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.$ds")
  fin=$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.$ds FINAL")
  echo "$ds physical=$phys final=$fin"
done
echo "=== transactions schema ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "DESCRIBE TABLE $DB.transactions"
