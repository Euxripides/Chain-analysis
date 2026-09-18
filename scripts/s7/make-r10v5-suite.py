#!/usr/bin/env python3
# make-r10v5-suite.py -- R10 v9 authoritative round: v5 spec/config generated from the v4 files.
#
# Why v5: the v4 spec RAN in the v9 SELFTEST round (evidence r10-export-excelize-v9-SELFTEST-20260913T070254Z)
# and exposed ONE real measurement error in MY test code: I asserted the fresh-create status as 202
# (read from writeJSON(w, 202, exp)), but the measured value is 201 for a genuinely new export and
# 200 for the reuse path. Evidence, not code reading, decides: v5 asserts 201.
#   measured 201: v8 uc-desktop/r10-02-evidence.json (createHttp.status=201)
#                 v9-SELFTEST uc-desktop/r10-01-evidence.json (=201, fresh include_meta export)
#                 v9-SELFTEST uc-desktop/r10-02-evidence.json (=201)
#   measured 200: v8 + v9-SELFTEST uc-mobile/r10-01-evidence.json (reuse of the same export id)
# No other assertion is relaxed: everything else is byte-identical to v4.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
JOBS = []


def add(src, dst, expect, patches, note):
    JOBS.append({"src": src, "dst": dst, "expect": expect, "patches": patches, "note": note})


add(R + "/frontend/tests/r10v4-export-excelize.spec.ts",
    R + "/frontend/tests/r10v5-export-excelize.spec.ts",
    "e7220ae06f2900fac50fb448e02ca840929e4e9e2c2e2092121da55e6ee2f0df",
    [
      ("v5-header",
       " * v4（本文件）：R10 v9 施工单（1420 交接 §5）。",
       """ * v5（本文件）：v4 的**测量侧**更正 —— 由 v9 SELFTEST 轮的真实结果驱动，只改一处期望值。
 *   v4 实测（轮 r10-export-excelize-v9-SELFTEST-20260913T070254Z）暴露：我把"全新导出"的状态码
 *   写成 202（照 export_v2.go 的 writeJSON(w, 202, exp) 读出来的），但**真实测量值是 201**：
 *     r10-02 uc-desktop createHttp.status = 201（v8 轮与 v9 自测轮一致）
 *     r10-01 uc-desktop createHttp.status = 201（v9 自测轮的真实新建导出）
 *     r10-01 uc-mobile  createHttp.status = 200（复用同一个 export id）
 *   => v5 断言 201=新建、200=复用（**以实测为准，不以读码为准**）。其余逐字节与 v4 相同。
 *
 * v4（本文件）：R10 v9 施工单（1420 交接 §5）。"""),
      ("v5-status",
       '      "全新导出必须走新建路径（202）。复用路径（200，export_v2.go 命中 query_snapshot 完全一致的历史导出）" +',
       '      "全新导出必须走新建路径（201，实测值；v4 曾误写 202）。复用路径（200，export_v2.go 命中 query_snapshot 完全一致的历史导出）" +'),
      ("v5-tobe",
       "    ).toBe(202);",
       "    ).toBe(201);"),
    ],
    "v4 export spec -> v5 (status 202 -> measured 201)")

add(R + "/frontend/tests/r10v4-legacy-export-import.spec.ts",
    R + "/frontend/tests/r10v5-legacy-export-import.spec.ts",
    "29370531314d4585ed38a878ad0dddf6c836b2af5f60daa0e1bd00c8ea0d7251",
    [], "legacy spec byte-identical copy")

add(R + "/frontend/playwright.r10v4.config.ts",
    R + "/frontend/playwright.r10v5.config.ts",
    "a33c9218fa01c98746f956b3a3f18c23b3d2d0ff21d0ffb5726aeb4e52db82d6",
    [
      ("cfg-title", " * R10 v4 — 服务端 excelize XLSX 导出", " * R10 v5 — 服务端 excelize XLSX 导出"),
      ("cfg-note",
       " * v3（playwright.r10v3.config.ts）原样保留，不 hot-edit。v4 相对 v3 只有两处机械变更：",
       " * v4（playwright.r10v4.config.ts）原样保留，不 hot-edit。v5 相对 v4 只有两处机械变更："),
      ("cfg-match", "  testMatch: /r10v4-.*\\.spec\\.ts/,", "  testMatch: /r10v5-.*\\.spec\\.ts/,"),
      ("cfg-outdir", '"test-results-r10v4-unspecified"', '"test-results-r10v5-unspecified"'),
    ],
    "v4 config -> v5 testMatch")

PLAN = {}
for job in JOBS:
    raw = open(job["src"], "rb").read()
    sha = hashlib.sha256(raw).hexdigest()
    print("src=%s sha256=%s" % (job["src"], sha))
    if sha != job["expect"]:
        print("FATAL baseline sha mismatch for %s (want %s)" % (job["src"], job["expect"]))
        sys.exit(3)
    text = raw.decode("utf-8")
    for tag, old, new in job["patches"]:
        n = text.count(old)
        if n != 1:
            print("FATAL patch %s@%s matched %d times (want 1)" % (tag, os.path.basename(job["src"]), n))
            sys.exit(4)
        text = text.replace(old, new, 1)
        print("  applied %s" % tag)
    PLAN[job["src"]] = (text, job["dst"], job["note"])

for src, (text, dst, note) in PLAN.items():
    if os.path.exists(dst):
        print("FATAL destination exists (refusing to overwrite): %s" % dst)
        sys.exit(5)
for src, (text, dst, note) in PLAN.items():
    with open(dst, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(text)
    out = open(dst, "rb").read()
    print("dst=%s note=%s bytes=%d sha256=%s" % (dst, note, len(out), hashlib.sha256(out).hexdigest()))
print("OK")
