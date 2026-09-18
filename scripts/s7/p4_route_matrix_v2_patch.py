#!/usr/bin/env python3
"""Apply the P4 matrix runner v2 fixes onto a copy of v1.

Every replacement must match EXACTLY ONCE: a runner defect that is silently not
applied would look like a product result. v1 and its evidence are never
rewritten; v2 is a new artifact that records which defects it fixes.

The pairs live in scripts/s7/p4_route_matrix_v2_pairs.py (loaded as a module so
that no line of the file is longer than a shell or editor can carry).
"""
import importlib.util
import os
import sys

R = "/home/euripides/projects/blockchain-platform"
SRC = R + "/scripts/s7/p4_route_matrix_gate_v1.py"
DST = R + "/scripts/s7/p4_route_matrix_gate_v2.py"
PAIRS_PY = R + "/scripts/s7/p4_route_matrix_v2_pairs.py"


def load_pairs():
    spec = importlib.util.spec_from_file_location("p4_v2_pairs", PAIRS_PY)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod.PAIRS


def main():
    with open(SRC, encoding="utf-8") as fh:
        src = fh.read()
    pairs = load_pairs()
    out = src
    for idx, item in enumerate(pairs):
        old, new = item[0], item[1]
        n = out.count(old)
        if n != 1:
            head = old.splitlines()[0][:90] if old else ""
            print("ANCHOR_%d_COUNT=%d expected exactly 1 :: %s" % (idx, n, head))
            return 2
        out = out.replace(old, new)
    out = out.replace("(v1).", "(v2).", 1)
    with open(DST, "w", encoding="utf-8", newline=chr(10)) as fh:
        fh.write(out)
    print("PATCHED_OK pairs=%d bytes=%d" % (len(pairs), len(out)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
