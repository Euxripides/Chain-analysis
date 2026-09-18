#!/usr/bin/env python3
# make-run-r10-v9c.py -- runner v9c, generated from v9b. v9b ran the authoritative v9 round; its bytes are
# preserved in that round's evidence dir. v9c = v9b pointed at the v6 spec/config and the STRICT crosscheck
# v10 (root 15:16). Evidence dir name stays r10-export-excelize-v9-<TS>. No other change.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/scripts/s7/run-r10-export-excelize-v9b.sh"
DST = sys.argv[2] if len(sys.argv) > 2 else R + "/scripts/s7/run-r10-export-excelize-v9c.sh"
EXPECT_SRC_SHA = "14e215f87aab8519e60575f0a8e3b896a54f6bbf49f3168506cdc16bdb26672d"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL source v9b runner sha mismatch"); sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("crosscheck-v10",
  'R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v8.py}"',
  '# R10 v9c：严格门 crosscheck v10（新建唯一 201；同一次发生 request/trace 三重一致；证据缺口即 FAIL）。\n'
  'R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v10.py}"'))
P.append(("spec-path", 'SPEC_PATH="$R/frontend/tests/r10v5-export-excelize.spec.ts"',
                        'SPEC_PATH="$R/frontend/tests/r10v6-export-excelize.spec.ts"'))
P.append(("cfg-path", 'CFG_PATH="$R/frontend/playwright.r10v5.config.ts"',
                       'CFG_PATH="$R/frontend/playwright.r10v6.config.ts"'))
P.append(("cfg-match",
  "  grep -qF 'testMatch: /r10v5-.*\\.spec\\.ts/' \"$CFG_PATH\" || { echo \"SPEC_SELFCHECK config testMatch 未命中 r10v5\"; SPEC_OK=0; }",
  "  grep -qF 'testMatch: /r10v6-.*\\.spec\\.ts/' \"$CFG_PATH\" || { echo \"SPEC_SELFCHECK config testMatch 未命中 r10v6\"; SPEC_OK=0; }"))
P.append(("playwright-config", '--config playwright.r10v5.config.ts > "$EV/playwright.console.log" 2>&1',
                               '--config playwright.r10v6.config.ts > "$EV/playwright.console.log" 2>&1'))
P.append(("header-v9c",
  "# v9b（本文件）：v9 的**第 2 版**。",
  r'''# v9c（本文件）：v9 的**第 3 版**，对应根 15:16 的严格门裁定。
#   v9b 跑的权威轮（r10-export-excelize-v9-20260913T070839Z）原样保留、一字未改；用严格门 crosscheck v10
#   离线复跑其冻结证据时得到 240 项 1 FAIL：r10-03 的导出 3d97973f 是真实新建，但 v5 spec 没有 dump
#   createHttp -> 严格门如实判"证据缺口"（不是产品缺陷）。v9c 因此：
#     N1 指向 v6 spec/config（v6 = v5 + R10-03 落盘 createHttp/createToggles，唯一增量）；
#     N2 crosscheck 换为严格版 v10（新建唯一 201；同一次发生 request/trace 必须三重一致；201 缺 ids 直接 FAIL）。
#   证据目录名仍是 r10-export-excelize-v9-<TS>；判据、冻结规则、P4b 审计链闸门均不变。
#
# v9b（本文件）：v9 的**第 2 版**。'''))

for tag, old, new in P:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want 1)" % (tag, n)); sys.exit(4)
    text = text.replace(old, new, 1); print("applied %s" % tag)

if os.path.exists(DST):
    print("FATAL destination exists: %s" % DST); sys.exit(5)
open(DST, "w", encoding="utf-8", newline="\n").write(text)
out = open(DST, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (DST, len(out), hashlib.sha256(out).hexdigest()))
