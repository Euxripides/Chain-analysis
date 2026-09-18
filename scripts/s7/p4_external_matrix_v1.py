#!/usr/bin/env python3
"""P4 matrix: every commit-gated write route that can reach a NON-PostgreSQL
side effect, its handler, its dependency class, and the mitigation that route
actually has today.

The commit gate wraps a handler in one PostgreSQL transaction and rolls it back
when the caller scope changed while the handler was running. PostgreSQL rolls
back; NATS, MinIO, ClickHouse and the chain providers do not. So for every gated
route that reaches one of those, the question is not "is it gated" but "what
stops the side effect when the gate rolls back".

This script answers that mechanically, from the real source, and refuses to
leave a route unclassified:

  outbox-in-tx   the handler records the effect in outbox_events inside the
                 request transaction, so a rollback takes the effect with it and
                 the outbox publisher runs it only after COMMIT;
  direct-bus     the handler publishes to NATS itself, after the gate opened;
  direct-object  the handler writes to MinIO itself, after the gate opened;
  direct-ch      the handler issues a ClickHouse statement itself;
  provider-call  the handler calls a chain/provider reader or writer itself;
  gate-recheck   the handler re-verifies the session scope token immediately
                 before the side effect (this is the shape AUTH-007 asks for).

Usage: python3 scripts/s7/p4_external_matrix_v1.py [--json OUT] [--md OUT]
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import scope_commit_coverage_v3 as cov  # noqa: E402  (single source of truth for route enumeration)

OUTBOX_TOKEN = "INSERT INTO outbox_events"
RECHECK_TOKENS = ("scopeCommitGateRecheck", "reverifyScopeToken", "scopeTokenStillCurrent")
BUS_TOKENS = ("s.publishEvent(", "deps.Bus.Publish", "s.deps.Bus.")
OBJECT_WRITE = re.compile(r"\.(PutObject|RemoveObject|RemoveObjects|CopyObject|ComposeObject|PutObjectStreaming)\(")
CH_WRITE = re.compile(r"\.(Exec|Batch)\(", )
CH_CLIENT = "s.deps.CH"
PROVIDER_TOKENS = cov.EXTERNAL_TOKENS["provider"]


def classify(chain_bodies):
    classes = set()
    blob = "\n".join(chain_bodies)
    if OUTBOX_TOKEN in blob:
        classes.add("outbox-in-tx")
    if any(t in blob for t in RECHECK_TOKENS):
        classes.add("gate-recheck")
    if any(t in blob for t in BUS_TOKENS):
        classes.add("direct-bus")
    if OBJECT_WRITE.search(blob):
        classes.add("direct-object")
    if CH_CLIENT in blob and re.search(CH_CLIENT + r"\.(Exec|Batch)\(", blob):
        classes.add("direct-ch")
    if any(t in blob for t in PROVIDER_TOKENS):
        classes.add("provider-call")
    return classes


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", default="")
    ap.add_argument("--md", default="")
    args = ap.parse_args()

    sources = {p.name: p.read_text(encoding="utf-8").split("\n")
               for p in sorted(cov.API.glob("*.go")) if not p.name.endswith("_test.go")}
    entries, mounts = [], {}
    for method, path, tail in cov.scan_body(sources["server.go"], "", mounts, mounts):
        entries.append({"method": method, "path": path, "tail": tail})
    func_bodies = {}
    for name, lines in sources.items():
        if name == "server.go":
            continue
        cur, depth, start = None, 0, 0
        for i, line in enumerate(lines):
            fm = cov.RE_FUNC.match(line)
            if fm:
                cur, start = fm.group(1), depth
                func_bodies[cur] = []
            if cur is not None:
                func_bodies[cur].append((i, line))
            depth += line.count("{") - line.count("}")
            if cur is not None and depth <= start and i > 0 and line.strip() == "}":
                cur = None
    scanned = set()
    for _ in range(8):
        changed = False
        for fname, lines in func_bodies.items():
            if fname not in mounts or fname in scanned:
                continue
            scanned.add(fname)
            child = {}
            for method, path, tail in cov.scan_body([ln for _, ln in lines], mounts[fname], mounts, child):
                entries.append({"method": method, "path": path, "tail": tail})
            for k, v in child.items():
                if k not in mounts:
                    mounts[k] = v
                    changed = True
        if not changed:
            break

    funcs = cov.build_funcs({k: v for k, v in sources.items() if k != "server.go"})
    # build_funcs keys are function names; rebuild with server.go included for completeness
    funcs_full = cov.build_funcs(sources)
    cache = {}
    rows = []
    for e in entries:
        tail = e["tail"]
        perms = [p for p in cov.RE_PERM.findall(tail) if ":" in p]
        hm = cov.RE_HANDLER.search(tail)
        handler = hm.group(1) if hm else ""
        gated = "s.require(" in tail or "s.requireAny(" in tail
        if not gated or e["method"] not in cov.WRITE_METHODS or not handler:
            continue
        classes, chain = cov.reachable_external(handler, funcs_full, cache)
        if not classes:
            continue
        chain_bodies = []
        for fn in chain:
            body = funcs_full.get(fn)
            if body:
                chain_bodies.extend(body)
        rows.append({
            "method": e["method"],
            "path": e["path"],
            "handler": handler,
            "permission": perms[0] if perms else "",
            "external": sorted(classes),
            "mitigation": sorted(classify(chain_bodies)),
            "chain_size": len(chain),
        })

    unmitigated = [r for r in rows if "direct-bus" in r["mitigation"] or "direct-object" in r["mitigation"]
                   or "direct-ch" in r["mitigation"] or "provider-call" in r["mitigation"]]
    without_recheck = [r for r in unmitigated if "gate-recheck" not in r["mitigation"] and "outbox-in-tx" not in r["mitigation"]]
    by_class = {}
    for r in rows:
        key = ",".join(r["external"])
        by_class[key] = by_class.get(key, 0) + 1

    print("gated_write_routes_reaching_external_dependency=" + str(len(rows)))
    for k in sorted(by_class):
        print("  external=" + k + " count=" + str(by_class[k]))
    print("routes_with_a_direct_side_effect=" + str(len(unmitigated)))
    print("routes_without_outbox_or_recheck=" + str(len(without_recheck)))
    print("--- matrix ---")
    for r in sorted(rows, key=lambda x: (x["path"], x["method"])):
        print("  %-6s %-62s handler=%-34s perm=%-26s external=%-26s mitigation=%s" % (
            r["method"], r["path"], r["handler"], r["permission"],
            ",".join(r["external"]), ",".join(r["mitigation"]) or "NONE"))
    if args.json:
        Path(args.json).write_text(json.dumps({
            "total": len(rows),
            "by_external_class": by_class,
            "direct_side_effect_routes": len(unmitigated),
            "routes_without_outbox_or_recheck": len(without_recheck),
            "rows": rows,
        }, indent=2, sort_keys=True), encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
