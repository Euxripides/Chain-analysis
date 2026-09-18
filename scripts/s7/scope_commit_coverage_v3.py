#!/usr/bin/env python3
"""P1-A/P1-B mechanical coverage: every guarded write route, its handler and the
external I/O that handler can reach.

v2 answered "which state-changing routes take the commit gate". The AUTH-007
question is the other half: the gate holds the caller session row lock across the
whole handler, so a scope mutation queues behind it. That is only acceptable
while every gated handler is short, and a handler is slow when it talks to
something that is not PostgreSQL.

v3 therefore resolves, mechanically and from the real source:

  route -> handler function -> transitive set of external dependencies reached

External dependency classes (anything whose latency is not bounded by a local
row operation):
  provider   Ankr/RPC/chain readers (s.deps.Prov, ChainRead, RPCGateway, Trace,
             Head, BalanceRead, TokenMeta, Registry, AddressType, MethodData,
             DeepDecode, Capabilities) -- network calls to third parties;
  object     MinIO object storage (s.deps.Raw);
  bus        NATS publish (s.deps.Bus, s.publishEvent);
  clickhouse ClickHouse query (s.deps.CH, s.query);
  http       raw outbound HTTP (net/http client usage in this package).

Output: counts, the per-route table, and the list of gated routes that reach at
least one external dependency (the candidates that need a bounded critical
section or an outbox split). It is a static enumeration of the real code, not a
latency measurement.

Usage: python3 scripts/s7/scope_commit_coverage_v3.py [--json OUT] [--md OUT]
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
API = ROOT / "backend" / "internal" / "api"
WRITE_METHODS = {"POST", "PUT", "PATCH", "DELETE"}

SELF_TARGET_PERMISSIONS = {
    "admin:session:revoke": "the handler revokes the session named by the {id} path parameter, so only a call whose target IS the caller own session would queue behind the row lock the gate holds",
}

RE_ROUTE = re.compile(r'^\s*(\w+)\.Route\("([^"]+)"')
RE_METHOD = re.compile(r'^\s*(\w+)\.(Get|Post|Put|Patch|Delete)\("([^"]+)"\s*,\s*(.*)$')
RE_MOUNT = re.compile(r"s\.(register\w+)\(")
RE_FUNC = re.compile(r"^func \(s \*Server\) (\w+)\(\w+ chi\.Router\)")
RE_FUNC_ANY = re.compile(r"^func \(s \*Server\) (\w+)\(")
RE_TOP_FUNC = re.compile(r"^func (\w+)\(")
RE_PERM = re.compile(r'"([a-z0-9][a-z0-9:_\-]{2,})"')
RE_HANDLER = re.compile(r"s\.(handle\w+)\b")
RE_SELF_CALL = re.compile(r"s\.(\w+)\(")
RE_PKG_CALL = re.compile(r"(?<![\w.])([a-z]\w{2,})\(")

# receiver token -> external class
EXTERNAL_TOKENS = {
    "provider": ["s.deps.Prov", "s.deps.ChainRead", "s.deps.RPCGateway", "s.deps.Trace",
                 "s.deps.Head", "s.deps.BalanceRead", "s.deps.TokenMeta", "s.deps.Registry",
                 "s.deps.AddressType", "s.deps.MethodData", "s.deps.DeepDecode",
                 "s.deps.Capabilities"],
    "object": ["s.deps.Raw"],
    "bus": ["s.deps.Bus", "s.publishEvent("],
    "clickhouse": ["s.deps.CH", "s.query.", "s.deps.Query"],
    "http": ["http.Get(", "http.Post(", "http.NewRequest", ".Do(req", ".Do(http"],
}


def scan_body(lines, base, mounts, collect_children=None):
    routes = []
    stack = []
    depth = 0
    for line in lines:
        rm = RE_ROUTE.match(line)
        if rm:
            stack.append((depth, rm.group(2)))
        mm = RE_METHOD.match(line)
        if mm:
            tail = mm.group(4)
            routes.append((mm.group(2).upper(), base + "".join(p for _, p in stack) + mm.group(3), tail))
        for fm in RE_MOUNT.finditer(line):
            if collect_children is not None:
                collect_children.setdefault(fm.group(1), base + "".join(p for _, p in stack))
        depth += line.count("{") - line.count("}")
        while stack and depth <= stack[-1][0]:
            stack.pop()
    return routes


def build_funcs(sources):
    """Return name -> list of source lines for every func in the package."""
    funcs = {}
    for name, lines in sources.items():
        cur = None
        depth = 0
        start = 0
        for i, line in enumerate(lines):
            m = RE_FUNC_ANY.match(line) or RE_TOP_FUNC.match(line)
            if m:
                cur = m.group(1)
                start = depth
                funcs[cur] = []
            if cur is not None:
                funcs[cur].append(line)
            depth += line.count("{") - line.count("}")
            if cur is not None and depth <= start and i > 0 and line.strip() == "}":
                cur = None
    return funcs


def external_of(body):
    hits = set()
    for line in body:
        stripped = line.strip()
        if stripped.startswith("//"):
            continue
        for cls, tokens in EXTERNAL_TOKENS.items():
            for t in tokens:
                if t in line:
                    hits.add(cls)
    return hits


def reachable_external(handler, funcs, cache):
    if handler in cache:
        return cache[handler]
    seen = set()
    stack = [handler]
    classes = set()
    depth_guard = 0
    while stack and depth_guard < 4000:
        depth_guard += 1
        fn = stack.pop()
        if fn in seen:
            continue
        seen.add(fn)
        body = funcs.get(fn)
        if body is None:
            continue
        classes |= external_of(body)
        for line in body:
            if line.strip().startswith("//"):
                continue
            for callee in RE_SELF_CALL.findall(line):
                if callee in funcs and callee not in seen:
                    stack.append(callee)
            for callee in RE_PKG_CALL.findall(line):
                if callee in funcs and callee not in seen:
                    stack.append(callee)
    cache[handler] = (classes, sorted(seen))
    return cache[handler]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", default="")
    ap.add_argument("--md", default="")
    args = ap.parse_args()
    sources = {p.name: p.read_text(encoding="utf-8").split("\n")
               for p in sorted(API.glob("*.go")) if not p.name.endswith("_test.go")}

    entries = []
    mounts = {}
    for method, path, tail in scan_body(sources["server.go"], "", mounts, mounts):
        entries.append({"file": "server.go", "method": method, "path": path, "tail": tail})

    func_bodies = {}
    for name, lines in sources.items():
        if name == "server.go":
            continue
        cur = None
        depth = 0
        start = 0
        for i, line in enumerate(lines):
            fm = RE_FUNC.match(line)
            if fm:
                cur = fm.group(1)
                start = depth
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
            base = mounts[fname]
            child = {}
            body = [ln for _, ln in lines]
            for method, path, tail in scan_body(body, base, mounts, child):
                entries.append({"file": "?", "method": method, "path": path, "tail": tail})
            for k, v in child.items():
                if k not in mounts:
                    mounts[k] = v
                    changed = True
        if not changed:
            break

    funcs = build_funcs(sources)
    cache = {}
    for e in entries:
        tail = e["tail"]
        perms = [p for p in RE_PERM.findall(tail) if ":" in p]
        path = e["path"]
        hm = RE_HANDLER.search(tail)
        e["handler"] = hm.group(1) if hm else ""
        if "s.require(" in tail or "s.requireAny(" in tail:
            hit = next((p for p in perms if p in SELF_TARGET_PERMISSIONS), "")
            e["classification"] = "commit-gate-self-target" if hit else "commit-gate"
            e["permission"] = hit or (perms[0] if perms else "")
        elif path.startswith("/api/auth/v1"):
            e["classification"] = "self-service"
            e["permission"] = perms[0] if perms else ""
        elif path.startswith("/api/v1"):
            e["classification"] = "actions-bearer"
            e["permission"] = perms[0] if perms else ""
        else:
            e["classification"] = "unguarded"
            e["permission"] = perms[0] if perms else ""
        e.pop("tail", None)

    writes = [e for e in entries if e["method"] in WRITE_METHODS]
    guarded = [e for e in writes if e["classification"].startswith("commit-gate")]

    for e in writes:
        if e["handler"]:
            classes, chain = reachable_external(e["handler"], funcs, cache)
            e["external"] = sorted(classes)
            e["external_chain_size"] = len(chain)
        else:
            e["external"] = []
            e["external_chain_size"] = 0

    counts = {}
    for e in writes:
        counts[e["classification"]] = counts.get(e["classification"], 0) + 1
    gcounts = {}
    for e in guarded:
        key = ",".join(e["external"]) if e["external"] else "db-only"
        gcounts[key] = gcounts.get(key, 0) + 1
    unresolved = sorted({e["handler"] for e in guarded if e["handler"] and e["handler"] not in funcs})

    print("registered_routes_total=" + str(len(entries)))
    print("state_changing_routes=" + str(len(writes)))
    for k in sorted(counts):
        print("  " + k + "=" + str(counts[k]))
    print("session_permission_guarded_writes=" + str(len(guarded)))
    print("--- guarded writes by external dependency class ---")
    for k in sorted(gcounts):
        print("  " + k + "=" + str(gcounts[k]))
    print("gated_handlers_not_found_in_package=" + str(len(unresolved)))
    for h in unresolved:
        print("  MISSING " + h)
    print("--- self-target carve-outs (path-named session row) ---")
    for e in writes:
        if e["classification"] == "commit-gate-self-target":
            print("  " + e["method"] + " " + e["path"] + " perm=" + e["permission"])
    print("--- unguarded writes (must be justified) ---")
    for e in writes:
        if e["classification"] == "unguarded":
            print("  " + e["method"] + " " + e["path"])
    print("--- gated writes reaching an external dependency ---")
    for e in sorted(guarded, key=lambda x: (x["path"], x["method"])):
        if e["external"]:
            print("  " + e["method"] + " " + e["path"] + " handler=" + e["handler"] +
                  " external=" + ",".join(e["external"]))
    if args.json:
        Path(args.json).write_text(json.dumps({
            "registered_routes_total": len(entries),
            "state_changing_routes": len(writes),
            "counts": counts,
            "guarded_external_counts": gcounts,
            "gated_handlers_not_found_in_package": unresolved,
            "routes": writes,
        }, ensure_ascii=False, indent=2), encoding="utf-8")
    if args.md:
        lines = ["# Commit-gate write coverage with external I/O (mechanically enumerated)", "",
                 "- registered routes: " + str(len(entries)),
                 "- state-changing routes: " + str(len(writes)),
                 "- session-permission guarded writes: " + str(len(guarded)), ""]
        for k in sorted(gcounts):
            lines.append("- gated by external class " + k + ": " + str(gcounts[k]))
        lines += ["", "| method | path | classification | permission | handler | external |",
                  "| --- | --- | --- | --- | --- | --- |"]
        for e in sorted(writes, key=lambda x: (x["path"], x["method"])):
            lines.append("| " + e["method"] + " | `" + e["path"] + "` | " + e["classification"] +
                         " | " + e["permission"] + " | " + e["handler"] + " | " +
                         (",".join(e["external"]) or "-") + " |")
        Path(args.md).write_text("\n".join(lines) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
