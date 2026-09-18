
#!/usr/bin/env python3
# make-r10-crosscheck-v7.py — 由 r10-crosscheck-v6.py（sha 67041bf3…）**机械生成** v7。
# 每个补丁都要求"恰好匹配一次"，否则退出非 0（fail-closed，不做静默部分替换）。
import hashlib, os, sys

SRC = "/home/euripides/projects/blockchain-platform/scripts/s7/r10-crosscheck-v6.py"
DST = "/home/euripides/projects/blockchain-platform/scripts/s7/r10-crosscheck-v7.py"
EXPECT_SRC_SHA = "67041bf3b7e692929aa3e947ac49ca4c81a8a010226ab9d5fd68d83bb9adbb71"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL source v6 sha mismatch (refusing to patch an unexpected baseline)")
    sys.exit(3)
text = raw.decode("utf-8")

PATCHES = []

PATCHES.append(("suite-v7", '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v6)"',
                             '"suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v7)"'))

PATCHES.append(("constants", 'RE_TXHASH = re.compile(r"^0x[0-9a-fA-F]{64}$")', r'''RE_TXHASH = re.compile(r"^0x[0-9a-fA-F]{64}$")

# ---- v7 新增常量：不可抵赖审计链（audit_events_v2, chain='audit'）的可信边界与真实路由白名单 ----
# seq 1-2343 永久缺失（0055 / ADR-018）；可信段起点 2344，其 prev_hash 即被销毁的最后一条事件的
# event_hash（sealed tail）。本套件只对 2344+ 做完整性判定，**绝不声称完整历史**。
TRUSTED_FROM_SEQ = 2344
SEALED_TAIL_HASH = "3db79aeb77ce12ae8ccab3540891a7ff241bfc34af8f4a9dcdfd283578686ad1"
# 导出创建的真实路由（完成事件由该请求派生的后台 goroutine 写入，因此 route 必须是其中之一）。
REAL_EXPORT_ROUTES = ("/api/v2/exports", "/api/v1/exports", "/api/v1/actions/export", "/api/v1/download-exports")
# 伪造路由：早期 writeAuditCtx 用 r0() 合成请求（POST /api/auth/v1/refresh）留下的假值。
FABRICATED_ROUTE = "/api/auth/v1/refresh"'''))

PATCHES.append(("helper-audit-v2-rows", r'''def audit_rows(export_id):
    return psql("SELECT action, count(*) FROM audit_events_v2 WHERE resource_id = '%s' GROUP BY 1 ORDER BY 1;\n" % export_id)''',
r'''def audit_rows(export_id):
    return psql("SELECT action, count(*) FROM audit_events_v2 WHERE resource_id = '%s' GROUP BY 1 ORDER BY 1;\n" % export_id)


def audit_v2_rows(export_id, action):
    """某导出某动作在 v2 不可抵赖审计链里的**真实行**（scope/actor/request/trace + 链链接）。

    v7 新增：仅"能查到一行"不算通过。request 维度必须是真实请求（R10v8 定点实测证明：
    旧实现把 route 写成 /api/auth/v1/refresh，是**伪造**值），且该行必须真实链接在链上。
    """
    rows = psql("SELECT seq::text, coalesce(actor_type,''), coalesce(actor_id::text,''), coalesce(actor_email,''),"
                " coalesce(organization_id::text,''), coalesce(workspace_id::text,''), coalesce(session_id::text,''),"
                " coalesce(http_method,''), coalesce(route_template,''), coalesce(request_id,''), coalesce(trace_id,''),"
                " coalesce(result,''), coalesce(prev_hash,''), coalesce(event_hash,''), occurred_at::text"
                " FROM audit_events_v2 WHERE chain='audit' AND resource_id = '%s' AND action = '%s' ORDER BY seq;\n"
                % (export_id, action))
    out = []
    for r in rows:
        r = (r + [""] * 15)[:15]
        try:
            seq = int(r[0])
        except (TypeError, ValueError):
            continue
        prev = psql("SELECT coalesce(event_hash,'') FROM audit_events_v2 WHERE chain='audit' AND seq < %d"
                    " ORDER BY seq DESC LIMIT 1;\n" % seq)
        expected_prev = prev[0][0] if prev and prev[0] else ""
        out.append({
            "seq": seq, "actor_type": r[1], "actor_id": r[2], "actor_email": r[3],
            "organization_id": r[4], "workspace_id": r[5], "session_id": r[6],
            "http_method": r[7], "route": r[8], "request_id": r[9], "trace_id": r[10],
            "result": r[11], "prev_hash": r[12], "event_hash": r[13], "occurred_at": r[14],
            "expected_prev_hash": expected_prev,
            "chain_link_ok": bool(r[13]) and bool(r[12]) and r[12] == expected_prev,
        })
    return out'''))

PATCHES.append(("exports-select-created-by", r'''        rows = psql("SELECT id::text, status, format, zip, rows, sha256, object_ref, coalesce(job_id::text,''),"
                    " coalesce(organization_id::text,''), coalesce(workspace_id::text,''), dataset"
                    " FROM exports WHERE id = '%s';\n" % export_id)''',
r'''        rows = psql("SELECT id::text, status, format, zip, rows, sha256, object_ref, coalesce(job_id::text,''),"
                    " coalesce(organization_id::text,''), coalesce(workspace_id::text,''), dataset,"
                    " coalesce(created_by::text,'')"
                    " FROM exports WHERE id = '%s';\n" % export_id)'''))

PATCHES.append(("db-dict-created-by", '''              "organization_id": rows[0][8], "workspace_id": rows[0][9], "dataset": rows[0][10]}''',
'''              "organization_id": rows[0][8], "workspace_id": rows[0][9], "dataset": rows[0][10],
              "created_by": rows[0][11]}'''))

PATCHES.append(("per-export-audit-completed", '''        check("audit-v2-export-create:" + name, "export.create" in actions, sorted(actions))
        if name.startswith(("r10-01", "r10-02", "r10-03")):
            check("audit-v2-export-download:" + name, "export.download" in actions, sorted(actions))
            check("audit-v2-export-ticket:" + name, "export.download_ticket" in actions, sorted(actions))''',
r'''        check("audit-v2-export-create:" + name, "export.create" in actions, sorted(actions))
        if name.startswith(("r10-01", "r10-02", "r10-03")):
            check("audit-v2-export-download:" + name, "export.download" in actions, sorted(actions))
            check("audit-v2-export-ticket:" + name, "export.download_ticket" in actions, sorted(actions))

        # ---- v7 新增：export.completed 必须是 v2 链上的**真实行**，且 request 维度不可伪造 ----
        legacy_actions = {a for a, _c in legacy}
        entry["legacy_actions"] = sorted(legacy_actions)
        completed = audit_v2_rows(export_id, "export.completed")
        entry["audit_v2_export_completed"] = completed
        check("audit-v2-export-completed-present:" + name, len(completed) >= 1,
              {"rows": len(completed), "audit_v2_actions": sorted(actions)})
        check("legacy-audit-export-completed-present:" + name, "export.completed" in legacy_actions,
              sorted(legacy_actions))
        if completed:
            row = completed[0]
            check("audit-v2-export-completed-scope:" + name,
                  row["organization_id"] == db["organization_id"] and row["workspace_id"] == db["workspace_id"],
                  {"audit": [row["organization_id"], row["workspace_id"]],
                   "exports": [db["organization_id"], db["workspace_id"]]})
            check("audit-v2-export-completed-actor:" + name,
                  bool(row["actor_id"]) and bool(row["actor_email"])
                  and (not db.get("created_by") or row["actor_id"] == db["created_by"]),
                  {"actor_id": row["actor_id"], "actor_email": row["actor_email"],
                   "exports_created_by": db.get("created_by")})
            check("audit-v2-export-completed-request:" + name,
                  bool(row["http_method"]) and bool(row["route"]) and bool(row["request_id"]),
                  {"http_method": row["http_method"], "route": row["route"], "request_id": row["request_id"]})
            check("audit-v2-export-completed-route-is-real:" + name,
                  row["route"] in REAL_EXPORT_ROUTES and row["route"] != FABRICATED_ROUTE,
                  {"route": row["route"], "allowed": sorted(REAL_EXPORT_ROUTES), "fabricated": FABRICATED_ROUTE})
            check("audit-v2-export-completed-trace:" + name, bool(row["trace_id"]), row["trace_id"])
            check("audit-v2-export-completed-chain-link:" + name, row["chain_link_ok"],
                  {"seq": row["seq"], "prev_hash": row["prev_hash"],
                   "expected_prev_hash": row["expected_prev_hash"]})
        else:
            observe("audit-v2-export-completed-absent:" + name, {"audit_v2_actions": sorted(actions)})'''))

PATCHES.append(("c9-chain-section", '''    failed = [c for c in checks if not c["ok"]]''',
r'''    # ---- C9（v7 新增）：v2 不可抵赖审计链的可信锚点与全链连续性（ADR-018 / INC-AUDIT-GAP-1-2343） ----
    first = psql("SELECT seq::text, coalesce(prev_hash,''), coalesce(event_hash,'') FROM audit_events_v2"
                 " WHERE chain='audit' ORDER BY seq LIMIT 1;\n")
    last = psql("SELECT seq::text, coalesce(event_hash,'') FROM audit_events_v2"
                " WHERE chain='audit' ORDER BY seq DESC LIMIT 1;\n")
    chain_rows = int(one("SELECT count(*) FROM audit_events_v2 WHERE chain='audit';\n") or 0)
    gap_rows = psql("SELECT count(*) FROM (SELECT seq, lag(seq) OVER (ORDER BY seq) AS p FROM audit_events_v2"
                    " WHERE chain='audit') t WHERE p IS NOT NULL AND seq <> p + 1;\n")
    link_rows = psql("SELECT count(*) FROM (SELECT seq, prev_hash, lag(event_hash) OVER (ORDER BY seq) AS pe"
                     " FROM audit_events_v2 WHERE chain='audit') t WHERE pe IS NOT NULL AND prev_hash <> pe;\n")
    below_rows = int(one("SELECT count(*) FROM audit_events_v2 WHERE chain='audit' AND seq < %d;\n"
                         % TRUSTED_FROM_SEQ) or 0)
    chain_summary = {
        "trusted_from_seq": TRUSTED_FROM_SEQ,
        "sealed_tail_hash_expected": SEALED_TAIL_HASH,
        "first_seq": int(first[0][0]) if first and first[0] else None,
        "first_prev_hash": first[0][1] if first and first[0] else None,
        "last_seq": int(last[0][0]) if last and last[0] else None,
        "last_event_hash": last[0][1] if last and last[0] else None,
        "rows": chain_rows,
        "sequence_gaps": int(gap_rows[0][0]) if gap_rows and gap_rows[0] else -1,
        "broken_prev_hash_links": int(link_rows[0][0]) if link_rows and link_rows[0] else -1,
        "rows_below_trusted_anchor": below_rows,
        "note": "seq 1-2343 永久缺失（0055/ADR-018）；本检查只对 2344+ 可信段做完整性判定，绝不声称完整历史。",
    }
    observe("audit-chain-summary", chain_summary)
    check("audit-chain-first-seq-equals-trusted-anchor", chain_summary["first_seq"] == TRUSTED_FROM_SEQ, chain_summary)
    check("audit-chain-first-prev-hash-is-sealed-tail", chain_summary["first_prev_hash"] == SEALED_TAIL_HASH, chain_summary)
    check("audit-chain-no-sequence-gap", chain_summary["sequence_gaps"] == 0, chain_summary)
    check("audit-chain-no-broken-prev-hash-link", chain_summary["broken_prev_hash_links"] == 0, chain_summary)
    check("audit-chain-no-rows-below-trusted-anchor", chain_summary["rows_below_trusted_anchor"] == 0, chain_summary)
    check("audit-chain-row-count-contiguous",
          bool(chain_summary["first_seq"]) and bool(chain_summary["last_seq"])
          and chain_summary["rows"] == chain_summary["last_seq"] - chain_summary["first_seq"] + 1, chain_summary)

    failed = [c for c in checks if not c["ok"]]'''))

PATCHES.append(("summary-additions", '''        "matrix_status_vocabulary": "Playwright JSON reporter: expected | unexpected | flaky | skipped (test item has NO outcome field)",''',
'''        "v7_audit_request_hardening": [
            "export.completed 必须是 audit_events_v2 的**真实行**（不是 legacy-only，也不是事后补写）",
            "scope：审计行 organization_id/workspace_id 必须与 exports 行一致",
            "actor：actor_id 必须等于 exports.created_by，actor_email 非空",
            "request：http_method/route_template/request_id 非空，且 route 必须是真实发起路由（拒绝伪造）",
            "trace：trace_id 非空",
            "chain：该行 prev_hash 必须等于链上前一行的 event_hash（真实链接，不是孤立行）",
            "C9：可信锚点 seq=2344 / prev_hash=sealed_tail / 无 seq 空洞 / 无断链 / 锚点以下 0 行",
        ],
        "matrix_status_vocabulary": "Playwright JSON reporter: expected | unexpected | flaky | skipped (test item has NO outcome field)",'''))

PATCHES.append(("summary-audit-chain", '''        "invariants": invariants,''', '''        "invariants": invariants,
        "audit_chain": chain_summary,'''))

for tag, old, new in PATCHES:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want exactly 1)" % (tag, n))
        sys.exit(4)
    text = text.replace(old, new)
    print("applied %s" % tag)

open(DST, "w", encoding="utf-8", newline="\n").write(text)
out = open(DST, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (DST, len(out), hashlib.sha256(out).hexdigest()))
