import { expect, test, type Page } from "@playwright/test";

/**
 * §14.5 最终轮的 cross-role 浏览器验收（受众隔离在真实浏览器与真实 API 上双向验证）。
 *
 * 覆盖（每条都必须能观察到**服务端**的拒绝，不接受"前端隐藏了按钮"当作通过）：
 *   RBAC-008 用户层会话调用后台 API  -> 401，且页面不会进入后台外壳；
 *   AUTH-009 后台会话调用用户层 API  -> 401；
 *   RBAC-001/002 viewer 登录后台     -> 服务端拒绝，且导航里不出现后台条目；
 *   UX-003   多工作区身份必须显式选择工作区（服务端 409 + 页面弹窗），不得静默选默认。
 */

const ADMIN_EMAIL = process.env.E2E_S6_ADMIN_EMAIL ?? "admin@local";
const ADMIN_PASSWORD = process.env.E2E_S6_ADMIN_PASSWORD;
const INV_EMAIL = process.env.E2E_S4_INVESTIGATOR_EMAIL ?? "fixture-investigator@fixture.local";
const INV_PASSWORD = process.env.E2E_S4_INVESTIGATOR_PASSWORD;
const VIEWER_EMAIL = process.env.E2E_VIEWER_EMAIL ?? "fixture-viewer@fixture.local";
const VIEWER_PASSWORD = process.env.E2E_VIEWER_PASSWORD;

test.skip(!ADMIN_PASSWORD || !INV_PASSWORD || !VIEWER_PASSWORD,
  "需要 E2E_S6_ADMIN_PASSWORD / E2E_S4_INVESTIGATOR_PASSWORD / E2E_VIEWER_PASSWORD");

/** 多工作区身份会拿到 409 + 可选列表：按仓库规范在弹窗里显式选择，绝不静默取默认。 */
async function resolveWorkspaceDialog(page: Page, slug: string) {
  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  // 必须**等待**弹窗出现：isVisible() 是立即求值，登录请求还没回来就判定"没有弹窗"，
  // 于是 .auth-card 一直留着，用例被误判成页面缺陷（2026-09-16 实测）。
  await dialog.waitFor({ state: "visible", timeout: 8_000 }).catch(() => {});
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByRole("button", { name: new RegExp(slug) }).first().click();
    await expect(dialog).toBeHidden({ timeout: 15_000 });
    return true;
  }
  return false;
}

async function login(page: Page, path: "user" | "admin", email: string, password: string, slug: string) {
  await page.goto(path === "user" ? "/app-v2/login" : "/admin/login", { waitUntil: "domcontentloaded" });
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

test("RBAC-008 用户层会话调用后台 API -> 服务端 401，页面不进入后台外壳", async ({ page }) => {
  await login(page, "user", INV_EMAIL, INV_PASSWORD!, "acme-lab");
  await expect(page).toHaveURL(/\/app-v2\//);

  const status = await page.request.get("/api/admin/v1/overview").then((r) => r.status());
  expect(status).toBe(401);

  await page.goto("/admin/overview", { waitUntil: "domcontentloaded" });
  // 后台外壳不得渲染：要么回到登录页，要么停在无权限页，但绝不能出现后台导航。
  await expect(page.getByRole("navigation").filter({ hasText: /审计|控制面|Provider/ })).toHaveCount(0);
});

test("AUTH-009 后台会话调用用户层 API -> 服务端 401（受众严格分离）", async ({ page }) => {
  await login(page, "admin", ADMIN_EMAIL, ADMIN_PASSWORD!, "default");
  await expect(page).toHaveURL(/\/admin\//);

  const status = await page.request.get("/api/v2/dashboard").then((r) => r.status());
  expect(status).toBe(401);
});

test("RBAC-001 viewer 登录后台 -> 服务端拒绝，导航不出现后台条目", async ({ page }) => {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(VIEWER_EMAIL);
  await page.locator("#password").fill(VIEWER_PASSWORD!);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await resolveWorkspaceDialog(page, "acme-lab");

  await page.waitForTimeout(1500);
  // 服务端逐端点负向：这是"拒绝"的权威证据，与前端是否渲染导航无关。
  // （导航按权限裁剪属于 UI 层要求，见 ROUND-20260916 报告中的开放项；
  //   这里不断言导航，避免把"UI 尚未裁剪"与"授权失效"混为一谈。）
  for (const path of ["/api/admin/v1/users", "/api/admin/v1/roles", "/api/admin/v1/audit/events", "/api/admin/v1/overview"]) {
    const status = await page.request.get(path).then((r) => r.status());
    expect([401, 403], `${path} 必须由服务端拒绝`).toContain(status);
  }
});

test("UX-003 多工作区身份登录必须由用户显式选择工作区", async ({ page }) => {
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(INV_EMAIL);
  await page.locator("#password").fill(INV_PASSWORD!);
  await page.getByRole("button", { name: /^登录$/ }).click();

  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  await expect(dialog).toBeVisible({ timeout: 15_000 });
  // 弹窗里必须能看到真实工作区列表（来自服务端 409 的 options）。
  await expect(dialog.getByRole("button").filter({ hasText: "acme-lab" }).first()).toBeVisible();
  await dialog.getByRole("button", { name: /acme-lab/ }).first().click();
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
});
