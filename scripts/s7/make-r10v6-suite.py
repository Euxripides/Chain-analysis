#!/usr/bin/env python3
# make-r10v6-suite.py -- v6 spec/config, generated from the v5 files.
# Reason (root 15:16 strict gate): crosscheck v10 requires every genuinely_new export to carry the UI
# createHttp request_id/trace_id. R10-03's evidence (genuinely new export 3d97973f) dumped neither, so the
# strict gate reported exactly one FAIL: an EVIDENCE GAP in my spec, not a product defect. v6 records them.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
JOBS = []


def add(src, dst, expect, patches, note):
    JOBS.append({"src": src, "dst": dst, "expect": expect, "patches": patches, "note": note})


add(R + "/frontend/tests/r10v5-export-excelize.spec.ts",
    R + "/frontend/tests/r10v6-export-excelize.spec.ts",
    "44e058b93f619dbeea8b036d8b7aa4d6f3063e43ed0cc943b5e3bd2d7910211f",
    [
      ("v6-header",
       " * v5（本文件）：v4 的**测量侧**更正 —— 由 v9 SELFTEST 轮的真实结果驱动，只改一处期望值。",
       """ * v6（本文件）：补上 R10-03 的**创建请求证据**（唯一增量，其余逐字节与 v5 相同）。
 *   严格门（crosscheck v10）要求：pre-snapshot 中不存在的**每个** genuinely_new 导出，都必须记录
 *   UI 创建请求的 request_id/trace_id，以证明 audit_events_v2 的 export.completed 与创建是**同一次发生**。
 *   v5 轮里 R10-03 的导出 3d97973f 是真实新建，但没有 dump createHttp -> 严格门如实判 1 项 FAIL（证据缺口，
 *   不是产品缺陷）。v6 把 R10-03 的 createHttp/createToggles 一并落盘，闭合该缺口。
 *
 * v5（本文件）：v4 的**测量侧**更正 —— 由 v9 SELFTEST 轮的真实结果驱动，只改一处期望值。"""),
      ("v6-r1003-dump",
       '    dump(testInfo, "r10-03-evidence.json", {\n      export: detail, ticket, ticketHttp,',
       '    dump(testInfo, "r10-03-evidence.json", {\n      export: detail,\n      createHttp: created.__http,\n      createToggles: created.__toggles,\n      ticket, ticketHttp,'),
    ],
    "v5 export spec -> v6 (R10-03 records createHttp)")

add(R + "/frontend/tests/r10v5-legacy-export-import.spec.ts",
    R + "/frontend/tests/r10v6-legacy-export-import.spec.ts",
    "29370531314d4585ed38a878ad0dddf6c836b2af5f60daa0e1bd00c8ea0d7251",
    [], "legacy spec byte-identical copy")

add(R + "/frontend/playwright.r10v5.config.ts",
    R + "/frontend/playwright.r10v6.config.ts",
    "9831688014711038efefb1ae9e0f12c529c96c4c540b88faf78e5e880f0b14f2",
    [
      ("cfg-title", " * R10 v5 — 服务端 excelize XLSX 导出", " * R10 v6 — 服务端 excelize XLSX 导出"),
      ("cfg-note",
       " * v4（playwright.r10v4.config.ts）原样保留，不 hot-edit。v5 相对 v4 只有两处机械变更：",
       " * v5（playwright.r10v5.config.ts）原样保留，不 hot-edit。v6 相对 v5 只有两处机械变更："),
      ("cfg-match", "  testMatch: /r10v5-.*\\.spec\\.ts/,", "  testMatch: /r10v6-.*\\.spec\\.ts/,"),
      ("cfg-outdir", '"test-results-r10v5-unspecified"', '"test-results-r10v6-unspecified"'),
    ],
    "v5 config -> v6 testMatch")

PLAN = {}
for job in JOBS:
    raw = open(job["src"], "rb").read()
    sha = hashlib.sha256(raw).hexdigest()
    print("src=%s sha256=%s" % (job["src"], sha))
    if sha != job["expect"]:
        print("FATAL baseline sha mismatch for %s (want %s)" % (job["src"], job["expect"])); sys.exit(3)
    text = raw.decode("utf-8")
    for tag, old, new in job["patches"]:
        n = text.count(old)
        if n != 1:
            print("FATAL patch %s@%s matched %d times (want 1)" % (tag, os.path.basename(job["src"]), n)); sys.exit(4)
        text = text.replace(old, new, 1); print("  applied %s" % tag)
    PLAN[job["src"]] = (text, job["dst"], job["note"])

for src, (text, dst, note) in PLAN.items():
    if os.path.exists(dst):
        print("FATAL destination exists (refusing to overwrite): %s" % dst); sys.exit(5)
for src, (text, dst, note) in PLAN.items():
    open(dst, "w", encoding="utf-8", newline="\n").write(text)
    out = open(dst, "rb").read()
    print("dst=%s note=%s bytes=%d sha256=%s" % (dst, note, len(out), hashlib.sha256(out).hexdigest()))
print("OK")
