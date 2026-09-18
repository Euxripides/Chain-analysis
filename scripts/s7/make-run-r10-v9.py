#!/usr/bin/env python3
# make-run-r10-v9.py — 由 run-r10-export-excelize-v8.sh（sha da944c0c…）**机械生成** v9。
# 每个补丁要求恰好匹配一次，否则 fail-closed 退出且不写文件。
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/scripts/s7/run-r10-export-excelize-v8.sh"
DST = sys.argv[2] if len(sys.argv) > 2 else R + "/scripts/s7/run-r10-export-excelize-v9.sh"
EXPECT_SRC_SHA = "da944c0c2c7a04e20117d0ecc9b2c9b7cd4c84ec3363f9d2500253271fee3a25"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL source v8 runner sha mismatch (refusing to patch an unexpected baseline)")
    sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("ev-dir",
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v8-$TS}"',
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v9-$TS}"'))
P.append(("ev-dir-selftest",
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v8-SELFTEST-$TS}"',
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v9-SELFTEST-$TS}"'))
P.append(("crosscheck",
  'R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v7.py}"',
  '# R10 v9：crosscheck v8 = v7 + 数值排序修复(X1) + 完成事件作用域感知(X2) + 同一次发生证明(X2b) + 认证核心(X3)。\n'
  'R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v8.py}"'))

P.append(("p0d-selfcheck",
  'echo "=== P1 pre-state（PK 快照，供原 PK 子集核验） ==="',
  r'''echo "=== P0d spec/config 静态自检（testMatch 命中 v4 spec；R10-09 在场；"地址维度不可复用"的源码依据成立） ==="
SPEC_PATH="$R/frontend/tests/r10v4-export-excelize.spec.ts"
CFG_PATH="$R/frontend/playwright.r10v4.config.ts"
SPEC_OK=1
[ -f "$SPEC_PATH" ] || { echo "SPEC_SELFCHECK missing spec $SPEC_PATH"; SPEC_OK=0; }
[ -f "$CFG_PATH" ] || { echo "SPEC_SELFCHECK missing config $CFG_PATH"; SPEC_OK=0; }
if [ -f "$CFG_PATH" ]; then
  grep -qE 'testMatch: /r10v4-.*\.spec\.ts/' "$CFG_PATH" || { echo "SPEC_SELFCHECK config testMatch 未命中 r10v4"; SPEC_OK=0; }
fi
if [ -f "$SPEC_PATH" ]; then
  grep -q 'R10-09 全新导出' "$SPEC_PATH" || { echo "SPEC_SELFCHECK spec 缺少 R10-09 用例"; SPEC_OK=0; }
  grep -q 'toBe(202)' "$SPEC_PATH" || { echo "SPEC_SELFCHECK spec 缺少 新建=202/复用=200 断言"; SPEC_OK=0; }
fi
# 静态依据（不是"跑一次看看"）：export_v2.go 的复用分支写在 if job != nil 之内，
# 而 job 只在请求携带 job_id 时加载 => 地址维度导出（不带 job_id）在服务端**结构上**不可能复用。
grep -q 'if req.JobID != "" {' "$R/backend/internal/api/export_v2.go" || { echo "SPEC_SELFCHECK export_v2.go 缺 job_id 守卫"; SPEC_OK=0; }
grep -q 'if job != nil {' "$R/backend/internal/api/export_v2.go" || { echo "SPEC_SELFCHECK export_v2.go 缺复用分支守卫"; SPEC_OK=0; }
echo "spec_sha256=$(sha256sum "$SPEC_PATH" 2>/dev/null | cut -d' ' -f1) config_sha256=$(sha256sum "$CFG_PATH" 2>/dev/null | cut -d' ' -f1) spec_selfcheck=$SPEC_OK" | tee -a "$EV/run-meta.txt"
if [ "$SPEC_OK" -ne 1 ]; then echo "SPEC_SELFCHECK_FAILED - refusing to run (no green from missing/invalid spec)"; exit 4; fi

echo "=== P1 pre-state（PK 快照，供原 PK 子集核验） ==="'''))

P.append(("playwright-config",
  'node node_modules/@playwright/test/cli.js test --config playwright.r10v3.config.ts > "$EV/playwright.console.log" 2>&1',
  'node node_modules/@playwright/test/cli.js test --config playwright.r10v4.config.ts > "$EV/playwright.console.log" 2>&1'))

P.append(("p4b-label",
  'echo "=== P4b 审计链独立闸门（v8 新增：export.completed 真实行 + request 不可伪造 + 可信 2344+ 链） ==="',
  'echo "=== P4b 审计链独立闸门（v9 沿用：冻结源码构建的 auditverify 做密码学校验 + 链头==库中最大 seq 行 + 可信 2344+ 链） ==="'))

P.append(("freeze-note",
  '# R10 v8：运行身份 = **含 D-R10V4-01 两面修复**的冻结产物（writeAuditCtx 写 v2 链 + request 维度真实）。',
  '# R10 v9：运行身份与 v8 相同（**不部署、不热改**）：484e2707 = 含 D-R10V4-01 两面修复的冻结产物。\n'
  '# R10 v8：运行身份 = **含 D-R10V4-01 两面修复**的冻结产物（writeAuditCtx 写 v2 链 + request 维度真实）。'))

P.append(("header-note",
  '# v8（本文件）：R10 v7 的"审计完整性撤回"补课轮。裁定：v7 虽 144/0、9UI PASS，但它**没有证明**',
  r'''# v9（本文件）：R10 v8 失败轮的**测量侧**更正轮 + 审计修复的运行认证轮。
# 裁定：v8 的 5 项失败**不是产品回归**（真值在 v8 轮已由 R10-02/03 的 v2 完成事件证明），
# 但也**绝不因此记 PASS** —— 其中 3 项是我 crosscheck 的排序缺陷（"SELECT seq::text ... ORDER BY seq"
# 被 PostgreSQL 解析为按输出列(text)排序，首尾 10000/9999），2 项是断言写错了对象
# （R10-01 复用了 02:49 的**修复前**导出 f62e4308，其完成事件只在 legacy 表是历史事实）。
# v9 相对 v8 的增量（1420 交接 §5 施工单 1-5；对应 K1-K5）：
#   K1 crosscheck v8：first/last 一律 ORDER BY seq::bigint（数值排序），修掉 3 项假失败；
#   K2 export.completed **作用域感知**（不是削弱）：本轮真实新建 = 硬性要求 + 同一次发生证明
#      （v2 完成行的 request_id/trace_id 必须等于 UI 创建请求与 export.create 行的同一对 id）；
#      复用到的历史导出若已有 v2 行 = 同样按硬性判据核；若没有 v2 行 = 仅当创建于 05:55:08Z
#      审计修复部署**之前**才降级 observation（legacy 有 + 早于修复 + **绝不记 PASS**），否则硬失败；
#   K3 spec v4：R10-01 通过真实 UI 打开 include_meta，使**任务维度**导出不再复用修复前导出；
#      新增 R10-09：结构上不可能复用的**地址维度**全新导出（HTTP 202）认证用例 ——
#      落地 Export/Job/Request/Trace/文件 全部证据，供 crosscheck 硬断言"同一次完成事件"；
#   K4 runner 新增 P0d **静态自检**：testMatch 必须命中 v4 spec、R10-09 必须在场、且
#      "地址维度导出不可能复用"的两条源码依据（复用分支在 if job != nil 内 / job 仅在 job_id 非空时加载）
#      必须在 export_v2.go 里当场成立，否则 exit 4（不给缺失输入发绿）；
#   K5 矩阵扩至 11 项（R10-09 桌面+移动各一项），按设计排除仍为 7 项（uc-mobile 的其余 7 项）。
# 运行身份不变（冻结校验 + P4b 审计链闸门照旧）；旧轮/旧对象一律不改写。
#
# v8（本文件）：R10 v7 的"审计完整性撤回"补课轮。裁定：v7 虽 144/0、9UI PASS，但它**没有证明**'''))

P.append(("verdict-fail",
  'echo "R10_V8_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"',
  'echo "R10_V9_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"'))
P.append(("verdict-pass",
  'echo "R10_V8_VERDICT=PASS playwright_rc=$RC report_rc=$REPORT_RC crosscheck_rc=$XRC summary_render_rc=$SUMMARY_RC freeze_writer_rc=$FREEZE_RC audit_chain_gate_rc=$ACRC freeze_stable=$FREEZE_STABLE traceback=$TB_FOUND" | tee -a "$EV/run-meta.txt"',
  'echo "R10_V9_VERDICT=PASS playwright_rc=$RC report_rc=$REPORT_RC crosscheck_rc=$XRC summary_render_rc=$SUMMARY_RC freeze_writer_rc=$FREEZE_RC audit_chain_gate_rc=$ACRC spec_selfcheck=$SPEC_OK freeze_stable=$FREEZE_STABLE traceback=$TB_FOUND" | tee -a "$EV/run-meta.txt"'))

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
