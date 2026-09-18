#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
run() { local label="$1"; local q="$2"; local t; t=$(curl -s -o /tmp/q.out -w '%{time_total}' "$CH" --data-binary "$q"); printf '%-42s %8.3fs  -> %s\n' "$label" "$t" "$(head -c 200 /tmp/q.out | tr '\n' ' ')"; }

echo "=== address_activity (tron test address) ==="
run "AA count() FINAL"            "SELECT count() FROM blockchain.address_activity FINAL WHERE chain='tron' AND address='$A'"
run "AA uniqExact(key) no FINAL"  "SELECT uniqExact(timestamp, tx_hash, activity_type, counterparty, token_address) FROM blockchain.address_activity WHERE chain='tron' AND address='$A'"
run "AA uniqExact(cp) FINAL"      "SELECT toUInt64(uniqExact(counterparty)) FROM blockchain.address_activity FINAL WHERE chain='tron' AND address='$A'"
run "AA uniqExact(cp) no FINAL"   "SELECT toUInt64(uniqExact(counterparty)) FROM blockchain.address_activity WHERE chain='tron' AND address='$A'"
run "AA min/max FINAL"            "SELECT min(timestamp), max(timestamp) FROM blockchain.address_activity FINAL WHERE chain='tron' AND address='$A'"
run "AA min/max no FINAL"         "SELECT min(timestamp), max(timestamp) FROM blockchain.address_activity WHERE chain='tron' AND address='$A'"
run "AA cntIf ds=tx FINAL"        "SELECT toInt64(countIf(dataset='transactions')) FROM blockchain.address_activity FINAL WHERE chain='tron' AND address='$A'"
run "AA uniqIf ds=tx no FINAL"    "SELECT toInt64(uniqExactIf((timestamp,tx_hash,activity_type,counterparty,token_address), dataset='transactions')) FROM blockchain.address_activity WHERE chain='tron' AND address='$A'"

echo
echo "=== token_transfers dedup shapes (tron test address) ==="
run "TT count raw rows"           "SELECT count() FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A')"
run "TT argMax GROUP BY (current)" "SELECT count() FROM (SELECT tx_hash, log_index, token_address, argMax(from_address, source_ingested_at) f FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') GROUP BY tx_hash, log_index, token_address)"
run "TT LIMIT 1 BY (proposed)"     "SELECT count() FROM (SELECT from_address FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') ORDER BY source_ingested_at DESC LIMIT 1 BY tx_hash, log_index, token_address)"
run "TT current full shape"        "SELECT count() FROM (SELECT d.fr_addr, d.to_addr, d.sym, d.token_address, toString(d.amt) a, d.tx_hash, d.ts FROM (SELECT tx_hash, log_index, token_address, argMax(from_address, source_ingested_at) AS fr_addr, argMax(to_address, source_ingested_at) AS to_addr, argMax(token_symbol, source_ingested_at) AS sym, argMax(amount, source_ingested_at) AS amt, argMax(timestamp, source_ingested_at) AS ts FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') GROUP BY tx_hash, log_index, token_address) AS d ORDER BY d.ts DESC LIMIT 50000)"
run "TT proposed full shape"       "SELECT count() FROM (SELECT from_address, to_address, token_symbol, token_address, toString(amount) AS amount, tx_hash, timestamp FROM blockchain.token_transfers WHERE chain='tron' AND (from_address='$A' OR to_address='$A') ORDER BY source_ingested_at DESC LIMIT 1 BY tx_hash, log_index, token_address) ORDER BY timestamp DESC LIMIT 50000"
