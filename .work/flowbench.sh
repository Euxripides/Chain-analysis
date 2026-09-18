#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
A_Q="SELECT count() FROM (SELECT d.fr_addr AS from_address, d.to_addr AS to_address, d.sym AS token_symbol, d.ta AS token_address, d.amt AS amount, d.tx_hash AS tx_hash, d.ts AS timestamp FROM (SELECT tx_hash, log_index, token_address, argMax(from_address, source_ingested_at) AS fr_addr, argMax(to_address, source_ingested_at) AS to_addr, argMax(token_symbol, source_ingested_at) AS sym, argMax(token_address, source_ingested_at) AS ta, argMax(amount, source_ingested_at) AS amt, argMax(timestamp, source_ingested_at) AS ts FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') GROUP BY tx_hash, log_index, token_address) AS d ORDER BY d.ts DESC LIMIT 50000) /*SHAPE_A*/"
B_Q="SELECT count() FROM (SELECT * FROM (SELECT from_address, to_address, token_symbol, token_address, amount, tx_hash, timestamp FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') ORDER BY source_ingested_at DESC LIMIT 1 BY tx_hash, log_index, token_address) ORDER BY timestamp DESC LIMIT 50000) /*SHAPE_B*/"
S=$(date -u +"%Y-%m-%d %H:%M:%S")
for i in 1 2 3 4 5; do
  curl -s -o /dev/null "$CH" --data-binary "$A_Q"
  curl -s -o /dev/null "$CH" --data-binary "$B_Q"
done
sleep 12
curl -s "$CH" --data-binary "SELECT if(position(query,'SHAPE_A')>0,'A','B') shape, round(quantile(0.5)(query_duration_ms)) p50_ms, round(quantile(0.95)(query_duration_ms)) p95_ms, min(query_duration_ms) min_ms, max(query_duration_ms) max_ms, count() n, round(avg(read_rows)) rows FROM system.query_log WHERE event_time >= toDateTime('$S','UTC') AND type='QueryFinish' AND (position(query,'SHAPE_A')>0 OR position(query,'SHAPE_B')>0) GROUP BY shape ORDER BY shape FORMAT TSV"
