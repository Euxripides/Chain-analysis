import { expect, test, type Browser, type Page } from "@playwright/test";

/**
 * §7.5 后台浏览器要求：**每个角色只看到允许的导航和按钮**。
 *
 * 这份规格刻意不复制任何"路径 -> 权限"的前端映射表：那样的表一旦与实现漂移，
 * 测试就会跟着一起错。这里把**服务端**当作唯一事实来源，做双向验证：
 *
 *   1. 以 Owner 登录，收集后台导航实际渲染出的入口集合（基线），逐个打开确认都能打开；
 *   2. 换 Platform Admin / Auditor 登录，对同一批路径逐个访问：
 *        - 服务端拒绝（Guard 渲染"没有访问该功能的权限"）的路径 **不得**出现在导航里；
 *        - 服务端允许的路径 **必须**出现在导航里。
 *
 * 于是"只看到允许的"与"允许的都看得到"两侧都由真实页面 + 真实会话权限证明，
 * 而不是由前端自述。角色之间的入口数量只做**关系断言**（更少 / 非空 / 是基线的子集），
 * 不写死数字——写死数字只会把测试变成对权限表快照的复读。
 */

const OWNER_EMAIL = process.env.E2E_S6_ADMIN_EMAIL ?? "admin@local";
const OWNER_PASSWORD = process.env.E2E_S6_ADMIN_PASSWORD;
const AUDITOR_EMAIL = process.env.E2E_S6_AUDITOR_EMAIL ?? "fixture-auditor@fixture.local";
const AUDITOR_PASSWORD = process.env.E2E_S6_AUDITOR_PASSWORD;
const PLATFORM_ADMIN_EMAIL = process.env.E2E_PLATFORM_ADMIN_EMAIL ?? "fixture-platform_admin@fixture.local";
const PLATFORM_ADMIN_PASSWORD = process.env.E2E_PLATFORM_ADMIN_PASSWORD;

test.skip(!OWNER_PASSWORD || !AUDITOR_PASSWORD || !PLATFORM_ADMIN_PASSWORD,
  "需要 E2E_S6_ADMIN_PASSWORD / E2E_S6_AUDITOR_PASSWORD / E2E_PLATFORM_ADMIN_PASSWORD");

// 路由级拒绝：<Guard> 渲染的 NoPermissionState（页面整体被替换成无权访问）。
const GUARD_DENIED = /没有访问该功能的权限/;
// 页面级失败：整体加载失败（含服务端 403 文案）。这种入口同样"打不开"，不能出现在导航里。
const PAGE_UNUSABLE = /加载失败|没有访问该功能的权限|没有该操作的权限/;
const NAV = 'nav[aria-label="主导航"] a[href^="/admin"]';

async function resolveWorkspaceDialog(page: Page, slug: string) {
  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  await dialog.waitFor({ state: "visible", timeout: 8_000 }).catch(() => {});
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByRole("button", { name: new RegExp(slug) }).first().click();
    await expect(dialog).toBeHidden({ timeout: 15_000 });
  }
}

async function loginAdmin(page: Page, email: string, password: string, slug: string) {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await resolveWorkspaceDialog(page, slug);
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
    await expect(notice).toBeHidden();
  }
}

/** 当前后台导航里真实渲染出的入口（去掉 /admin 前缀，便于直接 goto）。 */
async function navPaths(page: Page): Promise<string[]> {
  await expect(page.locator(NAV).first()).toBeVisible({ timeout: 20_000 });
  const hrefs = await page.locator(NAV).evaluateAll((nodes) =>
    nodes.map((node) => (node as HTMLAnchorElement).getAttribute("href") ?? ""));
  return [...new Set(hrefs.map((href) => href.replace(/^\/admin/, "") || "/"))].sort();
}

/** 会话权限（服务端权威判据，用于报告里可复算的对照）。 */
async function sessionPermissions(page: Page): Promise<string[]> {
  const body = await page.request.get("/api/auth/v1/session").then((r) => r.json());
  const permissions = body?.data?.permissions;
  return Array.isArray(permissions) ? permissions.filter((p: unknown) => typeof p === "string") : [];
}

/** 页面级状态判定（只看整体状态卡，不看某个区块自己的错误，避免把"局部降级"误判成"打不开"）。 */
async function openPath(page: Page, path: string): Promise<{ denied: boolean; unusable: boolean }> {
  await page.goto("/admin" + path, { waitUntil: "domcontentloaded" });
  await expect(page.locator(".shell__content")).toBeVisible({ timeout: 20_000 });
  await page.waitForTimeout(700);
  const titles = await page.locator(".shell__content .state__title").allInnerTexts();
  return {
    denied: titles.some((title) => GUARD_DENIED.test(title)),
    unusable: titles.some((title) => PAGE_UNUSABLE.test(title)),
  };
}

/** Owner 基线：整个规格共用。放在 beforeAll 里，worker 因失败重建时也会重新采集。 */
let ownerPaths: string[] = [];

test.beforeAll(async ({ browser }: { browser: Browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  await loginAdmin(page, OWNER_EMAIL, OWNER_PASSWORD!, "default");
  ownerPaths = await navPaths(page);
  await context.close();
});

test("§7.5 后台导航：Owner 的每个入口都能打开（导航不含走不通的入口）", async ({ page }) => {
  expect(ownerPaths.length, "Owner 的后台导航入口数量异常（裁剪逻辑可能误伤）").toBeGreaterThanOrEqual(20);
  await loginAdmin(page, OWNER_EMAIL, OWNER_PASSWORD!, "default");
  const permissions = await sessionPermissions(page);
  expect(permissions).toContain("admin:dashboard:read");
  expect(permissions.length).toBeGreaterThanOrEqual(70);

  const unreachable: string[] = [];
  for (const path of ownerPaths) {
    const state = await openPath(page, path);
    if (state.unusable) unreachable.push(path);
  }
  expect(unreachable, "Owner 的导航里出现了打不开的入口").toEqual([]);
});

// strictSubset：Platform Admin 的 59 项权限恰好覆盖了全部导航入口，因此"入口更少"对它不成立；
// Auditor（20 项）必须严格更少 —— 那正是"入口被裁剪过"的直接证据。
for (const persona of [
  { name: "Platform Admin", email: PLATFORM_ADMIN_EMAIL, password: PLATFORM_ADMIN_PASSWORD, slug: "acme-lab", strictSubset: false },
  { name: "Auditor", email: AUDITOR_EMAIL, password: AUDITOR_PASSWORD, slug: "acme-lab", strictSubset: true },
]) {
  test(`§7.5 后台导航：${persona.name} 只看到允许的入口，允许的入口一个不少`, async ({ page }) => {
    await loginAdmin(page, persona.email, persona.password!, persona.slug);
    const paths = await navPaths(page);
    const permissions = await sessionPermissions(page);
    const adminPermissions = permissions.filter((p) => p.startsWith("admin:"));

    // 关系断言（不写死数字）：入口非空、是 Owner 基线的子集、且确实被裁剪过。
    expect(adminPermissions.length, `${persona.name} 未获得任何后台权限，本用例失去意义`).toBeGreaterThan(0);
    expect(paths.length).toBeGreaterThan(0);
    if (persona.strictSubset) expect(paths.length).toBeLessThan(ownerPaths.length);
    else expect(paths.length).toBeLessThanOrEqual(ownerPaths.length);
    for (const path of paths) expect(ownerPaths).toContain(path);

    // 双向对照：服务端拒绝的入口不得出现；服务端允许的入口必须出现。
    const mismatches: string[] = [];
    for (const path of ownerPaths) {
      const { denied, unusable } = await openPath(page, path);
      const visible = paths.includes(path);
      if (denied && visible) mismatches.push(`${path}: 无权限但仍出现在导航里`);
      if (!denied && !visible) mismatches.push(`${path}: 有权限但导航里没有入口`);
      // 可见却整体打不开（例如次要资源 403 被当成整页失败）同样违反 §7.5。
      if (!denied && visible && unusable) mismatches.push(`${path}: 入口可见但页面整体加载失败`);
    }
    expect(mismatches, `${persona.name} 的导航与权限不一致（admin 权限 ${adminPermissions.length} 项）`).toEqual([]);
  });
}

test("§7.5 用户端导航同样按权限裁剪（viewer 不应看到写操作入口）", async ({ page }) => {
  const email = process.env.E2E_VIEWER_EMAIL ?? "fixture-viewer@fixture.local";
  const password = process.env.E2E_VIEWER_PASSWORD;
  test.skip(!password, "需要 E2E_VIEWER_PASSWORD");
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password!);
  await page.getByRole("button", { name: /^登录$/ }).click();
  await resolveWorkspaceDialog(page, "acme-lab");
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });

  const nav = page.locator('nav[aria-label="主导航"] a[href^="/app-v2"]');
  await expect(nav.first()).toBeVisible({ timeout: 20_000 });
  const hrefs = await nav.evaluateAll((nodes) =>
    nodes.map((node) => (node as HTMLAnchorElement).getAttribute("href") ?? ""));
  // viewer 无 download:create：写操作入口不得出现；读类入口必须保留（不能把角色清空）。
  expect(hrefs.some((href) => href.includes("/downloads/new"))).toBe(false);
  expect(hrefs.length).toBeGreaterThan(0);
});
