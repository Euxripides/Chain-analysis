#!/usr/bin/env python3
"""P3 mechanical seam coverage for the AUTH-007 request-transaction seam.

The commit gate carries one PostgreSQL transaction in the request context and
every store reaches PostgreSQL through internal/dbtx.Pool, whose
Exec/Query/QueryRow/SendBatch/CopyFrom/Begin route to that transaction. The
all-or-nothing promise therefore holds only while:

  * no request-reachable code path opens its own connection or its own pool,
  * the deliberate exceptions are exactly the audit writer, the dbtx seam itself
    and the commit gate's dedicated pool,
  * audit writes never join the request transaction (a rolled-back request must
    still leave its deny row, and the hash-chain head must not stay locked for
    the length of a handler).

This script checks all three mechanically against the real source and prints
PASS/FAIL lines. It is static; the live proof lives in
scripts/s7/p3-seam-live-gate-v1.sh.

Usage: python3 scripts/s7/p3_seam_coverage.py [--root DIR] [--json OUT]
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

RAW_POOL_ALLOW = {
    "backend/internal/dbtx/txpool.go": "the seam itself: it embeds *pgxpool.Pool and is the only package allowed to own one for request traffic",
    "backend/internal/audit/writer.go": "audit writes must land even when the business transaction is rolled back",
    "backend/internal/audit/chain.go": "same audit exception: the hash-chain head writer keeps the raw pool on purpose",
    "backend/internal/api/scopecommit_gate.go": "the commit gate owns a dedicated pool so its transaction can never starve the request pool; the gate is the seam controller, not a request path",
}

# Raw-pool construction sites that are allowed, with the reason.
POOL_CONSTRUCT_ALLOW = {
    "backend/internal/store/db.go": "the one application pool; it is wrapped by dbtx.New before any service sees it",
    "backend/internal/api/scopecommit_gate.go": "the dedicated commit-gate pool",
}

# Files allowed to call .Raw() on a wrapped pool.
RAW_CALL_ALLOW = {
    "backend/internal/app/app.go": "startup only: the migration runner needs the unwrapped pool before any request exists",
}

# Files allowed to call Acquire on a pool they own.
ACQUIRE_ALLOW = {
    "backend/internal/api/scopecommit_gate.go": "the gate takes one connection from its OWN dedicated pool outside any request transaction; dbtx.Pool.Acquire fails closed inside one",
}

SEAM_MIN_SERVICES = 21

RE_TYPE_RAW = re.compile(r"\*pgxpool\.Pool\b")
RE_TYPE_WRAP = re.compile(r"\*dbtx\.Pool\b")
# A call through a wrapped pool instance, e.g. s.deps.Store.Pool.Exec(...)
RE_WRAPPED_CALL = re.compile(r"\b(?:[A-Za-z_][\w]*\.)*Pool\.(Exec|Query|QueryRow|SendBatch|CopyFrom|Begin|BeginTx|Acquire)\(")
RE_ACQUIRE = re.compile(r"\b([A-Za-z_][\w]*)\.Acquire\(")
RE_RAW_CALL = re.compile(r"\b([A-Za-z_][\w]*)\.Raw\(\)")
RE_CONSTRUCT = re.compile(r"pgxpool\.(NewWithConfig|New|Connect|ParseConfig)\b")
RE_EMIT_AUDIT_DEF = re.compile(r"^func \(s \*Server\) emitAudit\(")
RE_WITHOUT_TX = re.compile(r"dbtx\.WithoutTx\(")


class Report:
    def __init__(self) -> None:
        self.p = 0
        self.f = 0
        self.lines = []

    def ok(self, msg: str) -> None:
        self.p += 1
        self.lines.append("PASS " + msg)

    def bad(self, msg: str) -> None:
        self.f += 1
        self.lines.append("FAIL " + msg)

    def note(self, msg: str) -> None:
        self.lines.append(msg)

    def chk(self, cond: bool, msg: str) -> None:
        (self.ok if cond else self.bad)(msg)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=str(Path(__file__).resolve().parents[2]))
    ap.add_argument("--json")
    args = ap.parse_args()
    root = Path(args.root).resolve()
    internal = root / "backend" / "internal"

    rep = Report()
    raw_sites: list[dict] = []
    wrap_sites: list[dict] = []
    wrapped_calls: dict[str, int] = {}
    acquire_sites: list[dict] = []
    raw_call_sites: list[dict] = []
    construct_sites: list[dict] = []
    seam_files: set[str] = set()
    emit_audit_defs: list[dict] = []
    emit_audit_without_tx = False

    for path in sorted(internal.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        rel = path.relative_to(root).as_posix()
        text = path.read_text(encoding="utf-8", errors="replace")
        for i, line in enumerate(text.splitlines(), 1):
            stripped = line.strip()
            if stripped.startswith("//"):
                continue
            if RE_TYPE_RAW.search(line):
                raw_sites.append({"file": rel, "line": i, "code": stripped,
                                  "allowed": rel in RAW_POOL_ALLOW})
            if RE_TYPE_WRAP.search(line):
                wrap_sites.append({"file": rel, "line": i, "code": stripped})
                seam_files.add(rel)
            for m in RE_WRAPPED_CALL.finditer(line):
                wrapped_calls[m.group(1)] = wrapped_calls.get(m.group(1), 0) + 1
            for m in RE_ACQUIRE.finditer(line):
                acquire_sites.append({"file": rel, "line": i, "recv": m.group(1), "code": stripped})
            for m in RE_RAW_CALL.finditer(line):
                raw_call_sites.append({"file": rel, "line": i, "recv": m.group(1), "code": stripped})
            for m in RE_CONSTRUCT.finditer(line):
                construct_sites.append({"file": rel, "line": i, "call": m.group(1), "code": stripped})
            if RE_EMIT_AUDIT_DEF.match(line):
                emit_audit_defs.append({"file": rel, "line": i})
                body = text.splitlines()[i - 1:i + 25]
                emit_audit_without_tx = any(RE_WITHOUT_TX.search(b) for b in body)

    rep.note("=== 1. raw *pgxpool.Pool sites (every one needs a written reason) ===")
    for s in raw_sites:
        rep.note("  %-52s line %-5d allowed=%s" % (s["file"], s["line"], s["allowed"]))
    unclassified_raw = [s for s in raw_sites if not s["allowed"]]
    rep.note("RAW_POOL_SITES=%d UNCLASSIFIED=%d" % (len(raw_sites), len(unclassified_raw)))
    rep.chk(not unclassified_raw,
            "every *pgxpool.Pool declaration outside the audit/gate/seam exceptions is absent")
    rep.chk(any(s["file"] == "backend/internal/dbtx/txpool.go" for s in raw_sites),
            "the seam itself still owns the only general-purpose wrapped pool type")

    rep.note("=== 2. services wired to the seam ===")
    for f in sorted(seam_files):
        rep.note("  %s" % f)
    rep.note("SEAM_FILES=%d SEAM_SITES=%d" % (len(seam_files), len(wrap_sites)))
    rep.chk(len(seam_files) >= SEAM_MIN_SERVICES,
            "at least %d packages hold *dbtx.Pool, not a raw pool (%d)" % (SEAM_MIN_SERVICES, len(seam_files)))

    rep.note("=== 3. calls that go through the wrapper ===")
    total = 0
    for k in sorted(wrapped_calls):
        total += wrapped_calls[k]
        rep.note("  %-10s %d" % (k, wrapped_calls[k]))
    rep.note("WRAPPED_POOL_CALL_SITES=%d" % total)
    rep.chk(total >= 300, "the wrapped-pool call sites are the ones the seam covers (%d)" % total)

    rep.note("=== 4. escape hatches ===")
    # An escape hatch is an Acquire on something that really is a PostgreSQL pool
    # (a field declared *pgxpool.Pool or *dbtx.Pool in the same file), not on the
    # unrelated provider rate/concurrency limiters that happen to share the name.
    pool_fields: dict[str, set] = {}
    for s in raw_sites + wrap_sites:
        m = re.search(r"([A-Za-z_]\w*)\s+\*pgxpool\.Pool|([A-Za-z_]\w*)\s+\*dbtx\.Pool", s["code"])
        if m:
            pool_fields.setdefault(s["file"], set()).add(m.group(1) or m.group(2))
    bad_acquire = [s for s in acquire_sites
                   if s["recv"] in pool_fields.get(s["file"], set()) and s["file"] not in ACQUIRE_ALLOW]
    for s in acquire_sites:
        rep.note("  %s:%d %s.Acquire" % (s["file"], s["line"], s["recv"]))
    rep.chk(not bad_acquire, "no request path acquires a second connection from a pooled wrapper")
    bad_raw = [s for s in raw_call_sites if s["file"] not in RAW_CALL_ALLOW]
    for s in raw_call_sites:
        rep.note("  %s:%d %s.Raw() allowed=%s" % (s["file"], s["line"], s["recv"], s["file"] in RAW_CALL_ALLOW))
    rep.chk(not bad_raw, "every .Raw() call site is the allow-listed startup migration path")
    bad_construct = [s for s in construct_sites if s["file"] not in POOL_CONSTRUCT_ALLOW]
    for s in construct_sites:
        rep.note("  %s:%d pgxpool.%s allowed=%s" % (s["file"], s["line"], s["call"], s["file"] in POOL_CONSTRUCT_ALLOW))
    rep.chk(not bad_construct, "the only pools constructed are the application pool and the dedicated gate pool")

    rep.note("=== 5. audit writes must never join the request transaction ===")
    for d in emit_audit_defs:
        rep.note("  emitAudit defined at %s:%d" % (d["file"], d["line"]))
    rep.chk(len(emit_audit_defs) == 1, "there is exactly one audit funnel")
    rep.chk(emit_audit_without_tx, "the audit funnel strips the request transaction with dbtx.WithoutTx")
    rep.chk(any(s["file"] == "backend/internal/audit/writer.go" for s in raw_sites),
            "the audit writer keeps the raw pool so a deny row survives the rollback it records")

    rep.note("=== summary ===")
    rep.note("PASS=%d FAIL=%d" % (rep.p, rep.f))
    rep.note("SEAM_FAIL=%d" % rep.f)
    print("\n".join(rep.lines))

    if args.json:
        Path(args.json).write_text(json.dumps({
            "raw_pool_sites": raw_sites,
            "unclassified_raw_pools": unclassified_raw,
            "seam_files": sorted(seam_files),
            "wrapped_pool_calls": wrapped_calls,
            "wrapped_pool_calls_total": total,
            "acquire_sites": acquire_sites,
            "raw_call_sites": raw_call_sites,
            "construct_sites": construct_sites,
            "pass": rep.p,
            "fail": rep.f,
        }, indent=2, sort_keys=True), encoding="utf-8")
    return 0 if rep.f == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
