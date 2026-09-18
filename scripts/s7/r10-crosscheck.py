#!/usr/bin/env python3
"""r10-crosscheck.py — 把 R10 浏览器证据与 PostgreSQL / ClickHouse / 审计逐项对照，输出机器 JSON。

用法: r10-crosscheck.py <EVIDENCE_DIR> <JOB_ID> <JOB_NO> <CHAIN> <ADDRESS>

退出码：0 = 全部对照通过；1 = 存在对照失败（详情在 JSON 的 checks 里）。
不写数据库、不删文件、不 mock：只读 exports / audit_events 与 ClickHouse。
"""

import json
import os
import subprocess
import sys

EV, JOB_ID, JOB_NO, CHAIN, ADDRESS = sys.argv[1:6]

DESKTOP_TESTS = ["R10-01", "R10-02", "R10-03", "R10-04", "R10-05"]
MOBILE_TESTS = ["R10-01"]


def psql(sql):
    out = subprocess.run(
        ["docker", "exec", "-i", "deploy-postgres-1", "psql", "-U", "blockchain", "-d", "blockchain_platform",
         "-tA", "-F", "\x1f", "-v", "ON_ERROR_STOP=1"],
        input=sql, capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError("psql failed: " + out.stderr.strip())
    return [line.split("\x1f") for line in out.stdout.strip().splitlines() if line != ""]


def ch(sql):
    out = subprocess.run(
        ["docker", "exec", "-i", "deploy-clickhouse-1", "clickhouse-client", "--query", sql],
        capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError("clickhouse failed: " + out.stderr.strip())
    return out.stdout.strip()


def load(path):
    with open(path, "r", encoding="utf-8") as handle:
        return json.load(handle)


checks = []
verdicts = []


def check(name, ok, detail):
    checks.append({"check": name, "ok": bool(ok), "detail": detail})
    return bool(ok)


def walk_evidence():
    found = {}
    for root, _dirs, files in os.walk(EV):
        for name in files:
            if name.startswith("r10-") and name.endswith("-evidence.json"):
                found[os.path.join(root, name)] = load(os.path.join(root, name))
    return found


evidence = walk_evidence()

# --- 1. Playwright 矩阵 -----------------------------------------------------
matrix = []
try:
    report = load(os.path.join(EV, "playwright-report.json"))
except Exception as exc:  # noqa: BLE001
    report = None
    check("playwright-report-readable", False, str(exc))

if report is not None:
    actual = {}

    def walk(suite, hint):
        project = suite.get("title") if suite.get("title") in ("uc-desktop", "uc-mobile") else hint
        for spec in suite.get("specs", []) or []:
            title = spec.get("title", "") or ""
            key = next((c for c in DESKTOP_TESTS if title.startswith(c)), None)
            if key is None:
                continue
            for item in spec.get("tests", []) or []:
                proj = item.get("projectName") or project
                status = item.get("status")
                if not status:
                    results = item.get("results") or [{}]
                    status = results[-1].get("status")
                if proj:
                    actual[(proj, key)] = status
        for child in suite.get("suites", []) or []:
            walk(child, project)

    for suite in report.get("suites", []) or []:
        walk(suite, None)

    for proj, expected_keys in (("uc-desktop", DESKTOP_TESTS), ("uc-mobile", MOBILE_TESTS)):
        for key in DESKTOP_TESTS:
            if key in expected_keys:
                expected, want = "run", "passed"
            else:
                expected, want = "excluded-by-project-design", "skipped"
            got = actual.get((proj, key), "MISSING")
            matrix.append({"project": proj, "test": key, "expected": expected, "actual": got, "ok": got == want})

    bad = [row for row in matrix if not row["ok"]]
    check("playwright-matrix", not bad, {"rows": matrix, "mismatch": bad})

# --- 2. 逐份证据与 DB 对照 --------------------------------------------------
per_export = []
for path, ev in sorted(evidence.items()):
    project = os.path.basename(os.path.dirname(path))
    name = os.path.basename(path)
    export = ev.get("export") or {}
    export_id = export.get("id")
    if not export_id:
        continue
    rows = psql("SELECT id, status, format, zip, rows, sha256, object_ref, job_id, dataset FROM exports WHERE id='%s';" % export_id)
    if not rows:
        check("db-export-row:" + name, False, "exports row not found for " + str(export_id))
        continue
    db_id, db_status, db_format, db_zip, db_rows, db_sha, db_ref, db_job, db_dataset = rows[0]
    entry = {
        "evidence": os.path.relpath(path, EV),
        "project": project,
        "export_id": export_id,
        "db": {"status": db_status, "format": db_format, "zip": db_zip == "t", "rows": int(db_rows),
               "sha256": db_sha, "object_ref": db_ref, "job_id": db_job, "dataset": db_dataset},
        "file": ev.get("file"),
    }
    check("db-status-completed:" + name, db_status == "COMPLETED", db_status)
    check("db-format-matches:" + name, db_format == str(export.get("format")), [db_format, export.get("format")])
    check("db-job-scope:" + name, (db_job or "") == JOB_ID if name.startswith("r10-01") else True, [db_job, JOB_ID])
    file_sha = (ev.get("file") or {}).get("sha256")
    if file_sha:
        entry["sha_chain"] = {"file": file_sha, "db": db_sha, "api": export.get("sha256")}
        check("sha256-file-equals-db:" + name, file_sha == db_sha, entry["sha_chain"])
        check("sha256-file-equals-api:" + name, file_sha == export.get("sha256"), entry["sha_chain"])
    if "sheets" in ev:
        sheet_total = sum(int(s.get("dataRowCount", 0)) for s in ev["sheets"])
        entry["sheet_row_total"] = sheet_total
        check("rows-sheet-total-equals-db:" + name, sheet_total == int(db_rows), [sheet_total, int(db_rows)])
        per_entry = []
        for sheet in ev["sheets"]:
            parts = sheet.get("entry", "").split("/")
            if len(parts) != 3:
                continue
            addr, dataset = parts[1], parts[2].rsplit(".", 1)[0]
            if dataset == "transactions":
                sql = ("SELECT uniqExact(tx_hash) FROM blockchain.transactions FINAL "
                       "WHERE chain='%s' AND (from_address='%s' OR to_address='%s')" % (CHAIN, addr, addr))
            elif dataset == "token_transfers":
                sql = ("SELECT uniqExact((tx_hash, log_index, token_address)) FROM blockchain.token_transfers FINAL "
                       "WHERE chain='%s' AND (from_address='%s' OR to_address='%s')" % (CHAIN, addr, addr))
            else:
                continue
            expected = int(ch(sql))
            per_entry.append({"entry": sheet["entry"], "sheetjs_rows": sheet["dataRowCount"], "clickhouse_uniq": expected})
            check("sheet-rows-equal-clickhouse:" + name + ":" + sheet["entry"],
                  int(sheet["dataRowCount"]) == expected, [sheet["dataRowCount"], expected])
        entry["per_entry"] = per_entry
    if "workbook" in ev:
        wb = ev["workbook"]
        entry["workbook_rows"] = wb.get("dataRowCount")
        check("rows-workbook-equals-db:" + name, int(wb.get("dataRowCount", -1)) == int(db_rows), [wb.get("dataRowCount"), int(db_rows)])
        sql = ("SELECT uniqExact(tx_hash) FROM blockchain.transactions FINAL "
               "WHERE chain='%s' AND (from_address='%s' OR to_address='%s')" % (CHAIN, ADDRESS, ADDRESS))
        expected = int(ch(sql))
        entry["clickhouse_uniq"] = expected
        check("workbook-rows-equal-clickhouse:" + name, int(wb.get("dataRowCount", -1)) == expected, [wb.get("dataRowCount"), expected])

    audit = psql("SELECT action, count(*) FROM audit_events WHERE resource_id='%s' GROUP BY 1 ORDER BY 1;" % export_id)
    entry["audit"] = [{"action": a, "count": int(c)} for a, c in audit]
    actions = {a for a, _c in audit}
    check("audit-export-completed:" + name, "export.completed" in actions, sorted(actions))
    if name.startswith("r10-01") or name.startswith("r10-02") or name.startswith("r10-03"):
        check("audit-export-download:" + name, "export.download" in actions, sorted(actions))

    if name.startswith("r10-03"):
        reuse = ev.get("reuse") or {}
        check("ticket-single-use-410:" + name, reuse.get("status") == 410, reuse.get("status"))

    per_export.append(entry)

check("evidence-count", len(per_export) >= 4, len(per_export))

failed = [c for c in checks if not c["ok"]]
summary = {
    "evidence_dir": EV,
    "job": {"id": JOB_ID, "job_no": JOB_NO},
    "scope": {"chain": CHAIN, "address": ADDRESS},
    "checks_total": len(checks),
    "checks_failed": len(failed),
    "failed": failed,
    "matrix": matrix,
    "exports": per_export,
    "verdict": "PASS" if not failed else "FAIL",
}
print(json.dumps(summary, ensure_ascii=False, indent=2))
sys.exit(0 if not failed else 1)
