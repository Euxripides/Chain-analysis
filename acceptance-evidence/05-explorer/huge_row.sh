#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, from_address, to_address, token_address, amount_raw, amount FROM $DB.token_transfers FINAL WHERE length(tx_hash)=66 AND length(amount_raw)>40 LIMIT 2 FORMAT Vertical"
