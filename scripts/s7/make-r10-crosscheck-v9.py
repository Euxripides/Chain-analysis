#!/usr/bin/env python3
# make-r10-crosscheck-v9.py -- r10-crosscheck-v9.py generated from r10-crosscheck-v8.py.
#
# v8 ran in the v9 round (r10-export-excelize-v9-20260913T070839Z, crosscheck_rc=0, 227 checks / 0 failed)
# and the round's own evidence exposed ONE defect in MY v8 code: the same-occurrence assertion was gated on
#   create_http.get("status") == 202
# but the measured new-create status is 201 (202 was read off writeJSON(w, 202, exp) instead of measured).
# Consequence: the extra "same occurrence" checks were silently skipped (downgraded to an observation);
# no false PASS was produced -- the hard scope/actor/request/route/trace/chain-link checks still ran and
# passed -- but a required assertion was not enforced. v9 enforces it and makes the gate fail-closed:
#   - NEW_CREATE_STATUSES = (201, 202): a genuinely-new export must report one of these, else FAIL;
#   - the same-occurrence checks now run whenever the create status is a new-create status.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/scripts/s7/r10-crosscheck-v8.py"
DST = sys.argv[2] if len(sys.argv) > 2 else R + "/scripts/s7/r10-crosscheck-v9.py"
EXPECT_SRC_SHA = "613821db5f45cf2dccd6ac632eb661a0e389e46097908e8d31367c7e73fb5260"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL source v8 crosscheck sha mismatch (refusing to patch an unexpected baseline)")
    sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("suite-v9",
  '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v8)"',
  '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v9)"'))

P.append(("const-new-status",
  '# 伪造路由：早期 writeAuditCtx 用 r0() 合成请求（POST /api/auth/v1/refresh）留下的假值。',
  r'''# v9 新增：导出**新建**路径的状态码（实测值，不读码推断）。
#   实测 201：v8 轮 uc-desktop/r10-02；v9 轮 uc-desktop/r10-01、r10-02；v9 轮 r10-09 桌面+移动。
#   实测 200：复用路径（命中 query_snapshot 完全一致的历史导出）。
# v8 曾把这里写成 202（照 export_v2.go 的 writeJSON(w, 202, exp) 读出来的），于是"同一次发生"判据
# 被静默跳过 —— 这是**我自己的测试侧缺陷**，由 v9 轮的实际证据暴露，在 v9 修正并改为 fail-closed。
NEW_CREATE_STATUSES = (201, 202)

# 伪造路由：早期 writeAuditCtx 用 r0() 合成请求（POST /api/auth/v1/refresh）留下的假值。'''))

P.append(("gate-new-status",
  r'''            create_http = ev.get("createHttp") or {}
            create_request = create_http.get("requestId")
            create_trace = create_http.get("traceId")
            if create_request and create_trace and create_http.get("status") == 202:''',
  r'''            create_http = ev.get("createHttp") or {}
            create_request = create_http.get("requestId")
            create_trace = create_http.get("traceId")
            create_status = create_http.get("status")
            if create_http and genuinely_new:
                check("audit-v2-export-create-status-is-new:" + name,
                      create_status in NEW_CREATE_STATUSES,
                      {"createHttp_status": create_status, "new_create_statuses": sorted(NEW_CREATE_STATUSES),
                       "measured": {"new": 201, "reuse": 200},
                       "note": "本轮真实新建的导出必须报告新建状态码（v8 把它写成 202，导致同一次发生判据被静默跳过）"})
            if create_request and create_trace and create_status in NEW_CREATE_STATUSES:'''))

P.append(("summary-notes-v9",
  '''        "v8_fixes": [''',
  '''        "v9_fixes": [
            "Y1 真实缺陷（测试侧，我自己的代码）：v8 把同一次发生判据的状态门限写成 202（读码而非实测），实测新建=201/复用=200 -> 该判据被静默跳过；v9 改为 NEW_CREATE_STATUSES=(201,202) 并在'本轮新建却报告非新建状态码'时硬失败",
            "Y2 v9 用更严的核验器对**同一份** v9 轮证据重跑，使同一次发生判据真正被强制（evidence 未改动）",
        ],
        "v8_fixes": ['''))

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
