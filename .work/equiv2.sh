#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
echo "== sorting keys =="
curl -s "$CH" --data-binary "SELECT name, sorting_key FROM system.tables WHERE database='blockchain' AND name IN ('nft_transfers','internal_calls','transactions','token_transfers','logs') ORDER BY name FORMAT TSV"
run() { local label="$1"; local q="$2"; local t; t=$(curl -s -o /tmp/q.out -w '%{time_total}' "$CH" --data-binary "$q"); printf '%-46s %8.3fs -> %s\n' "$label" "$t" "$(head -c 120 /tmp/q.out | tr '\n' ' ')"; }

echo
echo "=== addressmeta.dataCounts shapes (tron address) ==="
run "TX count() FINAL (current)"      "SELECT count() FROM blockchain.transactions FINAL WHERE chain='tron' AND (from_address='$A' OR to_address='$A')"
run "TX uniqExact(tx_hash) (new)"     "SELECT uniqExact(tx_hash) FROM blockchain.transactions WHERE chain='tron' AND (from_address='$A' OR to_address='$A')"
run "TT count() FINAL (current)"      "SELECT count() FROM blockchain.token_transfers FINAL WHERE chain='tron' AND (from_address='$A' OR to_address='$A')"
run "TT uniqExact(key) (new)"         "SELECT uniqExact(tx_hash, log_index, token_address) FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A')"
run "LOG count() FINAL (current)"     "SELECT count() FROM blockchain.logs FINAL WHERE chain='tron' AND address='$A'"
run "LOG uniqExact(key) (new)"        "SELECT uniqExact(tx_hash, log_index) FROM blockchain.logs WHERE chain='tron' AND address='$A'"
run "NFT count() FINAL (current)"     "SELECT count() FROM blockchain.nft_transfers FINAL WHERE chain='tron' AND (from_address='$A' OR to_address='$A')"

echo
echo "=== netflow shapes (overview+assets) ==="
run "NF FINAL (current)" "SELECT token_address, any(token_symbol) AS symbol, any(token_decimals) AS decimals, any(dataset) AS dataset, toString(sumIf(amount, direction IN ('in','self'))) AS in_raw, toString(sumIf(amount, direction IN ('out','self'))) AS out_raw, toInt64(count()) AS cnt FROM blockchain.address_activity FINAL WHERE chain='tron' AND address='$A' GROUP BY token_address ORDER BY cnt DESC LIMIT 100"
run "NF dedup-subquery (new)" "SELECT d.token_address AS token_address, any(d.sym) AS symbol, any(d.dec) AS decimals, any(d.ds) AS dataset, toString(sumIf(d.amt, d.dir IN ('in','self'))) AS in_raw, toString(sumIf(d.amt, d.dir IN ('out','self'))) AS out_raw, toInt64(count()) AS cnt FROM (SELECT timestamp, tx_hash, activity_type, counterparty, token_address, argMax(token_symbol, source_ingested_at) AS sym, argMax(token_decimals, source_ingested_at) AS dec, argMax(dataset, source_ingested_at) AS ds, argMax(direction, source_ingested_at) AS dir, argMax(amount, source_ingested_at) AS amt FROM blockchain.address_activity WHERE chain='tron' AND address='$A' GROUP BY timestamp, tx_hash, activity_type, counterparty, token_address) AS d GROUP BY d.token_address ORDER BY cnt DESC LIMIT 100"
