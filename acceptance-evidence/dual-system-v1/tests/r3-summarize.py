#!/usr/bin/env python3
"""R-3 证据汇总（v2，收紧判据）。

相对首轮的修正：
  1. 首轮用正则解析 list reporter，只识别 ms/s，漏掉了两条 2.0m 的用例
     （cases=12 而实际 14），并且 case 的 status/name 全是 None。本版改为读
     Playwright 的 **JSON reporter**（playwright-report.json）作为唯一事实来源。
  2. 首轮把"审计采集失败（0 字节输出）"写成了 audit_denials_count=0，等于把
     "没采到"误报成"已核对为 0 条"。本版把审计采集状态列为 **PASS 的必要条件**，
     采集失败一律 FAIL 并在 unclosed 里写明。
  3. PASS 判据收紧为四项同时成立：
     (a) 每个 project 的全部用例都被报告 accounted（数量 = 去重用例数 × project 数）；
     (b) skipped == 0；
     (c) playwright 退出码 == 0；
     (d) 审计必需证据采集成功（audit-collection-status.json 的 status == OK）。

用法：python3 r3-summarize.py <evidence_dir> <playwright_rc>
"""
import json
import os
import sys


def walk(suite, out, file_hint=None):
    current_file = suite.get("file", file_hint)
    for spec in suite.get("specs", []) or []:
        for test in spec.get("tests", []) or []:
            results = test.get("results", []) or []
            last = results[-1] if results else {}
            error = (last.get("error") or {}).get("message") or ""
            out.append({
                "project": test.get("projectName", ""),
                "file": current_file or "",
                "title": spec.get("title", ""),
                "status": last.get("status") or test.get("status", ""),
                "expected_status": test.get("expectedStatus", ""),
                "duration_ms": last.get("duration"),
                "retries": last.get("retry", 0),
                "error_head": error.strip().splitlines()[0] if error.strip() else None,
                "error_text": error.strip()[:1500] if error.strip() else None,
            })
    for child in suite.get("suites", []) or []:
        walk(child, out, current_file)


def main():
    evidence_dir, rc = sys.argv[1], int(sys.argv[2])
    report_path = os.path.join(evidence_dir, "playwright-report.json")
    audit_status_path = os.path.join(evidence_dir, "audit-collection-status.json")
    unclosed = []

    cases = []
    stats = {}
    report_error = None
    if os.path.exists(report_path):
        try:
            report = json.load(open(report_path, errors="replace"))
            for suite in report.get("suites", []) or []:
                walk(suite, cases)
            stats = report.get("stats", {}) or {}
        except Exception as exc:  # noqa: BLE001
            report_error = "playwright-report.json 解析失败: " + str(exc)
    else:
        report_error = "缺少 playwright-report.json（JSON reporter 未产出）"
    if report_error:
        unclosed.append(report_error)

    projects = sorted({c["project"] for c in cases})
    unique_titles = sorted({c["file"] + "::" + c["title"] for c in cases})
    expected_case_count = len(unique_titles) * len(projects) if projects else 0

    failed = [c for c in cases if c["status"] not in ("passed", "skipped")]
    skipped = [c for c in cases if c["status"] == "skipped" or c["expected_status"] == "skipped"]
    passed = [c for c in cases if c["status"] == "passed"]

    # 审计维度：采集必须成功，空/缺一律 FAIL。
    audit_status = None
    if os.path.exists(audit_status_path):
        try:
            audit_status = json.load(open(audit_status_path, errors="replace"))
        except Exception as exc:  # noqa: BLE001
            audit_status = {"status": "FAILED", "reason": "audit-collection-status.json 解析失败: " + str(exc)}
    else:
        audit_status = {"status": "FAILED", "reason": "缺少 audit-collection-status.json（审计维度未采集）"}
    audit_ok = audit_status.get("status") == "OK"

    if expected_case_count == 0 or len(cases) != expected_case_count:
        unclosed.append(
            "用例未被完整 accounted：报告 " + str(len(cases)) + " 条，期望 " + str(expected_case_count)
            + "（去重用例 " + str(len(unique_titles)) + " × project " + str(len(projects)) + "）"
        )
    if skipped:
        unclosed.append("存在 skipped 用例：" + ", ".join(sorted({c["title"] for c in skipped})))
    if failed:
        unclosed.append("存在失败用例：" + ", ".join(sorted({c["title"] for c in failed})))
    if rc != 0:
        unclosed.append("playwright 退出码非 0：" + str(rc))
    if not audit_ok:
        unclosed.append("审计证据采集未成功：" + str(audit_status.get("reason") or audit_status.get("status")))

    result = {
        "task": "R-3 legacy V1 UI (api-control / system) admin-audience defect",
        "evidence_dir": evidence_dir,
        "entry": os.environ.get("E2E_BASE_URL", ""),
        "playwright_rc": rc,
        "projects": projects,
        "unique_case_titles": len(unique_titles),
        "cases_accounted": len(cases),
        "cases_expected": expected_case_count,
        "passed": len(passed),
        "failed": len(failed),
        "skipped": len(skipped),
        "flaky": int(stats.get("flaky", 0) or 0),
        "cases": cases,
        "failed_case_details": [
            {"project": c["project"], "file": c["file"], "title": c["title"], "status": c["status"], "error": c["error_text"]}
            for c in failed
        ],
        "audit_evidence": {
            "status": audit_status.get("status"),
            "collector": audit_status.get("collector"),
            "rows_returned": audit_status.get("rows_returned"),
            "denied_request_ids_total": audit_status.get("denied_request_ids_total"),
            "request_ids_matched": audit_status.get("request_ids_matched"),
            "missing_request_ids": audit_status.get("request_ids_missing"),
            "incomplete_fields": audit_status.get("request_ids_with_incomplete_fields"),
            "window_denials": audit_status.get("window_denials"),
            "reason": audit_status.get("reason"),
            "stderr": audit_status.get("stderr"),
        },
        "unclosed": unclosed,
    }
    result["criteria"] = (
        "cases_accounted==cases_expected AND skipped==0 AND playwright_rc==0 "
        "AND failed==0 AND audit_evidence.status=='OK'"
    )
    result["status"] = "PASS" if not unclosed else "FAIL"

    json.dump(result, open(os.path.join(evidence_dir, "result.json"), "w"), indent=2, ensure_ascii=False)

    print(json.dumps({k: v for k, v in result.items() if k not in ("cases", "failed_case_details")}, indent=2, ensure_ascii=False))
    if failed:
        print("--- failed cases ---")
        for c in failed:
            print("[" + c["project"] + "] " + c["title"])
            print("    " + str(c["error_head"]))
    sys.exit(0 if result["status"] == "PASS" else 1)


if __name__ == "__main__":
    main()
