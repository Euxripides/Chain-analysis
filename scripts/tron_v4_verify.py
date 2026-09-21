#!/usr/bin/env python3
"""Cross-check stored TRON TRC20 identities against fresh Ankr receipts."""

import argparse
import base64
import json
import os
import urllib.parse
import urllib.request


ALPHABET = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
TRANSFER_TOPIC = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"


def base58_payload(value: str) -> bytes:
    number = 0
    for char in value:
        number = number * 58 + ALPHABET.index(char)
    raw = number.to_bytes((number.bit_length() + 7) // 8, "big")
    raw = b"\0" * (len(value) - len(value.lstrip("1"))) + raw
    if len(raw) != 25 or raw[0] != 0x41:
        raise ValueError(f"invalid TRON address: {value}")
    return raw[1:21]


def rpc(endpoint: str, method: str, params: list) -> dict:
    payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
    request = urllib.request.Request(endpoint, data=payload, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=30) as response:
        result = json.load(response)
    if result.get("error"):
        raise RuntimeError(result["error"])
    return result["result"]


def clickhouse_rows(address: str, start: str, end: str) -> list[dict]:
    query = f"""
SELECT tx_hash, log_index, token_address, from_address, to_address, amount_raw
FROM blockchain.token_transfers
WHERE chain = 'tron'
  AND (from_address = {{address:String}} OR to_address = {{address:String}})
  AND timestamp >= toDateTime64({{start:String}}, 3, 'UTC')
  AND timestamp <= toDateTime64({{end:String}}, 3, 'UTC')
ORDER BY timestamp, tx_hash, log_index
FORMAT JSONEachRow
"""
    params = urllib.parse.urlencode({"param_address": address, "param_start": start, "param_end": end})
    request = urllib.request.Request(f"http://127.0.0.1:8123/?{params}", data=query.encode())
    user = os.environ.get("CLICKHOUSE_USER", "default")
    password = os.environ.get("CLICKHOUSE_PASSWORD", "")
    credentials = base64.b64encode(f"{user}:{password}".encode()).decode()
    request.add_header("Authorization", f"Basic {credentials}")
    with urllib.request.urlopen(request, timeout=30) as response:
        return [json.loads(line) for line in response if line.strip()]


def matching_log(receipt: dict, row: dict) -> dict | None:
    token = base58_payload(row["token_address"]).hex()
    sender = base58_payload(row["from_address"]).hex().rjust(64, "0")
    recipient = base58_payload(row["to_address"]).hex().rjust(64, "0")
    amount = int(row["amount_raw"])
    for log in receipt.get("logs", []):
        topics = [str(topic).lower().removeprefix("0x") for topic in log.get("topics", [])]
        if len(topics) < 3 or topics[0] != TRANSFER_TOPIC:
            continue
        if str(log.get("address", "")).lower().removeprefix("0x") != token:
            continue
        if topics[1] != sender or topics[2] != recipient:
            continue
        if int(str(log.get("data", "0x0")), 16) == amount:
            return log
    return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--address", required=True)
    parser.add_argument("--from-time", required=True)
    parser.add_argument("--to-time", required=True)
    parser.add_argument("--limit", type=int, default=0)
    args = parser.parse_args()

    api_key = os.environ.get("ANKR_API_KEY", "").strip()
    if not api_key:
        raise SystemExit("ANKR_API_KEY is required")
    endpoint = f"https://rpc.ankr.com/tron_jsonrpc/{api_key}"
    rows = clickhouse_rows(args.address, args.from_time, args.to_time)
    if args.limit > 0:
        rows = rows[: args.limit]
    failures = []
    for row in rows:
        receipt = rpc(endpoint, "eth_getTransactionReceipt", ["0x" + row["tx_hash"]])
        log = matching_log(receipt, row)
        actual = int(str(log["logIndex"]), 16) if log else None
        if actual != int(row["log_index"]):
            failures.append({"tx_hash": row["tx_hash"], "stored": row["log_index"], "receipt": actual})

    print(json.dumps({"rows": len(rows), "matched": len(rows) - len(failures), "failures": failures}, ensure_ascii=False))
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
