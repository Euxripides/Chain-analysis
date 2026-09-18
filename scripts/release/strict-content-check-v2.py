#!/usr/bin/env python3
"""strict-content-check.py PRE_DIR POST_DIR COLS_JSON OUT_JSON
STRICT content-parity checker (replaces compare-runtime-content-v2.py for acceptance).

Defects of compare-runtime-content-v2.py that this fixes:
  D1 it silently skipped rows whose field count != len(columns)
  D2 it silently skipped duplicate primary keys
  D3 an empty/missing columns descriptor produced ok=true
  D4 it parsed the dump with csv.reader, but the dumps are PostgreSQL COPY TEXT
     output, whose backslash escapes (\\t \\n \\\\ \\N) are NOT csv semantics;
     fields containing tabs/newlines (e.g. checkpoint tokens, error text) were
     therefore mis-read.
Strict rules:
  S1 required table set must match exactly; columns+pk present; pk must be a real column
  S2 every row must parse to exactly len(columns) fields  -> else FAIL
  S3 duplicate primary key                               -> else FAIL
  S4 empty/missing dump                                   -> FAIL
  S5 R1 no row loss, R2 no row disappearance, R3 immutable job identity,
     R4 progress monotonicity
Exit code is non-zero whenever ok is false.
"""
import sys, os, json, gzip

REQUIRED = ["download_jobs", "download_shards", "checkpoints", "outbox_events",
            "platform_jobs", "backup_jobs", "job_event_log"]
IMMUTABLE_JOBS = ["job_no", "source", "chain", "range_type", "from_block", "to_block",
                  "created_at", "organization_id", "workspace_id", "project_id",
                  "request_fingerprint", "mode", "created_by"]
MONOTONIC = {
    "download_shards": ["downloaded_rows", "downloaded_bytes", "page_no", "wire_bytes", "decoded_bytes"],
    "checkpoints": ["page_no", "downloaded_rows", "downloaded_bytes"],
    "outbox_events": ["attempts"],
}
ESCAPES = {0x62: 0x08, 0x66: 0x0c, 0x6e: 0x0a, 0x72: 0x0d, 0x74: 0x09, 0x76: 0x0b}

def parse_copy_text(raw):
    """Decode PostgreSQL COPY ... TO STDOUT (text format). Returns (rows, errors)."""
    rows, errors = [], []
    n = len(raw)
    i = 0
    row, field, fraw = [], bytearray(), bytearray()
    while i < n:
        c = raw[i]
        if c == 0x5c and i + 1 < n:
            e = raw[i + 1]
            fraw.append(c); fraw.append(e)
            if e in ESCAPES:
                field.append(ESCAPES[e])
            else:
                field.append(e)
            i += 2
            continue
        if c == 0x09:
            row.append(None if bytes(fraw) == b"\\N" else field.decode("utf-8", "replace"))
            field, fraw = bytearray(), bytearray()
            i += 1
            continue
        if c == 0x0a:
            row.append(None if bytes(fraw) == b"\\N" else field.decode("utf-8", "replace"))
            rows.append(row)
            row, field, fraw = [], bytearray(), bytearray()
            i += 1
            continue
        field.append(c); fraw.append(c); i += 1
    if field or fraw or row:
        row.append(None if bytes(fraw) == b"\\N" else field.decode("utf-8", "replace"))
        rows.append(row)
    return rows, errors

def load(path):
    if not os.path.exists(path):
        return None
    with gzip.open(path, "rb") as fh:
        rows, _errs = parse_copy_text(fh.read())
        return rows

def as_int(v):
    try:
        return int(v)
    except Exception:
        return None

def main():
    pre_d, post_d, cols_path, out_p = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    failures, notes = [], []
    tables = {}
    if not os.path.exists(cols_path) or os.path.getsize(cols_path) == 0:
        failures.append("S1 columns descriptor missing or empty: " + cols_path)
        meta = {}
    else:
        try:
            meta = json.load(open(cols_path))
        except Exception as e:
            failures.append("S1 columns descriptor unparsable: %s" % e)
            meta = {}
    if not meta:
        failures.append("S1 columns descriptor empty")
    got = set(meta.keys())
    if got != set(REQUIRED):
        failures.append("S1 table set mismatch: missing=%s extra=%s" % (sorted(set(REQUIRED) - got), sorted(got - set(REQUIRED))))
    for t in REQUIRED:
        spec = meta.get(t)
        if not spec:
            continue
        cols = (spec.get("columns") or "").split(",")
        pk = spec.get("pk") or ""
        if not cols or not cols[0]:
            failures.append("S1 empty column list for " + t); continue
        if not pk:
            failures.append("S1 empty pk for " + t); continue
        if pk not in cols:
            failures.append("S1 pk %s not in column list for %s" % (pk, t)); continue
        pr = load(os.path.join(pre_d, t + ".tsv.gz"))
        po = load(os.path.join(post_d, t + ".tsv.gz"))
        if pr is None or po is None:
            failures.append("S4 missing dump for " + t); continue
        idx = dict((c, i) for i, c in enumerate(cols))
        tinfo = {"pre_rows": len(pr), "post_rows": len(po), "changed_rows": 0, "missing_rows": 0,
                 "monotonic_violations": 0, "immutable_violations": 0, "duplicate_pk": 0, "bad_row": 0}
        for label, rows in (("PRE", pr), ("POST", po)):
            seen_pk = set()
            for k, r in enumerate(rows):
                if len(r) != len(cols):
                    tinfo["bad_row"] += 1
                    if tinfo["bad_row"] <= 3:
                        failures.append("S2 %s %s row %d has %d fields, expected %d" % (label, t, k, len(r), len(cols)))
                    continue
                pv = r[idx[pk]]
                if pv is None:
                    failures.append("S2 %s %s row %d has NULL primary key" % (label, t, k)); continue
                if pv in seen_pk:
                    tinfo["duplicate_pk"] += 1
                    if tinfo["duplicate_pk"] <= 3:
                        failures.append("S3 %s %s duplicate pk %s" % (label, t, pv))
                seen_pk.add(pv)
        if len(po) < len(pr):
            failures.append("R1 row loss %s: pre=%d post=%d" % (t, len(pr), len(po)))
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
                if tinfo["missing_rows"] <= 3:
                    failures.append("R2 row disappeared %s pk=%s" % (t, k))
                continue
            if r != q:
                tinfo["changed_rows"] += 1
            if t == "download_jobs":
                for c in IMMUTABLE_JOBS:
                    if c in idx and r[idx[c]] != q[idx[c]]:
                        tinfo["immutable_violations"] += 1
                        if tinfo["immutable_violations"] <= 3:
                            failures.append("R3 immutable changed %s pk=%s col=%s" % (t, k, c))
            for c in MONOTONIC.get(t, []):
                if c not in idx:
                    continue
                a, b = as_int(r[idx[c]]), as_int(q[idx[c]])
                if a is not None and b is not None and b < a:
                    tinfo["monotonic_violations"] += 1
                    if tinfo["monotonic_violations"] <= 3:
                        failures.append("R4 progress regressed %s pk=%s col=%s pre=%d post=%d" % (t, k, c, a, b))
        tables[t] = tinfo
    notes.append("dumps are PostgreSQL COPY TEXT; escapes are decoded by this checker (not csv semantics)")
    notes.append("PRE and POST are each a SEQUENCE of per-table snapshots, NOT one transaction")
    verdict = {"strict": True, "tables": tables, "failures": failures, "notes": notes,
               "ok": len(failures) == 0, "columns_descriptor": cols_path}
    with open(out_p, "w") as fh:
        json.dump(verdict, fh, indent=1, ensure_ascii=False)
    print(json.dumps(verdict, indent=1, ensure_ascii=False))
    sys.exit(0 if verdict["ok"] else 1)

try:
    main()
except SystemExit:
    raise
except Exception as e:
    import traceback
    tb = traceback.format_exc()
    out_p = sys.argv[4] if len(sys.argv) > 4 else None
    if out_p:
        try:
            json.dump({"strict": True, "crashed": True, "error": str(e), "traceback": tb, "ok": False}, open(out_p, "w"), indent=1)
        except Exception:
            pass
    print("STRICT_CHECK_CRASHED: " + str(e))
    sys.exit(3)
