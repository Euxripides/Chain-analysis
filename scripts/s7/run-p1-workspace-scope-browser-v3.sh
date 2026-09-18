#!/usr/bin/env bash
# P1 工作区作用域 —— 真实浏览器双上下文 runner（P1-20260913）
# 从 0600 凭据文件注入夹具身份（口令只经环境变量，不进 argv、不进证据），
# 真实 Playwright 运行 → 校验 report 存在/非空/可解析 → 机器 JSON 汇总。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=${P1_EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p1-workspace-scope-browser-v2-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/browser-console.log") 2>&1
echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV"
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }
APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
echo "api_pid=$APIPID api_sha=$(sha256sum /proc/$APIPID/exe | cut -d' ' -f1)"

g(){ awk -F= -v k="$1" '$1==k{print substr($0,index($0,"=")+1)}' "$2"; }
export P1_WSB_EMAIL=$(g S7JS_VIEWER_EMAIL /home/euripides/.p1gate-view-b.env)
export P1_WSB_PW=$(g S7JS_VIEWER_PW /home/euripides/.p1gate-view-b.env)
export P1_WSBINV_EMAIL=$(g S7JS_INVESTIGATOR_EMAIL /home/euripides/.p1gate-inv-b.env)
export P1_WSBINV_PW=$(g S7JS_INVESTIGATOR_PW /home/euripides/.p1gate-inv-b.env)
export P1_WSA_EMAIL=$(g S7JS_INVESTIGATOR_EMAIL /home/euripides/.p1gate-inv-a.env)
export P1_WSA_PW=$(g S7JS_INVESTIGATOR_PW /home/euripides/.p1gate-inv-a.env)
export P1_MULTI_EMAIL=$(g S7JS_INVESTIGATOR_EMAIL /home/euripides/.p1gate-multi.env)
export P1_MULTI_PW=$(g S7JS_INVESTIGATOR_PW /home/euripides/.p1gate-multi.env)
export P1_NOMEM_EMAIL=$(g S7JS_VIEWER_EMAIL /home/euripides/.p1gate-nomem.env)
export P1_NOMEM_PW=$(g S7JS_VIEWER_PW /home/euripides/.p1gate-nomem.env)
export P1_WS_A=8b41836b-6d53-45e4-9a8a-3248a6e24e28
export P1_WS_B=62a87634-da99-40b9-a08e-9edbd339551c
export P1_WS_NOMEM=$(printf "SELECT id::text FROM workspaces WHERE slug='p1-nomem-ws';\n" | pgq)
export P1_JOB_A=554d50b9-d7a3-41af-a393-732370c25c45
export P1_EXPORT_A=$(printf "SELECT id::text FROM exports WHERE job_id='%s' AND workspace_id='%s' ORDER BY created_at LIMIT 1;\n" "$P1_JOB_A" "$P1_WS_A" | pgq)
echo "fixture_env: ws_a=$P1_WS_A ws_b=$P1_WS_B ws_nomem=$P1_WS_NOMEM export_a=$P1_EXPORT_A"
echo "fixture_identities: wsb=$P1_WSB_EMAIL wsa=$P1_WSA_EMAIL wsbinv=$P1_WSBINV_EMAIL multi=$P1_MULTI_EMAIL nomem=$P1_NOMEM_EMAIL"
[ -n "$P1_EXPORT_A" ] || { echo "PREFLIGHT_MISSING export_a"; exit 3; }

export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R10_OUT="$EV"
# node 解析：非登录 shell 没有 nvm，必须显式解析（v1 轮 rc=127 的真实成因）。
NODE=$(ls -d /home/euripides/.nvm/versions/node/*/bin/node 2>/dev/null | sort -V | tail -1)
[ -x "$NODE" ] || NODE=$(command -v node || true)
[ -x "$NODE" ] || { echo "PREFLIGHT_MISSING node"; exit 3; }
export PATH="$(dirname "$NODE"):$PATH"
echo "node=$NODE version=$($NODE -v)"
cd "$R/frontend" || exit 90
"$NODE" node_modules/@playwright/test/cli.js test --config playwright.p1v2.config.ts > "$EV/playwright.console.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC"
tail -n 40 "$EV/playwright.console.log"

REPORT="$EV/playwright-p1v2-report.json"
REPORT_OK=0
if [ -s "$REPORT" ]; then
  if python3 -c 'import json,sys
d=json.load(open(sys.argv[1]));sys.exit(0 if isinstance(d.get("suites"),list) and d["suites"] else 1)' "$REPORT"; then REPORT_OK=1; fi
fi
echo "report_ok=$REPORT_OK report_bytes=$(stat -c %s "$REPORT" 2>/dev/null || echo 0) report_sha256=$(sha256sum "$REPORT" 2>/dev/null | cut -d' ' -f1)"
python3 - "$REPORT" "$EV" "$RC" "$REPORT_OK" "$APIPID" <<'PY'
import json,sys,os,subprocess
rep,ev,rc,report_ok,pid=sys.argv[1],sys.argv[2],int(sys.argv[3]),int(sys.argv[4]),sys.argv[5]
stats={"expected":0,"unexpected":0,"flaky":0,"skipped":0}
failed=[]
if report_ok:
    d=json.load(open(rep,encoding="utf-8"))
    stats=d.get("stats",stats)
    def walk(suites):
        for s in suites:
            for sp in s.get("specs",[]):
                for t in sp.get("tests",[]):
                    if t.get("status")=="unexpected" or t.get("outcome")=="unexpected":
                        failed.append(sp.get("title"))
            walk(s.get("suites",[]))
    walk(d.get("suites",[]))
sha=subprocess.check_output(["sha256sum","/proc/%s/exe"%pid],text=True).split()[0]
summary={"evidence_dir":ev,"playwright_rc":rc,"report_ok":report_ok,
         "stats":stats,"unexpected_titles":failed,
         "gate":"P1 workspace scope (real browser, independent contexts)",
         "api_pid":pid,"api_sha256":sha}
open(os.path.join(ev,"p1-browser-summary.json"),"w").write(json.dumps(summary,ensure_ascii=False,indent=1))
print(json.dumps(summary,ensure_ascii=False,indent=1))
expected=stats.get("expected",0); unexpected=stats.get("unexpected",0)
sys.exit(0 if (report_ok==1 and rc==0 and unexpected==0 and expected>=7) else 1)
PY
SUM_RC=$?
echo "P1_BROWSER_EV=$EV sum_rc=$SUM_RC rc=$RC report_ok=$REPORT_OK"
exit $SUM_RC
