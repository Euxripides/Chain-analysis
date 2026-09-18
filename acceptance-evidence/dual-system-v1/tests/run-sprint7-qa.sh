#!/usr/bin/env bash
# S7-QA-01：执行 §27 全部测试套件并汇总为 Sprint 7 证据（真实 API/UI/DB/运行时证据）。
#
# 运行位置：Ubuntu VM。前置：Sprint 7 已部署，夹具与 Owner 密码通过 /tmp/s6-e2e-env.sh 提供。
#
# 判定规则（沿用 S5/S6，不放宽）：
#   每个 API 套件：pass > 0 且 fail == 0 且 not_verified == 0（BLOCKED 单独记录，不计入 pass）；
#   Playwright：failed == 0。
#   套件输出形式不一（sprint5/6 写 result.json；sprint4/rbac/iam/sprint12 打印汇总行），
#   本脚本统一解析，不允许"没有结果就算通过"。
#
# 输出：acceptance-evidence/dual-system-v1/tests/sprint7/qa/

set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7}"
OUT="$EV/qa"
TESTS="$PROJECT/acceptance-evidence/dual-system-v1/tests"
mkdir -p "$OUT"

export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then
  # 必须 export：run-iam-e2e.sh 等子进程只从环境读取口令，未导出会误判成"缺少 ADMIN_PASSWORD"
  set -a; . /tmp/s6-e2e-env.sh; set +a
fi

BUILD_SHA="$(sha256sum "$PROJECT/backend/bin/api" | awk '{print $1}')"

# 各套件需要的显式变量：不传会让套件以 rc=2 直接退出（编排缺参，不是产品失败）。
# PGPASS 供 run-sprint6/run-sprint7-sec-negative 的 DB 证据；CLICKHOUSE_PASSWORD 供
# run-sprint4/5 的 ClickHouse 证据；S6_BUILD_SHA / API_LOG 是 S6 套件的证据字段。
export PGPASS="${PGPASS:-${POSTGRES_PASSWORD:-}}"
export CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-}"
export S6_BUILD_SHA="${S6_BUILD_SHA:-$BUILD_SHA}"
export API_LOG="${API_LOG:-/tmp/api.log}"

# 运行日志证据：API 走 journald、worker 走 /tmp/worker.log，而套件按 /tmp/api.log 读取
# 运行证据（run-sprint4 RUNTIME、run-sprint6 AUDIT-002d/观测）。这里在开跑前把本次窗口的
# API 日志落盘，避免「日志文件不存在」被误判成产品失败。
if command -v journalctl >/dev/null 2>&1; then
  journalctl --user -u blockchain-api.service --since "-6 hours" --no-pager -o cat > "$API_LOG" 2>/dev/null || true
fi
if [ ! -s "$API_LOG" ]; then
  echo "警告：$API_LOG 为空，运行日志类断言将缺少证据"
fi

STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
: > "$OUT/summary.txt"

echo "S7-QA-01 start $STARTED_AT build_api_sha=$BUILD_SHA"
echo "pid=$$ host=$(hostname)"

run_suite() {
  local name="$1"; shift
  local script="$1"; shift
  local dir="$OUT/$name"
  mkdir -p "$dir"
  echo "=== $name ($script) $(date -u +%H:%M:%S) ==="
  env "$@" EVIDENCE_DIR="$dir" PROJECT="$PROJECT" bash "$TESTS/$script" >"$dir/console.log" 2>&1
  local rc=$?
  if [ -f "$dir/result.json" ]; then
    cp "$dir/result.json" "$OUT/$name.result.json"
  fi
  echo "$name rc=$rc" >> "$OUT/summary.txt"
  tail -3 "$dir/console.log"
}

run_suite sprint4 run-sprint4-e2e.sh
run_suite sprint5 run-sprint5-e2e.sh
run_suite sprint6 run-sprint6-e2e.sh
# RBAC 套件需要 FIXTURE_* 变量名（与 /tmp/s6-e2e-env.sh 的命名不同），
# 不传会直接报"需要 FIXTURE_VIEWER_PASSWORD"并以 rc!=0 退出 —— 那不是产品失败，是编排缺参。
run_suite rbac run-rbac-e2e.sh \
  FIXTURE_VIEWER_PASSWORD="${VIEWER_PASSWORD:-}" \
  FIXTURE_ANALYST_PASSWORD="${ANALYST_PASSWORD:-}" \
  FIXTURE_INVESTIGATOR_PASSWORD="${INVESTIGATOR_PASSWORD:-}" \
  FIXTURE_PLATFORM_ADMIN_PASSWORD="${PLATFORM_ADMIN_PASSWORD:-}" \
  FIXTURE_AUDITOR_PASSWORD="${AUDITOR_PASSWORD:-}" \
  FIXTURE_ENV=/tmp/dual-system-fixture.env
run_suite iam run-iam-e2e.sh
run_suite sprint12 run-sprint12-e2e.sh
# §7.3：功能开关必须在真实请求路径上求值。这个套件用真实请求 + 开关切换 + 评估记录来证明，
# 而不是"后台页面能试算"。它自己负责把开关恢复原状（失败时也会恢复，脚本内先关后开）。
run_suite featureflags run-sprint7-featureflags.sh

# 浏览器验收统一交给 run-sprint7-ui.sh：它负责构造 E2E_* 环境（含 Chromium 路径、入口 URL、
# 夹具账号与 S5 ids.env）。直接在 QA 脚本里裸调 playwright 会退化成 msedge（Linux 上不存在）
# 而整批失败 —— 那是编排缺陷，不是产品失败。
mkdir -p "$OUT/ui"
echo "=== playwright (run-sprint7-ui.sh) $(date -u +%H:%M:%S) ==="
PROJECT="$PROJECT" EV="$EV" bash "$TESTS/run-sprint7-ui.sh" >"$OUT/playwright.log" 2>&1
PW_RC=$?
tail -20 "$OUT/playwright.log"
echo "playwright rc=$PW_RC" >> "$OUT/summary.txt"

FINISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
python3 - "$OUT" "$BUILD_SHA" "$STARTED_AT" "$FINISHED_AT" "$PW_RC" <<'PY'
import json, os, sys, re
out, sha, started, finished, pw_rc = sys.argv[1:6]

def parse(name):
    rj = os.path.join(out, name + ".result.json")
    if os.path.exists(rj):
        d = json.load(open(rj))
        return {"pass": d.get("pass"), "fail": d.get("fail"),
                "not_verified": d.get("not_verified"), "blocked": d.get("blocked"),
                "source": "result.json"}
    log = os.path.join(out, name, "console.log")
    if not os.path.exists(log):
        return None
    txt = open(log, errors="replace").read()
    m = re.findall(r"PASS=(\d+)\s+FAIL=(\d+)\s+NOT_VERIFIED=(\d+)", txt)
    if m:
        p, f, n = m[-1]; return {"pass": int(p), "fail": int(f), "not_verified": int(n), "blocked": None, "source": "summary-line"}
    m = re.findall(r"PASS=(\d+)\s+FAIL=(\d+)\s+BLOCKED=(\d+)", txt)
    if m:
        p, f, b = m[-1]; return {"pass": int(p), "fail": int(f), "not_verified": None, "blocked": int(b), "source": "summary-line"}
    m = re.findall(r"PASS=(\d+)\s+FAIL=(\d+)", txt)
    if m:
        p, f = m[-1]; return {"pass": int(p), "fail": int(f), "not_verified": None, "blocked": None, "source": "summary-line"}
    return None

suites, ok = {}, True
for name in ("sprint4", "sprint5", "sprint6", "rbac", "iam", "sprint12"):
    r = parse(name)
    if r is None or r.get("pass") is None or r.get("fail") is None:
        suites[name] = {"status": "UNPARSED", "raw": r}; ok = False; continue
    good = (r["fail"] == 0) and (r.get("not_verified") in (0, None)) and r["pass"] > 0
    r["status"] = "PASS" if good else "FAIL"
    suites[name] = r
    if not good:
        ok = False

pw = {"rc": int(pw_rc), "passed": 0, "failed": 0, "skipped": 0}
uij = os.path.join(out, "ui", "result.json")
if os.path.exists(uij):
    pw = json.load(open(uij))
    pw["rc"] = int(pw_rc)
logp = os.path.join(out, "playwright.log")
if os.path.exists(logp) and not os.path.exists(uij):
    txt = open(logp, errors="replace").read()
    for key, pat in (("passed", r"(\d+)\s+passed"), ("failed", r"(\d+)\s+failed"), ("skipped", r"(\d+)\s+skipped")):
        m = re.findall(pat, txt)
        if m:
            pw[key] = int(m[-1])
if pw["failed"] != 0 or pw["rc"] != 0 or pw["passed"] == 0:
    ok = False

summary = {"suite": "run-sprint7-qa.sh", "build_api_sha": sha, "started_at": started,
           "finished_at": finished, "suites": suites, "playwright": pw,
           "criteria": "each suite: fail==0 and not_verified in (0,null) and pass>0; playwright: failed==0 and passed>0",
           "status": "PASS" if ok else "FAIL"}
json.dump(summary, open(os.path.join(out, "summary.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(summary, indent=2, ensure_ascii=False))
PY
echo "S7-QA-01 finished $FINISHED_AT"
