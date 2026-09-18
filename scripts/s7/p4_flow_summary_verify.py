#!/usr/bin/env python3
"""Verifier for the P4-FLOW-01 bounded flow-summary probe (candidate only).

Modes:
  footprint <query-log.tsv>   assert every candidate scan carried the bounded
                              interactive settings and stayed inside the budget
  compare   <api.json> <ref.tsv>  assert the API answer equals an independent
                              ClickHouse reference computed with LIMIT 1 BY
"""
import datetime
import json
import sys

FOOTPRINT_READ_ROWS_MAX = 5000000
FOOTPRINT_MEMORY_MAX = 512 * 1024 * 1024
EXPECTED_SETTINGS = {
    "max_memory_usage": "2147483648",
    "max_threads": "4",
}
# The server-side execution bound is derived by clickhouse-go from the request
# deadline, so it is checked as a range: present, positive, and far below the
# 60 s router middleware (an unbound scan reports 0/empty; a deadline-derived one
# measured 64 s before the explicit 20 s budget was applied on top).
EXECUTION_BOUND_MAX = 30
FIELDS = [
    "total_in", "total_out", "net_flow", "in_tx_count", "out_tx_count",
    "unique_in_addresses", "unique_out_addresses",
    "first_in_time", "last_in_time", "first_out_time", "last_out_time",
]


def parse_time(value):
    if value is None:
        return None
    text = str(value).strip()
    if text in ("", "\\N"):
        return None
    text = text.replace(" ", "T")
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    if "." in text:
        head, rest = text.split(".", 1)
        digits = ""
        for ch in rest:
            if not ch.isdigit():
                break
            digits += ch
        text = head + "." + (digits + "000000")[:6] + rest[len(digits):]
    try:
        parsed = datetime.datetime.fromisoformat(text)
    except ValueError:
        return text
    if parsed.tzinfo is not None:
        parsed = parsed.astimezone(datetime.timezone.utc).replace(tzinfo=None)
    return parsed


def footprint(path, expect="2"):
    rows = [line for line in open(path).read().splitlines() if line.strip()]
    print("scans=%d expect=%s" % (len(rows), expect))
    # A negative-control run refuses every scan on purpose, so "no completed
    # bounded scan" is a pass there and only there.
    ok = bool(rows) or expect == "0"
    for index, line in enumerate(rows):
        parts = line.split("\t")
        if len(parts) < 6:
            print("FAIL scan %d unreadable: %r" % (index, line[:80]))
            ok = False
            continue
        duration, read_rows, memory, met, mmu, threads = parts[:6]
        try:
            execution_bound = int(met)
        except ValueError:
            execution_bound = -1
        if execution_bound <= 0 or execution_bound > EXECUTION_BOUND_MAX:
            print("FAIL scan %d max_execution_time=%r is not a bounded value" % (index, met))
            ok = False
        if mmu != EXPECTED_SETTINGS["max_memory_usage"]:
            print("FAIL scan %d max_memory_usage=%r" % (index, mmu))
            ok = False
        if threads != EXPECTED_SETTINGS["max_threads"]:
            print("FAIL scan %d max_threads=%r" % (index, threads))
            ok = False
        if int(read_rows) >= FOOTPRINT_READ_ROWS_MAX:
            print("FAIL scan %d read %s rows" % (index, read_rows))
            ok = False
        if int(memory) >= FOOTPRINT_MEMORY_MAX:
            print("FAIL scan %d held %s bytes" % (index, memory))
            ok = False
        print("scan %d duration_ms=%s read_rows=%s memory=%.1f MiB threads=%s"
              % (index, duration, read_rows, int(memory) / 1048576.0, threads))
    print("PASS" if ok else "FAIL")


def compare(api_path, ref_path):
    payload = json.load(open(api_path))
    data = payload.get("data") or {}
    reference = open(ref_path).read().strip().split("\t")
    ok = True
    for index, name in enumerate(FIELDS):
        got = data.get(name)
        want = reference[index].strip() if index < len(reference) else None
        if name.endswith("_time"):
            same = parse_time(got) == parse_time(want)
        elif name.endswith("_count") or name.startswith("unique_"):
            same = int(got) == int(want)
        else:
            same = str(got) == want
        if same:
            print("OK   %s = %r" % (name, got))
        else:
            print("FAIL %s api=%r reference=%r" % (name, got, want))
            ok = False
    print("PASS" if ok else "FAIL")


if __name__ == "__main__":
    if len(sys.argv) >= 3 and sys.argv[1] == "footprint":
        footprint(sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "2")
    elif len(sys.argv) >= 4 and sys.argv[1] == "compare":
        compare(sys.argv[2], sys.argv[3])
    else:
        raise SystemExit("usage: footprint <tsv> | compare <api.json> <ref.tsv>")
