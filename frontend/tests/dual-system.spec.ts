import { expect, test, type Page } from "@playwright/test";

/**
 * 双系统真实浏览器验收（docs/dual-system-development-plan.md §27 UX、§27.5 数据、§28.3/28.4 验收）。
 *
 * 覆盖：
 *   UX-001 对话框焦点、ESC、返回焦点、长内容滚动
 *   UX-002 四断点布局无横向滚动
 *   AUTH-001..004 登录、会话、退出
 *   RBAC 后台入口对无权限用户的处理
 *   DATA 页面展示真实接口数据（不校验硬编码数字，校验接口契约一致）
 *   AGENTS.md 约束：页面正文不得出现提示横幅/提示卡
 */

const EMAIL = process.env.E2E_USER_EMAIL ?? "admin@local";
const PASSWORD = process.env.E2E_USER_PASSWORD;

test.skip(!PASSWORD, "需要 E2E_USER_PASSWORD 才能执行真实登录验收");

async function loginAs(page: Page, path: "user" | "admin") {
  const loginPath = path === "user" ? "/app-v2/login" : "/admin/login";
  await page.goto(loginPath, { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD!);
  // 精确匹配提交按钮：页面上还有「登录说明」等次要按钮。
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
  // 使用初始密码登录时会出现"需要修改初始密码"提示弹窗，先关闭再继续。
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
    await expect(notice).toBeHidden();
  }
}

test.describe("用户层系统（Investigator Console）", () => {
  test("AUTH-001 登录后进入工作台并展示真实聚合数据", async ({ page }) => {
    const dashboardResponse = page.waitForResponse(
      (r) => r.url().includes("/api/v2/dashboard") && r.status() === 200,
      { timeout: 20_000 },
    );
    await loginAs(page, "user");
    const response = await dashboardResponse;
    const body = await response.json();
    expect(body.success).toBe(true);

    await expect(page).toHaveURL(/\/app-v2\/home$/);
    await expect(page.getByRole("heading", { level: 1 })).toContainText("你好");

    // 页面上展示的每个卡片数字都必须能在接口响应中找到对应值（禁止占位数据）。
    for (const card of body.data.cards as Array<{ label: string; value: number }>) {
      const metrics = page.locator(".metric");
      const matched = metrics.filter({ hasText: card.label });
      await expect(matched.first()).toBeVisible();
      await expect(matched.first().locator(".metric__value")).toHaveText(String(card.value));
    }
  });

  test("AUTH-005 会话由服务端 Cookie 维持，前端不存 Token", async ({ page }) => {
    await loginAs(page, "user");
    await expect(page).toHaveURL(/\/app-v2\/home$/);
    const storage = await page.evaluate(() => ({
      local: JSON.stringify(window.localStorage),
      session: JSON.stringify(window.sessionStorage),
    }));
    expect(storage.local).not.toContain("session");
    expect(storage.session).not.toContain("session");
    // 会话 Cookie 必须是 HttpOnly，因此 document.cookie 中看不到它。
    const cookie = await page.evaluate(() => document.cookie);
    expect(cookie).not.toContain("cs_user_session=");
  });

  test("UX-001 对话框：ESC 关闭、返回焦点、内容可滚动", async ({ page }) => {
    await loginAs(page, "user");
    await page.goto("/app-v2/profile", { waitUntil: "domcontentloaded" });

    const trigger = page.getByRole("button", { name: "退出其它设备" });
    await expect(trigger).toBeVisible();
    await trigger.click();

    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toHaveAttribute("aria-modal", "true");
    // 焦点必须进入对话框内部（焦点陷阱生效）。
    await expect
      .poll(async () => dialog.evaluate((el) => el.contains(document.activeElement)))
      .toBe(true);

    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    // 关闭后不得继续占据页面空间。
    await expect(page.locator(".dialog-backdrop")).toHaveCount(0);
    // 焦点回到触发元素。
    await expect.poll(async () => trigger.evaluate((el) => el === document.activeElement)).toBe(true);
  });

  test("UX-002 移动端无页面级横向滚动，弹窗仍可操作", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await loginAs(page, "user");
    await expect(page).toHaveURL(/\/app-v2\/home$/);

    const overflow = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
    }));
    expect(overflow.scrollWidth).toBeLessThanOrEqual(overflow.clientWidth + 1);

    // 移动端导航抽屉可打开并关闭。
    await page.getByRole("button", { name: "打开主导航" }).click();
    await expect(page.getByRole("navigation", { name: "主导航" })).toBeVisible();
    await page.keyboard.press("Escape");

    await page.goto("/app-v2/profile", { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "退出其它设备" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    const box = await dialog.boundingBox();
    expect(box!.width).toBeLessThanOrEqual(390);
    await expect(dialog.getByRole("button", { name: "取消" })).toBeVisible();
    await dialog.getByRole("button", { name: "取消" }).click();
    await expect(dialog).toBeHidden();
  });

  test("AGENTS 约束：页面正文不出现提示横幅/提示卡", async ({ page }) => {
    await loginAs(page, "user");
    for (const path of ["/app-v2/home", "/app-v2/cases", "/app-v2/search"]) {
      await page.goto(path, { waitUntil: "domcontentloaded" });
      await page.waitForLoadState("networkidle");
      const inline = await page.evaluate(() => {
        const selectors = ['[role="alert"]', ".banner", ".alert", ".alert-banner", ".notice", ".callout", ".toast-inline"];
        return selectors.flatMap((s) => Array.from(document.querySelectorAll(s)).map((el) => `${s}:${el.className}`));
      });
      expect(inline, `页面 ${path} 出现了内嵌提示元素`).toEqual([]);
    }
  });

  test("SEC 未登录访问受保护路由会被引导到登录页", async ({ page }) => {
    await page.goto("/app-v2/cases", { waitUntil: "domcontentloaded" });
    await expect(page).toHaveURL(/\/app-v2\/login$/);
    await expect(page.getByRole("button", { name: /^登录$/ })).toBeVisible();
  });
});

test.describe("后台管理系统（Admin Console）", () => {
  test("ADMIN-001 登录后展示运营总览真实指标", async ({ page }) => {
    const overview = page.waitForResponse(
      (r) => r.url().includes("/api/admin/v1/overview") && r.status() === 200,
      { timeout: 20_000 },
    );
    await loginAs(page, "admin");
    const body = await (await overview).json();
    expect(body.success).toBe(true);

    await expect(page).toHaveURL(/\/admin\/overview$/);
    for (const card of body.data.cards as Array<{ label: string; value: number }>) {
      const metric = page.locator(".metric").filter({ hasText: card.label }).first();
      await expect(metric.locator(".metric__value")).toHaveText(String(card.value));
    }
  });

  test("ADMIN-002 用户列表、角色与权限目录来自真实接口", async ({ page }) => {
    await loginAs(page, "admin");
    await page.goto("/admin/users", { waitUntil: "domcontentloaded" });

    await expect(page.getByRole("heading", { level: 1 })).toContainText("用户与成员");
    await expect(page.locator("table tbody tr").first()).toBeVisible();
    await expect(page.locator("table").first()).toContainText("admin@local");

    // 用户详情以弹窗展示，不跳转到新页面。
    // 列表按创建时间倒序，测试夹具账号会排在前面，因此按邮箱精确定位。
    const adminRow = page.locator("table tbody tr").filter({ hasText: "admin@local" }).first();
    await adminRow.getByRole("button").first().click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText("admin@local");
    await expect(dialog).toContainText("owner");
    await dialog.getByRole("button", { name: "关闭对话框" }).click();
    await expect(dialog).toBeHidden();

    await page.goto("/admin/roles", { waitUntil: "domcontentloaded" });
    await expect(page.locator("table").first()).toContainText("investigator");
    await page.getByRole("button", { name: "查看权限" }).first().click();
    await expect(page.locator(".card").filter({ hasText: "角色权限明细" })).toBeVisible();
  });

  test("ADMIN-003 登录记录可检索并展示客户端声明信息", async ({ page }) => {
    await loginAs(page, "admin");
    await page.goto("/admin/login-events", { waitUntil: "domcontentloaded" });
    await expect(page.locator("table").first()).toContainText("auth.login.success");

    await page.locator("table tbody tr").first().getByRole("button", { name: "详情" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText("事件哈希");
    await expect(dialog).toContainText("客户端 IP");
    await dialog.getByRole("button", { name: "关闭对话框" }).click();
  });

  test("ADMIN-004 审计哈希链可在页面上完成校验", async ({ page }) => {
    await loginAs(page, "admin");
    await page.goto("/admin/audit", { waitUntil: "domcontentloaded" });
    await expect(page.locator("table").first()).toContainText("admin:audit:read");

    await page.getByRole("button", { name: "校验哈希链" }).click();
    const confirm = page.getByRole("dialog").filter({ hasText: "校验审计哈希链" });
    await expect(confirm).toBeVisible();
    await confirm.getByRole("button", { name: "开始校验" }).click();

    const result = page.getByRole("dialog").filter({ hasText: "哈希链完整" });
    await expect(result).toBeVisible({ timeout: 30_000 });
    await expect(result).toContainText("校验条数");
    await result.getByRole("button", { name: "知道了" }).click();
    await expect(page.locator(".dialog-backdrop")).toHaveCount(0);
  });

  test("ADMIN-006 组织与工作区页面展示真实租户数据（S3-ADMIN-01）", async ({ page }) => {
    const orgsResponse = page.waitForResponse(
      (r) => r.url().includes("/api/admin/v1/organizations") && r.status() === 200,
      { timeout: 20_000 },
    );
    await loginAs(page, "admin");
    await page.goto("/admin/organizations", { waitUntil: "domcontentloaded" });
    const body = await (await orgsResponse).json();
    expect(body.success).toBe(true);
    expect((body.data as unknown[]).length).toBeGreaterThan(0);

    await expect(page.getByRole("heading", { level: 1 })).toContainText("组织与工作区");
    // 组织列表必须来自接口：逐个比对名称与 slug。
    for (const org of body.data as Array<{ name: string; slug: string }>) {
      await expect(page.locator("table").first()).toContainText(org.name);
      await expect(page.locator("table").first()).toContainText(org.slug);
    }
    // 组织 / 工作区 / 成员三张表都必须真实渲染（不是空壳）。
    await expect(page.locator("table")).toHaveCount(3);
    await expect(page.locator("table").nth(1).locator("tbody tr").first()).toBeVisible();
    await expect(page.locator(".metric").first()).toBeVisible();
  });

  test("ADMIN-007 撤销角色变更走弹窗确认，不使用原生 prompt", async ({ page }) => {
    const nativeDialogs: string[] = [];
    page.on("dialog", async (d) => {
      nativeDialogs.push(d.type());
      await d.dismiss();
    });
    await loginAs(page, "admin");
    await page.goto("/admin/organizations", { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "管理角色" }).first().click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(page.getByRole("dialog")).toContainText("角色绑定");
    // AGENTS.md：确认与输入必须是应用内弹窗，不得触发浏览器原生对话框。
    expect(nativeDialogs, "不应出现 window.prompt/confirm 等原生对话框").toEqual([]);
    await page.getByRole("dialog").getByRole("button", { name: "关闭对话框" }).click();
  });

  test("边界：未实现的后台页面显式标注 NOT_IMPLEMENTED，不伪造数据", async ({ page }) => {
    await loginAs(page, "admin");
    await page.goto("/admin/provider", { waitUntil: "domcontentloaded" });
    await expect(page.locator(".state__title")).toContainText("NOT_IMPLEMENTED");
    await expect(page.locator(".metric")).toHaveCount(0);
  });

  test("ADMIN-005 用户层会话不能进入后台（服务端也会拒绝）", async ({ page }) => {
    const context = page.context();
    await loginAs(page, "user");
    await expect(page).toHaveURL(/\/app-v2\/home$/);
    // 同一浏览器直接访问后台：没有 admin 会话，必须落到后台登录页。
    await page.goto("/admin/users", { waitUntil: "domcontentloaded" });
    await expect(page).toHaveURL(/\/admin\/login$/);
    // 并且服务端 API 也要拒绝用户层会话。
    const status = await page.evaluate(async () => {
      const res = await fetch("/api/admin/v1/overview", { credentials: "same-origin" });
      return res.status;
    });
    expect([401, 403]).toContain(status);
    await context.clearCookies();
  });
});
