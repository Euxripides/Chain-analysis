#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== CH row for 0x4b61b535 (post-redeploy state) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, log_index, from_address, to_address, token_address, amount_raw, source_provider, source_ingested_at FROM $CLICKHOUSE_DB.token_transfers FINAL WHERE tx_hash='0x4b61b5353106750a10f66d12d61eba904ce0def3c09b5d43aaa4d55700d28e2a' AND from_address='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' FORMAT Vertical"
echo "=== control state ==="
curl -sS http://127.0.0.1:8080/api/v1/api-control/control/states | head -c 400
