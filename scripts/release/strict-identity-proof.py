#!/usr/bin/env python3
"""strict-identity-proof.py PRE_DIR POST_DIR COLS_JSON OUT_JSON
Raw identity proof, independent of any manifest or ok-boolean:
  I1 decompressed-bytes SHA-256 per table for PRE and POST
  I2 strict UTF-8 validity of every dump (invalid bytes are REJECTED, not replaced)
  I3 legitimately-empty dumps are proven valid (present, gzip-valid, 0 rows) rather
     than treated as missing
  I4 PK-set preservation + per-row byte preservation, and the appended PK list
Uses the same PostgreSQL COPY TEXT parser as strict-content-check-v2.py.
"""
import sys, os, json, gzip, hashlib, importlib.util

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("scc", os.path.join(HERE, "strict-content-check-v2.py"))
scc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scc)
REQUIRED = scc.REQUIRED

def main():
    pre_d, post_d, cols_path, out_p = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    meta = json.load(open(cols_path))
    res = {"tables": {}, "failures": [], "ok": True}
    for t in REQUIRED:
        cols = meta[t]["columns"].split(",")
        pk = meta[t]["pk"]
        idx = dict((c, i) for i, c in enumerate(cols))
        entry = {}
        parsed = {}
        for label, d in (("pre", pre_d), ("post", post_d)):
            p = os.path.join(d, t + ".tsv.gz")
            if not os.path.exists(p):
                res["failures"].append("I0 missing dump %s %s" % (label, t)); entry[label] = None; continue
            with gzip.open(p, "rb") as fh:
                raw = fh.read()
            entry[label + "_bytes"] = len(raw)
            entry[label + "_sha256"] = hashlib.sha256(raw).hexdigest()
            try:
                raw.decode("utf-8")
                entry[label + "_utf8_valid"] = True
            except UnicodeDecodeError as e:
                entry[label + "_utf8_valid"] = False
                res["failures"].append("I2 invalid UTF-8 in %s %s: %s" % (label, t, e))
            rows, errs = scc.parse_copy_text(raw)
            entry[label + "_rows"] = len(rows)
            if entry[label + "_rows"] == 0:
                entry[label + "_empty_dump_proven_valid"] = (len(raw) == 0)
                if len(raw) != 0:
                    res["failures"].append("I3 %s %s has 0 rows but %d raw bytes" % (label, t, len(raw)))
            bad = sum(1 for r in rows if len(r) != len(cols))
            if bad:
                res["failures"].append("I3 %s %s has %d rows with wrong field count" % (label, t, bad))
            parsed[label] = rows
        entry["raw_identical"] = entry.get("pre_sha256") == entry.get("post_sha256")
        pre, post = parsed.get("pre"), parsed.get("post")
        if pre is None or post is None:
            res["tables"][t] = entry; continue
        pre_map = {}
        for r in pre:
            if len(r) == len(cols):
                pre_map[r[idx[pk]]] = r
        post_map = {}
        for r in post:
            if len(r) == len(cols):
                post_map[r[idx[pk]]] = r
        entry["pre_pk_unique"] = len(pre_map) == len([r for r in pre if len(r) == len(cols)])
        entry["post_pk_unique"] = len(post_map) == len([r for r in post if len(r) == len(cols)])
        preserved = sum(1 for k, v in pre_map.items() if post_map.get(k) == v)
        entry["pre_pk_count"] = len(pre_map)
        entry["post_pk_count"] = len(post_map)
        entry["pre_rows_byte_preserved"] = preserved
        entry["missing_pk"] = sorted(set(pre_map) - set(post_map))[:5]
        entry["appended_pk_count"] = len(set(post_map) - set(pre_map))
        entry["appended_pk_sample"] = sorted(set(post_map) - set(pre_map))[:3]
        if entry["missing_pk"]:
            res["failures"].append("I4 %s lost PKs: %s" % (t, entry["missing_pk"]))
        if preserved != len(pre_map):
            res["failures"].append("I4 %s preserved only %d/%d pre rows byte-identically" % (t, preserved, len(pre_map)))
        res["tables"][t] = entry
    res["ok"] = len(res["failures"]) == 0
    res["scope_note"] = ("PRE and POST are each an ordered SEQUENCE of per-table snapshots, "
                         "not one transaction; POST is a post-rotation historical snapshot, not the live DB.")
    json.dump(res, open(out_p, "w"), indent=1, ensure_ascii=False)
    print(json.dumps(res, indent=1, ensure_ascii=False))
    sys.exit(0 if res["ok"] else 1)

main()
