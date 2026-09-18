#!/usr/bin/env python3
"""R-3 审计证据采集（authorization.denied）。

为什么单独一个脚本：首轮 run 直接用 psql + DATABASE_URL 命中
"ERROR: permission denied for table audit_events_v2"（应用运行账号不是审计读取账号），
汇总脚本却把 0 字节的空文件写成了 audit_denials_count=0，把"没采到"误报成"没有"。
本脚本改为：
  - 走**容器内已授权的只读路径** \`docker exec deploy-postgres-1 psql -U blockchain\`
    （不授予任何新权限、不改 pg_hba、不扩大应用账号权限）；
  - 以本轮 negative 用例产生的**真实 request_id** 为主键逐条核对；
  - 采集失败时显式失败（写 status=failed + 原始 stderr + 退出码），绝不返回空数组冒充"0 条"。

用法：python3 r3-collect-audit.py <evidence_dir> [since_iso] [until_iso]
输出：<evidence_dir>/audit-denials.json、<evidence_dir>/audit-collection-status.json
退出码：0 采集成功；1 采集失败；2 参数/环境错误
"""
import glob
import json
import os
import subprocess
import sys

CONTAINER = os.environ.get("R3_PG_CONTAINER", "deploy-postgres-1")
PGUSER = os.environ.get("R3_PG_USER", "blockchain")
PGDB = os.environ.get("R3_PG_DB", "blockchain_platform")

REQUIRED_FIELDS = [
    "action",
    "category",
    "result",
    "http_status",
    "route_template",
    "permission_key",
    "policy_decision",
    "deny_reason",
    "occurred_at",
    "event_hash",
]

SELECT_COLUMNS = (
    "request_id, occurred_at, action, category, result, http_status, http_method, "
    "route_template, permission_key, policy_decision, deny_reason, "
    "actor_type, actor_email, actor_id, organization_id, workspace_id, session_id, "
    "event_hash, trace_id, client_ip, user_agent, detail"
)


def psql_json(sql):
    """执行只读查询并返回 (ok, payload, raw_stdout, raw_stderr, returncode)。"""
    cmd = ["docker", "exec", CONTAINER, "psql", "-U", PGUSER, "-d", PGDB, "-qAt", "-v", "ON_ERROR_STOP=1", "-c", sql]
    proc = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    if proc.returncode != 0:
        return False, None, proc.stdout, proc.stderr, proc.returncode
    text = proc.stdout.strip()
    if not text:
        return False, None, proc.stdout, "psql 返回空结果（无法区分 0 行与查询未执行）", proc.returncode
    try:
        return True, json.loads(text), proc.stdout, proc.stderr, proc.returncode
    except Exception as exc:  # noqa: BLE001
        return False, None, proc.stdout, "JSON 解析失败: " + str(exc), proc.returncode


def collect_request_ids(evidence_dir):
    """从 negative-*.json 里取出本轮真实被拒请求的 request_id（含来源文件与状态码）。"""
    found = {}
    for path in sorted(glob.glob(os.path.join(evidence_dir, "negative-*.json"))):
        data = json.load(open(path, errors="replace"))
        ids = data.get("denied_request_ids")
        if ids is None:
            ids = [row.get("request_id") for row in data.get("service_denials", [])]
        for rid in ids:
            if rid:
                found.setdefault(rid, []).append(os.path.basename(path))
        rid = data.get("request_id")
        if rid:
            found.setdefault(rid, []).append(os.path.basename(path))
    return found


def main():
    if len(sys.argv) < 2:
        print("usage: r3-collect-audit.py <evidence_dir> [since_iso] [until_iso]", file=sys.stderr)
        return 2
    evidence_dir = sys.argv[1]
    since = sys.argv[2] if len(sys.argv) > 2 else None
    until = sys.argv[3] if len(sys.argv) > 3 else None

    status = {
        "collector": "docker exec " + CONTAINER + " psql -U " + PGUSER + " -d " + PGDB,
        "note": "只读查询；未授予任何新权限、未改 pg_hba、未扩大应用运行账号权限",
        "queries": [],
    }
    request_ids = collect_request_ids(evidence_dir)
    status["denied_request_ids_total"] = len(request_ids)
    status["denied_request_ids"] = sorted(request_ids)

    if not request_ids:
        status["status"] = "FAILED"
        status["reason"] = "negative 证据里没有可核对的 request_id（无法证明审计维度）"
        json.dump(status, open(os.path.join(evidence_dir, "audit-collection-status.json"), "w"), indent=2, ensure_ascii=False)
        print("AUDIT: FAILED - 无 request_id 可核对", file=sys.stderr)
        return 1

    id_list = ",".join("'" + rid.replace("'", "''") + "'" for rid in sorted(request_ids))
    sql = (
        "SELECT json_agg(t) FROM (SELECT " + SELECT_COLUMNS + " FROM audit_events_v2 "
        "WHERE request_id IN (" + id_list + ") AND action = 'authorization.denied' "
        "ORDER BY occurred_at) t;"
    )
    status["queries"].append({"purpose": "按本轮 request_id 取拒绝事件", "sql": sql})
    ok, rows, out, err, rc = psql_json(sql)
    if not ok:
        status["status"] = "FAILED"
        status["reason"] = "psql 采集失败（audit_events_v2）"
        status["stderr"] = err.strip()
        status["stdout_head"] = (out or "")[:400]
        status["exit_code"] = rc
        json.dump(status, open(os.path.join(evidence_dir, "audit-collection-status.json"), "w"), indent=2, ensure_ascii=False)
        json.dump({"collection_failed": True, "reason": status["reason"], "stderr": err.strip()},
                  open(os.path.join(evidence_dir, "audit-denials.json"), "w"), indent=2, ensure_ascii=False)
        print("AUDIT: FAILED - " + status["reason"], file=sys.stderr)
        return 1

    rows = rows or []
    by_request = {}
    for row in rows:
        by_request.setdefault(row.get("request_id", ""), []).append(row)

    matched = []
    missing = []
    incomplete = []
    for rid in sorted(request_ids):
        hits = by_request.get(rid, [])
        if not hits:
            missing.append(rid)
            continue
        sample = hits[0]
        gaps = [f for f in REQUIRED_FIELDS if sample.get(f) in (None, "", 0)]
        matched.append({"request_id": rid, "rows": len(hits), "missing_fields": gaps, "sample": sample})
        if gaps:
            incomplete.append({"request_id": rid, "missing_fields": gaps})

    status["rows_returned"] = len(rows)
    status["request_ids_matched"] = len(matched)
    status["request_ids_missing"] = missing
    status["request_ids_with_incomplete_fields"] = incomplete

    # 时间窗内的全量拒绝事件（上下文，用于交叉验证条数）。
    if since and until:
        win_sql = (
            "SELECT json_agg(t) FROM (SELECT " + SELECT_COLUMNS + " FROM audit_events_v2 "
            "WHERE action = 'authorization.denied' AND occurred_at >= '" + since + "'::timestamptz "
            "AND occurred_at <= '" + until + "'::timestamptz ORDER BY occurred_at DESC LIMIT 500) t;"
        )
        status["queries"].append({"purpose": "运行时间窗内全部拒绝事件", "sql": win_sql})
        wok, wrows, wout, werr, wrc = psql_json(win_sql)
        window_rows = wrows if wok else []
        status["window_denials"] = len(window_rows)
        if not wok:
            status["window_query_error"] = werr.strip()

    status["status"] = "OK" if (rows and not missing) else "PARTIAL"
    if not rows:
        status["status"] = "FAILED"
        status["reason"] = "按 request_id 未取到任何 authorization.denied 事件（采集成功但证据缺失）"

    json.dump(status, open(os.path.join(evidence_dir, "audit-collection-status.json"), "w"), indent=2, ensure_ascii=False)
    json.dump({
        "collector": status["collector"],
        "query": status["queries"][0]["sql"],
        "rows_returned": len(rows),
        "matched": matched,
        "missing_request_ids": missing,
        "window_denials": status.get("window_denials"),
        "window_rows": window_rows if since and until and status.get("window_query_error") is None else [],
    }, open(os.path.join(evidence_dir, "audit-denials.json"), "w"), indent=2, ensure_ascii=False)

    print("AUDIT: " + status["status"] + " rows=" + str(len(rows)) + " matched=" + str(len(matched)) + " missing=" + str(len(missing)))
    return 0 if status["status"] == "OK" else 1


if __name__ == "__main__":
    sys.exit(main())
