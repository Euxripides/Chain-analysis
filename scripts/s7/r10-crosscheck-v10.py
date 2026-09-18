#!/usr/bin/env python3
"""r10-crosscheck-v6.py — R10 严格机器核验（v5 的矩阵判据按根 11:46 要求强化；v3/v4/v5 原样保留）。

根 11:46 对 v5 的裁定：**不允许只把 "passed" 宽松映射成 "expected"** 就算通过。v5 的矩阵判据只比了
一个字符串，属于"宽松映射"；v6 把它换成对 Playwright JSON reporter **实际存在的字段**的逐项联合判据：
  对每个"应当执行"的用例，必须同时满足
    (1) item.status == "expected"（该 reporter 对通过用例的写法；"passed" 是 list reporter 的用词，item 上并不存在）
    (2) item.expectedStatus == "passed"
    (3) results 恰好 1 条且 results[0].status == "passed"
    (4) results[0].errors == []（无错误对象）
    (5) results[0].duration 为真实正数（> 0，不是 0/None）
    (6) results[0].startTime 存在且可解析
    (7) results[0].retry == 0（本轮 retries=0，出现重试即视为不符合冻结语义）
    (8) 该 (project, test) 的**证据 JSON 文件**存在、非空、可解析且为非空对象
  对"按设计排除"的用例：只允许 **明确 7 项**（uc-mobile 的 R10-02..R10-08），且 item.status 必须 == "skipped"。
  另加 report 级 stats 断言：expected==9、unexpected==0、skipped==7、flaky==0。
  说明（不隐藏）：本 reporter 的 test item 上**没有** outcome 字段（v5 时代若照抄"outcome"会再次误判）；
  trace/screenshot 是 retain-on-failure/only-on-failure，因此通过用例的 attachments 为空是设计使然——
  v6 用"每个用例自己的证据 JSON 必须存在且非空"来替代 attachment 断言，并把 attachments/annotations 原样记录备查。

v5（r10-crosscheck-v5.py）已实际运行并原样保留（r10-export-excelize-v5-20260913T034608Z，PASS/143/0）。

v4（r10-crosscheck-v4.py）已实际运行两轮并原样保留，不 hot-edit。v4 第 2 轮（r10-export-excelize-v4-20260913T034232Z）
的真实结果：playwright_rc=0、uc-desktop 8/8 expected、uc-mobile R10-01 expected，crosscheck 143 项中只失败 1 项：
  D7 **矩阵状态值写错**：Playwright JSON reporter 对"通过"的用例写 status="expected"，
     v1..v4 一直拿 "passed" 去比（"passed" 是 list reporter 的用词），因此即便全部用例都通过，
     playwright-matrix 也永远 FAIL。v3 轮里这一项被 R10-07/08 的真实失败掩盖，v4 轮才暴露出来。
     v5 更正：run -> want="expected"；excluded-by-project-design -> want="skipped"（不变）。
     同时把用例的实际 status 集合一并记录，便于人工复核矩阵语义。

v3（r10-crosscheck-v3.py）已实际运行并原样保留，不 hot-edit。v3 实测 20 项 FAIL，
其中 18 项是本脚本自身缺陷、2 项是 spec 缺陷（spec 侧已在 r10v3-*.spec.ts 修正）：

  D1 audit 断言查错了表：writeAuditNow 写的是 **audit_events_v2**（列 occurred_at），
     v3 查 audit_events（列 created_at）→ audit-export-download/ticket ×6 假失败。
     v4：主断言查 audit_events_v2；旧表只作为信息记录（observe，不计通过）。
  D2 NON_EXPORT_EVIDENCE 只列了 r10-07/08，r10-04/05/06 被当成"缺 export.id"而假失败。
     v4：改为**按内容分派**——有 export.id 走导出链核验，否则按 r10-04/05/06/07/08
     各自的真实语义逐项核验（每个都有独立断言，不是放行）。
  D3 exports 精确增量把"复用已有导出"算成新建：before 246 → after 248，new_ids 3（真实新建 2）。
     v4：只统计 **pre 快照中不存在的 id**（genuinely_new = new_ids - pre_ids）。
  D4 MinIO：v3 里 mc 失败时 stderr 为空（mc 把错误 JSON 写到 **stdout**），且未建 MC_CONFIG_DIR。
     v4：建 MC_CONFIG_DIR、stdout+stderr 都留证、按声明的 bucket 顺序解析真实所在 bucket，
     并把"产物实际落在 RAW_BUCKET 而非 EXPORT_BUCKET"作为 observation 记录（不是假通过）。
  D5 R10-07 探针/契约变更：真实接口已修 nil slice -> []，v3 的
     "casesIsNull OR tagsIsNull" 在修复后必然为假。v4 改为三块：
     真实接口 cases/tags 必须是数组（契约闭合）、真实响应下三页面渲染无 pageerror、
     injected null 下三页面渲染无 pageerror（客户端 fail-closed 防线，标注 injected）。
  D6 R10-08 身份/断言：改用**有 export:create 的跨工作区身份**，断言
     跨区创建 404、跨区明细 404、跨区列表 itemCount=0 且无 wsA 导出 id；
     并以 wsA 自己的非空列表作正向对照。

v4 新增（不是修 bug，是按根要求把"单元格级真值"也核到）：
  C7 单元格真值：R10-01 每张表 / R10-02 裸 xlsx / R10-04 客户端 xlsx 的**首行单元格**
     （tx_hash / 交易哈希）必须在 ClickHouse 中存在该行（真实单元格 ↔ 真实行）。
  C8 冻结绑定：读 freeze.json，断言本轮运行期间 api pid/sha 未变、且等于 scope.json 里的期望 sha
     （mid-run deploy 必须被判为不成立，而不是"看不见"）。

退出码：0=全部通过；1=有对照失败；2=核验自身错误（缺文件/DB 错误/解析失败）。
"""

import datetime
import hashlib
import json
import os
import re
import subprocess
import sys

EV = sys.argv[1]
SCOPE_PATH = os.path.join(EV, "scope.json")
FREEZE_PATH = os.path.join(EV, "freeze.json")
MINIO_ENV_PATH = os.environ.get("R10_MINIO_ENV", "")
MC_PATH = "/tmp/r10-mc/mc"
TEST_KEYS = ["R10-01", "R10-02", "R10-03", "R10-04", "R10-05", "R10-06", "R10-07", "R10-08", "R10-09"]
DESKTOP_TESTS = list(TEST_KEYS)
# v8：R10-09（全新导出完成认证）在桌面与移动**都**执行；因此"按设计排除"仍是 uc-mobile 的 7 项。
MOBILE_TESTS = ["R10-01", "R10-09"]

RE_CHAIN = re.compile(r"^[a-z0-9_-]{2,20}$")
RE_ADDR_EVM = re.compile(r"^0x[0-9a-f]{40}$")
RE_ADDR_TRON = re.compile(r"^T[1-9A-HJ-NP-Za-km-z]{33}$")
RE_JOB_NO = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
RE_DATASET = re.compile(r"^(transactions|token_transfers|nft_transfers|logs|balances|internal_transactions)$")
RE_TXHASH = re.compile(r"^0x[0-9a-fA-F]{64}$")

# ---- v7 新增常量：不可抵赖审计链（audit_events_v2, chain='audit'）的可信边界与真实路由白名单 ----
# seq 1-2343 永久缺失（0055 / ADR-018）；可信段起点 2344，其 prev_hash 即被销毁的最后一条事件的
# event_hash（sealed tail）。本套件只对 2344+ 做完整性判定，**绝不声称完整历史**。
TRUSTED_FROM_SEQ = 2344
SEALED_TAIL_HASH = "3db79aeb77ce12ae8ccab3540891a7ff241bfc34af8f4a9dcdfd283578686ad1"
# 导出创建的真实路由（完成事件由该请求派生的后台 goroutine 写入，因此 route 必须是其中之一）。
REAL_EXPORT_ROUTES = ("/api/v2/exports", "/api/v1/exports", "/api/v1/actions/export", "/api/v1/download-exports")
# v9 新增：导出**新建**路径的状态码（实测值，不读码推断）。
#   实测 201：v8 轮 uc-desktop/r10-02；v9 轮 uc-desktop/r10-01、r10-02；v9 轮 r10-09 桌面+移动。
#   实测 200：复用路径（命中 query_snapshot 完全一致的历史导出）。
# v8 曾把这里写成 202（照 export_v2.go 的 writeJSON(w, 202, exp) 读出来的），于是"同一次发生"判据
# 被静默跳过 —— 这是**我自己的测试侧缺陷**，由 v9 轮的实际证据暴露，在 v9 修正并改为 fail-closed。
# v10（根 15:16 裁定，严格）：**新建唯一状态 = 201**；200 只允许表示"复用了既有导出"。
# 任何 201 都必须通过"同一次发生"强断言；201 若落入 not-applicable 一律 FAIL（v8 因写成 202 而静默跳过）。
NEW_CREATE_STATUS = 201
REUSE_STATUS = 200

# 伪造路由：早期 writeAuditCtx 用 r0() 合成请求（POST /api/auth/v1/refresh）留下的假值。
FABRICATED_ROUTE = "/api/auth/v1/refresh"

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
        return {line.strip() for line in handle if line.strip()}

checks = []
observations = []


def check(name, ok, detail):
    checks.append({"check": name, "ok": bool(ok), "detail": detail})
    return bool(ok)


def observe(name, detail):
    """信息性记录：不参与 PASS/FAIL，但完整留在 JSON 里（不掩盖、不假装通过）。"""
    observations.append({"observation": name, "detail": detail})


class HardError(Exception):
    pass


def run(cmd, stdin=None, timeout=600, env=None):
    out = subprocess.run(cmd, input=stdin, capture_output=True, text=False, timeout=timeout, env=env)
    if out.returncode != 0:
        raise HardError("command failed (%s) rc=%d: %s | stdout=%s" % (
            " ".join(cmd[:6]), out.returncode,
            out.stderr.decode("utf-8", "replace").strip()[:600],
            out.stdout.decode("utf-8", "replace").strip()[:400]))
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


def load(path):
    with open(path, "r", encoding="utf-8") as handle:
        return json.load(handle)


PK_COLUMN = {"download_jobs": "id", "download_shards": "id", "checkpoints": "shard_id", "outbox_events": "id", "exports": "id"}


def pk_subset_ok(table, ids_path, pk="id"):
    with open(ids_path, "r", encoding="utf-8") as handle:
        ids = [line.strip() for line in handle if line.strip()]
    if not ids:
        raise HardError("empty pk snapshot " + ids_path)
    script = ("CREATE TEMP TABLE pre_ids(id text);\n"
              "COPY pre_ids FROM STDIN;\n" + "\n".join(ids) + "\n\\.\n"
              "SELECT 'RESULT:' || count(*) FROM pre_ids p "
              "WHERE NOT EXISTS (SELECT 1 FROM " + table + " t WHERE t." + pk + "::text = p.id);\n")
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


def cell_truth(chain, dataset, tx_hash, address, bounds):
    """C7：把导出文件里的**单元格值**拉回 ClickHouse 找同一行。返回命中的真实行数。"""
    if dataset not in ("transactions", "token_transfers", "nft_transfers", "logs"):
        return None
    if not RE_TXHASH.match(tx_hash or ""):
        return None
    sql = ("SELECT count() FROM blockchain." + dataset + " FINAL WHERE chain = '" + chain +
           "' AND tx_hash = '" + tx_hash + "' AND (from_address = '" + address +
           "' OR to_address = '" + address + "')" + bounds + ";\n")
    return int(ch(sql))


def audit_rows(export_id):
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
                " FROM audit_events_v2 WHERE chain='audit' AND resource_id = '%s' AND action = '%s' ORDER BY seq::bigint;\n"
                % (export_id, action))
    out = []
    for r in rows:
        r = (r + [""] * 15)[:15]
        try:
            seq = int(r[0])
        except (TypeError, ValueError):
            continue
        # X1：seq 是 bigint，但显式 ::bigint 排序，杜绝任何输出列歧义。
        prev = psql("SELECT coalesce(event_hash,'') FROM audit_events_v2 WHERE chain='audit' AND seq < %d"
                    " ORDER BY seq::bigint DESC LIMIT 1;\n" % seq)
        expected_prev = prev[0][0] if prev and prev[0] else ""
        out.append({
            "seq": seq, "actor_type": r[1], "actor_id": r[2], "actor_email": r[3],
            "organization_id": r[4], "workspace_id": r[5], "session_id": r[6],
            "http_method": r[7], "route": r[8], "request_id": r[9], "trace_id": r[10],
            "result": r[11], "prev_hash": r[12], "event_hash": r[13], "occurred_at": r[14],
            "expected_prev_hash": expected_prev,
            "chain_link_ok": bool(r[13]) and bool(r[12]) and r[12] == expected_prev,
        })
    return out


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
    viewer_ws = scope.get("viewer_ws_id") or ""
    readonly_ws = scope.get("readonly_ws_id") or ""

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

    # ---- C8：冻结绑定（api pid/sha 在整轮内不得变化，且必须等于期望 sha） ----
    freeze = {}
    if os.path.exists(FREEZE_PATH):
        freeze = load(FREEZE_PATH)
        check("freeze-api-sha-before-matches-expected",
              freeze.get("api_sha_before") == scope.get("api_sha"),
              {"before": freeze.get("api_sha_before"), "expected": scope.get("api_sha")})
        check("freeze-api-sha-unchanged-during-run",
              freeze.get("api_sha_after") == freeze.get("api_sha_before"),
              {"before": freeze.get("api_sha_before"), "after": freeze.get("api_sha_after")})
        check("freeze-api-pid-unchanged-during-run",
              freeze.get("api_pid_before") == freeze.get("api_pid_after") and bool(freeze.get("api_pid_after")),
              {"before": freeze.get("api_pid_before"), "after": freeze.get("api_pid_after")})
        check("freeze-worker-sha-unchanged-during-run",
              freeze.get("worker_sha_after") == freeze.get("worker_sha_before"),
              {"before": freeze.get("worker_sha_before"), "after": freeze.get("worker_sha_after")})
        check("freeze-api-exe-is-backend-bin-api",
              str(freeze.get("api_exe_after", "")).endswith("/backend/bin/api"),
              freeze.get("api_exe_after"))
    else:
        check("freeze-file-present", False, "freeze.json missing (runner must record api pid/sha before+after)")

    # ---- C1：真实校验机器 report ----
    report_path = os.path.join(EV, "playwright-report.json")
    if not os.path.exists(report_path) or os.path.getsize(report_path) == 0:
        raise HardError("playwright-report.json missing/empty — CLI reporter override 未修正（v1 的真实缺陷）")
    report = load(report_path)
    check("playwright-report-has-suites", bool(report.get("suites")), len(report.get("suites") or []))

    stats = report.get("stats") or {}
    # v8：用例数 9 -> 11（新增 R10-09 在桌面与移动各一项）。按设计排除仍是 7 项。
    check("playwright-stats", stats.get("expected") == 11 and stats.get("unexpected") == 0
          and stats.get("skipped") == 7 and stats.get("flaky") == 0, stats)

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
                results = item.get("results") or []
                if not proj:
                    continue
                actual[(proj, key)] = {
                    "title": title,
                    "spec_file": spec.get("file"),
                    "spec_line": spec.get("line"),
                    "status": item.get("status"),
                    "expectedStatus": item.get("expectedStatus"),
                    "outcome_field_present": "outcome" in item,
                    "result_statuses": [r.get("status") for r in results],
                    "result_durations": [r.get("duration") for r in results],
                    "result_error_counts": [len(r.get("errors") or []) for r in results],
                    "result_error_messages": [(r.get("errors") or [{}])[0].get("message", "")[:200]
                                              for r in results if (r.get("errors") or [])],
                    "result_retries": [r.get("retry") for r in results],
                    "result_start_times": [r.get("startTime") for r in results],
                    "attachments": [a.get("name") for r in results for a in (r.get("attachments") or [])],
                    "annotations": item.get("annotations") or [],
                }
        for child in suite.get("suites", []) or []:
            walk(child, project)

    for suite in report.get("suites", []) or []:
        walk(suite, None)

    matrix = []
    for proj, expected_keys in (("uc-desktop", DESKTOP_TESTS), ("uc-mobile", MOBILE_TESTS)):
        for key in TEST_KEYS:
            should_run = key in expected_keys
            info = actual.get((proj, key))
            row = {"project": proj, "test": key,
                   "expected": "run" if should_run else "excluded-by-project-design",
                   "observed": info, "failures": []}
            if info is None:
                row["failures"].append("playwright-report.json 中不存在该 (project,test) 的用例项")
            elif should_run:
                # 联合判据：只用该 reporter 真实存在的字段（v5 的宽松字符串映射已被根 11:46 驳回）
                if info["status"] != "expected":
                    row["failures"].append("item.status=%r != 'expected'" % info["status"])
                if info["expectedStatus"] != "passed":
                    row["failures"].append("item.expectedStatus=%r != 'passed'" % info["expectedStatus"])
                if info["result_statuses"] != ["passed"]:
                    row["failures"].append("results.status=%r != ['passed']" % (info["result_statuses"],))
                if info["result_error_counts"] != [0]:
                    row["failures"].append("results.errors 非空: %r" % (info["result_error_messages"],))
                durations = info["result_durations"]
                if len(durations) != 1 or not isinstance(durations[0], int) or durations[0] <= 0:
                    row["failures"].append("results.duration=%r 不是真实正数（用例未真正执行）" % (durations,))
                if info["result_retries"] != [0]:
                    row["failures"].append("results.retry=%r != [0]（本轮 retries=0）" % (info["result_retries"],))
                starts = info["result_start_times"]
                if len(starts) != 1 or not starts[0]:
                    row["failures"].append("results.startTime 缺失: %r" % (starts,))
                else:
                    try:
                        datetime.datetime.fromisoformat(str(starts[0]).replace("Z", "+00:00"))
                    except Exception:  # noqa: BLE001
                        row["failures"].append("results.startTime 不可解析: %r" % (starts[0],))
                ev_path = os.path.join(EV, proj, key.lower() + "-evidence.json")
                row["evidence_file"] = os.path.relpath(ev_path, EV)
                if not os.path.exists(ev_path) or os.path.getsize(ev_path) == 0:
                    row["failures"].append("证据 JSON 缺失或为空: %s" % row["evidence_file"])
                    row["evidence_bytes"] = 0
                else:
                    row["evidence_bytes"] = os.path.getsize(ev_path)
                    try:
                        payload = load(ev_path)
                    except Exception as exc:  # noqa: BLE001
                        payload = None
                        row["failures"].append("证据 JSON 不可解析: %r" % (exc,))
                    if not isinstance(payload, dict) or not payload:
                        row["failures"].append("证据 JSON 不是非空对象")
            else:
                # 只允许"按设计排除"的明确 7 项（uc-mobile R10-02..R10-08）
                row["skip_allowed"] = (proj == "uc-mobile" and key not in ("R10-01", "R10-09"))
                if not row["skip_allowed"]:
                    row["failures"].append("不允许的排除项: %s/%s" % (proj, key))
                if info["status"] != "skipped":
                    row["failures"].append("item.status=%r != 'skipped'" % info["status"])
            row["ok"] = not row["failures"]
            matrix.append(row)
    mismatch = [{"project": r["project"], "test": r["test"], "failures": r["failures"]} for r in matrix if not r["ok"]]
    ran = [r for r in matrix if r["expected"] == "run"]
    skipped_rows = [r for r in matrix if r["expected"] != "run"]
    check("playwright-matrix", not mismatch and len(ran) == 11 and len(skipped_rows) == 7,
          {"ran_expected": len(ran), "skipped_by_design": len(skipped_rows), "mismatch": mismatch})

    # ---- 逐份证据：DB / CH / 审计 ----
    evidence = {}
    for root, _dirs, files in os.walk(EV):
        for name in files:
            if name.startswith("r10-") and name.endswith("-evidence.json"):
                path = os.path.join(root, name)
                evidence[path] = load(path)
    check("evidence-file-count", len(evidence) >= 9, sorted(os.path.basename(p) for p in evidence))

    per_export = []
    new_export_ids = set()
    specials = []
    for path, ev in sorted(evidence.items()):
        name = os.path.basename(path)
        rel = os.path.relpath(path, EV)
        project = os.path.basename(os.path.dirname(path))
        export = ev.get("export") or {}
        export_id = export.get("id")
        if not export_id:
            # D2：不是"缺 export.id"的错误，而是这类证据本身没有导出对象；按真实语义逐项核验。
            handlers = {
                "r10-04": special_legacy_client_export,
                "r10-05": special_legacy_import,
                "r10-06": special_reuse_contract,
                "r10-07": special_list_contract,
                "r10-08": special_workspace_scope,
            }
            prefix = name.split("-evidence")[0]
            handler = handlers.get(prefix)
            if handler is None:
                check("evidence-kind-recognized:" + rel, False, "unknown evidence kind without export.id")
                continue
            check("evidence-kind-recognized:" + rel, True, prefix)
            handler(name, rel, ev, specials, {
                "chain": chain, "address": server_address,
                "scope": {
                    # 规范化的作用域：同时提供 runner 原始命名与处理器使用的命名（v4 真实缺陷：KeyError workspace_id）
                    "job_id": job_id, "org_id": org_id, "chain": chain, "address": server_address,
                    "ws_id": ws_id, "workspace_id": ws_id,
                    "viewer_ws_id": viewer_ws, "viewer_workspace_id": viewer_ws,
                    "readonly_ws_id": readonly_ws, "readonly_workspace_id": readonly_ws,
                },
            })
            continue
        new_export_ids.add(export_id)
        rows = psql("SELECT id::text, status, format, zip, rows, sha256, object_ref, coalesce(job_id::text,''),"
                    " coalesce(organization_id::text,''), coalesce(workspace_id::text,''), dataset,"
                    " coalesce(created_by::text,''), created_at::text"
                    " FROM exports WHERE id = '%s';\n" % export_id)
        if not rows:
            check("db-export-row:" + name, False, "exports row missing for " + export_id)
            continue
        db = {"id": rows[0][0], "status": rows[0][1], "format": rows[0][2], "zip": rows[0][3] == "t",
              "rows": int(rows[0][4]), "sha256": rows[0][5], "object_ref": rows[0][6], "job_id": rows[0][7],
              "organization_id": rows[0][8], "workspace_id": rows[0][9], "dataset": rows[0][10],
              "created_by": rows[0][11], "created_at": rows[0][12]}
        entry = {"evidence": rel, "project": project, "export_id": export_id, "db": db}

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
                cell = sheet.get("firstRow") or {}
                tx_hash = str(cell.get("tx_hash") or "")
                hits = cell_truth(chain, dataset, tx_hash, server_address, bounds)
                per_entry.append({"entry": sheet["entry"], "entry_address": addr, "dataset": dataset,
                                  "sheetjs_rows": sheet["dataRowCount"], "clickhouse_uniq_with_job_bounds": expected,
                                  "bounds": bounds or "(none)", "cell_probe": {"column": "tx_hash", "value": tx_hash, "clickhouse_rows": hits}})
                check("sheet-entry-address-matches:" + name + ":" + sheet["entry"], addr == server_address, [addr, server_address])
                check("sheet-rows-equal-clickhouse:" + name + ":" + sheet["entry"],
                      int(sheet["dataRowCount"]) == expected, [sheet["dataRowCount"], expected])
                check("sheet-cell-truth-in-clickhouse:" + name + ":" + sheet["entry"],
                      hits is not None and hits > 0, per_entry[-1]["cell_probe"])
            entry["per_entry"] = per_entry
        if "workbook" in ev:
            wb = ev["workbook"]
            entry["workbook_rows"] = wb.get("dataRowCount")
            expected = address_expected_rows(chain, server_address, scope["dataset"], no_job_bounds)
            entry["clickhouse_uniq_no_bounds"] = expected
            check("rows-workbook-equals-db:" + name, int(wb.get("dataRowCount", -1)) == db["rows"], [wb.get("dataRowCount"), db["rows"]])
            check("workbook-rows-equal-clickhouse:" + name, int(wb.get("dataRowCount", -1)) == expected, [wb.get("dataRowCount"), expected])
            tx_hash = str((wb.get("firstRow") or {}).get("tx_hash") or "")
            hits = cell_truth(chain, scope["dataset"], tx_hash, server_address, no_job_bounds)
            entry["cell_probe"] = {"column": "tx_hash", "value": tx_hash, "clickhouse_rows": hits}
            check("workbook-cell-truth-in-clickhouse:" + name, hits is not None and hits > 0, entry["cell_probe"])

        # D1：审计真值在 audit_events_v2（列 occurred_at）
        audit = audit_rows(export_id)
        entry["audit_v2"] = [{"action": a, "count": int(c)} for a, c in audit]
        actions = {a for a, _c in audit}
        legacy = psql("SELECT action, count(*) FROM audit_events WHERE resource_id = '%s' GROUP BY 1 ORDER BY 1;\n" % export_id)
        entry["audit_v1_legacy"] = [{"action": a, "count": int(c)} for a, c in legacy]
        observe("audit-tables:" + name, {"audit_events_v2": entry["audit_v2"], "audit_events": entry["audit_v1_legacy"]})
        check("audit-v2-export-create:" + name, "export.create" in actions, sorted(actions))
        if name.startswith(("r10-01", "r10-02", "r10-03")):
            check("audit-v2-export-download:" + name, "export.download" in actions, sorted(actions))
            check("audit-v2-export-ticket:" + name, "export.download_ticket" in actions, sorted(actions))

        # ---- v7/v8：export.completed 必须是 v2 链上的**真实行**，且 request 维度不可伪造 ----
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
            # X2 同一次发生证明：只有当本轮**真的新建**（202）且 evidence 记录了创建请求时才判定。
            # 复用（200）时完成事件属于当初那次创建，不能拿本轮的请求 id 去比 —— 如实记 observation。
            create_http = ev.get("createHttp") or {}
            create_request = create_http.get("requestId")
            create_trace = create_http.get("traceId")
            create_status = create_http.get("status")
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
                                   "也不据此记 PASS/FAIL（硬判据已按 v8 的 X2 对历史导出行本身逐项核过）"})
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
                       "audit_v2_actions": sorted(actions), "scope": entry["completion_scope"]})

        if name.startswith("r10-03"):
            reuse = ev.get("reuse") or {}
            check("ticket-single-use-410:" + name, reuse.get("status") == 410, reuse.get("status"))

        entry["completion_proved"] = bool(completed) and genuinely_new
        entry["minio_independent"] = minio_check(export_id, db["object_ref"], db["sha256"], file_sha, name)
        per_export.append(entry)

    # ---- C3：状态不变量（原 PK 不丢 + exports 精确增量，只认真正新建的 id） ----
    invariants = {}
    for table in ("download_jobs", "download_shards", "checkpoints", "outbox_events", "exports"):
        ids_path = os.path.join(EV, "pre-ids-%s.txt" % table)
        if not os.path.exists(ids_path):
            raise HardError("missing pre-state pk snapshot for " + table)
        missing, total = pk_subset_ok(table, ids_path, PK_COLUMN.get(table, "id"))
        after = int(one("SELECT count(*) FROM " + table + ";\n") or 0)
        before = total
        invariants[table] = {"before": before, "after": after, "original_pk_missing": missing}
        check("no-original-pk-lost:" + table, missing == 0, invariants[table])
        if table != "exports":
            check("count-not-decreased:" + table, after >= before, invariants[table])
    exports_after = invariants["exports"]["after"]
    exports_before = invariants["exports"]["before"]
    with open(os.path.join(EV, "pre-ids-exports.txt"), "r", encoding="utf-8") as handle:
        pre_export_ids = {line.strip() for line in handle if line.strip()}
    genuinely_new = sorted(new_export_ids - pre_export_ids)
    reused_existing = sorted(new_export_ids & pre_export_ids)
    check("exports-delta-equals-new-ids", exports_after == exports_before + len(genuinely_new),
          {"before": exports_before, "after": exports_after, "genuinely_new": len(genuinely_new),
           "new_ids_in_evidence": sorted(new_export_ids), "reused_existing_ids": reused_existing})
    observe("exports-delta-detail", {"genuinely_new": genuinely_new, "reused_existing": reused_existing})
    new_rows = psql("SELECT count(*) FROM exports WHERE id::text IN (%s);\n" %
                    ",".join("'" + i + "'" for i in sorted(new_export_ids))) if new_export_ids else [["0"]]
    check("all-new-export-ids-exist", int(new_rows[0][0]) == len(new_export_ids),
          {"found": new_rows[0][0], "expected": len(new_export_ids)})

    # ---- X3（v8 认证核心）：至少一个**本轮真实新建**的导出在 v2 链上有完整 export.completed ----
    fresh_proved = sorted({e["export_id"] for e in per_export if e.get("completion_proved")})
    check("fresh-export-v2-completion-certified", len(fresh_proved) >= 1,
          {"fresh_export_ids_with_v2_completion": fresh_proved,
           "note": "导出完成事件必须能被**本轮新建的**导出观测到（v8 轮 2 项假失败的结构性根因即在于此）"})
    observe("fresh-export-ids-this-round", {"genuinely_new": sorted(genuinely_new), "reused_existing": reused_existing,
                                            "with_v2_completion_proof": fresh_proved})

    data_control = {
        "investigator_export_list_count": int(one("SELECT count(*) FROM exports WHERE organization_id='%s' AND workspace_id='%s';\n" % (org_id, ws_id)) or 0),
        "wsB_export_rows": int(one("SELECT count(*) FROM exports WHERE workspace_id='%s';\n" % viewer_ws) or 0) if viewer_ws else None,
        "readonly_ws_export_rows": int(one("SELECT count(*) FROM exports WHERE workspace_id='%s';\n" % readonly_ws) or 0) if readonly_ws else None,
    }
    observe("scope-data-control", data_control)

    # ---- C9（v7 新增）：v2 不可抵赖审计链的可信锚点与全链连续性（ADR-018 / INC-AUDIT-GAP-1-2343） ----
    # X1（v8 真实缺陷修复）：v7 写的是 "SELECT seq::text ... ORDER BY seq"。PostgreSQL 对裸列名的
    # ORDER BY 优先解析为**输出列**（此处是 text），于是按字典序取首尾 -> first=10000 / last=9999，
    # 3 项 audit-chain-* 判据因此假失败。改为显式 ::bigint 数值排序。
    first = psql("SELECT seq::text, coalesce(prev_hash,''), coalesce(event_hash,'') FROM audit_events_v2"
                 " WHERE chain='audit' ORDER BY seq::bigint LIMIT 1;\n")
    last = psql("SELECT seq::text, coalesce(event_hash,'') FROM audit_events_v2"
                " WHERE chain='audit' ORDER BY seq::bigint DESC LIMIT 1;\n")
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

    failed = [c for c in checks if not c["ok"]]
    summary = {
        "suite": "R10 server-side excelize XLSX export end-to-end (crosscheck v10, strict same-occurrence)",
        "evidence_dir": EV,
        "v3_defects_corrected": ["D1 audit→audit_events_v2", "D2 non-export evidence dispatch", "D3 exports delta by pre-snapshot",
                                 "D4 minio mc stdout/config-dir/bucket resolution", "D5 R10-07 probes+contract+injected",
                                 "D6 R10-08 export:create identity + list=0"],
        "v4_additions": ["C7 cell-level ClickHouse truth", "C8 freeze binding (mid-run deploy detection)"],
        "v5_defects_corrected": ["D7 playwright-matrix status vocabulary (expected vs passed)"],
        "v6_matrix_hardening": ["strict per-test joint criteria (status/expectedStatus/result status/errors/duration/startTime/retry/evidence artifact)",
                                "exactly 7 explicit skips allowed (uc-mobile R10-02..R10-08)",
                                "report stats expected=9 unexpected=0 skipped=7 flaky=0",
                                "full per-test observations recorded for audit"],
        "v10_fixes": [
            "Z1（根 15:16 裁定）新建唯一状态严格 201；200 只允许出现在复用既有导出上",
            "Z2 任何 201 都必须通过同一次发生强断言：completed.request_id/trace_id == UI createHttp 与 export.create 行的同一对；201 缺少 ids 直接 FAIL，绝不落入 not-applicable",
            "Z3 新增证据缺口硬判据：genuinely_new 导出若未记录 UI createHttp ids，直接 FAIL（v9 冻结证据里 r10-03 即命中该缺口）",
            "Z4 修正 not-applicable 的说明文本（旧文本错误地写成『复用(200)/未新建』，与本轮 201 新建的事实矛盾）",
        ],
        "v9_fixes": [
            "Y1 真实缺陷（测试侧，我自己的代码）：v8 把同一次发生判据的状态门限写成 202（读码而非实测），实测新建=201/复用=200 -> 该判据被静默跳过；v9 改为 NEW_CREATE_STATUSES=(201,202) 并在'本轮新建却报告非新建状态码'时硬失败",
            "Y2 v9 用更严的核验器对**同一份** v9 轮证据重跑，使同一次发生判据真正被强制（evidence 未改动）",
        ],
        "v8_fixes": [
            "X1 真实缺陷（测试侧）：SELECT seq::text ... ORDER BY seq 被解析为按输出列(text)排序 -> first=10000/last=9999；改 ORDER BY seq::bigint",
            "X2 export.completed 作用域感知：本轮新建=硬性要求；复用且已有 v2 行=同样硬性；复用且无 v2 行=仅当创建于 05:55:08Z 审计修复部署前才降级 observation（绝不记 PASS）",
            "X2b 同一次发生证明：v2 export.completed 的 request_id/trace_id 必须等于 UI 创建请求（202）与 export.create 行的同一对 id",
            "X3 认证核心 fresh-export-v2-completion-certified：至少一个本轮真实新建的导出有完整 v2 完成事件",
            "矩阵扩至 11 项（R10-09 桌面+移动），按设计排除仍为 7 项",
        ],
        "v7_audit_request_hardening": [
            "export.completed 必须是 audit_events_v2 的**真实行**（不是 legacy-only，也不是事后补写）",
            "scope：审计行 organization_id/workspace_id 必须与 exports 行一致",
            "actor：actor_id 必须等于 exports.created_by，actor_email 非空",
            "request：http_method/route_template/request_id 非空，且 route 必须是真实发起路由（拒绝伪造）",
            "trace：trace_id 非空",
            "chain：该行 prev_hash 必须等于链上前一行的 event_hash（真实链接，不是孤立行）",
            "C9：可信锚点 seq=2344 / prev_hash=sealed_tail / 无 seq 空洞 / 无断链 / 锚点以下 0 行",
        ],
        "matrix_status_vocabulary": "Playwright JSON reporter: expected | unexpected | flaky | skipped (test item has NO outcome field)",
        "scope": {"org_id": org_id, "workspace_id": ws_id, "viewer_workspace_id": viewer_ws,
                  "readonly_workspace_id": readonly_ws, "chain": chain, "address": server_address,
                  "job": {"id": job_id, "job_no": job_no, "status": job["status"], "datasets": job["datasets"],
                          "range_type": job["range_type"], "from_block": job["from_block"], "to_block": job["to_block"],
                          "from_timestamp": job["from_timestamp"], "to_timestamp": job["to_timestamp"]},
                  "export_bounds_predicate": bounds or "(none — no job scope)"},
        "freeze": freeze,
        "checks_total": len(checks),
        "checks_failed": len(failed),
        "failed": failed,
        "checks": checks,
        "matrix": matrix,
        "invariants": invariants,
        "audit_chain": chain_summary,
        "exports": per_export,
        "specials": specials,
        "observations": observations,
        "verdict": "PASS" if not failed else "FAIL",
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0 if not failed else 1


# ---------------------------------------------------------------------------
# 无导出对象的证据（D2：按真实语义逐项核验，不是"跳过"）
# ---------------------------------------------------------------------------

def special_legacy_client_export(name, rel, ev, sink, ctx):
    """r10-04：legacy 地址详情页**客户端** SheetJS 导出的真实 xlsx（不是服务端导出，故无 exports 行）。"""
    wb = ev.get("workbook") or {}
    page = ev.get("page") or {}
    file_info = ev.get("file") or {}
    cell = ev.get("cellProbe") or {}
    tx_hash = str(cell.get("value") or (wb.get("firstRow") or {}).get("交易哈希") or "")
    row = wb.get("firstRow") or {}
    dataset = str(row.get("数据集") or "")
    row_address = str(row.get("地址") or ctx["address"])
    hits = None
    if RE_TXHASH.match(tx_hash) and RE_DATASET.match(dataset):
        hits = cell_truth(ctx["chain"], dataset, tx_hash, row_address, "")
    detail = {"workbook": {"sheetNames": wb.get("sheetNames"), "dataRowCount": wb.get("dataRowCount"),
                           "headerCount": len(wb.get("header") or [])},
              "page": page, "file_sha256": file_info.get("sha256"),
              "cell": {"tx_hash": tx_hash, "dataset": dataset, "address": row_address, "clickhouse_rows": hits}}
    check("r10-04-client-export-nonempty-file:" + rel, int(file_info.get("bytes") or 0) > 0, detail)
    check("r10-04-client-export-sheet-named:" + rel, "数据" in (wb.get("sheetNames") or []), wb.get("sheetNames"))
    check("r10-04-client-export-has-rows:" + rel, int(wb.get("dataRowCount") or 0) > 0, detail)
    check("r10-04-client-export-page-had-rows:" + rel, int(page.get("tableRowCount") or 0) > 0, page)
    check("r10-04-client-export-cell-truth-in-clickhouse:" + rel, hits is not None and hits > 0, detail["cell"])
    sink.append({"evidence": rel, "kind": "legacy-client-export", **detail})


def special_legacy_import(name, rel, ev, sink, ctx):
    """r10-05：legacy 下载页导入（正向带入 + 负向格式防线如实记录）。"""
    positive = ev.get("positive") or {}
    negative = ev.get("negative") or {}
    diagnostics = ev.get("diagnostics") or {}
    detail = {"positive": positive, "negative": negative, "legacyFormatGuard": ev.get("legacyFormatGuard"),
              "diagnostics": diagnostics}
    check("r10-05-legacy-import-rendered:" + rel, diagnostics.get("rendered") is True, diagnostics)
    check("r10-05-legacy-import-no-pageerror:" + rel, not (diagnostics.get("pageErrors") or []), diagnostics.get("pageErrors"))
    check("r10-05-legacy-import-positive-second-row:" + rel,
          "0x00000000000000000000000000000000000000d1" in str(positive.get("textarea") or "").lower(), positive)
    check("r10-05-legacy-import-positive-count:" + rel, "2 个有效" in str(positive.get("metaText") or ""), positive.get("metaText"))
    observe("r10-05-legacy-format-guard:" + rel, {"negative": negative, "verdict": ev.get("legacyFormatGuard")})
    sink.append({"evidence": rel, "kind": "legacy-import", **detail})


def special_reuse_contract(name, rel, ev, sink, ctx):
    """r10-06：导出复用路径响应契约（D-R10-02 修复后必须与新建契约一致）。"""
    observed = ev.get("observed") or {}
    first = observed.get("first") or {}
    second = observed.get("second") or {}
    keys = observed.get("createContractKeys") or []
    missing = [k for k in keys if k not in (second.get("keys") or [])]
    reused_id = str(ev.get("reusedExportId") or "")
    detail = {"first": first, "second": second, "missing": missing, "reused_export_id": reused_id}
    check("r10-06-reuse-shape-is-create-contract:" + rel, second.get("shape") == "create", detail)
    check("r10-06-reuse-http-200:" + rel, second.get("httpStatus") == 200, second.get("httpStatus"))
    check("r10-06-reuse-has-create-contract-keys:" + rel, not missing, missing)
    check("r10-06-reuse-dialog-shows-export-id:" + rel,
          str(second.get("dialog") or "").find(reused_id) >= 0 if reused_id else False, second.get("dialog"))
    if reused_id:
        rows = psql("SELECT id::text, coalesce(workspace_id::text,''), coalesce(job_id::text,'') FROM exports WHERE id='%s';\n" % reused_id)
        check("r10-06-reused-export-exists:" + rel, len(rows) == 1, rows)
        if rows:
            check("r10-06-reused-export-in-scope:" + rel, rows[0][1] == ctx["scope"]["ws_id"] and rows[0][2] == ctx["scope"]["job_id"], rows)
    sink.append({"evidence": rel, "kind": "reuse-contract", **detail})


def special_list_contract(name, rel, ev, sink, ctx):
    """r10-07：列表契约（真实 []) + legacy 三页面渲染 + injected null 客户端防线。"""
    contract = ev.get("contract") or {}
    routes = ev.get("routes") or []
    injected = ev.get("injected") or {}
    injected_routes = injected.get("routes") or []
    broken_real = [r for r in routes if not r.get("rendered") or (r.get("pageErrors") or [])]
    broken_injected = [r for r in injected_routes if not r.get("rendered") or (r.get("pageErrors") or [])]
    detail = {"contract": contract, "routes": routes, "injected_routes": injected_routes,
              "broken_real": broken_real, "broken_injected": broken_injected}
    check("r10-07-real-cases-is-array:" + rel, contract.get("casesIsArray") is True, contract)
    check("r10-07-real-tags-is-array:" + rel, contract.get("tagsIsArray") is True, contract)
    check("r10-07-real-api-not-null:" + rel, contract.get("casesIsNull") is False and contract.get("tagsIsNull") is False, contract)
    check("r10-07-legacy-pages-render-real:" + rel, len(routes) >= 3 and not broken_real, {"routes": routes, "broken": broken_real})
    check("r10-07-client-guard-under-injected-null:" + rel,
          len(injected_routes) >= 3 and not broken_injected,
          {"injected_note": injected.get("note"), "broken": broken_injected})
    observe("r10-07-injected-is-not-server-evidence:" + rel,
            "injected 块是浏览器层故障注入（模拟修复前服务端 null），只证明客户端防线，不作为服务端证据")
    sink.append({"evidence": rel, "kind": "list-contract", **detail})


def special_workspace_scope(name, rel, ev, sink, ctx):
    """r10-08：跨工作区导出作用域（可写身份 404、只读身份 404、两者列表必须为空）。"""
    cross = ev.get("crossCreate") or {}
    listing = ev.get("list") or {}
    readonly_list = ev.get("readonlyList") or {}
    writer_detail = ev.get("writerDetail") or {}
    readonly_detail = ev.get("readonlyDetail") or {}
    ws_a = ev.get("wsAExport") or {}
    sessions = ev.get("sessions") or {}
    scope = ctx["scope"]
    detail = {"crossCreate": cross, "list": listing, "readonlyList": readonly_list,
              "writerDetail": writer_detail, "readonlyDetail": readonly_detail,
              "wsAExport": ws_a, "sessions": sessions}
    check("r10-08-wsA-control-nonempty:" + rel, int(ws_a.get("itemCount") or 0) > 0, ws_a)
    check("r10-08-cross-workspace-create-404:" + rel, cross.get("status") == 404, cross)
    check("r10-08-cross-workspace-detail-404:" + rel, writer_detail.get("status") == 404, writer_detail)
    check("r10-08-writer-list-empty:" + rel,
          "itemCount" in listing and int(listing.get("itemCount")) == 0
          and int(listing.get("leakedJobItems") or 0) == 0 and int(listing.get("leakedWsAIds") or 0) == 0, listing)
    check("r10-08-readonly-list-empty:" + rel,
          bool(readonly_list) and not readonly_list.get("skipped") and "itemCount" in readonly_list
          and int(readonly_list.get("itemCount")) == 0 and int(readonly_list.get("leakedWsAIds") or 0) == 0, readonly_list)
    check("r10-08-readonly-detail-404:" + rel, readonly_detail.get("status") == 404, readonly_detail)
    writer_ws = str((sessions.get("writerB") or {}).get("workspace_id") or "")
    readonly_ws = str((sessions.get("readonlyB") or {}).get("workspace_id") or "")
    inv_ws = str((sessions.get("investigator") or {}).get("workspace_id") or "")
    check("r10-08-writer-session-in-wsB:" + rel, bool(scope.get("viewer_workspace_id")) and writer_ws == scope["viewer_workspace_id"],
          {"session_ws": writer_ws, "expected": scope.get("viewer_workspace_id")})
    check("r10-08-readonly-session-in-wsB:" + rel, bool(scope.get("readonly_workspace_id")) and readonly_ws == scope["readonly_workspace_id"],
          {"session_ws": readonly_ws, "expected": scope.get("readonly_workspace_id")})
    check("r10-08-investigator-session-in-wsA:" + rel, inv_ws == scope["workspace_id"],
          {"session_ws": inv_ws, "expected": scope["workspace_id"]})
    check("r10-08-sessions-are-distinct-contexts:" + rel, writer_ws != inv_ws and (readonly_ws != inv_ws), detail["sessions"])
    sink.append({"evidence": rel, "kind": "workspace-scope", **detail})


# ---------------------------------------------------------------------------
# D4：独立对象存储校验（S3/mc），凭据只经进程环境
# ---------------------------------------------------------------------------

def minio_env():
    env = dict(os.environ)
    if not MINIO_ENV_PATH or not os.path.exists(MINIO_ENV_PATH):
        return None
    for line in open(MINIO_ENV_PATH, "r", encoding="utf-8"):
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        env[k] = v
    env["MC_HOST_local"] = "http://%s:%s@%s" % (env["MINIO_ROOT_USER"], env["MINIO_ROOT_PASSWORD"], env["MINIO_ENDPOINT"])
    env["MC_CONFIG_DIR"] = "/tmp/r10-mc-cfg"
    os.makedirs(env["MC_CONFIG_DIR"], exist_ok=True)
    return env


def mc_json(env, args, timeout=300):
    proc = subprocess.run([MC_PATH] + args, env=env, capture_output=True, timeout=timeout)
    return proc.returncode, proc.stdout.decode("utf-8", "replace"), proc.stderr.decode("utf-8", "replace")


def minio_check(export_id, object_ref, db_sha, file_sha, name):
    """独立对象存储校验：走 S3（mc），凭据仅经进程环境；错误 JSON 在 stdout，必须一起留证。"""
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
    env = minio_env()
    if env is None:
        result["ok"] = False
        result["reason"] = "restricted minio env not provided (R10_MINIO_ENV)"
        check("minio-stat:" + name, False, result)
        return result
    declared_export = env.get("EXPORT_BUCKET", "blockchain-exports")
    declared_raw = env.get("RAW_BUCKET", "blockchain-raw")
    candidates = [declared_export, declared_raw, "blockchain-raw", "blockchain-exports"]
    visited = set()
    attempts = []
    found = None
    for bucket in candidates:
        if bucket in visited:
            continue
        visited.add(bucket)
        target = "local/%s/%s" % (bucket, object_ref)
        rc, out, err = mc_json(env, ["--json", "stat", target], timeout=120)
        payload = None
        try:
            payload = json.loads(out.strip().splitlines()[-1]) if out.strip() else None
        except Exception:  # noqa: BLE001
            payload = None
        if rc == 0 and payload and payload.get("status") == "success":
            found = {"bucket": bucket, "target": target, "stat": payload, "stdout": out[:800], "stderr": err[:400]}
            break
        attempts.append({"bucket": bucket, "rc": rc, "stdout": out[:400], "stderr": err[:400]})
    if found is None:
        result["ok"] = False
        result["reason"] = "mc stat failed in every candidate bucket (stdout 留证；mc 的错误 JSON 写在 stdout)"
        result["attempts"] = attempts
        check("minio-stat:" + name, False, result)
        return result
    bucket = found["bucket"]
    target = found["target"]
    result["bucket"] = bucket
    result["stat"] = {k: found["stat"].get(k) for k in ("size", "etag", "type", "lastModified", "key")}
    cat = subprocess.run([MC_PATH, "cat", target], env=env, capture_output=True, timeout=600)
    if cat.returncode != 0:
        result["ok"] = False
        result["reason"] = "mc cat rc=%d stdout=%s stderr=%s" % (
            cat.returncode, cat.stdout.decode("utf-8", "replace")[:400], cat.stderr.decode("utf-8", "replace")[:400])
        check("minio-stat:" + name, False, result)
        return result
    object_sha = hashlib.sha256(cat.stdout).hexdigest()
    size = int(found["stat"].get("size") or 0)
    result.update({"ok": True, "object_sha256": object_sha, "db_sha256": db_sha, "file_sha256": file_sha, "size": size})
    if bucket != declared_export:
        observe("minio-bucket-location:" + name,
                {"object_ref": object_ref, "actual_bucket": bucket, "declared_export_bucket": declared_export,
                 "declared_raw_bucket": declared_raw,
                 "note": "导出产物实际落在 raw bucket；EXPORT_BUCKET 只被 governance 报告路径（PutNamed/OpenNamed）使用。"})
    check("minio-object-bytes-equal-db-sha:" + name, object_sha == db_sha, result)
    if file_sha:
        check("minio-object-bytes-equal-downloaded-file:" + name, object_sha == file_sha, result)
    check("minio-object-size-positive:" + name, size > 0, result)
    return result


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HardError as exc:
        print(json.dumps({"verdict": "ERROR", "evidence_dir": EV, "checks": checks,
                          "observations": observations, "hard_error": str(exc)}, ensure_ascii=False, indent=2))
        sys.exit(2)
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"verdict": "ERROR", "evidence_dir": EV, "checks": checks,
                          "observations": observations, "unexpected_error": repr(exc)}, ensure_ascii=False, indent=2))
        sys.exit(2)
