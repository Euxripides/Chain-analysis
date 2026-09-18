#!/usr/bin/env bash
# S7-QA-01 补充：真实浏览器（Playwright）验收。
#
# 为什么单独一个脚本：dual-system-* 规范依赖大量 E2E_* 环境变量（账号、真实链上对象、
# 以及由 API 套件产出的 ids.env）。缺变量时 Playwright 会退化到 msedge（Linux 上不存在）
# 或整批失败，不能作为证据。本脚本显式构造完整环境，并要求"跑了 N 条且 0 失败"。
#
# 【入口协议必须与公网一致（2026-09-13 修复）】
# 公网入口是 cloudflared -> 127.0.0.1:8088，nginx 对各 /api 路由写死
# proxy_set_header X-Forwarded-Proto https，因此后端在**所有**入口都下发
# Secure + __Host- 会话 Cookie。Chromium 对 http://127.0.0.1 的 Secure Cookie 宽容，
# 但 Playwright 的 APIRequestContext（page.request / context.cookies(url)）严格按
# "secure cookie 只在 https 源回送"处理：http 源下 Cookie 被丢弃 -> 用例内独立 API
# 断言全部 401（假失败）。因此验收必须在**真实 https 源**上执行：
#   deploy/blockchain-e2e-tls.service（socat，仅监听 127.0.0.1:8448）-> 127.0.0.1:8088
# 该入口只监听回环地址，与公网链路（8088）完全隔离，且不修改任何生产 Cookie 策略。
#
# 用法（VM）：bash run-sprint7-ui.sh
# 输出：acceptance-evidence/dual-system-v1/tests/sprint7/qa/ui/

set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7}"
QA="$EV/qa"
OUT="$QA/ui"
mkdir -p "$OUT"

export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then set -a; . /tmp/s6-e2e-env.sh; set +a; fi

# 浏览器与入口：VM 上只有系统 Chromium；入口是**本地 TLS 验收入口**（见文件头说明）。
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"

[ -x "$E2E_CHROMIUM_PATH" ] || { echo "缺少 Chromium：$E2E_CHROMIUM_PATH"; exit 2; }

# ---- 入口前置检查（不允许静默退化到 http 入口）----
PREFLIGHT="$OUT/entry-preflight.txt"
{
  echo "entry=$E2E_BASE_URL"
  echo "checked_at=$(date -Is)"
  case "$E2E_BASE_URL" in
    https://*) echo "scheme=https OK" ;;
    *)
      echo "scheme=NOT-https"
      echo "结论：拒绝执行。http 入口下 Playwright APIRequestContext 不会回送 Secure/__Host- Cookie，"
      echo "      用例内独立 API 断言会 401（假失败），不能作为验收证据。"
      echo "      请启用 deploy/blockchain-e2e-tls.service（127.0.0.1:8448）后重跑。"
      exit 3 ;;
  esac
  if ! curl -sk -o /dev/null -w "health=%{http_code}\n" "$E2E_BASE_URL/health/live"; then
    echo "结论：入口不可达。"; exit 2
  fi
  echo "--- TLS 证书（本地自签，仅回环）---"
  echo | openssl s_client -connect 127.0.0.1:8448 -servername 127.0.0.1 2>/dev/null \
    | openssl x509 -noout -subject -dates -fingerprint -sha256 2>/dev/null || echo "非 TLS 入口或证书不可读"
  echo "--- 监听归属（8448 仅回环；8088 为公网 tunnel 目标，保持原状）---"
  ss -ltn | grep -E ':8448|:8088' || true
  echo "--- 公网入口未被本脚本改动 ---"
  curl -s -o /dev/null -w "public_entry_health=%{http_code}\n" http://127.0.0.1:8088/health/live || true
} 2>&1 | tee "$PREFLIGHT"
grep -q "scheme=https OK" "$PREFLIGHT" || { echo "入口前置检查失败，终止"; exit 3; }

export E2E_S6_ADMIN_EMAIL="${E2E_S6_ADMIN_EMAIL:-admin@local}"
export E2E_S6_ADMIN_PASSWORD="${E2E_S6_ADMIN_PASSWORD:-$ADMIN_PASSWORD}"
export E2E_S6_AUDITOR_EMAIL="${E2E_S6_AUDITOR_EMAIL:-fixture-auditor@fixture.local}"
export E2E_S6_AUDITOR_PASSWORD="${E2E_S6_AUDITOR_PASSWORD:-$AUDITOR_PASSWORD}"
export E2E_S6_PROVIDER_KEY="${E2E_S6_PROVIDER_KEY:-ankr-multichain}"
# dual-system-quality.spec.ts 用 E2E_USER_EMAIL/E2E_USER_PASSWORD 登录后访问 /admin/*，
# 因此这里必须是**后台可达**的账号（spec 自身默认值也是 admin@local）。
export E2E_USER_EMAIL="${E2E_USER_EMAIL:-admin@local}"
export E2E_USER_PASSWORD="${E2E_USER_PASSWORD:-$ADMIN_PASSWORD}"
export E2E_S4_INVESTIGATOR_EMAIL="${E2E_S4_INVESTIGATOR_EMAIL:-fixture-investigator@fixture.local}"
export E2E_S4_INVESTIGATOR_PASSWORD="${E2E_S4_INVESTIGATOR_PASSWORD:-$INVESTIGATOR_PASSWORD}"
export E2E_S4_REVIEWER_EMAIL="${E2E_S4_REVIEWER_EMAIL:-fixture-reviewer@fixture.local}"
export E2E_S4_REVIEWER_PASSWORD="${E2E_S4_REVIEWER_PASSWORD:-$REVIEWER_PASSWORD}"
# §7.5「管理员、安全管理员、审计员分别登录测试」：导航裁剪用例需要三者的独立口令。
export E2E_PLATFORM_ADMIN_EMAIL="${E2E_PLATFORM_ADMIN_EMAIL:-fixture-platform_admin@fixture.local}"
export E2E_PLATFORM_ADMIN_PASSWORD="${E2E_PLATFORM_ADMIN_PASSWORD:-$PLATFORM_ADMIN_PASSWORD}"

# S5 真实对象来自 S5 API 套件产出的 ids.env（含一次性夹具密码，只在运行时读取，不回写仓库）
if [ -f "$QA/sprint5/ids.env" ]; then
  set -a; . "$QA/sprint5/ids.env"; set +a
else
  echo "警告：缺少 $QA/sprint5/ids.env，S5 相关浏览器用例将被跳过"
fi

SPECS="${SPECS:-tests/dual-system-sprint4.spec.ts tests/dual-system-sprint5.spec.ts tests/dual-system-sprint6.spec.ts tests/dual-system-quality.spec.ts}"
PROJECTS="${PROJECTS:---project=user-desktop --project=admin-desktop}"

cd "$PROJECT/frontend"
node node_modules/@playwright/test/cli.js test --config playwright.dual.config.ts \
  $SPECS $PROJECTS --reporter=list >"$OUT/playwright.log" 2>&1
RC=$?
tail -n 12 "$OUT/playwright.log"

python3 - "$OUT" "$RC" <<'PY'
import json, os, re, sys
out, rc = sys.argv[1], int(sys.argv[2])
txt = open(os.path.join(out, "playwright.log"), errors="replace").read()
def last(pat):
    m = re.findall(pat, txt)
    return int(m[-1]) if m else 0
res = {"passed": last(r"(\d+)\s+passed"), "failed": last(r"(\d+)\s+failed"),
       "skipped": last(r"(\d+)\s+skipped"), "flaky": last(r"(\d+)\s+flaky"), "rc": rc}
res["status"] = "PASS" if (res["failed"] == 0 and res["rc"] == 0 and res["passed"] > 0) else "FAIL"
res["criteria"] = "failed==0 and rc==0 and passed>0"
res["entry"] = os.environ.get("E2E_BASE_URL", "")
json.dump(res, open(os.path.join(out, "result.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(res, indent=2, ensure_ascii=False))
PY
exit $RC
