#!/usr/bin/env bash
# S7-QA-01：重跑 rbac / iam 与真实浏览器验收（修正环境与陈旧断言后）。
set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7"
QA="$EV/qa"
TESTS="$PROJECT/acceptance-evidence/dual-system-v1/tests"
mkdir -p "$QA"

export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then set -a; . /tmp/s6-e2e-env.sh; set +a; fi

echo "=== rbac (with FIXTURE_* passwords) ==="
mkdir -p "$QA/rbac"
env FIXTURE_VIEWER_PASSWORD="$VIEWER_PASSWORD" \
    FIXTURE_ANALYST_PASSWORD="$ANALYST_PASSWORD" \
    FIXTURE_INVESTIGATOR_PASSWORD="$INVESTIGATOR_PASSWORD" \
    FIXTURE_ENV=/tmp/dual-system-fixture.env \
    EVIDENCE_DIR="$QA/rbac" PROJECT="$PROJECT" \
    bash "$TESTS/run-rbac-e2e.sh" >"$QA/rbac/console.log" 2>&1
echo "rbac rc=$?"
tail -n 4 "$QA/rbac/console.log"

echo "=== iam (SMOKE-09 updated) ==="
mkdir -p "$QA/iam"
env EVIDENCE_DIR="$QA/iam" PROJECT="$PROJECT" bash "$TESTS/run-iam-e2e.sh" >"$QA/iam/console.log" 2>&1
echo "iam rc=$?"
tail -n 4 "$QA/iam/console.log"

echo "=== playwright (real browser) ==="
env PROJECT="$PROJECT" EV="$EV" bash "$TESTS/run-sprint7-ui.sh"
echo "ui rc=$?"

echo "=== re-merge summary ==="
python3 - "$QA" <<'PY'
import json, os, re, sys
qa = sys.argv[1]
def parse(name):
    rj = os.path.join(qa, name + ".result.json")
    if os.path.exists(rj):
        d = json.load(open(rj))
        return {"pass": d.get("pass"), "fail": d.get("fail"), "not_verified": d.get("not_verified"),
                "blocked": d.get("blocked"), "source": "result.json"}
    log = os.path.join(qa, name, "console.log")
    if not os.path.exists(log):
        return None
    txt = open(log, errors="replace").read()
    for pat, keys in ((r"PASS=(\d+)\s+FAIL=(\d+)\s+NOT_VERIFIED=(\d+)", ("pass","fail","not_verified")),
                      (r"PASS=(\d+)\s+FAIL=(\d+)\s+BLOCKED=(\d+)", ("pass","fail","blocked")),
                      (r"PASS=(\d+)\s+FAIL=(\d+)", ("pass","fail"))):
        m = re.findall(pat, txt)
        if m:
            r = dict(zip(keys, (int(x) for x in m[-1])))
            r.setdefault("not_verified", None); r.setdefault("blocked", None)
            r["source"] = "summary-line"
            return r
    return None
sp = os.path.join(qa, "summary.json")
summary = json.load(open(sp)) if os.path.exists(sp) else {"suites": {}, "playwright": {}}
summary.setdefault("suites", {})
for name in ("rbac", "iam"):
    r = parse(name)
    if r is None or r.get("pass") is None:
        summary["suites"][name] = {"status": "UNPARSED"}
        continue
    good = (r["fail"] == 0) and (r.get("not_verified") in (0, None)) and r["pass"] > 0
    r["status"] = "PASS" if good else "FAIL"
    summary["suites"][name] = r
ui = os.path.join(qa, "ui", "result.json")
if os.path.exists(ui):
    summary["playwright"] = json.load(open(ui))
ok = all(v.get("status") == "PASS" for v in summary["suites"].values()) and \
     summary.get("playwright", {}).get("status") == "PASS"
summary["status"] = "PASS" if ok else "FAIL"
summary["criteria"] = "every suite fail==0 and not_verified in (0,null) and pass>0; playwright failed==0 and passed>0"
json.dump(summary, open(sp, "w"), indent=2, ensure_ascii=False)
print(json.dumps(summary, indent=2, ensure_ascii=False))
PY
echo "S7-QA-01 rerun finished"
