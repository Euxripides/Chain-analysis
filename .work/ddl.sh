#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
for t in address_activity token_transfers transactions address_balances logs; do
  echo "--- $t ---"
  curl -s "$CH" --data-binary "SHOW CREATE TABLE blockchain.$t"
  echo
done
echo "--- version ---"
curl -s "$CH" --data-binary "SELECT version()"
