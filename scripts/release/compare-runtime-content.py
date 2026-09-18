#!/usr/bin/env python3
"""compare-runtime-content.py PRE_DIR POST_DIR OUT_JSON
Content-level parity across a credential rotation. Gates:
  R1 no row loss: post rows >= pre rows
  R2 no row disappearance: every pre PK still present in post
  R3 immutable identity fields unchanged (download_jobs)
  R4 progress monotonicity: work counters must not decrease
Everything else may legitimately change (live workload) and is reported only.
"""
import sys, os, json, gzip, csv, io

IMMUTABLE_JOBS = ["job_no","source","chain","range_type","from_block","to_block",
                  "created_at","organization_id","workspace_id","project_id",
                  "request_fingerprint","mode","created_by"]
MONOTONIC = {
    "download_shards": ["downloaded_rows","downloaded_bytes","page_no","wire_bytes","decoded_bytes"],
    "checkpoints":     ["page_no","downloaded_rows","downloaded_bytes"],
    "outbox_events":   ["attempts"],
}

def load(d, table):
    p = os.path.join(d, table + ".tsv.gz")
    if not os.path.exists(p):
        return None
    rows = []
    with gzip.open(p, "rt", encoding="utf-8", errors="replace", newline="") as fh:
        for line in fh:
            line = line.rstrip("\n")
            if line == "":
                continue
            rows.append(next(csv.reader(io.StringIO(line), delimiter="\t", quotechar="\x00")))
    return rows

def as_int(v):
    try:
        return int(v)
    except Exception:
        return None

def main():
    pre_d, post_d, out_p = sys.argv[1], sys.argv[2], sys.argv[3]
    meta = json.load(open(os.path.join(pre_d, "columns-pre.json")))
    verdict = {"tables": {}, "failures": [], "notes": []}
    total_changed = 0
    for table, spec in meta.items():
        cols = spec["columns"].split(",")
        pk = spec["pk"]
        pr = load(pre_d, table)
        po = load(post_d, table)
        if pr is None or po is None:
            verdict["failures"].append(table + ": missing dump")
            continue
        idx = dict((c, i) for i, c in enumerate(cols))
        pn, qn = len(pr), len(po)
        tinfo = {"pre_rows": pn, "post_rows": qn, "changed_rows": 0,
                 "monotonic_violations": 0, "immutable_violations": 0, "missing_rows": 0}
        if qn < pn:
            verdict["failures"].append("R1 row loss %s: pre=%d post=%d" % (table, pn, qn))
        post_map = {}
        for r in po:
            if len(r) == len(cols):
                post_map[r[idx[pk]]] = r
        seen = set()
        for r in pr:
            if len(r) != len(cols):
                continue
            k = r[idx[pk]]
            if k in seen:
                continue
            seen.add(k)
            q = post_map.get(k)
            if q is None:
                tinfo["missing_rows"] += 1
                verdict["failures"].append("R2 row disappeared %s pk=%s" % (table, k))
                continue
            if r != q:
                tinfo["changed_rows"] += 1
                total_changed += 1
            if table == "download_jobs":
                for c in IMMUTABLE_JOBS:
                    if c in idx and r[idx[c]] != q[idx[c]]:
                        tinfo["immutable_violations"] += 1
                        verdict["failures"].append(
                            "R3 immutable changed %s pk=%s col=%s pre=%s post=%s" % (
                                table, k, c, r[idx[c]][:60], q[idx[c]][:60]))
            for c in MONOTONIC.get(table, []):
                if c not in idx:
                    continue
                a, b = as_int(r[idx[c]]), as_int(q[idx[c]])
                if a is not None and b is not None and b < a:
                    tinfo["monotonic_violations"] += 1
                    verdict["failures"].append(
                        "R4 progress regressed %s pk=%s col=%s pre=%d post=%d" % (table, k, c, a, b))
        verdict["tables"][table] = tinfo
    verdict["total_changed_rows"] = total_changed
    verdict["ok"] = len(verdict["failures"]) == 0
    verdict["notes"].append("changed_rows>0 expected under live load; only R1-R4 gate")
    with open(out_p, "w") as fh:
        json.dump(verdict, fh, indent=1, ensure_ascii=False)
    print(json.dumps(verdict, indent=1, ensure_ascii=False))
    sys.exit(0 if verdict["ok"] else 1)

main()
