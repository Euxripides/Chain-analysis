#!/usr/bin/env python3
"""Find deterministic TronGrid timestamp windows for end-to-end benchmarks."""

import argparse
import json
import os
import urllib.parse
import urllib.request


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--address", required=True)
    parser.add_argument("--targets", default="200,1000,10000")
    args = parser.parse_args()
    targets = sorted({int(value) for value in args.targets.split(",")})
    key = os.environ.get("TRONGRID_API_KEY", "").strip()
    if not key:
        raise SystemExit("TRONGRID_API_KEY is required")

    rows = []
    fingerprint = ""
    while len(rows) < max(targets):
        query = {"limit": 200, "only_confirmed": "true", "order_by": "block_timestamp,desc"}
        if fingerprint:
            query["fingerprint"] = fingerprint
        url = f"https://api.trongrid.io/v1/accounts/{urllib.parse.quote(args.address)}/transactions/trc20?{urllib.parse.urlencode(query)}"
        request = urllib.request.Request(url, headers={"TRON-PRO-API-KEY": key})
        with urllib.request.urlopen(request, timeout=30) as response:
            page = json.load(response)
        data = page.get("data", [])
        if not data:
            break
        rows.extend(data)
        fingerprint = page.get("meta", {}).get("fingerprint", "")
        if not fingerprint:
            break

    result = {}
    for target in targets:
        sample = rows[:target]
        result[str(target)] = {
            "available": len(sample),
            "from_ms": min(int(row["block_timestamp"]) for row in sample) if sample else None,
            "to_ms": max(int(row["block_timestamp"]) for row in sample) if sample else None,
            "unique_tx": len({row["transaction_id"] for row in sample}),
        }
    print(json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
