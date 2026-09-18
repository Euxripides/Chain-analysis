#!/usr/bin/env python3
"""Print how many rows the legacy console token actually returned."""
import json
import os
import sys

WANTED = [
    "legacy_cases_list.json",
    "legacy_job_list.json",
    "legacy_investigation_list.json",
    "legacy_ankr_provider_status_admin_key.json",
]


def rows_of(data):
    if isinstance(data, list):
        return len(data)
    if isinstance(data, dict):
        for key in ("items", "cases", "jobs", "investigations", "rows"):
            value = data.get(key)
            if isinstance(value, list):
                return len(value)
        return len(data)
    return 0


def main():
    evidence = sys.argv[1]
    for name in WANTED:
        path = os.path.join(evidence, name)
        if not os.path.exists(path):
            print("%-46s missing" % name)
            continue
        try:
            payload = json.load(open(path))
        except Exception as exc:
            print("%-46s unreadable: %s" % (name, exc))
            continue
        # The console API answers with a bare envelope ({"cases": [...]}), not
        # with the GPT Actions {"success":..,"data":..} wrapper.
        print("%-46s rows=%d keys=%s" % (name, rows_of(payload), sorted(payload)[:4]))


if __name__ == "__main__":
    main()
