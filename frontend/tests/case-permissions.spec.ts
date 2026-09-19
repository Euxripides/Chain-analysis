import { expect, test, type Page } from "@playwright/test";

/**
 * 案件 Tab 三层权限回归（真实后端联调规格）
 *
 * ============================================================================
 * 为什么必须单独有这一份，而不是靠 scripts/uc-v4-render-check.mjs
 * ============================================================================
 * uc-v4-render-check 用的是**浏览器侧 mock 的 API**，它能证明 UI 与路由两层，
 * 但证明不了安全边界——mock 出来的 403 是我们自己写的，不是后端给的。
 *
 * **真正的安全边界是后端的 403。** 前端的 UI 隐藏与路由守卫只负责
 * "不给你无意义的入口和页面"，任何绕过前端的手段（curl、改 JS、直接 fetch）
 * 都必须被后端挡住。所以本规格对每个 Tab 都额外做一次**绕过前端的直接调用**，
 * 那一条才是安全断言；前两条是可用性断言。
 *
 * ============================================================================
 * 需要什么环境变量
 * ============================================================================
 *   E2E_BASE_URL              用户端地址，默认 http://127.0.0.1:3000
 *   E2E_PERM_FULL_EMAIL       拥有全部案件 Tab 权限的账号（对照组）
 *   E2E_PERM_FULL_PASSWORD
 *   E2E_PERM_LIMITED_EMAIL    权限受限账号，**必须至少缺一个 Tab 的权限**
 *   E2E_PERM_LIMITED_PASSWORD
 *   E2E_PERM_CASE_ID          一个真实存在的 case id（受限账号需要 case:read 才能走到守卫）
 *
 * 缺任何一个都会 test.skip，不会假通过。
 *
 * ============================================================================
 * 为什么本地跑不了
 * ============================================================================
 * 它需要真实后端 + 两个真实账号 + 一个真实案件。本地开发机没有这套环境时
 * 全体 skip —— 这是刻意的：这套断言的价值就在于"后端真的返回了 403"，
 * 用 mock 顶替就退化成 uc-v4-render-check 已经覆盖过的东西了。
 *
 * 跑法（CI 联调环境）：
 *   E2E_BASE_URL=https://... E2E_PERM_FULL_EMAIL=... pnpm exec playwright test case-permissions
 */

const BASE = process.env.E2E_BASE_URL ?? "http://127.0.0.1:3000";
const APP = "/app-v2";
const FULL_EMAIL = process.env.E2E_PERM_FULL_EMAIL ?? "";
const FULL_PASSWORD = process.env.E2E_PERM_FULL_PASSWORD ?? "";
const LIMITED_EMAIL = process.env.E2E_PERM_LIMITED_EMAIL ?? "";
const LIMITED_PASSWORD = process.env.E2E_PERM_LIMITED_PASSWORD ?? "";
const CASE_ID = process.env.E2E_PERM_CASE_ID ?? "";

/**
 * Tab → 所需权限 + 该 Tab 会调用的接口。
 *
 * **必须与 apps/user-console/src/app/case-tabs.ts 保持一致**：
 * 那份文件是权限的唯一事实来源，而且已经被 uc-contract-freeze-check 冻结。
 * 这里重复一份是刻意的——如果两边漂移，本规格会失败，那正是我们想知道的。
 */
const TAB_MATRIX = [
  { tab: "overview", permissions: ["case:read"], endpoints: [`/api/v2/cases/${CASE_ID}`] },
  { tab: "subjects", permissions: ["case:read", "subject:list"], endpoints: [`/api/v2/cases/${CASE_ID}/subjects`] },
  { tab: "investigations", permissions: ["case:read", "investigation:list"], endpoints: ["/api/v2/investigations"] },
  { tab: "evidence", permissions: ["case:read", "evidence:list", "investigation:list"], endpoints: ["/api/v2/investigations"] },
  { tab: "reports", permissions: ["case:read", "report:list", "investigation:list"], endpoints: [`/api/v2/cases/${CASE_ID}/reports`] },
  { tab: "members", permissions: ["case:read", "case:member:list"], endpoints: [`/api/v2/cases/${CASE_ID}/members`] },
  { tab: "activity", permissions: ["case:read", "case:activity:read"], endpoints: [`/api/v2/cases/${CASE_ID}/activity`] },
] as const;

async function login(page: Page, email: string, password: string) {
  await page.goto(`${APP}/login`, { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
}

/** 直接向**真实后端**发起请求，不经过前端代码——这一层才是安全边界。 */
async function directApi(page: Page, path: string): Promise<number> {
  const origin = new URL(page.url()).origin;
  const cookies = await page.context().cookies(origin);
  const csrf = cookies.find((cookie) => cookie.name === "cs_user_session_csrf")?.value ?? "";
  const response = await page.request.fetch(`${origin}${path}`, {
    method: "GET",
    headers: { "X-Auth-Audience": "user", "X-CSRF-Token": csrf },
  });
  return response.status();
}

/** 读当前会话的权限集合（服务端返回什么就是什么，不做本地推断）。 */
async function sessionPermissions(page: Page): Promise<Set<string>> {
  const origin = new URL(page.url()).origin;
  const response = await page.request.fetch(`${origin}/api/auth/v1/session`, {
    headers: { "X-Auth-Audience": "user" },
  });
  const body = await response.json().catch(() => null);
  return new Set<string>(body?.data?.permissions ?? []);
}

test.describe("案件 Tab 权限：UI / Route / API 三层", () => {
  test.skip(!FULL_EMAIL || !FULL_PASSWORD || !LIMITED_EMAIL || !LIMITED_PASSWORD || !CASE_ID, "需要 E2E_PERM_* 环境变量（真实后端联调环境）");

  test("受限账号必须真的缺少权限，否则拒绝用例是空转", async ({ page }) => {
    await login(page, LIMITED_EMAIL, LIMITED_PASSWORD);
    const permissions = await sessionPermissions(page);
    const deniedTabs = TAB_MATRIX.filter((entry) => !entry.permissions.every((permission) => permissions.has(permission)));
    /*
     * 这条断言保护的是**本规格自己**：如果 CI 里配的"受限账号"其实权限齐全，
     * 下面所有拒绝用例都会通过——但通过的原因是"没有可拒绝的东西"，
     * 而不是权限生效。这种假通过比没有测试更危险。
     */
    expect(
      deniedTabs.length,
      "E2E_PERM_LIMITED 账号必须至少缺少一个 Tab 的权限，否则本规格的拒绝用例全部空转",
    ).toBeGreaterThan(0);
  });

  for (const entry of TAB_MATRIX) {
    test(`${entry.tab}：受限时被三层拦截，允许时三层放行`, async ({ page }) => {
      await login(page, LIMITED_EMAIL, LIMITED_PASSWORD);
      const permissions = await sessionPermissions(page);
      const allowed = entry.permissions.every((permission) => permissions.has(permission));

      const requests: string[] = [];
      page.on("request", (request) => requests.push(request.url()));

      await page.goto(`${APP}/cases/${CASE_ID}/${entry.tab}`, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(1500);
      const mainText = (await page.locator("#main-content").innerText().catch(() => "")) ?? "";
      const mounted = await page.locator(".cs-tabs").count();

      if (allowed) {
        /* 有权限时收得过紧同样是缺陷。 */
        expect(mainText, `有权限却看不到 ${entry.tab} 的内容`).not.toContain("没有访问");
        expect(mounted, `有权限但 CaseDetailPage 没有挂载`).toBeGreaterThan(0);
        return;
      }

      /* ---- 第①层 UI：入口不该出现 ---- */
      await page.goto(`${APP}/cases/${CASE_ID}/overview`, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(1200);
      const hrefs = await page.evaluate(() =>
        Array.from(document.querySelectorAll(".cs-tabs a")).map((anchor) => anchor.getAttribute("href") ?? ""),
      );
      expect(hrefs.some((href) => href.endsWith(`/${entry.tab}`)), `无权限时 Tab 入口仍然可见`).toBe(false);

      /* ---- 第②层 Route：直达被拦截，且页面不挂载 ---- */
      requests.length = 0;
      await page.goto(`${APP}/cases/${CASE_ID}/${entry.tab}`, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(1500);
      const deniedText = (await page.locator("#main-content").innerText().catch(() => "")) ?? "";
      expect(deniedText, "无权限直达未被拦截").toContain("没有访问");
      expect(await page.locator(".cs-tabs").count(), "无权限直达时 CaseDetailPage 仍然挂载").toBe(0);

      /* 该 Tab 自己的接口一次都不该被请求。 */
      for (const endpoint of entry.endpoints) {
        const calls = requests.filter((url) => url.includes(endpoint)).length;
        expect(calls, `无权限直达时仍然请求了 ${endpoint}`).toBe(0);
      }

      /* ---- 第③层 API：**真正的安全边界** ---- */
      for (const endpoint of entry.endpoints) {
        const status = await directApi(page, endpoint);
        expect(
          status,
          `绕过前端直接调用 ${endpoint} 应当被后端拒绝（403），实际 ${status}；` +
            "前端的两层只负责不给入口，后端 403 才是安全边界",
        ).toBe(403);
      }
    });
  }
});
