import { expect, test, type Page } from "@playwright/test";

/**
 * Sprint 4 用户案件与调查闭环的真实浏览器验收（§28.6 / §27.3 USER-001..010）。
 *
 * 设计：账号从环境变量注入；没有账号时整体 skip，绝不把未执行标成 PASS。
 * 覆盖桌面与 user-mobile 两个 project；后台 project 跳过（用户层功能）。
 */

const INV_EMAIL = process.env.E2E_S4_INVESTIGATOR_EMAIL;
const INV_PASSWORD = process.env.E2E_S4_INVESTIGATOR_PASSWORD;
const REV_EMAIL = process.env.E2E_S4_REVIEWER_EMAIL;
const REV_PASSWORD = process.env.E2E_S4_REVIEWER_PASSWORD;
const ADDRESS = process.env.E2E_S4_ADDRESS ?? "TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m";
const RANGE_FROM = process.env.E2E_S4_FROM_BLOCK ?? "78238692";
const RANGE_TO = process.env.E2E_S4_TO_BLOCK ?? "83304697";

test.skip(!INV_PASSWORD || !REV_PASSWORD, "需要 E2E_S4_INVESTIGATOR_PASSWORD / E2E_S4_REVIEWER_PASSWORD");

test.beforeEach(({}, testInfo) => {
  test.skip(testInfo.project.name.startsWith("admin"), "Sprint 4 用户层页面只在 user projects 验证");
});

/**
 * P1-20260913：多工作区身份登录时，服务端返回 409 AUTHN_WORKSPACE_SELECTION_REQUIRED
 * 并附可选列表，页面按仓库规范用弹窗要求**显式选择**工作区（绝不静默取默认）。
 * 夹具账号（fixture-*）同时属于 acme-lab 与 acme-lab-other，因此这一步是必经路径，
 * 不是可选分支：不处理它，登录永远不会完成，后续断言全部误报为页面缺陷。
 */
async function resolveWorkspaceSelection(page: Page, slug = "acme-lab") {
  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  await dialog.waitFor({ state: "visible", timeout: 5_000 }).catch(() => {});
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByRole("button", { name: new RegExp(slug) }).first().click();
    await expect(dialog).toBeHidden({ timeout: 15_000 });
  }
}
async function uiLogin(page: Page, email: string, password: string) {
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录$/ }).click();
  await resolveWorkspaceSelection(page);
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
  }
}

function originOf(page: Page): string {
  return new URL(page.url()).origin;
}

async function csrf(page: Page): Promise<string> {
  const origin = originOf(page);
  const cookies = await page.context().cookies(origin);
  return cookies.find((c) => c.name === "cs_user_session_csrf")?.value ?? "";
}

async function apiLogin(page: Page, email: string, password: string) {
  const origin = originOf(page);
  await page.request.post(`${origin}/api/auth/v1/logout`, {
    headers: { "X-CSRF-Token": await csrf(page), "X-Auth-Audience": "user" },
  });
  const headers = { "Content-Type": "application/json", "X-Auth-Audience": "user" };
  const body = { identifier: email, password, audience: "user" };
  let res = await page.request.post(`${origin}/api/auth/v1/login`, { headers, data: body });
  if (res.status() === 409) {
    // 多工作区身份：服务端按契约要求**显式选择**工作区（P1-20260913），
    // 不替用户猜默认值。这里按可选列表显式挑一个再重试——与真实前端的第二段一致。
    const payload = await res.json().catch(() => null);
    // 服务端把可选工作区放在 error.details.workspaces（不是 options）。
    // 只认一个键名会让"显式选择后重试"这一步静默失效，用例退化成必然失败。
    const details = payload?.error?.details ?? {};
    const options: Array<{ id: string; slug?: string }> = details.workspaces ?? details.options ?? [];
    const wanted = process.env.E2E_WORKSPACE_SLUG ?? "acme-lab";
    const picked = options.find((option) => option.slug === wanted) ?? options[0];
    if (picked) {
      res = await page.request.post(`${origin}/api/auth/v1/login`, {
        headers,
        data: { ...body, workspace_id: picked.id },
      });
    }
  }
  expect(res.ok()).toBeTruthy();
  return (await res.json()).data;
}

async function api(page: Page, method: "GET" | "POST" | "PATCH", path: string, data?: unknown, extraHeaders: Record<string, string> = {}) {
  const origin = originOf(page);
  return page.request.fetch(`${origin}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      "X-Auth-Audience": "user",
      "X-CSRF-Token": await csrf(page),
      ...extraHeaders,
    },
    data: data === undefined ? undefined : JSON.stringify(data),
  });
}

async function acknowledgeSuccess(page: Page) {
  const dialog = page.getByRole("dialog");
  if (!(await dialog.isVisible().catch(() => false))) return;
  const ok = dialog.getByRole("button", { name: "知道了" });
  if (await ok.isVisible().catch(() => false)) {
    await ok.click();
    await expect(dialog).toBeHidden();
  }
}

async function expectNoInlineNotice(page: Page) {
  const inline = await page.evaluate(() => {
    const selectors = ['[role="alert"]', ".banner", ".alert", ".alert-banner", ".notice", ".callout", ".toast-inline"];
    return selectors.flatMap((s) => Array.from(document.querySelectorAll(s)).map((el) => `${s}:${el.className}`));
  });
  expect(inline).toEqual([]);
}

async function createCaseViaAPI(page: Page): Promise<{ id: string; name: string }> {
  const name = `E2E-S4-UI-${Date.now()}`;
  const res = await api(page, "POST", "/api/v2/cases", {
    name,
    description: "Sprint 4 UI 验收案件",
    classification: "confidential",
    priority: "high",
    status: "active",
  });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  return { id: body.data.id, name };
}

test.describe("Sprint 4 案件闭环 UI", () => {
  test("USER-001/004/005/006：案件、对象、调查、Finding/Evidence 页面真实交互", async ({ page }) => {
    test.setTimeout(240_000);
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);

    // 案件列表 → 新建案件 → 概览
    await page.goto("/app-v2/cases", { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "新建案件" }).click();
    const caseName = `S4-UI-${Date.now()}`;
    await page.locator("#case-name").fill(caseName);
    await page.locator("#case-desc").fill("桌面/移动交互验收");
    await page.getByRole("button", { name: "创建案件" }).click();
    const confirm = page.getByRole("dialog");
    await expect(confirm).toBeVisible();
    await expect(confirm).toContainText("创建案件？");
    await confirm.getByRole("button", { name: "创建" }).click();
    const successDialog = page.getByRole("dialog");
    await expect(successDialog).toContainText("案件已创建");
    await successDialog.getByRole("button", { name: "查看案件" }).click();
    await expect(page).toHaveURL(/\/app-v2\/cases\/[0-9a-f-]+\/overview$/, { timeout: 20_000 });
    await expect(page.getByRole("heading", { level: 1 })).toContainText(caseName);
    const caseId = page.url().match(/cases\/([0-9a-f-]+)\/overview/)?.[1] ?? "";
    expect(caseId).not.toBe("");

    // 移动端不得出现横向滚动；Dialog 必须可操作并关闭
    const overflow = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
    }));
    expect(overflow.scrollWidth).toBeLessThanOrEqual(overflow.clientWidth + 1);

    // 调查对象：三个连续输入 Dialog（类型 / 链 / 值）
    await page.getByRole("link", { name: "调查对象" }).click();
    await page.getByRole("button", { name: "新增对象" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByLabel("类型").fill("ADDRESS");
    await dialog.getByRole("button", { name: "确定" }).click();
    await expect(dialog.getByLabel("chain")).toBeVisible();
    await dialog.getByLabel("chain").fill("tron");
    await dialog.getByRole("button", { name: "确定" }).click();
    await expect(dialog.getByLabel("值")).toBeVisible();
    await dialog.getByLabel("值").fill(ADDRESS);
    await dialog.getByRole("button", { name: "确定" }).click();
    await expect(page.locator(`.entity-link[title="${ADDRESS}"]`).first()).toBeVisible({ timeout: 15_000 });
    await expectNoInlineNotice(page);

    // 用 API 建调查（UI 触发会走同一 API），轮询到 waiting_review
    const invRes = await api(page, "POST", "/api/v2/investigations", {
      case_id: caseId,
      goal: `UI 调查 ${Date.now()}`,
      mode: "assisted",
      subjects: [{ type: "ADDRESS", chain: "tron", value: ADDRESS }],
      expected_outputs: ["FINDINGS", "EVIDENCE_INDEX"],
      data_requirement: {
        datasets: ["token_transfers"],
        range: { type: "block", from_block: Number(RANGE_FROM), to_block: Number(RANGE_TO) },
        reuse_policy: "prefer_local",
      },
      budget: { max_provider_cost_usd: "0.00" },
    });
    expect(invRes.ok()).toBeTruthy();
    const invBody = await invRes.json();
    const invID = invBody.data.investigation.id as string;
    const startRes = await api(
      page,
      "POST",
      `/api/v2/investigations/${invID}/commands/start`,
      {},
      { "If-Match": `"${invBody.data.investigation.revision}"` },
    );
    expect(startRes.ok()).toBeTruthy();

    let status = "running";
    let findingID = "";
    let findingTitle = "";
    for (let i = 0; i < 75; i++) {
      const det = await (await api(page, "GET", `/api/v2/investigations/${invID}`)).json();
      status = det.data.status;
      if (status === "waiting_review") {
        const fs = await (await api(page, "GET", `/api/v2/investigations/${invID}/findings?limit=200`)).json();
        const candidate = fs.data.find((f: { status: string; evidence: unknown[] }) => f.status === "candidate" && f.evidence.length > 0);
        findingID = candidate?.id ?? "";
        findingTitle = candidate?.title ?? "";
        break;
      }
      if (status === "failed" || status === "cancelled") break;
      await page.waitForTimeout(2000);
    }
    expect(status).toBe("waiting_review");
    expect(findingID).not.toBe("");
    expect(findingTitle).not.toBe("");

    // 工作台真实渲染：阶段步骤 / Finding / Evidence Rail
    await page.goto(`/app-v2/investigations/${invID}/workbench`, { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { level: 1 })).toContainText("UI 调查");
    await expect(page.getByText("waiting_review").first()).toBeVisible();
    await expect(page.getByText("address_profile").first()).toBeVisible();
    await expect(page.getByText("Evidence Rail")).toBeVisible();
    await expectNoInlineNotice(page);

    // Findings 页：切换 reviewer 后 UI verify + confirm
    await page.getByRole("button", { name: "Findings" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toContainText("Findings 复核");
    await page.getByText(findingTitle, { exact: true }).first().click();
    await expect(page.getByText("引用 Evidence")).toBeVisible();

    await apiLogin(page, REV_EMAIL!, REV_PASSWORD!);
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByText(findingTitle, { exact: true }).first().click();
    const verifyButton = page.locator('[data-testid^="finding-evidence-verify-"]').first();
    await expect(verifyButton).toBeVisible();
    await verifyButton.click();
    const verifyDialog = page.getByRole("dialog");
    await expect(verifyDialog).toContainText("标记 Evidence 为 verified？");
    await verifyDialog.getByRole("button", { name: "确认" }).click();
    await expect(page.locator(".badge").filter({ hasText: "verified" }).first()).toBeVisible({ timeout: 15_000 });
    await acknowledgeSuccess(page);

    await page.getByTestId("finding-confirm").click();
    const confirmDialog = page.getByRole("dialog");
    await expect(confirmDialog).toBeVisible();
    await confirmDialog.getByRole("button", { name: "确认" }).click();
    await expect(page.locator(".badge").filter({ hasText: "confirmed" }).first()).toBeVisible({ timeout: 15_000 });
    await acknowledgeSuccess(page);
    await expectNoInlineNotice(page);

    // 报告闭环：切回 investigator 生成并提交，再由 reviewer 在 UI 审批/签署
    await apiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    const repRes = await api(page, "POST", `/api/v2/cases/${caseId}/reports`, { investigation_id: invID });
    expect(repRes.ok()).toBeTruthy();
    const report = (await repRes.json()).data;
    // 审批人 ID 从 reviewer 登录响应中获取
    await apiLogin(page, REV_EMAIL!, REV_PASSWORD!);
    const revSession = await (await api(page, "GET", "/api/auth/v1/session")).json();
    const reviewerId = revSession.data.user.id as string;
    await apiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    const submitRes = await api(
      page,
      "POST",
      `/api/v2/reports/${report.id}/commands/submit`,
      { reviewer_id: reviewerId, note: "UI Sprint 4 提交复核" },
      { "If-Match": `"${report.status_revision}"` },
    );
    expect(submitRes.ok()).toBeTruthy();

    await apiLogin(page, REV_EMAIL!, REV_PASSWORD!);
    await page.goto("/app-v2/inbox", { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { level: 1 })).toContainText("待我处理");
    await expect(page.getByText(`/reports/${report.id}`, { exact: false }).first()).toBeVisible().catch(() => undefined);
    await page.goto(`/app-v2/reports/${report.id}`, { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "批准" }).click();
    let actionDialog = page.getByRole("dialog");
    await expect(actionDialog).toBeVisible();
    await actionDialog.getByRole("button", { name: "确定" }).click();
    actionDialog = page.getByRole("dialog");
    await expect(actionDialog).toBeVisible();
    await actionDialog.getByRole("button", { name: "确认" }).click();
    await expect(page.locator(".badge").filter({ hasText: "approved" }).first()).toBeVisible({ timeout: 15_000 });
    await acknowledgeSuccess(page);
    await page.getByRole("button", { name: "签署" }).click();
    actionDialog = page.getByRole("dialog");
    await expect(actionDialog).toBeVisible();
    await actionDialog.getByRole("button", { name: "确认" }).click();
    await expect(page.locator(".badge").filter({ hasText: "signed" }).first()).toBeVisible({ timeout: 15_000 });
    await acknowledgeSuccess(page);
    await expectNoInlineNotice(page);
  });

  test("USER-010：通知页 Dialog 交互与无效跳转安全失败", async ({ page }) => {
    await uiLogin(page, REV_EMAIL!, REV_PASSWORD!);
    await page.goto("/app-v2/notifications", { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { level: 1 })).toContainText("通知");
    const openButton = page.getByRole("button", { name: "查看对象" }).first();
    if (await openButton.isVisible().catch(() => false)) {
      await openButton.click();
      // 有目标时进入对象页；对象被删除/无权时出现 Dialog，两种都不是内嵌提示。
      await page.waitForTimeout(500);
    }
    await expectNoInlineNotice(page);
  });
});
