#!/usr/bin/env bash
# §14.5 最终轮的**补充浏览器档位**：admin-tablet / cross-role / reduced-motion / **user-mobile**。
#
# 2026-09-16 修正：`user-mobile` 之前哪个运行器都没带（主档位只有 user-desktop/admin-desktop），
# 于是 USER-011「手机调查查看」在矩阵里长期是 skip/PARTIAL —— 那是编排缺口，不是产品缺口。
# 现在补进来：sprint4/sprint5 的移动端用例（含横向溢出与 Dialog 可关闭）在 390×844 上实跑。
#
# 为什么单独一个脚本：run-sprint7-ui.sh 跑的是 user-desktop 与 admin-desktop 两个主档位；
# 最新验收标准还要求平板宽度、跨角色负向与"减少动态效果"三项，且这三项的规格文件不同
# （各自只在自己的 project 下运行，避免把主套件的时间翻三倍）。
#
# 入口协议与主套件一致：必须走本地 TLS 验收入口（http 源下 Playwright 的
# APIRequestContext 不回送 Secure/__Host- Cookie，会产生假失败）。
set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7}"
OUT="${OUT:-$EV/qa/ui-extended}"
mkdir -p "$OUT"

export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$PROJECT/deploy/.env"; set +a

if [ -f /tmp/s6-e2e-env.sh ]; then set -a; . /tmp/s6-e2e-env.sh; set +a; fi
if [ -f /tmp/dual-system-fixture.env ]; then set -a; . /tmp/dual-system-fixture.env; set +a; fi

export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
[ -x "$E2E_CHROMIUM_PATH" ] || { echo "缺少 Chromium：$E2E_CHROMIUM_PATH"; exit 2; }
case "$E2E_BASE_URL" in
  https://*) : ;;
  *) echo "拒绝执行：入口必须是 https（否则 Playwright 不回送 Secure Cookie，结论无效）"; exit 3 ;;
esac

export E2E_S6_ADMIN_EMAIL="${E2E_S6_ADMIN_EMAIL:-admin@local}"
export E2E_S6_ADMIN_PASSWORD="${E2E_S6_ADMIN_PASSWORD:-$ADMIN_PASSWORD}"
export E2E_S6_AUDITOR_EMAIL="${E2E_S6_AUDITOR_EMAIL:-fixture-auditor@fixture.local}"
export E2E_S6_AUDITOR_PASSWORD="${E2E_S6_AUDITOR_PASSWORD:-$AUDITOR_PASSWORD}"
export E2E_S6_PROVIDER_KEY="${E2E_S6_PROVIDER_KEY:-ankr-multichain}"
export E2E_USER_EMAIL="${E2E_USER_EMAIL:-admin@local}"
export E2E_USER_PASSWORD="${E2E_USER_PASSWORD:-$ADMIN_PASSWORD}"
export E2E_S4_INVESTIGATOR_EMAIL="${E2E_S4_INVESTIGATOR_EMAIL:-fixture-investigator@fixture.local}"
export E2E_S4_INVESTIGATOR_PASSWORD="${E2E_S4_INVESTIGATOR_PASSWORD:-$INVESTIGATOR_PASSWORD}"
export E2E_S4_REVIEWER_EMAIL="${E2E_S4_REVIEWER_EMAIL:-fixture-reviewer@fixture.local}"
export E2E_S4_REVIEWER_PASSWORD="${E2E_S4_REVIEWER_PASSWORD:-$REVIEWER_PASSWORD}"
export E2E_VIEWER_EMAIL="${E2E_VIEWER_EMAIL:-fixture-viewer@fixture.local}"
export E2E_VIEWER_PASSWORD="${E2E_VIEWER_PASSWORD:-$VIEWER_PASSWORD}"
# §7.5 要求"管理员、安全管理员、审计员分别登录测试"：三者权限集合不同，导航裁剪必须据此验证。
export E2E_PLATFORM_ADMIN_EMAIL="${E2E_PLATFORM_ADMIN_EMAIL:-fixture-platform_admin@fixture.local}"
export E2E_PLATFORM_ADMIN_PASSWORD="${E2E_PLATFORM_ADMIN_PASSWORD:-$PLATFORM_ADMIN_PASSWORD}"

if [ -f "$EV/qa/sprint5/ids.env" ]; then set -a; . "$EV/qa/sprint5/ids.env"; set +a; fi

cd "$PROJECT/frontend"
node node_modules/@playwright/test/cli.js test --config playwright.dual.config.ts \
  tests/dual-system-cross-role.spec.ts tests/dual-system-nav-permission.spec.ts \
  tests/dual-system-reduced-motion.spec.ts tests/dual-system-sprint4.spec.ts \
  tests/dual-system-sprint5.spec.ts tests/dual-system-sprint6.spec.ts tests/dual-system-quality.spec.ts \
  --project=cross-role --project=reduced-motion --project=admin-tablet --project=user-mobile \
  --reporter=list >"$OUT/playwright-extended.log" 2>&1
RC=$?
tail -n 20 "$OUT/playwright-extended.log"

python3 - "$OUT" "$RC" <<'PY'
import json, os, re, sys
out, rc = sys.argv[1], int(sys.argv[2])
txt = open(os.path.join(out, "playwright-extended.log"), errors="replace").read()
def last(pat):
    m = re.findall(pat, txt)
    return int(m[-1]) if m else 0
res = {"passed": last(r"(\d+)\s+passed"), "failed": last(r"(\d+)\s+failed"),
       "skipped": last(r"(\d+)\s+skipped"), "flaky": last(r"(\d+)\s+flaky"), "rc": rc,
       "projects": ["cross-role", "reduced-motion", "admin-tablet", "user-mobile"]}
res["status"] = "PASS" if (res["failed"] == 0 and res["rc"] == 0 and res["passed"] > 0) else "FAIL"
res["criteria"] = "failed==0 and rc==0 and passed>0"
res["entry"] = os.environ.get("E2E_BASE_URL", "")
json.dump(res, open(os.path.join(out, "result.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(res, indent=2, ensure_ascii=False))
PY
exit $RC
