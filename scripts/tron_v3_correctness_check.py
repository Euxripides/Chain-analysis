#!/usr/bin/env python3
"""Compare a TronGrid history sample with Ankr TRON detail and receipt data.

Required environment: ANKR_API_KEY. The key is never printed or persisted.
Input must be a TronGrid JSON response containing a top-level ``data`` array.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import sys
import urllib.error
import urllib.request
from collections import Counter
from typing import Any


def post_json(url: str, payload: dict[str, Any]) -> dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def first_contract(tx: dict[str, Any]) -> dict[str, Any]:
    contracts = tx.get("raw_data", {}).get("contract", [])
    return contracts[0] if contracts else {}


def compare_one(base_url: str, grid: dict[str, Any]) -> dict[str, Any]:
    tx_hash = str(grid.get("txID", ""))
    try:
        detail = post_json(
            base_url + "/wallet/gettransactionbyid",
            # TronGrid history uses canonical hex addresses in raw_data. Ask
            # Ankr for the same representation so from/to comparisons are
            # semantic field checks rather than Base58-vs-hex format checks.
            {"value": tx_hash, "visible": False},
        )
        receipt = post_json(
            base_url + "/wallet/gettransactioninfobyid",
            {"value": tx_hash, "visible": True},
        )
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
        return {"tx_hash": tx_hash, "request_error": type(exc).__name__}

    grid_contract = first_contract(grid)
    detail_contract = first_contract(detail)
    grid_status = (grid.get("ret") or [{}])[0].get("contractRet")
    detail_status = (detail.get("ret") or [{}])[0].get("contractRet")
    receipt_status = receipt.get("receipt", {}).get("result")

    checks = {
        "tx_hash": tx_hash == detail.get("txID") == receipt.get("id"),
        "block_number": grid.get("blockNumber") == receipt.get("blockNumber"),
        "timestamp": grid.get("block_timestamp") == receipt.get("blockTimeStamp"),
        "contract_type": grid_contract.get("type") == detail_contract.get("type"),
        "from_to_value_raw": grid_contract.get("parameter", {}).get("value")
        == detail_contract.get("parameter", {}).get("value"),
        "status": grid_status == detail_status == receipt_status,
    }
    return {"tx_hash": tx_hash, "checks": checks}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("input", help="TronGrid response JSON")
    parser.add_argument("--sample-size", type=int, default=100)
    parser.add_argument("--workers", type=int, default=4)
    args = parser.parse_args()

    api_key = os.environ.get("ANKR_API_KEY", "").strip()
    if not api_key:
        print("ANKR_API_KEY is required", file=sys.stderr)
        return 2
    with open(args.input, "r", encoding="utf-8") as source:
        rows = json.load(source).get("data", [])[: args.sample_size]
    if len(rows) < args.sample_size:
        print(f"need {args.sample_size} rows, got {len(rows)}", file=sys.stderr)
        return 2

    base_url = "https://rpc.ankr.com/premium-http/tron/" + api_key
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        results = list(pool.map(lambda row: compare_one(base_url, row), rows))

    errors = [result for result in results if "request_error" in result]
    mismatches: Counter[str] = Counter()
    for result in results:
        for field, matched in result.get("checks", {}).items():
            if not matched:
                mismatches[field] += 1
    summary = {
        "sample_size": len(rows),
        "request_errors": len(errors),
        "matches": {
            field: len(rows) - len(errors) - mismatches[field]
            for field in (
                "tx_hash",
                "block_number",
                "timestamp",
                "contract_type",
                "from_to_value_raw",
                "status",
            )
        },
        "mismatches": dict(mismatches),
        "error_tx_hashes": [result["tx_hash"] for result in errors],
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if not errors and not mismatches else 1


if __name__ == "__main__":
    raise SystemExit(main())
