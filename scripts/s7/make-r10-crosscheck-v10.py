#!/usr/bin/env python3
# make-r10-crosscheck-v10.py -- strict same-occurrence gate, generated from r10-crosscheck-v9.py.
#
# Root ruling (15:16): the new-create status is STRICTLY 201; 200 may only mean "reused an existing
# export". Every genuinely_new export (id absent from the pre-snapshot) MUST have an audit_events_v2
# export.completed whose request_id/trace_id equal BOTH the UI createHttp ids and the export.create row
# ids, with route/scope/actor/chain-link intact. Any 201 that would fall into "not applicable" is a FAIL.
# v9 (201/202 tuple) is preserved; v10 replaces it with the strict single status and adds the
# evidence-gap check (genuinely_new export without recorded createHttp ids = FAIL, never a silent skip).
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/scripts/s7/r10-crosscheck-v9.py"
DST = sys.argv[2] if len(sys.argv) > 2 else R + "/scripts/s7/r10-crosscheck-v10.py"
EXPECT_SRC_SHA = "2baa4f758198445f84e6e1cd5549666f0ed3a40437dd945022ad6fc0506f1115"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL source v9 crosscheck sha mismatch (refusing to patch an unexpected baseline)")
    sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("suite-v10",
  '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v9)"',
  '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v10, strict same-occurrence)"'))

P.append(("const-strict",
  "NEW_CREATE_STATUSES = (201, 202)",
  r'''# v10（根 15:16 裁定，严格）：**新建唯一状态 = 201**；200 只允许表示"复用了既有导出"。
# 任何 201 都必须通过"同一次发生"强断言；201 若落入 not-applicable 一律 FAIL（v8 因写成 202 而静默跳过）。
NEW_CREATE_STATUS = 201
REUSE_STATUS = 200'''))

P.append(("gate-strict",
  r'''            create_status = create_http.get("status")
            if create_http and genuinely_new:
                check("audit-v2-export-create-status-is-new:" + name,
                      create_status in NEW_CREATE_STATUSES,
                      {"createHttp_status": create_status, "new_create_statuses": sorted(NEW_CREATE_STATUSES),
                       "measured": {"new": 201, "reuse": 200},
                       "note": "本轮真实新建的导出必须报告新建状态码（v8 把它写成 202，导致同一次发生判据被静默跳过）"})
            if create_request and create_trace and create_status in NEW_CREATE_STATUSES:
                create_rows = audit_v2_rows(export_id, "export.create")
                create_req = create_rows[0]["request_id"] if create_rows else ""
                create_tr = create_rows[0]["trace_id"] if create_rows else ""
                check("audit-v2-export-completed-same-occurrence-request:" + name,
                      row["request_id"] == create_request and create_req == create_request,
                      {"completed_request_id": row["request_id"], "create_row_request_id": create_req,
                       "ui_create_request_id": create_request})
                check("audit-v2-export-completed-same-occurrence-trace:" + name,
                      row["trace_id"] == create_trace and create_tr == create_trace,
                      {"completed_trace_id": row["trace_id"], "create_row_trace_id": create_tr,
                       "ui_create_trace_id": create_trace})
                check("legacy-and-v2-completion-same-export:" + name,
                      "export.completed" in legacy_actions and bool(row["event_hash"]),
                      {"legacy_actions": sorted(legacy_actions), "v2_seq": row["seq"],
                       "v2_event_hash": row["event_hash"]})
            elif create_http:
                observe("audit-v2-export-completed-same-occurrence-not-applicable:" + name,
                        {"createHttp_status": create_http.get("status"), "completed_seq": row["seq"],
                         "completed_request_id": row["request_id"],
                         "reason": "本轮创建请求为复用(200)/未新建，完成事件属于当初那次创建；本用例不据此记 PASS/FAIL"})''',
  r'''            create_status = create_http.get("status")
            # Y3（v10，根 15:16 裁定）：新建唯一状态严格 201；200 只能出现在"复用既有导出"上。
            # 任一 genuinely_new（pre-snapshot 中不存在的 id）都必须走下面的强断言，不允许静默降级。
            if create_status == NEW_CREATE_STATUS:
                check("audit-v2-export-create-status-strict:" + name, True,
                      {"createHttp_status": create_status, "required_new_status": NEW_CREATE_STATUS,
                       "genuinely_new": genuinely_new})
                if not (create_request and create_trace):
                    check("audit-v2-export-completed-same-occurrence-request:" + name, False,
                          {"createHttp_status": create_status, "ui_create_request_id": create_request,
                           "ui_create_trace_id": create_trace,
                           "reason": "状态 201（真实新建）却缺少 UI create 的 request_id/trace_id —— 同一次发生判据无法成立，直接 FAIL（不得落入 not-applicable）"})
                else:
                    create_rows = audit_v2_rows(export_id, "export.create")
                    create_req = create_rows[0]["request_id"] if create_rows else ""
                    create_tr = create_rows[0]["trace_id"] if create_rows else ""
                    check("audit-v2-export-completed-same-occurrence-request:" + name,
                          row["request_id"] == create_request and create_req == create_request,
                          {"completed_request_id": row["request_id"], "create_row_request_id": create_req,
                           "ui_create_request_id": create_request})
                    check("audit-v2-export-completed-same-occurrence-trace:" + name,
                          row["trace_id"] == create_trace and create_tr == create_trace,
                          {"completed_trace_id": row["trace_id"], "create_row_trace_id": create_tr,
                           "ui_create_trace_id": create_trace})
                    check("legacy-and-v2-completion-same-export:" + name,
                          "export.completed" in legacy_actions and bool(row["event_hash"]),
                          {"legacy_actions": sorted(legacy_actions), "v2_seq": row["seq"],
                           "v2_event_hash": row["event_hash"]})
            elif genuinely_new:
                check("audit-v2-export-create-status-strict:" + name, False,
                      {"createHttp_status": create_status, "required_new_status": NEW_CREATE_STATUS,
                       "reuse_status": REUSE_STATUS, "createHttp_present": bool(create_http),
                       "reason": "pre-snapshot 中不存在的导出（=本轮真实新建）必须以 201 创建，"
                                 "并且必须在 evidence 里记录 UI create 的 request_id/trace_id；"
                                 "缺失即证据缺口，直接 FAIL（v8 曾把它静默降级为 not-applicable）"})
            elif create_http:
                # 只有"复用既有导出"才允许走到这里：完成事件属于当初那次创建，本轮无法也不应据它记 PASS。
                observe("audit-v2-export-completed-same-occurrence-not-applicable:" + name,
                        {"createHttp_status": create_status, "reuse_status": REUSE_STATUS,
                         "genuinely_new": False, "in_pre_ids_snapshot": True, "completed_seq": row["seq"],
                         "completed_request_id": row["request_id"],
                         "reason": "本轮创建请求返回 200=复用了既有导出（该 id 在 pre-snapshot 中已存在）；"
                                   "export.completed 属于当初那次创建，故同一次发生判据对本轮不适用，"
                                   "也不据此记 PASS/FAIL（硬判据已按 v8 的 X2 对历史导出行本身逐项核过）"})'''))

P.append(("summary-notes-v10",
  '''        "v9_fixes": [''',
  '''        "v10_fixes": [
            "Z1（根 15:16 裁定）新建唯一状态严格 201；200 只允许出现在复用既有导出上",
            "Z2 任何 201 都必须通过同一次发生强断言：completed.request_id/trace_id == UI createHttp 与 export.create 行的同一对；201 缺少 ids 直接 FAIL，绝不落入 not-applicable",
            "Z3 新增证据缺口硬判据：genuinely_new 导出若未记录 UI createHttp ids，直接 FAIL（v9 冻结证据里 r10-03 即命中该缺口）",
            "Z4 修正 not-applicable 的说明文本（旧文本错误地写成『复用(200)/未新建』，与本轮 201 新建的事实矛盾）",
        ],
        "v9_fixes": ['''))

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
