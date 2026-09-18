#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
run() { local label="$1"; local q="$2"; local t; t=$(curl -s -o /tmp/q.out -w '%{time_total}' "$CH" --data-binary "$q"); printf '%-34s %7.3fs -> %s\n' "$label" "$t" "$(head -c 160 /tmp/q.out | tr '\n' ' ')"; }

INNER_CURRENT="(SELECT tx_hash, log_index, token_address, argMax(from_address, source_ingested_at) AS fr_addr, argMax(to_address, source_ingested_at) AS to_addr, argMax(token_symbol, source_ingested_at) AS sym, argMax(token_address, source_ingested_at) AS ta, argMax(amount, source_ingested_at) AS amt, argMax(timestamp, source_ingested_at) AS ts FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') GROUP BY tx_hash, log_index, token_address)"
SIG="count(), sum(toFloat64(amt)), uniqExact(tx_hash)"

echo "=== A. current (GROUP BY + argMax) ==="
run "A current" "SELECT $SIG FROM (SELECT * FROM $INNER_CURRENT ORDER BY ts DESC LIMIT 50000)"

echo
echo "=== B. LIMIT 1 BY + outer re-sort ==="
run "B1 plain" "SELECT count(), sum(toFloat64(amount)), uniqExact(tx_hash) FROM (SELECT * FROM (SELECT from_address, to_address, token_symbol, token_address, amount, tx_hash, timestamp FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') ORDER BY source_ingested_at DESC LIMIT 1 BY tx_hash, log_index, token_address) ORDER BY timestamp DESC LIMIT 50000)"
run "B2 with inner LIMIT" "SELECT count(), sum(toFloat64(amount)), uniqExact(tx_hash) FROM (SELECT * FROM (SELECT from_address, to_address, token_symbol, token_address, amount, tx_hash, timestamp FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') ORDER BY source_ingested_at DESC LIMIT 1 BY tx_hash, log_index, token_address LIMIT 100000000) ORDER BY timestamp DESC LIMIT 50000)"

echo
echo "=== C. window function row_number ==="
run "C window" "SELECT count(), sum(toFloat64(amount)), uniqExact(tx_hash) FROM (SELECT from_address, to_address, token_symbol, token_address, amount, tx_hash, timestamp, row_number() OVER (PARTITION BY tx_hash, log_index, token_address ORDER BY source_ingested_at DESC) AS rn FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A')) WHERE rn = 1 ORDER BY timestamp DESC LIMIT 50000"
