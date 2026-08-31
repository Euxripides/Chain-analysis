#!/bin/bash
# 端到端 Benchmark（文档 §89）：mock Provider，1/10/100/1000 地址
# 用法：bash scripts/bench_e2e.sh   （需 API 运行中；输出 rows/s）
set -e
API="${API_URL:-http://127.0.0.1:8080/api/v1}"
RANGE_FROM=0
RANGE_TO=20000
OFFSET="${1:-0}"   # 地址偏移：避免命中历史 Coverage

gen_addrs() {
  local n=$1
  local out=""
  for ((i=0;i<n;i++)); do
    a=$(printf "0x%040d" $((i+OFFSET)))
    out="$out\"$a\""
    if (( i+1 < n )); then out="$out,"; fi
  done
  echo "$out"
}

for N in 1 10 100 1000; do
  addrs=$(gen_addrs $N)
  payload=$(cat <<JSON
{"subjects":[${addrs}],"chain":"bsc","datasets":["transactions","token_transfers"],"range":{"type":"block","from_block":${RANGE_FROM},"to_block":${RANGE_TO}},"reuse_policy":"prefer_local"}
JSON
)
  JOB=$(curl -s -X POST "$API/datasets/ensure" -H 'Content-Type: application/json' -d "$payload")
  JID=$(echo "$JOB" | python3 -c 'import json,sys; print(json.load(sys.stdin)["job_id"])')
  T0=$(date +%s)
  STATUS=""
  for ((i=0;i<300;i++)); do
    D=$(curl -s "$API/download-jobs/$JID")
    STATUS=$(echo "$D" | python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["status"])')
    case "$STATUS" in COMPLETED|PARTIAL_FAILED|FAILED) break;; esac
    sleep 2
  done
  T1=$(date +%s)
  ROWS=$(echo "$D" | python3 -c 'import json,sys; print((json.load(sys.stdin)["progress"] or {}).get("downloaded_rows", 0))')
  ELAPSED=$((T1-T0))
  if [ "$ELAPSED" -gt 0 ]; then RPS=$((ROWS/ELAPSED)); else RPS=$ROWS; fi
  echo "E2E addresses=$N status=$STATUS rows=$ROWS elapsed=${ELAPSED}s throughput=${RPS} rows/s"
done
