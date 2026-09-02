#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
A=0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
DB=$CLICKHOUSE_DB
echo "=== PG dataset_coverage for benchmark ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, row_count FROM dataset_coverage WHERE subject='$A' ORDER BY dataset;"
echo "=== workspace_addresses counters ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT transaction_count, token_transfer_count, nft_transfer_count, log_count, total_activity_count, first_data_block, last_data_block, last_sync_at FROM workspace_addresses WHERE address='$A';"
echo "=== CH physical vs FINAL per dataset ==="
for ds in transactions token_transfers nft_transfers logs; do
  phys=$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.$ds WHERE chain='bsc' AND (from_address='$A' OR to_address='$A')")
  fin=$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.$ds FINAL WHERE chain='bsc' AND (from_address='$A' OR to_address='$A')")
  echo "$ds physical=$phys final=$fin"
done
phys=$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.address_balances WHERE chain='bsc' AND address='$A'")
fin=$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.address_balances FINAL WHERE chain='bsc' AND address='$A'")
echo "balances physical=$phys final=$fin"
