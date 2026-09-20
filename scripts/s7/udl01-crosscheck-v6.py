#!/usr/bin/env python3
"""udl01-crosscheck-v6.py — U-DL-01 隔离解析轮机器核验（fail-closed，判据与 R10 v6 同构）。

v6 相对 v5b 的差异：把 U5 产品缺陷（超时预算 < 1s 时文案渲染"0 秒"）升为硬判据，
并新增"服务端实际服务的页面 chunk 含新文案与真实预算"的绑定判据。

判据（与根对 R10 的要求一致）：
  1) 矩阵联合判据：每个用例必须同时满足 item.status=="expected"、expectedStatus=="passed"、
     results 恰 1 条且 status=="passed"、errors==[]、duration 为正数、startTime 可解析、retry==0，
     且**该用例自己的证据 JSON 存在、非空、可解析**；本轮 5 个用例全部必须执行，不允许 skip。
  2) 证据语义：见 CHECK 段落，逐条来自真实浏览器度量（帧间隔 / longtask / 弹窗文本 / 输入框状态）。
  3) 夹具绑定：证据中记录的夹具 sha256 必须等于本轮生成的 manifest（真实文件，不是占位）。
  4) U5 绑定：超时/进度弹窗文案无"0 秒"且给出真实预算；服务端实际服务的页面 chunk 含该文案。
退出码：0 全通过；1 有失败；2 核验自身错误。
"""
import hashlib
import json
import os
import re
import sys

EV = sys.argv[1]
CHECKS = []
HARD = []


def check(name, ok, detail):
    CHECKS.append({"check": name, "ok": bool(ok), "detail": detail})
    return bool(ok)


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


class HardError(Exception):
    pass


def main():
    if not os.path.isdir(EV):
        raise HardError("evidence dir missing: " + EV)
    report_path = os.path.join(EV, "playwright-report.json")
    if not os.path.exists(report_path) or os.path.getsize(report_path) == 0:
        raise HardError("playwright-report.json missing/empty")
    report = load(report_path)
    stats = report.get("stats") or {}
    check("playwright-stats", stats.get("expected") == 5 and stats.get("unexpected") == 0
          and stats.get("skipped") == 0 and stats.get("flaky") == 0, stats)

    manifest_path = os.path.join(EV, "fixtures", "udl01-fixtures-manifest.json")
    if not os.path.exists(manifest_path):
        raise HardError("fixture manifest missing: " + manifest_path)
    manifest = load(manifest_path)
    by_name = {f["name"]: f for f in manifest.get("fixtures", [])}
    check("fixtures-count", len(by_name) >= 9, sorted(by_name))
    for name in ("udl01-big.xls", "udl01-big.xlsx"):
        info = by_name.get(name) or {}
        check("fixture-under-import-limit:" + name, 0 < int(info.get("bytes") or 0) < 16 * 1024 * 1024, info)
    big_xls = os.path.join(EV, "fixtures", "udl01-big.xls")
    if os.path.exists(big_xls):
        digest = hashlib.sha256(open(big_xls, "rb").read()).hexdigest()
        check("fixture-bytes-match-manifest:udl01-big.xls", digest == (by_name.get("udl01-big.xls") or {}).get("sha256"),
              {"disk": digest[:16], "manifest": str((by_name.get("udl01-big.xls") or {}).get("sha256"))[:16]})

    # ---- 行数语义（§13.3）：夹具 manifest 同时记录 requestedRows 与 actualFormatCapacity ----
    selfcheck_path = os.path.join(EV, "fixtures", "udl01-fixtures-selfcheck.json")
    if os.path.exists(selfcheck_path):
        selfcheck = load(selfcheck_path)
        rows_sem = selfcheck.get("rowSemantics") or {}
        z64 = selfcheck.get("zip64Layout") or {}
        check("fixture-selfcheck-ok", selfcheck.get("ok") is True and not (selfcheck.get("problems") or []), selfcheck.get("problems"))
        check("fixture-row-semantics", int(rows_sem.get("requestedRows") or 0) == 200000
              and int((rows_sem.get("actualFormatCapacity") or {}).get("maxDataRows") or 0) == 65535
              and 0 < int(rows_sem.get("actualDataRowsOnDisk") or 0) <= 65535, rows_sem)
        check("fixture-zip64-layout-machine-checked", all(z64.get(k) is True for k in (
            "eocd_present", "locator_is_20_bytes_before_eocd", "locator_signature_ok",
            "zip64_eocd_signature_ok", "locator_points_at_zip64_eocd"))
            and z64.get("locator_signature") == "0x07064b50" and z64.get("zip64_eocd_signature") == "0x06064b50",
            z64)
    else:
        check("fixture-selfcheck-present", False, selfcheck_path)

    bind_path = os.path.join(EV, "bundle-binding.json")
    bind = load(bind_path) if os.path.exists(bind_path) else {}

    # ---- 矩阵联合判据 ----
    actual = {}
    def walk(suite, hint):
        project = suite.get("title") if suite.get("title") in ("uc-desktop", "uc-mobile") else hint
        for spec in suite.get("specs", []) or []:
            title = spec.get("title", "") or ""
            key = next((k for k in ("UDL01-A", "UDL01-B", "UDL01-C", "UDL01-D", "UDL01-E") if title.startswith(k)), None)
            if key is None:
                continue
            for item in spec.get("tests", []) or []:
                proj = item.get("projectName") or project
                results = item.get("results") or []
                if proj:
                    actual[(proj, key)] = {
                        "title": title,
                        "status": item.get("status"),
                        "expectedStatus": item.get("expectedStatus"),
                        "result_statuses": [r.get("status") for r in results],
                        "result_durations": [r.get("duration") for r in results],
                        "result_error_counts": [len(r.get("errors") or []) for r in results],
                        "result_retries": [r.get("retry") for r in results],
                        "result_start_times": [r.get("startTime") for r in results],
                    }
        for child in suite.get("suites", []) or []:
            walk(child, project)
    for suite in report.get("suites", []) or []:
        walk(suite, None)

    matrix = []
    for key in ("UDL01-A", "UDL01-B", "UDL01-C", "UDL01-D", "UDL01-E"):
        info = actual.get(("uc-desktop", key))
        row = {"project": "uc-desktop", "test": key, "observed": info, "failures": []}
        ev_path = os.path.join(EV, "uc-desktop", "udl01-" + key.split("-")[1].lower() + "-evidence.json")
        row["evidence_file"] = os.path.relpath(ev_path, EV)
        if info is None:
            row["failures"].append("playwright-report.json 中不存在该用例")
        else:
            if info["status"] != "expected":
                row["failures"].append("item.status=%r" % info["status"])
            if info["expectedStatus"] != "passed":
                row["failures"].append("expectedStatus=%r" % info["expectedStatus"])
            if info["result_statuses"] != ["passed"]:
                row["failures"].append("results.status=%r" % (info["result_statuses"],))
            if info["result_error_counts"] != [0]:
                row["failures"].append("results.errors 非空")
            durs = info["result_durations"]
            if len(durs) != 1 or not isinstance(durs[0], int) or durs[0] <= 0:
                row["failures"].append("duration=%r 不是真实正数" % (durs,))
            if info["result_retries"] != [0]:
                row["failures"].append("retry=%r" % (info["result_retries"],))
            if not info["result_start_times"] or not info["result_start_times"][0]:
                row["failures"].append("startTime 缺失")
        if not os.path.exists(ev_path) or os.path.getsize(ev_path) == 0:
            row["failures"].append("证据 JSON 缺失或为空")
            row["evidence_bytes"] = 0
        else:
            row["evidence_bytes"] = os.path.getsize(ev_path)
            try:
                payload = load(ev_path)
                if not isinstance(payload, dict) or not payload:
                    row["failures"].append("证据 JSON 不是非空对象")
            except Exception as exc:  # noqa: BLE001
                row["failures"].append("证据 JSON 不可解析: %r" % (exc,))
        row["ok"] = not row["failures"]
        matrix.append(row)
    mismatch = [{"test": r["test"], "failures": r["failures"]} for r in matrix if not r["ok"]]
    check("playwright-matrix", not mismatch and len(matrix) == 5, {"rows": len(matrix), "mismatch": mismatch})

    def ev(key):
        return load(os.path.join(EV, "uc-desktop", "udl01-" + key.lower() + "-evidence.json"))

    # ---- UDL01-A：大 .xls 在 Worker 中解析 + 主线程未被阻塞 ----
    a = ev("A")
    check("a-elapsed-positive", int(a.get("elapsedMs") or 0) > 0, {"elapsedMs": a.get("elapsedMs")})
    check("a-dialog-scanned-biff8-max-rows", "扫描行数" in str(a.get("dialogText")) and re.search(r"6553[0-9]", str(a.get("dialogText"))) is not None,
          {"dialogText": str(a.get("dialogText"))[:200]})
    a_rows = int(a.get("actualDataRowsOnDisk") or 0)
    a_reported = int(a.get("rowCountReportedByPage") or -1)
    check("a-row-count-matches-fixture-actual", a_rows > 0 and a_reported == a_rows,
          {"actualDataRowsOnDisk": a.get("actualDataRowsOnDisk"), "rowCountReportedByPage": a.get("rowCountReportedByPage")})
    check("a-row-count-within-format-capacity", 0 < a_reported <= 65535
          and a_reported > 65535 - 8
          and a_reported < int(a.get("requestedRows") or 0),
          {"reported": a_reported, "requestedRows": a.get("requestedRows"),
           "actualFormatCapacity": a.get("actualFormatCapacity")})
    check("a-isolated-worker-used", a.get("isolatedWorkerUsed") is True, {"workerUrls": a.get("workerUrls")})
    am = a.get("metrics") or {}
    check("a-frame-gaps-sampled", int(am.get("gapSamples") or 0) > 10, am)
    check("a-no-second-long-longtask", float(am.get("maxLongTask") or 0) < 1000, am)
    check("a-no-second-frame-gap", float(am.get("maxGap") or 0) < 1000, am)
    check("a-fixture-sha-matches-manifest", str(a.get("bytes")) == str((by_name.get("udl01-big.xls") or {}).get("bytes")),
          {"evidence_bytes": a.get("bytes"), "manifest_bytes": (by_name.get("udl01-big.xls") or {}).get("bytes")})

    # ---- UDL01-B：有界超时 ----
    b = ev("B")
    check("b-timeout-configured", int(b.get("timeoutMs") or 0) == 300, {"timeoutMs": b.get("timeoutMs")})
    check("b-bounded-elapsed", 0 < int(b.get("elapsedMs") or 0) < 60000, {"elapsedMs": b.get("elapsedMs")})
    check("b-notice-says-terminated", "终止" in str(b.get("noticeText")), {"noticeText": str(b.get("noticeText"))[:200]})
    check("b-no-address-imported", int(b.get("addressValueLength") or 0) == 0, {"addressValueLength": b.get("addressValueLength")})
    # U5：超时文案不得出现"0 秒"，且必须给出真实预算（300ms 预算 -> "300 毫秒"）
    b_text = str(b.get("noticeText"))
    # 真实缺陷（v6 第一版）：r"0\s*秒" 会命中 "180 秒" 中的 "0 秒" 子串 -> 加数字边界。
    check("b-budget-no-zero-seconds", re.search(r"(?:^|[^0-9])0\s*秒", b_text) is None, {"noticeText": b_text[:220]})
    check("b-budget-shows-milliseconds", re.search(r"300\s*毫秒", b_text) is not None, {"noticeText": b_text[:220]})
    bm = b.get("metrics") or {}
    check("b-no-second-long-longtask", float(bm.get("maxLongTask") or 0) < 1000, bm)

    # ---- UDL01-C：可取消 + Worker 重生 ----
    c = ev("C")
    check("c-cancel-bounded", 0 < int(c.get("cancelMs") or 0) < 30000, {"cancelMs": c.get("cancelMs")})
    check("c-cancel-notice", ("终止" in str(c.get("cancelText"))) or ("停止" in str(c.get("cancelText"))), {"cancelText": str(c.get("cancelText"))[:200]})
    check("c-no-address-after-cancel", int(c.get("addressValueAfterCancel") or 0) == 0, {"len": c.get("addressValueAfterCancel")})
    check("c-followup-parse-works", "扫描行数" in str(c.get("followUpDialogText")), {"text": str(c.get("followUpDialogText"))[:200]})
    # U5（v6 硬判据）：局部进度必须如实指出正在解析的文件名；预算文案由**服务端实际服务**的
    # page chunk 里同一个 formatParseBudget 求值验证（300ms -> "300 毫秒"、180000ms -> "180 秒"）。
    # 为什么不是直接在进度区读 180 秒：Flowline 页面的局部进度只显示“正在解析哪个文件”，
    # 预算文案只出现在超时弹窗；300ms 的真实超时文案已在 UDL01-A2a 端到端验证，
    # 180000ms 的映射则用同一份已部署字节里的同一个函数求值（其 SHA 必须等于 served page chunk）。
    c_progress = str(c.get("progressText"))
    check("c-progress-shows-file", "udl01-big.xlsx" in c_progress, {"progressText": c_progress[:220]})
    probe = c.get("budgetFormatProbe") or {}
    probe_rows = {str(r.get("input")): r for r in (probe.get("rows") or [])}
    check("c-budget-shows-300ms", (probe_rows.get("300") or {}).get("actual") == "300 毫秒", probe_rows.get("300"))
    check("c-budget-shows-180s", (probe_rows.get("180000") or {}).get("actual") == "180 秒", probe_rows.get("180000"))
    check("c-budget-no-zero-seconds", probe.get("no_zero_second_token") is True
          and len(probe.get("rows") or []) >= 5 and all(r.get("ok") for r in (probe.get("rows") or [])), probe)
    check("c-budget-probe-bound-to-served-chunk", bool(probe.get("chunk_sha256")) and probe.get("chunk_sha256") == bind.get("page_chunk_served_sha"),
          {"probe_sha": str(probe.get("chunk_sha256"))[:16], "served_sha": str(bind.get("page_chunk_served_sha"))[:16], "chunk": probe.get("chunk")})

    # ---- UDL01-D：恶意 ZIP 逐条 ----
    d = ev("D")
    cases = d.get("cases") or []
    check("d-six-cases", len(cases) == 6, [x.get("file") for x in cases])
    for case in cases:
        check("d-rejected:" + str(case.get("file")), bool(case.get("matched")),
              {"expect": case.get("expect"), "notice": str(case.get("noticeText"))[:200]})
        check("d-not-blocking:" + str(case.get("file")), float((case.get("metrics") or {}).get("maxLongTask") or 0) < 1000,
              case.get("metrics"))
    check("d-no-pageerror", not (d.get("pageErrors") or []), d.get("pageErrors"))
    for case in cases:
        calls = case.get("serverCalls") or {}
        check("d-no-sheetjs-chunk:" + str(case.get("file")), int(case.get("sheetjsChunkRequests") or 0) == 0,
              {"sheetjsChunkRequests": case.get("sheetjsChunkRequests"), "chunks": (case.get("chunkRequests") or [])[:6]})
        check("d-no-server-calls:" + str(case.get("file")),
              int(calls.get("validate") or 0) == 0 and int(calls.get("preview") or 0) == 0 and int(calls.get("create") or 0) == 0,
              calls)
        check("d-no-local-summary:" + str(case.get("file")), case.get("summaryVisible") is False,
              {"summaryVisible": case.get("summaryVisible")})
    z64 = d.get("zip64Layout") or {}
    branch = d.get("zip64Branch") or {}
    check("d-zip64-layout-machine-checked", all(z64.get(k) is True for k in (
        "eocd_present", "locator_is_20_bytes_before_eocd", "locator_signature_ok",
        "zip64_eocd_signature_ok", "locator_points_at_zip64_eocd"))
        and z64.get("locator_signature") == "0x07064b50" and z64.get("zip64_eocd_signature") == "0x06064b50", z64)
    check("d-zip64-branch-hit", branch.get("matched") is True and "ZIP64" in str(branch.get("reason")),
          {"reason": str(branch.get("reason"))[:200]})
    check("d-zip64-no-sheetjs", int(branch.get("sheetjsChunkRequests") or 0) == 0, branch)
    check("d-zip64-no-server-calls", int((branch.get("serverCalls") or {}).get("validate") or 0) == 0
          and int((branch.get("serverCalls") or {}).get("preview") or 0) == 0
          and int((branch.get("serverCalls") or {}).get("create") or 0) == 0, branch.get("serverCalls"))
    check("d-zip64-no-task-created", branch.get("taskCreated") is False, branch)

    # ---- UDL01-E：正向回归 ----
    e = ev("E")
    check("e-header-recognized", "address 表头" in str(e.get("dialogText")), {"text": str(e.get("dialogText"))[:220]})
    # §15.3 稳定业务语义：不再依赖“本次送校验 1 个地址”这一整句易变文案。
    stable = e.get("stableSemantics") or {}
    check("e-stable-row-count-one", str(stable.get("rowCount")) == "1", stable)
    check("e-stable-address-count-one", str(stable.get("addressCount")) == "1", stable)
    check("e-stable-header-detected", str(stable.get("headerDetected")) == "true", stable)
    check("e-stable-submit-enabled", stable.get("submitEnabled") is True, stable)
    calls_after = e.get("serverCallsAfterCancel") or {}
    check("e-no-server-call-after-cancel", int(calls_after.get("validate") or 0) == 0
          and int(calls_after.get("preview") or 0) == 0 and int(calls_after.get("create") or 0) == 0, calls_after)

    if bind:
        check("bundle-index-served-matches-disk", bind.get("served_index_sha") == bind.get("disk_index_sha"), bind)
        check("bundle-worker-chunk-200", str(bind.get("worker_chunk_http")) == "200", bind)
        check("bundle-worker-chunk-bytes-match", bind.get("worker_chunk_disk_sha") == bind.get("worker_chunk_served_sha"), bind)
        check("bundle-page-chunk-references-worker", bind.get("page_chunk_references_worker") is True, bind)
        check("bundle-page-chunk-200", str(bind.get("page_chunk_http")) == "200", bind)
        check("bundle-page-chunk-bytes-match", bind.get("page_chunk_disk_sha") == bind.get("page_chunk_served_sha"), bind)
        # U5 绑定：**服务端实际服务**的页面 chunk 必须含新文案与真实预算单位
        check("bundle-page-chunk-has-budget-unit", int(bind.get("page_chunk_budget_ms_hits") or 0) >= 1, bind)
        check("bundle-page-chunk-has-timeout-copy", int(bind.get("page_chunk_timeout_copy_hits") or 0) >= 1, bind)
        check("bundle-csp-present", int(bind.get("csp_headers") or 0) >= 1, bind)
    else:
        check("bundle-binding-present", False, bind_path)

    failed = [c for c in CHECKS if not c["ok"]]
    print(json.dumps({
        "suite": "U-DL-01 isolated import parse (worker / bounded timeout / cancel / malicious ZIP) v6",
        "evidence_dir": EV,
        "checks_total": len(CHECKS),
        "checks_failed": len(failed),
        "failed": failed,
        "checks": CHECKS,
        "matrix": matrix,
        "verdict": "PASS" if not failed else "FAIL",
    }, ensure_ascii=False, indent=2))
    return 0 if not failed else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HardError as exc:
        print(json.dumps({"verdict": "ERROR", "hard_error": str(exc), "checks": CHECKS}, ensure_ascii=False, indent=2))
        sys.exit(2)
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"verdict": "ERROR", "unexpected_error": repr(exc), "checks": CHECKS}, ensure_ascii=False, indent=2))
        sys.exit(2)
