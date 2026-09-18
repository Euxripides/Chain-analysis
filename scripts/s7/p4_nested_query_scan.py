#!/usr/bin/env python3
"""p4_nested_query_scan.py -- find nested result sets on one PostgreSQL connection.

Why this exists
---------------
pgx allows exactly ONE in-flight statement per connection. A second statement
issued while a previous result set is still open fails with "conn busy"
(pgconn: "This only should be possible in case of an application bug"), and the
failing baseRows.Close() marks the still-open statement as invalidated in pgx's
statement cache. On a *dbtx.Pool bound to the request transaction (which the
commit gate opens) both statements run on the SAME connection, so the gate's
release re-read then fails with "failed to deallocate cached statement(s): conn
busy" and the whole request is refused 409 AUTHZ_WORKSPACE_SCOPE_STALE. That is
P4-ANCHOR-01 (internal/audit/verify.go VerifyChain).

Only multi-row result sets matter: QueryRow is closed by Scan, CopyFrom is
synchronous, so neither is tracked. A result set is considered closed when
<var>.Close() is called or when the enclosing for <var>.Next() loop ends.

Read-only, dependency-free. Usage:
    python3 scripts/s7/p4_nested_query_scan.py [--json OUT] [root]
Exit 0 when no site is found, 1 when at least one site is found.
"""
import argparse
import json
import os
import re
import sys

# openers that leave a result set that must be drained/closed
OPEN_ASSIGN = re.compile(
    r"^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*\s*)?:=\s*"
    r"([A-Za-z_][A-Za-z0-9_\.]*)\.(Query|SendBatch)\s*\(")
OPEN_ASSIGN_BARE = re.compile(
    r"^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"
    r"([A-Za-z_][A-Za-z0-9_\.]*)\.(Query|SendBatch)\s*\(")
ANY_CALL = re.compile(
    r"([A-Za-z_][A-Za-z0-9_\.]*)\.(Query|QueryRow|Exec|SendBatch|CopyFrom)\s*\(")
CLOSE_CALL = re.compile(r"([A-Za-z_][A-Za-z0-9_]*)\.Close\s*\(\s*\)")
DEFER_CLOSE = re.compile(r"^\s*defer\s+([A-Za-z_][A-Za-z0-9_]*)\.Close\s*\(\s*\)")
FOR_NEXT = re.compile(r"^\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\.Next\s*\(\s*\)")
FUNC_START = re.compile(r"^func\s")


def pool_like(receiver):
    tail = receiver.split(".")[-1]
    return tail in ("pool", "Pool", "tx", "Tx", "q", "db", "conn", "sender")


def scan_file(path):
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        lines = fh.read().split("\n")
    findings = []
    func_name = ""
    depth = 0
    in_func = False
    open_sets = {}
    loops = []
    for idx, line in enumerate(lines):
        lineno = idx + 1
        stripped = line.strip()
        if not in_func and FUNC_START.match(line):
            in_func = True
            depth = 0
            open_sets = {}
            loops = []
            m = re.match(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)", line)
            func_name = m.group(1) if m else "<anon>"
        if in_func and not stripped.startswith("//"):
            om = OPEN_ASSIGN.match(line) or OPEN_ASSIGN_BARE.match(line)
            if om:
                var = om.group(1)
                receiver = om.group(2)
                if not pool_like(receiver):
                    pass
                elif open_sets:
                    findings.append({
                        "file": path, "line": lineno, "func": func_name,
                        "kind": "opens-a-second-result-set",
                        "open": dict(open_sets),
                        "text": stripped[:160],
                    })
                    open_sets[var] = lineno
                else:
                    open_sets[var] = lineno
            elif DEFER_CLOSE.match(line):
                # defer <var>.Close() runs at function exit, so a TRACKED result
                # set stays open for every following statement of this function.
                # A deferred closer for anything that was never tracked as a
                # result set (pool.Close, store.Close, tx.Rollback) is ignored.
                pass
            else:
                for cm in CLOSE_CALL.finditer(line):
                    var = cm.group(1)
                    open_sets.pop(var, None)
                if open_sets:
                    for call in ANY_CALL.finditer(line):
                        if not pool_like(call.group(1)):
                            continue
                        findings.append({
                            "file": path, "line": lineno, "func": func_name,
                            "kind": "statement-while-result-set-open",
                            "open": dict(open_sets),
                            "text": stripped[:160],
                        })
                        break
            fm = FOR_NEXT.match(line)
            if fm:
                loops.append((fm.group(1), depth))
        if in_func:
            depth += line.count("{") - line.count("}")
            while loops and depth <= loops[-1][1]:
                open_sets.pop(loops.pop()[0], None)
            if depth <= 0:
                in_func = False
                open_sets = {}
                loops = []
    return findings


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("root", nargs="?", default="backend")
    ap.add_argument("--json", default="")
    args = ap.parse_args()

    files = []
    for dirpath, dirnames, filenames in os.walk(args.root):
        dirnames[:] = [d for d in dirnames if d not in (".git", "node_modules", "bin", "bin.new")]
        for name in filenames:
            if name.endswith(".go") and not name.endswith("_test.go"):
                files.append(os.path.join(dirpath, name))
    files.sort()

    all_findings = []
    for path in files:
        all_findings.extend(scan_file(path))

    print("GO_FILES_SCANNED=%d" % len(files))
    print("NESTED_SITES=%d" % len(all_findings))
    by_file = {}
    for f in all_findings:
        by_file.setdefault(f["file"], []).append(f)
    for path in sorted(by_file):
        print("  %s" % path)
        for f in by_file[path]:
            print("    L%d %s func=%s open=%s" % (f["line"], f["kind"], f["func"], f["open"]))
            print("      %s" % f["text"])
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump({"files_scanned": len(files), "findings": all_findings}, fh,
                      ensure_ascii=True, indent=1)
        print("JSON=%s" % args.json)
    return 1 if all_findings else 0


if __name__ == "__main__":
    sys.exit(main())
