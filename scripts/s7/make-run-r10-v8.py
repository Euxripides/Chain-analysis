#!/usr/bin/env python3
# make-run-r10-v8.py — 由 run-r10-export-excelize-v7.sh（sha 前缀 c0a9247d）**机械生成** v8。
# 每个补丁要求恰好匹配一次，否则 fail-closed 退出。
import hashlib, sys

SRC = sys.argv[1] if len(sys.argv) > 1 else "/home/euripides/projects/blockchain-platform/scripts/s7/run-r10-export-excelize-v7.sh"
DST = sys.argv[2] if len(sys.argv) > 2 else "/home/euripides/projects/blockchain-platform/scripts/s7/run-r10-export-excelize-v8.sh"
EXPECT_SRC_SHA = sys.argv[3] if len(sys.argv) > 3 else "c0a9247d95bf"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if not sha.startswith(EXPECT_SRC_SHA):
    print("FATAL source v7 runner sha mismatch (want prefix %s)" % EXPECT_SRC_SHA)
    sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("ev-dir",
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v7-$TS}"',
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v8-$TS}"'))
P.append(("ev-dir-selftest",
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v7-SELFTEST-$TS}"',
  'EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v8-SELFTEST-$TS}"'))
P.append(("freeze-sha",
  'FREEZE_API_SHA="${FREEZE_API_SHA:-4ec819add8efa61f7d5f25e8d499ce9bef712f17a1c36293ee08f458fb0e3978}"',
  '# R10 v8：运行身份 = **含 D-R10V4-01 两面修复**的冻结产物（writeAuditCtx 写 v2 链 + request 维度真实）。\n'
  '# 前一候选 569f40ec 已被定点实测判为不合格（export.completed 的 route 是伪造的 /api/auth/v1/refresh）。\n'
  'FREEZE_API_SHA="${FREEZE_API_SHA:-484e2707e6574cce10e41e68047176ff5255aa231d1615c4b10cddd3fe5db2e4}"'))
P.append(("crosscheck",
  'R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v6.py}"',
  'R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v7.py}"\n'
  '# 冻结源码构建的验链器（auditverify）与审计链闸门脚本（v8 新增阶段 P4b）。\n'
  'AUDITVERIFY_SRC="${R10_AUDITVERIFY:-$R/acceptance-evidence/dual-system-v1/sprint7/r10v8-auditrequest-build-20260913T060129Z/auditverify.build}"\n'
  'AUDIT_CHAIN_GATE="${R10_AUDIT_CHAIN_GATE:-$R/scripts/s7/r10-audit-chain-gate-v1.sh}"'))

P.append(("p4b-stage",
'''echo "=== P5 summary 渲染''',
'''echo "=== P4b 审计链独立闸门（v8 新增：export.completed 真实行 + request 不可伪造 + 可信 2344+ 链） ==="
if [ ! -x "$AUDITVERIFY_SRC" ]; then
  echo "AUDIT_GATE_FAILCLOSED missing frozen auditverify: $AUDITVERIFY_SRC" | tee "$EV/p4b-audit-chain-gate.out"
  ACRC=97
else
  mkdir -p "$EV/audit-chain"
  cp -p "$AUDITVERIFY_SRC" "$EV/audit-chain/auditverify.build"
  echo "auditverify_src=$AUDITVERIFY_SRC sha256=$(sha256sum "$AUDITVERIFY_SRC" | cut -d' ' -f1)" | tee "$EV/p4b-audit-chain-gate.out"
  bash "$AUDIT_CHAIN_GATE" "$EV/audit-chain" "$FREEZE_API_SHA" >> "$EV/p4b-audit-chain-gate.out" 2>&1
  ACRC=$?
fi
tail -n 30 "$EV/p4b-audit-chain-gate.out"
echo "audit_chain_gate_rc=$ACRC" >> "$EV/gate.txt"

echo "=== P5 summary 渲染'''))

P.append(("gate-wire",
'''gate_chk freeze-writer "$FREEZE_RC"''',
'''gate_chk freeze-writer "$FREEZE_RC"
gate_chk audit-chain-gate "$ACRC"'''))

P.append(("verdict-fail", 'echo "R10_V7_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"',
                          'echo "R10_V8_VERDICT=FAIL gate_failures=$GATE_FAILS" | tee -a "$EV/run-meta.txt"'))
P.append(("verdict-pass",
  'echo "R10_V7_VERDICT=PASS playwright_rc=$RC report_rc=$REPORT_RC crosscheck_rc=$XRC summary_render_rc=$SUMMARY_RC freeze_writer_rc=$FREEZE_RC freeze_stable=$FREEZE_STABLE traceback=$TB_FOUND" | tee -a "$EV/run-meta.txt"',
  'echo "R10_V8_VERDICT=PASS playwright_rc=$RC report_rc=$REPORT_RC crosscheck_rc=$XRC summary_render_rc=$SUMMARY_RC freeze_writer_rc=$FREEZE_RC audit_chain_gate_rc=$ACRC freeze_stable=$FREEZE_STABLE traceback=$TB_FOUND" | tee -a "$EV/run-meta.txt"'))

P.append(("header-note",
  '# v1/v2/v3 轮（run-r10-export-excelize*.sh、r10-crosscheck*.py、r10v2-*.spec.ts）全部原样保留，不 hot-edit。',
  '''# v8（本文件）：R10 v7 的"审计完整性撤回"补课轮。裁定：v7 虽 144/0、9UI PASS，但它**没有证明**
# export.completed 在 v2 不可变审计链；且 v7 绑定的是不含审计修复的 4ec819ad。
# v8 相对 v7 的增量：
#   H1 运行身份改为 484e2707…（= 审计修复 + request 维度真实化，两处均为真实缺陷修复）；
#   H2 crosscheck v7：export.completed 必须是 audit_events_v2 真实行，scope/actor/request/trace/链链接逐项判定，
#      route 必须真实（显式拒绝伪造值 /api/auth/v1/refresh）；
#   H3 新增独立阶段 P4b「审计链闸门」：用**从冻结源码构建**的 auditverify 做密码学校验，
#      要求 ok/valid/无断点/可信锚点 2344/封存尾哈希/链头==库中最大 seq 行 event_hash；rc 纳入最终 gate（fail-closed）。
#
# v1/v2/v3 轮（run-r10-export-excelize*.sh、r10-crosscheck*.py、r10v2-*.spec.ts）全部原样保留，不 hot-edit。'''))

for tag, old, new in P:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want 1)" % (tag, n)); sys.exit(4)
    text = text.replace(old, new); print("applied %s" % tag)

open(DST, "w", encoding="utf-8", newline="\n").write(text)
out = open(DST, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (DST, len(out), hashlib.sha256(out).hexdigest()))
