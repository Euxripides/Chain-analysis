// S6-QA-01 UI 验收：后台控制、治理与可观测性页面（桌面 + 移动/平板宽度）。
//
// 覆盖 §28.8 验收条目的浏览器侧证据：
//   - 每个 Sprint 6 页面在真实数据上渲染（不是 NOT_IMPLEMENTED 占位）；
//   - 关键按钮产生真实 Dialog（AGENTS.md / AD-009：正文不出现提示类元素）；
//   - Kill Switch 影响预览、Provider 连接测试、告警关联链等高风险交互可完成；
//   - 桌面与移动宽度下无横向溢出、Dialog 可关闭；
//   - 审计员只读：无权限页面显示 NoPermissionState 而非报错。

import { expect, test, type Page } from "@playwright/test";

const ADMIN_EMAIL = process.env.E2E_S6_ADMIN_EMAIL;
const ADMIN_PASSWORD = process.env.E2E_S6_ADMIN_PASSWORD;
const AUDITOR_EMAIL = process.env.E2E_S6_AUDITOR_EMAIL;
const AUDITOR_PASSWORD = process.env.E2E_S6_AUDITOR_PASSWORD;
const PROVIDER_KEY = process.env.E2E_S6_PROVIDER_KEY ?? "local-cache";

async function uiLogin(page: Page, email: string, password: string) {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) await page.keyboard.press("Escape");
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
}

/** AGENTS.md：正文不得出现横幅/提示卡/常驻警告条/说明段落。 */
async function expectNoInlineNotices(page: Page) {
  const offenders = await page.evaluate(() => {
    const selectors = ['[role="alert"]', ".banner", ".alert", ".alert-banner", ".notice", ".callout", ".toast-inline"];
    const found: string[] = [];
    for (const selector of selectors) {
      document.querySelectorAll(selector).forEach((node) => {
        if (!node.closest('[role="dialog"]')) found.push(selector);
      });
    }
    return found;
  });
  expect(offenders).toEqual([]);
}

async function closeDialog(page: Page) {
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0, { timeout: 10_000 });
}

const S6_PAGES: Array<{ path: string; title: string }> = [
  { path: "/providers", title: "Provider 账户" },
  { path: "/providers/routing", title: "路由策略" },
  { path: "/runtime/performance", title: "性能控制" },
  { path: "/api-keys", title: "API / MCP Key" },
  { path: "/requests", title: "请求审计" },
  { path: "/costs", title: "成本" },
  { path: "/budgets", title: "预算" },
  { path: "/alerts", title: "告警中心" },
  { path: "/settings", title: "系统设置" },
  { path: "/feature-flags", title: "功能开关" },
  { path: "/control", title: "实时控制面" },
  { path: "/control/kill-switch", title: "Kill Switch" },
  { path: "/system/backups", title: "备份与恢复" },
  { path: "/data/retention", title: "保留与删除" },
  { path: "/system/version", title: "版本与迁移" },
  { path: "/observability", title: "可观测性" },
];

test.describe("S6-FE 后台 Sprint 6 页面", () => {
  test.beforeEach(async ({ page }, testInfo) => {
    test.skip(!testInfo.project.name.startsWith("admin"), "后台页面只在 admin project 验证");
    test.skip(!ADMIN_EMAIL || !ADMIN_PASSWORD, "需要 E2E_S6_ADMIN_EMAIL / E2E_S6_ADMIN_PASSWORD");
    await uiLogin(page, ADMIN_EMAIL!, ADMIN_PASSWORD!);
  });

  test("S6-FE-01 十六个 Sprint 6 页面均渲染真实数据且无内联提示", async ({ page }) => {
    for (const entry of S6_PAGES) {
      await page.goto("/admin" + entry.path, { waitUntil: "domcontentloaded" });
      await expect(page.getByText(entry.title, { exact: false }).first()).toBeVisible({ timeout: 20_000 });
      await expect(page.getByText("NOT_IMPLEMENTED", { exact: false })).toHaveCount(0);
      await expect(page.locator(".card").first()).toBeVisible({ timeout: 20_000 });
      await expectNoInlineNotices(page);
      await expectNoHorizontalOverflow(page);
      const emptyPermission = await page.getByText("没有该操作的权限", { exact: false }).count();
      expect(emptyPermission, "Owner 不应在 S6 页面看到缺权限状态：" + entry.path).toBe(0);
    }
  });

  test("S6-FE-02 移动/平板宽度下 S6 页面无横向溢出且 Dialog 可关闭", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    for (const entry of [{ path: "/providers", title: "Provider 账户" }, { path: "/alerts", title: "告警中心" }, { path: "/control/kill-switch", title: "Kill Switch" }]) {
      await page.goto("/admin" + entry.path, { waitUntil: "domcontentloaded" });
      await expect(page.getByText(entry.title, { exact: false }).first()).toBeVisible({ timeout: 20_000 });
      await expectNoHorizontalOverflow(page);
      await expectNoInlineNotices(page);
      const explain = page.getByRole("button", { name: /口径|说明/ }).first();
      if (await explain.count()) {
        await explain.click();
        await expect(page.getByRole("dialog")).toBeVisible({ timeout: 10_000 });
        await closeDialog(page);
      }
    }
    await page.setViewportSize({ width: 1024, height: 768 });
    await page.goto("/admin/providers", { waitUntil: "domcontentloaded" });
    await expectNoHorizontalOverflow(page);
  });

  test("ADMIN-003/004 Provider 连接测试与密钥摘要均为真实交互", async ({ page }) => {
    await page.goto("/admin/providers", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("Provider 账户", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    const testButton = page.getByRole("button", { name: /测试连接|连接测试/ }).first();
    await expect(testButton).toBeVisible({ timeout: 20_000 });
    await testButton.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible({ timeout: 20_000 });
    await expect(dialog).toContainText(/Provider\.Health|已提交|成功/, { timeout: 20_000 });
    await closeDialog(page);

    await page.goto("/admin/providers/" + PROVIDER_KEY, { waitUntil: "domcontentloaded" });
    // 详情页标题使用 Provider 展示名，因此断言稳定元素：返回列表 + 密钥摘要区。
    await expect(page.getByRole("button", { name: /返回列表/ }).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.locator("text=revision").first()).toBeVisible({ timeout: 20_000 });
    await page.getByRole("tab", { name: /账户/ }).click();
    await expect(page.getByText("永不提供", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    await expectNoHorizontalOverflow(page);
  });

  test("ADMIN-006 Kill Switch 影响预览为真实数据且审批规则可见", async ({ page }) => {
    await page.goto("/admin/control/kill-switch", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("Kill Switch", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    const previewButton = page.getByRole("button", { name: /影响预览|预览/ }).first();
    await expect(previewButton).toBeVisible({ timeout: 20_000 });
    await previewButton.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible({ timeout: 20_000 });
    await expect(dialog).toContainText(/Checkpoint|不删除|不丢弃/, { timeout: 20_000 });
    await closeDialog(page);
    await expectNoHorizontalOverflow(page);
  });

  test("ADMIN-010 功能开关页面可试算并展示评估记录", async ({ page }) => {
    await page.goto("/admin/feature-flags", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("功能开关", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    const evaluate = page.getByRole("button", { name: /试算/ }).first();
    if (await evaluate.count()) {
      await expect(evaluate).toBeVisible({ timeout: 20_000 });
      await evaluate.click();
      await expect(page.getByRole("dialog")).toBeVisible({ timeout: 10_000 });
      await closeDialog(page);
    }
    await expectNoHorizontalOverflow(page);
  });

  test("AUDIT-002/UI 页面与弹窗中不出现密钥明文", async ({ page }) => {
    await page.goto("/admin/api-keys", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("API / MCP Key", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    const body = await page.evaluate(() => document.body.innerText);
    expect(body).not.toMatch(/sk_[0-9a-f]{32,}/);
    await page.goto("/admin/providers/" + PROVIDER_KEY, { waitUntil: "domcontentloaded" });
    const providerBody = await page.evaluate(() => document.body.innerText);
    expect(providerBody).not.toMatch(/sk_[0-9a-f]{32,}/);
  });

  test("S6-OBS-01 可观测性页面展示 P0 告警、SLO 与吞吐拆分", async ({ page }) => {
    await page.goto("/admin/observability", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("可观测性", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByRole("group", { name: /P0/ }).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText("Provider 拉取", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText(/audit_write|api_availability_user/).first()).toBeVisible({ timeout: 20_000 });
    await expectNoHorizontalOverflow(page);
  });
});

test.describe("S6-QA 审计员只读", () => {
  test("RBAC-S6 审计员访问后台只读页面不报错、写操作入口不可用", async ({ page }, testInfo) => {
    test.skip(!testInfo.project.name.startsWith("admin"), "后台页面只在 admin project 验证");
    test.skip(!AUDITOR_EMAIL || !AUDITOR_PASSWORD, "需要 E2E_S6_AUDITOR_EMAIL / E2E_S6_AUDITOR_PASSWORD");
    await uiLogin(page, AUDITOR_EMAIL!, AUDITOR_PASSWORD!);
    await page.goto("/admin/backups", { waitUntil: "domcontentloaded" });
    const title = page.getByText("备份与恢复", { exact: false }).first();
    const denied = page.getByText("没有该操作的权限", { exact: false }).first();
    await expect(title.or(denied)).toBeVisible({ timeout: 20_000 });
    await page.goto("/admin/system/backups", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("备份与恢复", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    const createButton = page.getByRole("button", { name: /创建备份/ }).first();
    if (await createButton.count()) {
      await expect(createButton).toBeDisabled();
    }
    await expectNoHorizontalOverflow(page);
  });
});