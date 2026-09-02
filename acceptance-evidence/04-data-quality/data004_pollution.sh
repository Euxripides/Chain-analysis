#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== chain distribution (transactions) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT chain, count() FROM $DB.transactions FINAL GROUP BY chain ORDER BY count() DESC"
echo "=== chain distribution (token_transfers) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT chain, count() FROM $DB.token_transfers FINAL GROUP BY chain ORDER BY count() DESC"
echo "=== placeholder address rows (transactions) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE from_address IN ('0x1111111111111111111111111111111111111111','0x2222222222222222222222222222222222222222','0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','0xcccccccccccccccccccccccccccccccccccccccc','0xdddddddddddddddddddddddddddddddddddddddd','0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','0xffffffffffffffffffffffffffffffffffffffff','0x1234567890abcdef1234567890abcdef12345678','0x2468ace02468ace02468ace02468ace02468ace0') OR to_address IN ('0x1111111111111111111111111111111111111111','0x2222222222222222222222222222222222222222','0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','0xcccccccccccccccccccccccccccccccccccccccc','0xdddddddddddddddddddddddddddddddddddddddd','0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','0xffffffffffffffffffffffffffffffffffffffff','0x1234567890abcdef1234567890abcdef12345678','0x2468ace02468ace02468ace02468ace02468ace0')"
echo "=== low sequential addresses (0x0000...00XX) token_transfers ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.token_transfers FINAL WHERE from_address LIKE '0x00000000000000000000000000000000000000%' OR to_address LIKE '0x00000000000000000000000000000000000000%'"
echo "=== empty tx_hash transactions ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT length(tx_hash) AS l, count() FROM $DB.transactions FINAL GROUP BY l ORDER BY count() DESC"
echo "=== empty addresses token_transfers ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT length(from_address) AS l, count() FROM $DB.token_transfers FINAL GROUP BY l ORDER BY count() DESC LIMIT 5"
echo "=== real-format rows (proper tx hash) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x')"
echo "=== real bsc rows only ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE chain='bsc' AND length(tx_hash)=66"
