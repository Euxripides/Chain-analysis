#!/usr/bin/env python3
# make-run-r10-v9b.py -- R10 v9 authoritative-round runner, generated from run-r10-export-excelize-v9.sh.
#
# v9 (sha e461c875...) ran ONCE, as the deliberately-broken SELFTEST round; its bytes are preserved in that
# round's evidence dir. v9b is a NEW file (no in-place edit of anything that ran) with three mechanical
# changes only:
#   1) point at the v5 spec/config (v5 = v4 with the fresh-create status corrected 202 -> measured 201),
#   2) P0d asserts toBe(201) instead of toBe(202),
#   3) P0d SPEC_PATH/CFG_PATH/testMatch greps follow v5.
# The EVIDENCE DIRECTORY NAME STAYS r10-export-excelize-v9-<TS>: the authoritative round identity that the
# handover asked for is v9. No assertion, gate or freeze rule is changed.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/scripts/s7/run-r10-export-excelize-v9.sh"
DST = sys.argv[2] if len(sys.argv) > 2 else R + "/scripts/s7/run-r10-export-excelize-v9b.sh"
EXPECT_SRC_SHA = "e461c875d2c517e469aec5f2605d938b6018f6ebbd69893d2ef3cfb7cd544874"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL source v9 runner sha mismatch (refusing to patch an unexpected baseline)")
    sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("spec-path",
  'SPEC_PATH="$R/frontend/tests/r10v4-export-excelize.spec.ts"',
  'SPEC_PATH="$R/frontend/tests/r10v5-export-excelize.spec.ts"'))
P.append(("cfg-path",
  'CFG_PATH="$R/frontend/playwright.r10v4.config.ts"',
  'CFG_PATH="$R/frontend/playwright.r10v5.config.ts"'))
P.append(("cfg-match",
  "  grep -qF 'testMatch: /r10v4-.*\\.spec\\.ts/' \"$CFG_PATH\" || { echo \"SPEC_SELFCHECK config testMatch 未命中 r10v4\"; SPEC_OK=0; }",
  "  grep -qF 'testMatch: /r10v5-.*\\.spec\\.ts/' \"$CFG_PATH\" || { echo \"SPEC_SELFCHECK config testMatch 未命中 r10v5\"; SPEC_OK=0; }"))
P.append(("status-201",
  "  grep -q 'toBe(202)' \"$SPEC_PATH\" || { echo \"SPEC_SELFCHECK spec 缺少 新建=202/复用=200 断言\"; SPEC_OK=0; }",
  "  grep -q 'toBe(201)' \"$SPEC_PATH\" || { echo \"SPEC_SELFCHECK spec 缺少 新建=201/复用=200 断言\"; SPEC_OK=0; }"))
P.append(("playwright-config",
  '--config playwright.r10v4.config.ts > "$EV/playwright.console.log" 2>&1',
  '--config playwright.r10v5.config.ts > "$EV/playwright.console.log" 2>&1'))
P.append(("p0d-label",
  'echo "=== P0d spec/config 静态自检（testMatch 命中 v4 spec；R10-09 在场；"地址维度不可复用"的源码依据成立） ==="',
  'echo "=== P0d spec/config 静态自检（testMatch 命中 v5 spec；R10-09 在场；新建=201/复用=200；地址维度不可复用的源码依据成立） ==="'))
P.append(("header-v9b",
  "# v9（本文件）：R10 v8 失败轮的**测量侧**更正轮 + 审计修复的运行认证轮。",
  r'''# v9b（本文件）：v9 的**第 2 版**。v9 只跑过一次，且是刻意破坏 summary 的 SELFTEST 轮
# （证据 r10-export-excelize-v9-SELFTEST-20260913T070254Z，该轮 script_sha256=e461c875…，字节已留档）。
# 该自测轮暴露了 v9 测试侧的一处**真实测量错误**：R10-09 把"全新导出"的状态码断言成 202
# （照 export_v2.go 的 writeJSON(w, 202, exp) 读码得出），实测是 **201**：
#   v8 轮 uc-desktop/r10-02 createHttp.status=201；v9 自测轮 r10-01/r10-02 亦为 201；
#   复用路径（同一 export id）为 200（v8 与 v9 自测轮的 uc-mobile/r10-01 均如此）。
# v9b 只做机械变更（不改任何判据/gate/冻结规则）：
#   M1 指向 v5 spec/config（v5 = v4 仅把该状态码期望改为**实测的 201**）；
#   M2 P0d 断言 toBe(201) 并在标签里写明"新建=201/复用=200"；
#   M3 证据目录名**仍是 r10-export-excelize-v9-<TS>**（权威轮身份就是 v9，不变）。
#
# v9（本文件）：R10 v8 失败轮的**测量侧**更正轮 + 审计修复的运行认证轮。'''))

for tag, old, new in P:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want 1)" % (tag, n)); sys.exit(4)
    text = text.replace(old, new, 1); print("applied %s" % tag)

if os.path.exists(DST):
    print("FATAL destination exists (refusing to overwrite): %s" % DST); sys.exit(5)
open(DST, "w", encoding="utf-8", newline="\n").write(text)
out = open(DST, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (DST, len(out), hashlib.sha256(out).hexdigest()))
