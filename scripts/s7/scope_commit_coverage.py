#!/usr/bin/env python3
"""P1-A mechanical enumeration of every state-changing route in backend/internal/api.

The C1 scope race must be closed by a SHARED commit-boundary invariant, so the write
surface has to be enumerated mechanically instead of sampled. This script resolves the
real router topology (server.go Route blocks plus the register* mount functions,
including mounts nested inside other mounts), then classifies every state-changing route
by the gate that actually guards it.

Classification of a state-changing (POST/PUT/PATCH/DELETE) route:
  commit-gate          guarded by s.require/s.requireAny, session-scoped, not excluded:
                       the commit gate locks the caller session row across the handler.
  commit-gate-excluded same, but the handler mutates the caller own session row, so it
                       must not take that same row lock.
  self-service         /api/auth/v1 self-service surface (login, logout, workspace switch,
                       password change, device and session self-management).
  actions-bearer       /api/v1 GPT Actions surface: Bearer API key, system principal, no
                       session workspace exists.
  unguarded            anything else: printed explicitly, never silently accepted.

Usage: python3 scripts/s7/scope_commit_coverage.py [--json OUT] [--md OUT]
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

EXCLUDED_PERMISSIONS = {
    "admin:session:revoke": "revokes the session row named in the path; a self-targeted call would queue behind the gate own row lock",
}
RE_ROUTE = re.compile(r'^\s*(\w+)\.Route\("([^"]+)"')
RE_METHOD = re.compile(r'^\s*(\w+)\.(Get|Post|Put|Patch|Delete)\("([^"]+)"\s*,\s*(.*)$')
RE_MOUNT = re.compile(r"s\.(register\w+)\(")
RE_FUNC = re.compile(r"^func \(s \*Server\) (\w+)\(\w+ chi\.Router\)")
RE_PERM = re.compile(r'"([a-z0-9][a-z0-9:_\-]{2,})"')


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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", default="")
    ap.add_argument("--md", default="")
    args = ap.parse_args()
    sources = {p.name: p.read_text(encoding="utf-8").split("\n") for p in sorted(API.glob("*.go")) if not p.name.endswith("_test.go")}

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
    for f in func_bodies:
        if f not in mounts:
            print("UNRESOLVED_MOUNT_FUNC=" + f)

    for e in entries:
        tail = e["tail"]
        perms = [p for p in RE_PERM.findall(tail) if ":" in p]
        path = e["path"]
        if "s.require(" in tail or "s.requireAny(" in tail:
            hit = next((p for p in perms if p in EXCLUDED_PERMISSIONS), "")
            e["classification"] = "commit-gate-excluded" if hit else "commit-gate"
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
    counts = {}
    for e in writes:
        counts[e["classification"]] = counts.get(e["classification"], 0) + 1
    print("registered_routes_total=" + str(len(entries)))
    print("state_changing_routes=" + str(len(writes)))
    for k in sorted(counts):
        print("  " + k + "=" + str(counts[k]))
    sess_guarded = counts.get("commit-gate", 0) + counts.get("commit-gate-excluded", 0)
    print("session_permission_guarded_writes=" + str(sess_guarded))
    print("--- excluded (session-row self mutation) ---")
    for e in writes:
        if e["classification"] == "commit-gate-excluded":
            print("  " + e["method"] + " " + e["path"] + " perm=" + e["permission"])
    print("--- unguarded writes (must be justified) ---")
    for e in writes:
        if e["classification"] == "unguarded":
            print("  " + e["method"] + " " + e["path"])
    if args.json:
        Path(args.json).write_text(json.dumps({
            "registered_routes_total": len(entries),
            "state_changing_routes": len(writes),
            "counts": counts,
            "mounts": mounts,
            "excluded_permissions": EXCLUDED_PERMISSIONS,
            "routes": writes,
        }, ensure_ascii=False, indent=2), encoding="utf-8")
    if args.md:
        lines = ["# P1-A commit-gate route coverage (mechanically enumerated)", "",
                 "- registered routes: " + str(len(entries)),
                 "- state-changing routes: " + str(len(writes)), ""]
        for k in sorted(counts):
            lines.append("- " + k + ": " + str(counts[k]))
        lines += ["", "| method | path | classification | permission |",
                  "| --- | --- | --- | --- |"]
        for e in sorted(writes, key=lambda x: (x["path"], x["method"])):
            lines.append("| " + e["method"] + " | `" + e["path"] + "` | " + e["classification"] + " | " + e["permission"] + " |")
        Path(args.md).write_text("\n".join(lines) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
