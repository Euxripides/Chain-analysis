#!/usr/bin/env python3
# make-r10-crosscheck-v8.py — 由 r10-crosscheck-v7.py（sha fc871315…）**机械生成** v8。
# 每个补丁要求"恰好匹配一次"，否则退出非 0 且不写文件（fail-closed）。
#
# v8 三项更正（对应 1420 交接 §5 施工单 1/2 + 根 14:49 的要求）：
#   X1 **真实缺陷（我自己的代码）**：v7 的 first/last 查询写成 "SELECT seq::text … ORDER BY seq"，
#      PostgreSQL 把 ORDER BY seq 解析为**输出列**（text）→ 按字典序排序 → first=10000/last=9999，
#      导致 3 项 audit-chain-* 判据在 v8 轮假失败。v8 一律 ORDER BY seq::bigint（数值排序）。
#      （audit_v2_rows 的 ORDER BY seq 同样是输出列歧义，一并修正。）
#   X2 export.completed 断言改为**作用域感知**（不是削弱）：
#      - 本轮真实新建的导出（id 不在 pre-ids-exports.txt）：硬性要求 v2 真实行 + scope/actor/request/
#        trace/链链接/route 真实，且必须与**本轮那次创建请求**的 request_id/trace_id 一致（同一次发生）；
#      - 复用到的历史导出若已有 v2 完成行：同样按硬性判据核；
#      - 复用到的历史导出若没有 v2 完成行：只有当它创建于审计修复部署（05:55:08Z）**之前**才降级为
#        observation（legacy 有 + 早于修复 + 绝不记 PASS）；修复之后创建却缺 v2 完成行 = 硬失败。
#   X3 追加认证核心判据 fresh-export-v2-completion-certified：至少一个**本轮真实新建**的导出
#      在 v2 链上有完整 export.completed（含同一次请求证明）—— 这才是"审计修复可观测"的认证。
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/scripts/s7/r10-crosscheck-v7.py"
DST = sys.argv[2] if len(sys.argv) > 2 else R + "/scripts/s7/r10-crosscheck-v8.py"
EXPECT_SRC_SHA = "fc871315317092d59850c8f33f650372a4339a0a685bde86289bd95755e4c302"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL source v7 crosscheck sha mismatch (refusing to patch an unexpected baseline)")
    sys.exit(3)
text = raw.decode("utf-8")
PATCHES = []

PATCHES.append(("suite-v8",
  '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v7)"',
  '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v8)"'))

PATCHES.append(("test-keys-v8",
  'TEST_KEYS = ["R10-01", "R10-02", "R10-03", "R10-04", "R10-05", "R10-06", "R10-07", "R10-08"]\nDESKTOP_TESTS = list(TEST_KEYS)\nMOBILE_TESTS = ["R10-01"]',
  'TEST_KEYS = ["R10-01", "R10-02", "R10-03", "R10-04", "R10-05", "R10-06", "R10-07", "R10-08", "R10-09"]\nDESKTOP_TESTS = list(TEST_KEYS)\n# v8：R10-09（全新导出完成认证）在桌面与移动**都**执行；因此"按设计排除"仍是 uc-mobile 的 7 项。\nMOBILE_TESTS = ["R10-01", "R10-09"]'))

PATCHES.append(("constants-v8",
  'FABRICATED_ROUTE = "/api/auth/v1/refresh"',
  r'''FABRICATED_ROUTE = "/api/auth/v1/refresh"

# ---- v8 新增：审计修复的部署时点（用于"修复前既存导出"的适用范围判定） ----
# r10v8-auditfix-deploy-20260913T055508Z：运行身份 4ec819ad -> 569f40ec，export.completed 开始写入 v2 链。
# 早于该时点创建的导出，其 export.completed 只存在于 legacy 表是**历史事实**，不适用 v2 硬性要求
# —— 但也**绝不记 PASS**，只记 observation（v8 轮 2 项假失败的真实根因）。
AUDIT_FIX_DEPLOYED_UTC = datetime.datetime(2026, 9, 13, 5, 55, 8, tzinfo=datetime.timezone.utc)
_PREFIX_OVERRIDE = os.environ.get("R10_AUDIT_FIX_DEPLOY_UTC", "")
if _PREFIX_OVERRIDE:
    # 仅用于**负向校准**（证明该分支 fail-closed）。正式轮次的 runner 不设置该环境变量。
    AUDIT_FIX_DEPLOYED_UTC = datetime.datetime.fromisoformat(_PREFIX_OVERRIDE.replace("Z", "+00:00"))


def parse_ts(text):
    try:
        return datetime.datetime.fromisoformat(str(text))
    except Exception:  # noqa: BLE001
        return None


def is_pre_fix(created_at_text):
    ts = parse_ts(created_at_text)
    return ts is not None and ts < AUDIT_FIX_DEPLOYED_UTC


def load_pre_export_ids():
    path = os.path.join(EV, "pre-ids-exports.txt")
    if not os.path.exists(path):
        raise HardError("missing pre-state pk snapshot for exports")
    with open(path, "r", encoding="utf-8") as handle:
        return {line.strip() for line in handle if line.strip()}'''))

PATCHES.append(("orderby-numeric-v8",
  '" FROM audit_events_v2 WHERE chain=\'audit\' AND resource_id = \'%s\' AND action = \'%s\' ORDER BY seq;\\n"',
  '" FROM audit_events_v2 WHERE chain=\'audit\' AND resource_id = \'%s\' AND action = \'%s\' ORDER BY seq::bigint;\\n"'))

PATCHES.append(("orderby-prev-v8",
  '''        prev = psql("SELECT coalesce(event_hash,'') FROM audit_events_v2 WHERE chain='audit' AND seq < %d"
                    " ORDER BY seq DESC LIMIT 1;\\n" % seq)''',
  '''        # X1：seq 是 bigint，但显式 ::bigint 排序，杜绝任何输出列歧义。
        prev = psql("SELECT coalesce(event_hash,'') FROM audit_events_v2 WHERE chain='audit' AND seq < %d"
                    " ORDER BY seq::bigint DESC LIMIT 1;\\n" % seq)'''))

PATCHES.append(("orderby-chain-v8",
  '''    first = psql("SELECT seq::text, coalesce(prev_hash,''), coalesce(event_hash,'') FROM audit_events_v2"
                 " WHERE chain='audit' ORDER BY seq LIMIT 1;\\n")
    last = psql("SELECT seq::text, coalesce(event_hash,'') FROM audit_events_v2"
                " WHERE chain='audit' ORDER BY seq DESC LIMIT 1;\\n")''',
  '''    # X1（v8 真实缺陷修复）：v7 写的是 "SELECT seq::text ... ORDER BY seq"。PostgreSQL 对裸列名的
    # ORDER BY 优先解析为**输出列**（此处是 text），于是按字典序取首尾 -> first=10000 / last=9999，
    # 3 项 audit-chain-* 判据因此假失败。改为显式 ::bigint 数值排序。
    first = psql("SELECT seq::text, coalesce(prev_hash,''), coalesce(event_hash,'') FROM audit_events_v2"
                 " WHERE chain='audit' ORDER BY seq::bigint LIMIT 1;\\n")
    last = psql("SELECT seq::text, coalesce(event_hash,'') FROM audit_events_v2"
                " WHERE chain='audit' ORDER BY seq::bigint DESC LIMIT 1;\\n")'''))

PATCHES.append(("db-created-at-v8",
  '''        rows = psql("SELECT id::text, status, format, zip, rows, sha256, object_ref, coalesce(job_id::text,''),"
                    " coalesce(organization_id::text,''), coalesce(workspace_id::text,''), dataset,"
                    " coalesce(created_by::text,'')"
                    " FROM exports WHERE id = '%s';\\n" % export_id)''',
  '''        rows = psql("SELECT id::text, status, format, zip, rows, sha256, object_ref, coalesce(job_id::text,''),"
                    " coalesce(organization_id::text,''), coalesce(workspace_id::text,''), dataset,"
                    " coalesce(created_by::text,''), created_at::text"
                    " FROM exports WHERE id = '%s';\\n" % export_id)'''))

PATCHES.append(("db-dict-created-at-v8",
  '''              "organization_id": rows[0][8], "workspace_id": rows[0][9], "dataset": rows[0][10],
              "created_by": rows[0][11]}''',
  '''              "organization_id": rows[0][8], "workspace_id": rows[0][9], "dataset": rows[0][10],
              "created_by": rows[0][11], "created_at": rows[0][12]}'''))

PATCHES.append(("stats-v8",
  '''    check("playwright-stats", stats.get("expected") == 9 and stats.get("unexpected") == 0
          and stats.get("skipped") == 7 and stats.get("flaky") == 0, stats)''',
  '''    # v8：用例数 9 -> 11（新增 R10-09 在桌面与移动各一项）。按设计排除仍是 7 项。
    check("playwright-stats", stats.get("expected") == 11 and stats.get("unexpected") == 0
          and stats.get("skipped") == 7 and stats.get("flaky") == 0, stats)'''))

PATCHES.append(("skip-allowed-v8",
  '''                row["skip_allowed"] = (proj == "uc-mobile" and key != "R10-01")''',
  '''                row["skip_allowed"] = (proj == "uc-mobile" and key not in ("R10-01", "R10-09"))'''))

PATCHES.append(("matrix-count-v8",
  '''    check("playwright-matrix", not mismatch and len(ran) == 9 and len(skipped_rows) == 7,''',
  '''    check("playwright-matrix", not mismatch and len(ran) == 11 and len(skipped_rows) == 7,'''))

PATCHES.append(("completion-scope-v8",
  r'''        # ---- v7 新增：export.completed 必须是 v2 链上的**真实行**，且 request 维度不可伪造 ----
        legacy_actions = {a for a, _c in legacy}
        entry["legacy_actions"] = sorted(legacy_actions)
        completed = audit_v2_rows(export_id, "export.completed")
        entry["audit_v2_export_completed"] = completed
        check("audit-v2-export-completed-present:" + name, len(completed) >= 1,
              {"rows": len(completed), "audit_v2_actions": sorted(actions)})
        check("legacy-audit-export-completed-present:" + name, "export.completed" in legacy_actions,
              sorted(legacy_actions))
        if completed:
            row = completed[0]''',
  r'''        # ---- v7/v8：export.completed 必须是 v2 链上的**真实行**，且 request 维度不可伪造 ----
        # X2（v8 作用域感知，**不是削弱**）：
        #   本轮真实新建（id 不在 pre-ids-exports.txt）-> v2 真实行是硬性要求；
        #   复用到的历史导出若已有 v2 完成行 -> 同样按硬性判据核（更严）；
        #   复用到的历史导出若无 v2 完成行 -> 只有创建于审计修复部署(05:55:08Z)**之前**才降级为
        #   observation（legacy 有 + 早于修复 + 绝不记 PASS）；修复之后创建却缺 = 硬失败。
        legacy_actions = {a for a, _c in legacy}
        entry["legacy_actions"] = sorted(legacy_actions)
        completed = audit_v2_rows(export_id, "export.completed")
        entry["audit_v2_export_completed"] = completed
        pre_ids = load_pre_export_ids()
        genuinely_new = export_id not in pre_ids
        created_at = db.get("created_at") or ""
        pre_fix = is_pre_fix(created_at)
        entry["completion_scope"] = {
            "genuinely_new_this_round": genuinely_new,
            "in_pre_ids_snapshot": not genuinely_new,
            "created_at": created_at,
            "pre_audit_fix_deploy": pre_fix,
            "audit_fix_deployed_utc": AUDIT_FIX_DEPLOYED_UTC.isoformat(),
        }
        check("legacy-audit-export-completed-present:" + name, "export.completed" in legacy_actions,
              sorted(legacy_actions))
        if completed:
            check("audit-v2-export-completed-present:" + name, True,
                  {"rows": len(completed), "genuinely_new_this_round": genuinely_new,
                   "audit_v2_actions": sorted(actions)})
            row = completed[0]'''))

PATCHES.append(("completion-same-occurrence-v8",
  r'''            check("audit-v2-export-completed-chain-link:" + name, row["chain_link_ok"],
                  {"seq": row["seq"], "prev_hash": row["prev_hash"],
                   "expected_prev_hash": row["expected_prev_hash"]})
        else:
            observe("audit-v2-export-completed-absent:" + name, {"audit_v2_actions": sorted(actions)})''',
  r'''            check("audit-v2-export-completed-chain-link:" + name, row["chain_link_ok"],
                  {"seq": row["seq"], "prev_hash": row["prev_hash"],
                   "expected_prev_hash": row["expected_prev_hash"]})
            # X2 同一次发生证明：只有当本轮**真的新建**（202）且 evidence 记录了创建请求时才判定。
            # 复用（200）时完成事件属于当初那次创建，不能拿本轮的请求 id 去比 —— 如实记 observation。
            create_http = ev.get("createHttp") or {}
            create_request = create_http.get("requestId")
            create_trace = create_http.get("traceId")
            if create_request and create_trace and create_http.get("status") == 202:
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
                         "reason": "本轮创建请求为复用(200)/未新建，完成事件属于当初那次创建；本用例不据此记 PASS/FAIL"})
        else:
            if genuinely_new:
                check("audit-v2-export-completed-present:" + name, False,
                      {"reason": "本轮**真实新建**的导出必须在 audit_events_v2 有 export.completed（审计修复的可观测认证）",
                       "audit_v2_actions": sorted(actions), "scope": entry["completion_scope"]})
            elif pre_fix:
                observe("audit-v2-export-completed-not-applicable-prefix:" + name,
                        {"reason": "修复前创建并被复用的导出：v2 硬性要求不适用；legacy 有完成事件，但**绝不记 PASS**",
                         "audit_v2_actions": sorted(actions), "legacy_actions": sorted(legacy_actions),
                         "scope": entry["completion_scope"]})
            else:
                check("audit-v2-export-completed-present:" + name, False,
                      {"reason": "复用到的导出创建于审计修复部署之后却缺 v2 完成事件 —— 修复未生效",
                       "audit_v2_actions": sorted(actions), "scope": entry["completion_scope"]})'''))

PATCHES.append(("completion-proved-flag-v8",
  '''        entry["minio_independent"] = minio_check(export_id, db["object_ref"], db["sha256"], file_sha, name)''',
  '''        entry["completion_proved"] = bool(completed) and genuinely_new
        entry["minio_independent"] = minio_check(export_id, db["object_ref"], db["sha256"], file_sha, name)'''))

PATCHES.append(("fresh-certified-v8",
  '''    check("all-new-export-ids-exist", int(new_rows[0][0]) == len(new_export_ids),
          {"found": new_rows[0][0], "expected": len(new_export_ids)})''',
  '''    check("all-new-export-ids-exist", int(new_rows[0][0]) == len(new_export_ids),
          {"found": new_rows[0][0], "expected": len(new_export_ids)})

    # ---- X3（v8 认证核心）：至少一个**本轮真实新建**的导出在 v2 链上有完整 export.completed ----
    fresh_proved = sorted({e["export_id"] for e in per_export if e.get("completion_proved")})
    check("fresh-export-v2-completion-certified", len(fresh_proved) >= 1,
          {"fresh_export_ids_with_v2_completion": fresh_proved,
           "note": "导出完成事件必须能被**本轮新建的**导出观测到（v8 轮 2 项假失败的结构性根因即在于此）"})
    observe("fresh-export-ids-this-round", {"genuinely_new": sorted(genuinely_new), "reused_existing": reused_existing,
                                            "with_v2_completion_proof": fresh_proved})'''))

PATCHES.append(("summary-notes-v8",
  '''        "v7_audit_request_hardening": [''',
  '''        "v8_fixes": [
            "X1 真实缺陷（测试侧）：SELECT seq::text ... ORDER BY seq 被解析为按输出列(text)排序 -> first=10000/last=9999；改 ORDER BY seq::bigint",
            "X2 export.completed 作用域感知：本轮新建=硬性要求；复用且已有 v2 行=同样硬性；复用且无 v2 行=仅当创建于 05:55:08Z 审计修复部署前才降级 observation（绝不记 PASS）",
            "X2b 同一次发生证明：v2 export.completed 的 request_id/trace_id 必须等于 UI 创建请求（202）与 export.create 行的同一对 id",
            "X3 认证核心 fresh-export-v2-completion-certified：至少一个本轮真实新建的导出有完整 v2 完成事件",
            "矩阵扩至 11 项（R10-09 桌面+移动），按设计排除仍为 7 项",
        ],
        "v7_audit_request_hardening": ['''))

for tag, old, new in PATCHES:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want 1)" % (tag, n))
        sys.exit(4)
    text = text.replace(old, new, 1)
    print("applied %s" % tag)

if os.path.exists(DST):
    print("FATAL destination exists (refusing to overwrite): %s" % DST)
    sys.exit(5)
open(DST, "w", encoding="utf-8", newline="\n").write(text)
out = open(DST, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (DST, len(out), hashlib.sha256(out).hexdigest()))
