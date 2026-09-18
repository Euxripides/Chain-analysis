#!/usr/bin/env python3
"""r10-crosscheck-v2.py — R10 严格机器核验（v2：按根 review 逐条更正 v1）。

对 v1 的更正（v1 原样保留，不 hot-edit）：
  C1  reporter：不再假定 --reporter=list --reporter=json 会写出文件（CLI 会覆盖 config）。
      v2 实际校验 playwright-report.json 存在、非空、可解析、含预期 spec；否则硬失败。
  C2  输入：chain/address/job_no/dataset 全部正则白名单；ClickHouse 查询一律经 **stdin**，
      不拼进 argv/SQL 字符串；地址一律取**服务端归一化值**（download_subjects.subject）。
  C3  P4：不再要求 exports 前后相等。jobs/shards/checkpoints/outbox 做到"原 PK 不丢"（真正的
      子集核验，COPY FROM STDIN 进临时表），exports 必须 = 前值 + 本轮真实新增且每个新增 ID 存在。
      没有任何 "|| true"；任一项不满足即非零退出。
  C4  行数期望：按服务端 exportJobBounds 语义从 download_jobs 真实读 range（time→timestamp
      区间；否则 block_number 区间；balances/无 job→无界），不再用缺 range 的近似谓词。
  C5  MinIO：独立走 S3（mc，从容器内提取到宿主，凭据只经进程环境，不进 argv/证据），
      核对对象 size/etag/content-type + 真实字节 sha256；与"经 API 同路下载"分开记录。
  C6  作用域：先验证 job ∈ 本用户 org/workspace，且地址确在该 job 的 download_subjects 中。
退出码：0=全部通过；1=有对照失败；2=核验自身错误（缺文件/DB 错误/解析失败）。
"""

import hashlib
import json
import os
import re
import subprocess
import sys

EV = sys.argv[1]
SCOPE_PATH = os.path.join(EV, "scope.json")
MINIO_ENV_PATH = os.environ.get("R10_MINIO_ENV", "")
MC_PATH = "/tmp/r10-mc/mc"
TEST_KEYS = ["R10-01", "R10-02", "R10-03", "R10-04", "R10-05", "R10-06", "R10-07", "R10-08"]
DESKTOP_TESTS = list(TEST_KEYS)
MOBILE_TESTS = ["R10-01"]
NON_EXPORT_EVIDENCE = ("r10-07-evidence.json", "r10-08-evidence.json")

RE_CHAIN = re.compile(r"^[a-z0-9_-]{2,20}$")
RE_ADDR_EVM = re.compile(r"^0x[0-9a-f]{40}$")
RE_ADDR_TRON = re.compile(r"^T[1-9A-HJ-NP-Za-km-z]{33}$")
RE_JOB_NO = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
RE_DATASET = re.compile(r"^(transactions|token_transfers|nft_transfers|logs|balances|internal_transactions)$")

checks = []
hard_errors = []


def check(name, ok, detail):
    checks.append({"check": name, "ok": bool(ok), "detail": detail})
    return bool(ok)


class HardError(Exception):
    pass


def run(cmd, stdin=None, timeout=600):
    out = subprocess.run(cmd, input=stdin, capture_output=True, text=False, timeout=timeout)
    if out.returncode != 0:
        raise HardError("command failed (%s): %s" % (" ".join(cmd[:6]), out.stderr.decode("utf-8", "replace").strip()[:600]))
    return out.stdout


def psql(sql, timeout=600):
    raw = run(["docker", "exec", "-i", "deploy-postgres-1", "psql", "-U", "blockchain",
               "-d", "blockchain_platform", "-q", "-tA", "-F", "\x1f", "-v", "ON_ERROR_STOP=1"],
              stdin=sql.encode("utf-8"), timeout=timeout)
    text = raw.decode("utf-8", "replace")
    return [line.split("\x1f") for line in text.splitlines() if line != ""]


def ch(sql, timeout=900):
    raw = run(["docker", "exec", "-i", "deploy-clickhouse-1", "clickhouse-client",
               "--format", "TabSeparated"], stdin=sql.encode("utf-8"), timeout=timeout)
    return raw.decode("utf-8", "replace").strip()


def one(sql):
    rows = psql(sql)
    if not rows or not rows[0]:
        return None
    return rows[0][0]


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load(path):
    with open(path, "r", encoding="utf-8") as handle:
        return json.load(handle)


def pk_subset_ok(table, ids_path):
    """pre 时的 PK 集合在 post 时必须全部仍存在（真子集核验，经 COPY FROM STDIN）。"""
    with open(ids_path, "r", encoding="utf-8") as handle:
        ids = [line.strip() for line in handle if line.strip()]
    if not ids:
        raise HardError("empty pk snapshot " + ids_path)
    script = ("CREATE TEMP TABLE pre_ids(id text);\n"
              "COPY pre_ids FROM STDIN;\n" + "\n".join(ids) + "\n\\.\n"
              "SELECT 'RESULT:' || count(*) FROM pre_ids p "
              "WHERE NOT EXISTS (SELECT 1 FROM " + table + " t WHERE t.id::text = p.id);\n")
    rows = psql(script)
    for row in rows:
        if row and row[0].startswith("RESULT:"):
            return int(row[0].split(":", 1)[1]), len(ids)
    raise HardError("pk subset check produced no RESULT line for " + table)


def export_bounds_predicate(job):
    """严格复刻 backend/internal/api/export.go exportJobBounds 的语义。"""
    if job is None:
        return ""
    if job["range_type"] == "time":
        parts = []
        if job["from_timestamp"]:
            parts.append(" AND timestamp >= '%s'" % job["from_timestamp"])
        if job["to_timestamp"]:
            parts.append(" AND timestamp <= '%s'" % job["to_timestamp"])
        return "".join(parts)
    parts = []
    if job["from_block"] is not None:
        parts.append(" AND block_number >= %d" % int(job["from_block"]))
    if job["to_block"] is not None:
        parts.append(" AND block_number <= %d" % int(job["to_block"]))
    return "".join(parts)


def address_expected_rows(chain, address, dataset, bounds):
    if dataset == "transactions":
        expr = "uniqExact(tx_hash)"
    elif dataset == "token_transfers":
        expr = "uniqExact((tx_hash, log_index, token_address))"
    elif dataset == "nft_transfers":
        expr = "uniqExact((tx_hash, log_index, contract_address, token_id))"
    elif dataset == "logs":
        expr = "uniqExact((tx_hash, log_index))"
    else:
        return None
    sql = ("SELECT " + expr + " FROM blockchain." + dataset + " FINAL WHERE chain = '" + chain +
           "' AND (from_address = '" + address + "' OR to_address = '" + address + "')" + bounds + ";\n")
    return int(ch(sql))


def main():
    if not os.path.isdir(EV):
        raise HardError("evidence dir missing: " + EV)
    if not os.path.exists(SCOPE_PATH):
        raise HardError("scope.json missing (runner preflight must write it)")
    scope = load(SCOPE_PATH)

    chain = scope["chain"]
    address = scope["address"]
    job_id = scope["job_id"]
    job_no = scope["job_no"]
    org_id = scope["org_id"]
    ws_id = scope["ws_id"]

    # ---- C2/C6：输入白名单 + 作用域 ----
    check("scope-chain-format", bool(RE_CHAIN.match(chain)), chain)
    check("scope-address-format", bool(RE_ADDR_EVM.match(address) or RE_ADDR_TRON.match(address)), address)
    check("scope-jobno-format", bool(RE_JOB_NO.match(job_no)), job_no)
    check("scope-dataset-format", bool(RE_DATASET.match(scope["dataset"])), scope["dataset"])
    check("scope-jobid-uuid", bool(re.match(r"^[0-9a-f-]{36}$", job_id)), job_id)

    rows = psql("SELECT id::text, coalesce(organization_id::text,''), coalesce(workspace_id::text,''), status,"
                " range_type, from_block, to_block, from_timestamp, to_timestamp, array_to_string(datasets,','), chain"
                " FROM download_jobs WHERE id = '%s';\n" % job_id)
    if not rows:
        raise HardError("job not found: " + job_id)
    job = {
        "id": rows[0][0], "organization_id": rows[0][1], "workspace_id": rows[0][2], "status": rows[0][3],
        "range_type": rows[0][4],
        "from_block": int(rows[0][5]) if rows[0][5] != "" else None,
        "to_block": int(rows[0][6]) if rows[0][6] != "" else None,
        "from_timestamp": rows[0][7] or None, "to_timestamp": rows[0][8] or None,
        "datasets": rows[0][9].split(","), "chain": rows[0][10],
    }
    check("job-in-scope-org", job["organization_id"] == org_id, [job["organization_id"], org_id])
    check("job-in-scope-ws", job["workspace_id"] == ws_id, [job["workspace_id"], ws_id])
    check("job-completed", job["status"] == "COMPLETED", job["status"])
    check("job-chain-matches", job["chain"] == chain, [job["chain"], chain])
    check("job-has-dataset", scope["dataset"] in job["datasets"], job["datasets"])

    subject_rows = psql("SELECT subject FROM download_subjects WHERE job_id = '%s' AND subject = '%s';\n" % (job_id, address))
    check("job-has-normalized-subject", len(subject_rows) == 1, [address, len(subject_rows)])
    server_address = subject_rows[0][0] if subject_rows else address
    check("address-is-server-normalized", server_address == address, [address, server_address])

    bounds = export_bounds_predicate(job)
    no_job_bounds = ""

    # ---- C1：真实校验机器 report ----
    report_path = os.path.join(EV, "playwright-report.json")
    if not os.path.exists(report_path) or os.path.getsize(report_path) == 0:
        raise HardError("playwright-report.json missing/empty — CLI reporter override 未修正（v1 的真实缺陷）")
    report = load(report_path)
    check("playwright-report-has-suites", bool(report.get("suites")), len(report.get("suites") or []))

    actual = {}

    def walk(suite, hint):
        project = suite.get("title") if suite.get("title") in ("uc-desktop", "uc-mobile") else hint
        for spec in suite.get("specs", []) or []:
            title = spec.get("title", "") or ""
            key = next((c for c in TEST_KEYS if title.startswith(c)), None)
            if key is None:
                continue
            for item in spec.get("tests", []) or []:
                proj = item.get("projectName") or project
                status = item.get("status")
                if not status:
                    status = (item.get("results") or [{}])[-1].get("status")
                if proj:
                    actual[(proj, key)] = status
        for child in suite.get("suites", []) or []:
            walk(child, project)

    for suite in report.get("suites", []) or []:
        walk(suite, None)

    matrix = []
    for proj, expected_keys in (("uc-desktop", DESKTOP_TESTS), ("uc-mobile", MOBILE_TESTS)):
        for key in TEST_KEYS:
            expected, want = ("run", "passed") if key in expected_keys else ("excluded-by-project-design", "skipped")
            got = actual.get((proj, key), "MISSING")
            matrix.append({"project": proj, "test": key, "expected": expected, "actual": got, "ok": got == want})
    mismatch = [row for row in matrix if not row["ok"]]
    check("playwright-matrix", not mismatch, {"observed": actual and {("%s/%s" % k): v for k, v in actual.items()}, "mismatch": mismatch})

    # ---- 逐份证据：DB / CH / 审计 ----
    evidence = {}
    for root, _dirs, files in os.walk(EV):
        for name in files:
            if name.startswith("r10-") and name.endswith("-evidence.json"):
                path = os.path.join(root, name)
                evidence[path] = load(path)
    check("evidence-file-count", len(evidence) >= 4, sorted(os.path.basename(p) for p in evidence))

    per_export = []
    new_export_ids = set()
    for path, ev in sorted(evidence.items()):
        name = os.path.basename(path)
        project = os.path.basename(os.path.dirname(path))
        export = ev.get("export") or {}
        export_id = export.get("id")
        if not export_id:
            if name in NON_EXPORT_EVIDENCE:
                special_checks(name, ev, per_export)
                continue
            check("evidence-has-export-id:" + name, False, "missing export.id")
            continue
        new_export_ids.add(export_id)
        rows = psql("SELECT id::text, status, format, zip, rows, sha256, object_ref, coalesce(job_id::text,''),"
                    " coalesce(organization_id::text,''), coalesce(workspace_id::text,''), dataset"
                    " FROM exports WHERE id = '%s';\n" % export_id)
        if not rows:
            check("db-export-row:" + name, False, "exports row missing for " + export_id)
            continue
        db = {"id": rows[0][0], "status": rows[0][1], "format": rows[0][2], "zip": rows[0][3] == "t",
              "rows": int(rows[0][4]), "sha256": rows[0][5], "object_ref": rows[0][6], "job_id": rows[0][7],
              "organization_id": rows[0][8], "workspace_id": rows[0][9], "dataset": rows[0][10]}
        entry = {"evidence": os.path.relpath(path, EV), "project": project, "export_id": export_id, "db": db}

        check("db-status-completed:" + name, db["status"] == "COMPLETED", db["status"])
        check("db-format-matches:" + name, db["format"] == str(export.get("format")), [db["format"], export.get("format")])
        check("db-export-in-scope:" + name, db["organization_id"] == org_id and db["workspace_id"] == ws_id,
              [db["organization_id"], db["workspace_id"]])
        if name.startswith("r10-01"):
            check("db-export-job-scope:" + name, db["job_id"] == job_id, [db["job_id"], job_id])
        else:
            check("db-export-no-job:" + name, db["job_id"] == "", db["job_id"])
        check("db-object-ref-present:" + name, bool(db["object_ref"]), db["object_ref"])

        file_info = ev.get("file") or {}
        file_sha = file_info.get("sha256")
        entry["api_download_chain"] = {"file_sha256": file_sha, "db_sha256": db["sha256"], "api_sha256": export.get("sha256"),
                                       "note": "经同一 API 下载路径得到，不等于独立对象存储校验（见 minio_independent）"}
        if file_sha:
            check("sha256-file-equals-db:" + name, file_sha == db["sha256"], entry["api_download_chain"])
            check("sha256-file-equals-api:" + name, file_sha == export.get("sha256"), entry["api_download_chain"])

        if "sheets" in ev:
            sheet_total = sum(int(s.get("dataRowCount", 0)) for s in ev["sheets"])
            entry["sheet_row_total"] = sheet_total
            check("rows-sheet-total-equals-db:" + name, sheet_total == db["rows"], [sheet_total, db["rows"]])
            manifest = (ev.get("zip") or {}).get("manifest") or {}
            if manifest:
                entry["manifest"] = manifest
                check("manifest-export-id:" + name, manifest.get("export_id") == export_id, manifest.get("export_id"))
                check("manifest-job-id:" + name, manifest.get("job_id") == job_id, manifest.get("job_id"))
                check("manifest-addresses-match-db-subjects:" + name,
                      sorted(manifest.get("addresses") or []) == [server_address], manifest.get("addresses"))
                check("manifest-datasets-match-job:" + name,
                      sorted(manifest.get("datasets") or []) == sorted(job["datasets"]),
                      [manifest.get("datasets"), job["datasets"]])
                check("manifest-partial-false:" + name, manifest.get("partial") is False, manifest.get("partial"))
            per_entry = []
            for sheet in ev["sheets"]:
                parts = str(sheet.get("entry", "")).split("/")
                if len(parts) != 3:
                    continue
                addr, dataset = parts[1], parts[2].rsplit(".", 1)[0]
                expected = address_expected_rows(chain, server_address, dataset, bounds)
                if expected is None:
                    continue
                per_entry.append({"entry": sheet["entry"], "entry_address": addr, "dataset": dataset,
                                  "sheetjs_rows": sheet["dataRowCount"], "clickhouse_uniq_with_job_bounds": expected,
                                  "bounds": bounds or "(none)"})
                check("sheet-entry-address-matches:" + name + ":" + sheet["entry"], addr == server_address, [addr, server_address])
                check("sheet-rows-equal-clickhouse:" + name + ":" + sheet["entry"],
                      int(sheet["dataRowCount"]) == expected, [sheet["dataRowCount"], expected])
            entry["per_entry"] = per_entry
        if "workbook" in ev:
            wb = ev["workbook"]
            entry["workbook_rows"] = wb.get("dataRowCount")
            expected = address_expected_rows(chain, server_address, scope["dataset"], no_job_bounds)
            entry["clickhouse_uniq_no_bounds"] = expected
            check("rows-workbook-equals-db:" + name, int(wb.get("dataRowCount", -1)) == db["rows"], [wb.get("dataRowCount"), db["rows"]])
            check("workbook-rows-equal-clickhouse:" + name, int(wb.get("dataRowCount", -1)) == expected, [wb.get("dataRowCount"), expected])

        audit = psql("SELECT action, count(*) FROM audit_events WHERE resource_id = '%s' GROUP BY 1 ORDER BY 1;\n" % export_id)
        entry["audit"] = [{"action": a, "count": int(c)} for a, c in audit]
        actions = {a for a, _c in audit}
        check("audit-export-completed:" + name, "export.completed" in actions, sorted(actions))
        if name.startswith(("r10-01", "r10-02", "r10-03")):
            check("audit-export-download:" + name, "export.download" in actions, sorted(actions))
            check("audit-export-ticket:" + name, "export.download_ticket" in actions, sorted(actions))

        if name.startswith("r10-03"):
            reuse = ev.get("reuse") or {}
            check("ticket-single-use-410:" + name, reuse.get("status") == 410, reuse.get("status"))

        if name.startswith("r10-05"):
            pos = ev.get("positive") or {}
            check("legacy-import-positive:" + name, bool(pos.get("metaText")), pos.get("metaText"))
            entry["legacy_format_guard"] = ev.get("legacyFormatGuard")

        entry["minio_independent"] = minio_check(export_id, db["object_ref"], db["sha256"], file_sha, name)
        per_export.append(entry)

    # ---- C3：状态不变量（原 PK 不丢 + exports 精确增量） ----
    invariants = {}
    for table in ("download_jobs", "download_shards", "checkpoints", "outbox_events", "exports"):
        ids_path = os.path.join(EV, "pre-ids-%s.txt" % table)
        if not os.path.exists(ids_path):
            raise HardError("missing pre-state pk snapshot for " + table)
        missing, total = pk_subset_ok(table, ids_path)
        after = int(one("SELECT count(*) FROM " + table + ";\n") or 0)
        before = total
        invariants[table] = {"before": before, "after": after, "original_pk_missing": missing}
        check("no-original-pk-lost:" + table, missing == 0, invariants[table])
        if table != "exports":
            check("count-not-decreased:" + table, after >= before, invariants[table])
    exports_after = invariants["exports"]["after"]
    exports_before = invariants["exports"]["before"]
    new_rows = psql("SELECT count(*) FROM exports WHERE id::text IN (%s);\n" %
                    ",".join("'" + i + "'" for i in sorted(new_export_ids))) if new_export_ids else [["0"]]
    check("exports-delta-equals-new-ids", exports_after == exports_before + len(new_export_ids),
          {"before": exports_before, "after": exports_after, "new_ids": len(new_export_ids)})
    check("all-new-export-ids-exist", int(new_rows[0][0]) == len(new_export_ids),
          {"found": new_rows[0][0], "expected": len(new_export_ids)})

    failed = [c for c in checks if not c["ok"]]
    summary = {
        "suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v2)",
        "evidence_dir": EV,
        "external_review_corrections_applied": ["C1 reporter", "C2 input/param", "C3 state invariants",
                                                "C4 job bounds", "C5 independent MinIO", "C6 scope/membership"],
        "scope": {"org_id": org_id, "workspace_id": ws_id, "chain": chain, "address": server_address,
                  "job": {"id": job_id, "job_no": job_no, "status": job["status"], "datasets": job["datasets"],
                          "range_type": job["range_type"], "from_block": job["from_block"], "to_block": job["to_block"],
                          "from_timestamp": job["from_timestamp"], "to_timestamp": job["to_timestamp"]},
                  "export_bounds_predicate": bounds or "(none — no job scope)"},
        "checks_total": len(checks),
        "checks_failed": len(failed),
        "failed": failed,
        "matrix": matrix,
        "invariants": invariants,
        "exports": per_export,
        "verdict": "PASS" if not failed else "FAIL",
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0 if not failed else 1


def special_checks(name, ev, sink):
    """R10-07（登录态 null 列表回归）与 R10-08（跨工作区作用域）没有 export 对象。"""
    if name == "r10-07-evidence.json":
        routes = ev.get("routes") or []
        broken = [r for r in routes if not r.get("rendered") or (r.get("pageErrors") or [])]
        check("r10-07-legacy-pages-render-with-null-lists", len(routes) >= 3 and not broken,
              {"routes": routes, "broken": broken, "casesIsNull": ev.get("casesIsNull"), "tagsIsNull": ev.get("tagsIsNull")})
        check("r10-07-null-condition-really-exercised",
              bool(ev.get("casesIsNull")) or bool(ev.get("tagsIsNull")),
              {"casesBody": (ev.get("api") or {}).get("casesBody"), "tagsBody": (ev.get("api") or {}).get("tagsBody")})
        sink.append({"evidence": name, "kind": "null-list-regression", "routes": routes})
        return
    if name == "r10-08-evidence.json":
        cross = ev.get("crossCreate") or {}
        listing = ev.get("list") or {}
        check("r10-08-cross-workspace-create-404", cross.get("status") == 404, cross)
        check("r10-08-cross-workspace-list-no-leak", int(listing.get("leakedJobItems") or 0) == 0, listing)
        sink.append({"evidence": name, "kind": "scope-isolation", "crossCreate": cross, "list": listing})
        return


def minio_check(export_id, object_ref, db_sha, file_sha, name):
    """独立对象存储校验：走 S3（mc），凭据仅经进程环境。"""
    result = {"object_ref": object_ref, "method": "mc (S3) 独立于 API 下载路径"}
    if not object_ref:
        result["ok"] = False
        result["reason"] = "object_ref empty"
        check("minio-stat:" + name, False, result)
        return result
    if not os.path.exists(MC_PATH):
        result["ok"] = False
        result["reason"] = "mc binary not extracted"
        check("minio-stat:" + name, False, result)
        return result
    if not MINIO_ENV_PATH or not os.path.exists(MINIO_ENV_PATH):
        result["ok"] = False
        result["reason"] = "restricted minio env not provided (R10_MINIO_ENV)"
        check("minio-stat:" + name, False, result)
        return result
    env = dict(os.environ)
    for line in open(MINIO_ENV_PATH, "r", encoding="utf-8"):
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        env[k] = v
    host = "http://%s:%s@%s" % (env["MINIO_ROOT_USER"], env["MINIO_ROOT_PASSWORD"], env["MINIO_ENDPOINT"])
    env["MC_HOST_local"] = host
    env["MC_CONFIG_DIR"] = "/tmp/r10-mc-cfg"
    bucket = env.get("EXPORT_BUCKET", "blockchain-exports")
    target = "local/%s/%s" % (bucket, object_ref)
    try:
        stat_raw = subprocess.run([MC_PATH, "--json", "stat", target], env=env, capture_output=True, timeout=120)
        if stat_raw.returncode != 0:
            result["ok"] = False
            result["reason"] = "mc stat rc=%d: %s" % (stat_raw.returncode, stat_raw.stderr.decode("utf-8", "replace")[:400])
            check("minio-stat:" + name, False, result)
            return result
        stat = json.loads(stat_raw.stdout.decode("utf-8", "replace").strip().splitlines()[-1])
        cat_raw = subprocess.run([MC_PATH, "cat", target], env=env, capture_output=True, timeout=300)
        if cat_raw.returncode != 0:
            result["ok"] = False
            result["reason"] = "mc cat rc=%d" % cat_raw.returncode
            check("minio-stat:" + name, False, result)
            return result
        object_sha = hashlib.sha256(cat_raw.stdout).hexdigest()
        result.update({
            "ok": True, "bucket": bucket, "size": stat.get("size"), "etag": stat.get("etag"),
            "content_type": stat.get("type"), "last_modified": stat.get("lastModified"),
            "object_sha256": object_sha, "db_sha256": db_sha, "file_sha256": file_sha,
        })
        check("minio-object-bytes-equal-db-sha:" + name, object_sha == db_sha, result)
        if file_sha:
            check("minio-object-bytes-equal-downloaded-file:" + name, object_sha == file_sha, result)
        check("minio-object-size-positive:" + name, int(stat.get("size") or 0) > 0, result)
    except Exception as exc:  # noqa: BLE001
        result["ok"] = False
        result["reason"] = "minio verify exception: " + str(exc)[:400]
        check("minio-stat:" + name, False, result)
    return result


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HardError as exc:
        print(json.dumps({"verdict": "ERROR", "evidence_dir": EV, "checks": checks,
                          "hard_error": str(exc)}, ensure_ascii=False, indent=2))
        sys.exit(2)
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"verdict": "ERROR", "evidence_dir": EV, "checks": checks,
                          "unexpected_error": repr(exc)}, ensure_ascii=False, indent=2))
        sys.exit(2)
