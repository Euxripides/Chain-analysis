#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
SIG="count() AS n, toString(sum(amount)) AS exact_sum, uniqExact(tx_hash) AS txs, sum(cityHash64(concat(from_address,'|',to_address,'|',token_symbol,'|',token_address,'|',toString(amount),'|',tx_hash))) AS fp"
A_Q="SELECT $SIG FROM (SELECT d.fr_addr AS from_address, d.to_addr AS to_address, d.sym AS token_symbol, d.ta AS token_address, d.amt AS amount, d.tx_hash AS tx_hash, d.ts AS timestamp FROM (SELECT tx_hash, log_index, token_address, argMax(from_address, source_ingested_at) AS fr_addr, argMax(to_address, source_ingested_at) AS to_addr, argMax(token_symbol, source_ingested_at) AS sym, argMax(token_address, source_ingested_at) AS ta, argMax(amount, source_ingested_at) AS amt, argMax(timestamp, source_ingested_at) AS ts FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') GROUP BY tx_hash, log_index, token_address) AS d ORDER BY d.ts DESC LIMIT 50000)"
B_Q="SELECT $SIG FROM (SELECT * FROM (SELECT from_address, to_address, token_symbol, token_address, amount, tx_hash, timestamp FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') ORDER BY source_ingested_at DESC LIMIT 1 BY tx_hash, log_index, token_address) ORDER BY timestamp DESC LIMIT 50000)"
run() { local label="$1"; local q="$2"; local t; t=$(curl -s -o /tmp/q.out -w '%{time_total}' "$CH" --data-binary "$q"); printf '%-12s %7.3fs -> %s\n' "$label" "$t" "$(head -c 300 /tmp/q.out | tr '\n' ' ')"; }
run "A current" "$A_Q"
run "B1 new"    "$B_Q"
run "A again"   "$A_Q"
run "B1 again"  "$B_Q"
