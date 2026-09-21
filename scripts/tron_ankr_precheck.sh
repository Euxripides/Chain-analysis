#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${ANKR_API_KEY:-}" ]]; then
  echo "ANKR_API_KEY is not configured" >&2
  exit 2
fi

rpc="https://rpc.ankr.com/tron_jsonrpc/${ANKR_API_KEY}"
rest="https://rpc.ankr.com/premium-http/tron/${ANKR_API_KEY}"

probe() {
  local name="$1" url="$2" body="$3"
  local output="/tmp/tron-ankr-${name}.json"
  local code error size state
  code="$(curl -sS --connect-timeout 8 --max-time 30 -o "$output" -w "%{http_code}" \
    -H "content-type: application/json" --data "$body" "$url" || true)"
  error="$(jq -r 'if type == "object" then (.error.message // .Error // .result.message // empty) else empty end' "$output" 2>/dev/null | head -c 180 || true)"
  size="$(wc -c < "$output" 2>/dev/null || echo 0)"
  if [[ "$code" == "200" && -z "$error" ]]; then
    state="SUPPORTED"
  elif [[ "$code" == "401" || "$code" == "403" ]]; then
    state="DENIED"
  else
    state="UNSUPPORTED"
  fi
  printf '%s|%s|http=%s|bytes=%s|error=%s\n' "$name" "$state" "$code" "$size" "${error:-none}"
}

probe eth_blockNumber "$rpc" '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
probe eth_getLogs "$rpc" '{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x50faab7","toBlock":"0x50faab7","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"]}],"id":2}'
probe getblockbylimitnext "$rest/wallet/getblockbylimitnext" '{"startNum":84912055,"endNum":84912056,"visible":true}'
probe gettransactioninfobyblocknum "$rest/wallet/gettransactioninfobyblocknum" '{"num":84912055,"visible":true}'
probe gettransactioninfobyid "$rest/wallet/gettransactioninfobyid" '{"value":"9e9a6aaeb224beb19c75cf78f2a80bd7ece8d0e352be181fc11e6a037655e621","visible":true}'
probe getaccount "$rest/wallet/getaccount" '{"address":"TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh","visible":true}'
probe getcontract "$rest/wallet/getcontract" '{"value":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","visible":true}'
