#!/usr/bin/env python3
"""strict-identity-proof-v2.py PRE_DIR POST_DIR COLS_JSON OUT_JSON
Self-contained (v1 imported strict-content-check-v2.py, whose module-level main()
re-executed on import and overwrote this script's output). Raw identity proof:
  I1 decompressed-bytes SHA-256 per table, PRE vs POST
  I2 strict UTF-8 validity (invalid bytes REJECTED, not replaced)
  I3 legitimately-empty dumps proven valid (present, gzip-valid, 0 raw bytes)
  I4 PK-set preservation, per-row byte preservation, appended PK count
"""
import sys, os, json, gzip, hashlib

REQUIRED = ["download_jobs", "download_shards", "checkpoints", "outbox_events",
            "platform_jobs", "backup_jobs", "job_event_log"]
ESCAPES = {0x62: 0x08, 0x66: 0x0c, 0x6e: 0x0a, 0x72: 0x0d, 0x74: 0x09, 0x76: 0x0b}

def parse_copy_text(raw):
    rows, bad_utf8 = [], 0
    n = len(raw); i = 0
    row, field, fraw = [], bytearray(), bytearray()
    def push():
        nonlocal bad_utf8
        if bytes(fraw) == b"\\N":
            row.append(None)
        else:
            try:
                row.append(field.decode("utf-8"))
            except UnicodeDecodeError:
                bad_utf8 += 1
                row.append(field.decode("utf-8", "replace"))
    while i < n:
        c = raw[i]
        if c == 0x5c and i + 1 < n:
            e = raw[i + 1]
            fraw.append(c); fraw.append(e)
            field.append(ESCAPES.get(e, e)); i += 2; continue
        if c == 0x09:
            push(); field, fraw = bytearray(), bytearray(); i += 1; continue
        if c == 0x0a:
            push(); rows.append(row); row, field, fraw = [], bytearray(), bytearray(); i += 1; continue
        field.append(c); fraw.append(c); i += 1
    if field or fraw or row:
        push(); rows.append(row)
    return rows, bad_utf8

def main():
    pre_d, post_d, cols_path, out_p = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    meta = json.load(open(cols_path))
    res = {"tables": {}, "failures": [], "ok": True}
    for t in REQUIRED:
        cols = meta[t]["columns"].split(",")
        pk = meta[t]["pk"]
        idx = dict((c, i) for i, c in enumerate(cols))
        entry, parsed = {}, {}
        for label, d in (("pre", pre_d), ("post", post_d)):
            p = os.path.join(d, t + ".tsv.gz")
            if not os.path.exists(p):
                res["failures"].append("I0 missing dump %s %s" % (label, t)); continue
            with gzip.open(p, "rb") as fh:
                raw = fh.read()
            entry[label + "_bytes"] = len(raw)
            entry[label + "_sha256"] = hashlib.sha256(raw).hexdigest()
            try:
                raw.decode("utf-8"); entry[label + "_utf8_valid"] = True
            except UnicodeDecodeError as e:
                entry[label + "_utf8_valid"] = False
                res["failures"].append("I2 invalid UTF-8 %s %s: %s" % (label, t, e))
            rows, bad = parse_copy_text(raw)
            entry[label + "_rows"] = len(rows)
            if bad:
                res["failures"].append("I2 %s %s has %d invalid-UTF-8 fields" % (label, t, bad))
            if len(rows) == 0:
                entry[label + "_empty_dump_proven_valid"] = (len(raw) == 0)
                if len(raw) != 0:
                    res["failures"].append("I3 %s %s 0 rows but %d raw bytes" % (label, t, len(raw)))
            nbad = sum(1 for r in rows if len(r) != len(cols))
            if nbad:
                res["failures"].append("I3 %s %s %d rows wrong field count" % (label, t, nbad))
            parsed[label] = rows
        entry["raw_bytes_identical"] = (entry.get("pre_sha256") == entry.get("post_sha256")
                                        and entry.get("pre_sha256") is not None)
        pre, post = parsed.get("pre"), parsed.get("post")
        if pre is None or post is None:
            res["tables"][t] = entry; continue
        pre_map, post_map = {}, {}
        for r in pre:
            if len(r) == len(cols): pre_map[r[idx[pk]]] = r
        for r in post:
            if len(r) == len(cols): post_map[r[idx[pk]]] = r
        entry["pre_pk_unique"] = len(pre_map) == len([r for r in pre if len(r) == len(cols)])
        entry["post_pk_unique"] = len(post_map) == len([r for r in post if len(r) == len(cols)])
        preserved = sum(1 for k, v in pre_map.items() if post_map.get(k) == v)
        entry["pre_pk_count"] = len(pre_map); entry["post_pk_count"] = len(post_map)
        entry["pre_rows_byte_preserved"] = preserved
        entry["missing_pk"] = sorted(set(pre_map) - set(post_map))[:5]
        entry["appended_pk_count"] = len(set(post_map) - set(pre_map))
        entry["appended_pk_sample"] = sorted(set(post_map) - set(pre_map))[:3]
        if entry["missing_pk"]:
            res["failures"].append("I4 %s lost PKs %s" % (t, entry["missing_pk"]))
        if preserved != len(pre_map):
            res["failures"].append("I4 %s preserved %d/%d pre rows byte-identically" % (t, preserved, len(pre_map)))
        res["tables"][t] = entry
    res["ok"] = len(res["failures"]) == 0
    res["scope_note"] = ("PRE/POST are each an ordered SEQUENCE of per-table snapshots, not one "
                         "transaction; POST is a post-rotation historical snapshot, not the live DB.")
    json.dump(res, open(out_p, "w"), indent=1, ensure_ascii=False)
    print(json.dumps(res, indent=1, ensure_ascii=False))
    sys.exit(0 if res["ok"] else 1)

if __name__ == "__main__":
    main()
