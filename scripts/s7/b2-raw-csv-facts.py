#!/usr/bin/env python3
"""Emit byte-level and CSV-structural facts for an exported artifact (read-only, no side effects).

Used by the B2 raw-only UI sample round to prove that the server full download and the file the
real browser downloaded are the same artifact and that the CSV structure matches the PG row
count. It never writes anything and never contacts the network.
"""
import csv
import hashlib
import json
import sys


def facts(path):
    with open(path, "rb") as handle:
        blob = handle.read()
    out = {"path": path, "bytes": len(blob), "sha256": hashlib.sha256(blob).hexdigest()}
    try:
        text = blob.decode("utf-8")
    except UnicodeDecodeError as exc:
        out["utf8"] = False
        out["decode_error"] = str(exc)
        return out
    out["utf8"] = True
    out["crlf"] = "\r\n" in text
    out["ends_with_newline"] = text.endswith("\n")
    lines = text.splitlines()
    rows = list(csv.reader(lines))
    out["total_lines"] = len(lines)
    out["csv_rows_including_header"] = len(rows)
    out["csv_data_rows"] = len(rows) - 1 if rows else 0
    out["header"] = rows[0] if rows else []
    out["header_columns"] = len(rows[0]) if rows else 0
    out["first_data_row"] = rows[1] if len(rows) > 1 else []
    out["column_counts_uniform"] = len({len(row) for row in rows}) <= 1
    out["distinct_column_counts"] = sorted({len(row) for row in rows})
    return out


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: b2-raw-csv-facts.py <file>", file=sys.stderr)
        sys.exit(2)
    print(json.dumps(facts(sys.argv[1]), ensure_ascii=False, indent=2))
