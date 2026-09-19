import { expect, test, type Page, type APIResponse } from "@playwright/test";

/**
 * Sprint 5 真实浏览器验收（Explorer / 下载 / 任务 / 导出 / Canvas / 后台任务）。
 *
 * 账号与夹具全部由 E2E 环境变量注入；缺失时整组 skip，绝不把未执行标成 PASS。
 * user-desktop + user-mobile 跑用户层；admin-desktop 跑后台全局任务。
 */

const INV_EMAIL = process.env.E2E_S5_INVESTIGATOR_EMAIL;
const INV_PASSWORD = process.env.E2E_S5_INVESTIGATOR_PASSWORD;
const ADMIN_EMAIL = process.env.E2E_S5_ADMIN_EMAIL;
const ADMIN_PASSWORD = process.env.E2E_S5_ADMIN_PASSWORD;
const CHAIN = process.env.E2E_S5_CHAIN ?? "tron";
const ADDRESS = process.env.E2E_S5_ADDRESS ?? "";
const TX_CHAIN = process.env.E2E_S5_TX_CHAIN ?? CHAIN;
const TX_HASH = process.env.E2E_S5_TX_HASH ?? "";
const COVERED_CHAIN = process.env.E2E_S5_COVERED_CHAIN ?? CHAIN;
const COVERED_ADDRESS = process.env.E2E_S5_COVERED_ADDRESS ?? "";
const COVERED_FROM = process.env.E2E_S5_COVERED_FROM ?? "";
const COVERED_TO = process.env.E2E_S5_COVERED_TO ?? "";
const JOB_ID = process.env.E2E_S5_JOB_ID ?? "";
const CANVAS_ID = process.env.E2E_S5_CANVAS_ID ?? "";
const FLOW_CHAIN = process.env.E2E_S5_FLOW_CHAIN ?? COVERED_CHAIN;
const FLOW_ADDRESS = process.env.E2E_S5_FLOW_ADDRESS ?? COVERED_ADDRESS;
const SEARCH_Q = process.env.E2E_S5_SEARCH_Q ?? "";
const BIG10K_CHAIN = process.env.E2E_S5_10K_CHAIN ?? "";
const BIG10K_ADDRESS = process.env.E2E_S5_10K_ADDRESS ?? "";

test.beforeEach(({}, testInfo) => {
  const isAdmin = testInfo.project.name.startsWith("admin");
  if (isAdmin && (!ADMIN_EMAIL || !ADMIN_PASSWORD)) {
    test.skip(true, "需要 E2E_S5_ADMIN_EMAIL / E2E_S5_ADMIN_PASSWORD");
  }
  if (!isAdmin && (!INV_EMAIL || !INV_PASSWORD)) {
    test.skip(true, "需要 E2E_S5_INVESTIGATOR_EMAIL / E2E_S5_INVESTIGATOR_PASSWORD");
  }
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
async function uiLogin(page: Page, email: string, password: string, base = "/app-v2") {
  await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
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

async function userCSRF(page: Page): Promise<string> {
  const cookies = await page.context().cookies(originOf(page));
  return cookies.find((c) => c.name === "cs_user_session_csrf")?.value ?? "";
}

async function api(
  page: Page,
  method: "GET" | "POST" | "PATCH",
  path: string,
  data?: unknown,
  extraHeaders: Record<string, string> = {},
): Promise<{ status: number; body: any }> {
  const origin = originOf(page);
  const csrf = await userCSRF(page);
  const response = await page.request.fetch(`${origin}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      "X-Auth-Audience": "user",
      "X-CSRF-Token": csrf,
      ...extraHeaders,
    },
    data: data === undefined ? undefined : JSON.stringify(data),
  });
  let body: unknown = null;
  try {
    body = await response.json();
  } catch {
    body = null;
  }
  return { status: response.status(), body };
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow, "页面不得出现页面级横向滚动").toBeLessThanOrEqual(1);
}

async function expectDialogTitle(page: Page, pattern: RegExp) {
  const dialog = page.getByRole("dialog").filter({ hasText: pattern });
  await expect(dialog).toBeVisible({ timeout: 15_000 });
  return dialog;
}

async function closeDialog(page: Page) {
  // 导入流程会**叠**两层弹窗（本地解析摘要 → 服务端校验结果）：
  // 用 getByRole("dialog") 单数定位会触发 strict 模式冲突，被 catch 吞掉后一个都没关，
  // 后续按钮点击被遮罩吃掉（2026-09-16 实测：预览请求根本没发出去）。
  // 这里逐层按 Escape 关闭，直到没有可见弹窗为止。
  for (let i = 0; i < 5; i += 1) {
    if ((await page.getByRole("dialog").count()) === 0) break;
    await page.keyboard.press("Escape");
    await page.waitForTimeout(150);
  }
  await expect(page.getByRole("dialog")).toHaveCount(0, { timeout: 5_000 });
}

test.describe("S5-FE-01 搜索 / Explorer / 地址 / 交易", () => {
  test.beforeEach(({}, testInfo) => {
    test.skip(testInfo.project.name.startsWith("admin"), "用户层页面只在 user projects 验证");
  });

  test("全局搜索按类型分组，并可从结果进入地址详情", async ({ page }) => {
    test.skip(!SEARCH_Q || !ADDRESS || !CHAIN, "需要 E2E_S5_SEARCH_Q / ADDRESS / CHAIN");
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    await page.goto("/app-v2/search", { waitUntil: "domcontentloaded" });
    const input = page.getByLabel("搜索关键词");
    await input.fill(SEARCH_Q);
    await page.getByRole("button", { name: /^搜索$/ }).click();
    await expect(page.getByText(/案件|调查|任务|报告|地址|交易/).first()).toBeVisible({ timeout: 15_000 });
    // 至少存在一个真实分组标题。
    await expect(page.locator("h2, h3").filter({ hasText: /案件|调查|任务|报告|地址|交易/ }).first()).toBeVisible();
    await expectNoHorizontalOverflow(page);

    const overviewResponsePromise = page.waitForResponse(
      (r) => r.url().includes(`/api/v2/explorer/addresses/${CHAIN}/${ADDRESS}/overview`) && r.status() === 200,
      { timeout: 20_000 },
    );
    await page.goto(`/app-v2/addresses/${CHAIN}/${ADDRESS}`, { waitUntil: "domcontentloaded" });
    const overview = (await (await overviewResponsePromise).json()).data as { subject: { normalized_address: string }; summary: { current_balance: { is_current_balance: boolean }; historical_net_flow: { is_current_balance: boolean } } };
    expect(overview.subject.normalized_address.length).toBeGreaterThan(10);
    expect(overview.summary.current_balance.is_current_balance).toBe(true);
    expect(overview.summary.historical_net_flow.is_current_balance).toBe(false);
    await expect(page.getByText(/当前余额|历史净流量/).first()).toBeVisible();
    await expectNoHorizontalOverflow(page);

    // 资产/活动 Tab 走真实接口，并把 cursor/tab 写入 URL。
    await page.getByRole("link", { name: "资产" }).click();
    await page.waitForResponse((r) => r.url().includes(`/assets`) && r.status() === 200, { timeout: 20_000 });
    await expect(page).toHaveURL(/tab=assets/);
    await page.getByRole("link", { name: "Token Transfer" }).click();
    await page.waitForResponse((r) => r.url().includes(`/activity`) && r.status() === 200, { timeout: 20_000 });
    await expect(page).toHaveURL(/tab=transfers/);
    await expectNoHorizontalOverflow(page);
  });

  test("交易详情本地解码与 Logs/Trace Tab", async ({ page }) => {
    test.skip(!TX_HASH || !TX_CHAIN, "需要 E2E_S5_TX_HASH / E2E_S5_TX_CHAIN");
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    await page.goto(`/app-v2/transactions/${TX_CHAIN}/${TX_HASH}`, { waitUntil: "domcontentloaded" });
    await expect(page.getByText(/交易详情/).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText(/解码来源|方法/).first()).toBeVisible();
    const logsResponse = page.waitForResponse((r) => r.url().includes(`/logs`) && r.status() === 200, { timeout: 20_000 });
    await page.getByRole("link", { name: "Logs" }).click();
    await logsResponse;
    await expectNoHorizontalOverflow(page);
    // local_only 可能 404（业务空状态），也可能 200；两者都必须是真实响应。
    const traceResponse = page.waitForResponse((r) => r.url().includes(`/trace`) && r.request().method() === "GET", { timeout: 20_000 });
    await page.getByRole("link", { name: "Trace" }).click();
    const trace = await traceResponse;
    expect([200, 404]).toContain(trace.status());
    await expectNoHorizontalOverflow(page);
  });
});

test.describe("S5-FE-02 下载表单 / 任务列表 / 任务详情", () => {
  test.beforeEach(({}, testInfo) => {
    test.skip(testInfo.project.name.startsWith("admin"), "用户层页面只在 user projects 验证");
  });

  test("Coverage HIT 预览默认不复用 Provider，创建成功提示走 Dialog", async ({ page }) => {
    test.skip(!COVERED_ADDRESS || !COVERED_FROM || !COVERED_TO, "需要 Coverage HIT 夹具");
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    await page.goto("/app-v2/downloads/new", { waitUntil: "domcontentloaded" });
    // 导入的服务端复核是**按链作用域**的（chain + normalized address 才是去重键）。
    // 因此必须先选定链：表单默认会自动选中"第一个可用链"，若它不是本地址所属链，
    // 服务端会逐条拒绝，界面只会显示 INVALID_ADDRESS —— 这一点已作为 UX 发现记录
    // （ROUND-20260916 §3 UI-3），用例按当前契约先选链。
    await page.locator("#download-chain").selectOption(COVERED_CHAIN);
    await page.locator("#download-addresses").fill(COVERED_ADDRESS);
    await page.getByRole("button", { name: "解析导入" }).click();
    const importDialog = await expectDialogTitle(page, /地址导入解析摘要/);
    await importDialog.getByRole("button", { name: "提交服务端校验" }).click();
    // 服务端复核结果必须显式呈现接受/拒绝计数：被拒条目不得静默丢弃。
    const verdictDialog = await expectDialogTitle(page, /服务端地址校验结果/);
    await expect(verdictDialog).toContainText("服务端接受");
    // 链分布必须把该地址记在所选链下（tron:1），且不得出现任何"拒绝 第N行"条目。
    await expect(verdictDialog).toContainText(`${COVERED_CHAIN}:1`);
    await expect(verdictDialog).not.toContainText("拒绝 第");
    // 第三段交互：服务端结果弹窗里的「使用服务端结果」才是导入的落地点
    //（旧文案是「使用这些地址」）。按 Escape 关闭等于取消导入，地址数会停在 0。
    await verdictDialog.getByRole("button", { name: "使用服务端结果" }).click();
    await expect(page.getByText(/地址列表（已导入 1 个）/)).toBeVisible({ timeout: 10_000 });
    // 默认会勾选 transactions+token_transfers；本用例只验证 token_transfers 的 Coverage HIT。
    const transactionsCheckbox = page.getByRole("checkbox", { name: "普通交易（transactions）" });
    if (await transactionsCheckbox.isChecked()) await transactionsCheckbox.uncheck();
    const transferCheckbox = page.getByRole("checkbox", { name: "Token Transfer（token_transfers）" });
    if (!(await transferCheckbox.isChecked())) await transferCheckbox.check();
    await page.locator("#download-range-type").selectOption("block");
    await page.locator("#download-from-block").fill(COVERED_FROM);
    await page.locator("#download-to-block").fill(COVERED_TO);
    const previewResponse = page.waitForResponse(
      (r) => r.url().includes("/api/v2/downloads/preview") && r.request().method() === "POST",
      { timeout: 20_000 },
    );
    await page.getByRole("button", { name: /预览 Coverage 与成本/ }).click();
    const preview = (await (await previewResponse).json()).data as {
      coverage: { overall_ratio: number };
      provider_work_required: boolean;
      reuse: { coverage_hit: boolean };
    };
    expect(preview.coverage.overall_ratio).toBe(1);
    expect(preview.provider_work_required).toBe(false);
    expect(preview.reuse.coverage_hit).toBe(true);
    await expectDialogTitle(page, /无需新增 Provider 请求|预览完成|Coverage 与成本预览完成/);
    await closeDialog(page);

    const createResponse = page.waitForResponse(
      (r) => r.url().endsWith("/api/v2/downloads") && r.request().method() === "POST",
      { timeout: 20_000 },
    );
    await page.getByRole("button", { name: "创建下载任务" }).click();
    const createDialog = page.getByRole("dialog").filter({ hasText: "确认创建下载任务" });
    await expect(createDialog).toBeVisible({ timeout: 10_000 });
    await createDialog.getByRole("button", { name: "确认创建" }).click();
    const created = (await (await createResponse).json()).data as { status: string; provider_work_required: boolean; created: boolean };
    expect(created.status).toBe("ready");
    expect(created.provider_work_required).toBe(false);
    expect(created.created).toBe(false);
    await expectDialogTitle(page, /本地数据已覆盖|Coverage 已完整命中/);
    await expectNoHorizontalOverflow(page);
    await closeDialog(page);
  });

  test("任务列表与详情展示地址/Dataset/Shard 三层进度并连接 SSE", async ({ page }) => {
    test.skip(!JOB_ID, "需要 E2E_S5_JOB_ID");
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    await page.goto("/app-v2/jobs", { waitUntil: "domcontentloaded" });
    await expect(page.getByText(/任务|进度|Dataset|Shard/).first()).toBeVisible({ timeout: 20_000 });
    await expectNoHorizontalOverflow(page);

    const sseRequest = page.waitForRequest((r) => r.url().includes(`/api/v2/jobs/${JOB_ID}/events`), { timeout: 25_000 });
    await page.goto(`/app-v2/jobs/${JOB_ID}`, { waitUntil: "domcontentloaded" });
    await sseRequest;
    await expect(page.getByText(/requested|effective|成功游标|地址进度|Shard/).first()).toBeVisible({ timeout: 20_000 });
    // 用独立真实 API 请求核对三层数据确实存在（不复用页面响应对象，避免导航竞态）。
    const detail = await api(page, "GET", `/api/v2/jobs/${JOB_ID}`);
    expect(detail.status).toBe(200);
    expect((detail.body.data.layers.addresses as unknown[]).length).toBeGreaterThan(0);
    expect(String(detail.body.data.performance.requested_mode).length).toBeGreaterThan(0);
    await expectNoHorizontalOverflow(page);
  });
});

test.describe("S5-FE-03 Canvas 并发保存 / USER-007 / USER-011 / USER-012 / UX-004", () => {
  test.beforeEach(({}, testInfo) => {
    test.skip(testInfo.project.name.startsWith("admin"), "用户层页面只在 user projects 验证");
  });

  test("Canvas 第二客户端 stale 保存收到 412 与字段级 diff，且不静默覆盖", async ({ page }) => {
    test.skip(!CANVAS_ID || !FLOW_ADDRESS, "需要 E2E_S5_CANVAS_ID / FLOW_ADDRESS");
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    const initial = page.waitForResponse((r) => r.url().includes(`/api/v2/canvases/${CANVAS_ID}`) && r.status() === 200, { timeout: 20_000 });
    await page.goto(`/app-v2/canvases/${CANVAS_ID}`, { waitUntil: "domcontentloaded" });
    const canvas = (await (await initial).json()).data as {
      revision: number;
      name: string;
      node_refs?: string[];
      edge_refs?: string[];
      groups?: unknown[];
      annotations?: unknown[];
      filters?: Record<string, unknown>;
      layout?: Record<string, unknown>;
      camera?: Record<string, unknown>;
      view_state?: Record<string, unknown>;
    };
    // 第二客户端通过 API 更新（真实服务端 revision 前进），保留节点/边供移动端复验。
    const bumped = await api(page, "PATCH", `/api/v2/canvases/${CANVAS_ID}`, {
      name: `${canvas.name} · 第二客户端`,
      node_refs: canvas.node_refs ?? [],
      edge_refs: canvas.edge_refs ?? [],
      groups: canvas.groups ?? [],
      annotations: canvas.annotations ?? [],
      filters: canvas.filters ?? {},
      layout: canvas.layout ?? {},
      camera: canvas.camera ?? {},
      view_state: canvas.view_state ?? {},
    }, { "If-Match": `"${canvas.revision}"` });
    expect(bumped.status).toBe(200);
    expect((bumped.body.data as { revision: number }).revision).toBe(canvas.revision + 1);

    // 第一客户端只通过 UI 修改：加入本地资金流后保存，服务端应返回 412 + diff。
    await expect(page.getByRole("heading", { name: "图谱" })).toBeVisible({ timeout: 20_000 });
    const addFlowButton = page.getByRole("button", { name: /加入地址资金流/ }).last();
    await expect(addFlowButton).toBeVisible({ timeout: 10_000 });
    await addFlowButton.click();
    const chainPrompt = await expectDialogTitle(page, /加入地址资金流（1\/2）/);
    await chainPrompt.locator("input").fill(FLOW_CHAIN);
    await chainPrompt.getByRole("button", { name: "下一步" }).click();
    const addressPrompt = await expectDialogTitle(page, /加入地址资金流（2\/2）/);
    await addressPrompt.locator("input").fill(FLOW_ADDRESS);
    await addressPrompt.getByRole("button", { name: "查询本地索引" }).click();
    const confirmFlow = await expectDialogTitle(page, /从本地索引加入资金流/);
    await confirmFlow.getByRole("button", { name: "加入" }).click();
    await expectDialogTitle(page, /资金流已加入/);
    await closeDialog(page);
    await expect(page.getByText(/有未保存修改/)).toBeVisible({ timeout: 15_000 });

    await page.getByRole("button", { name: "保存" }).click();
    const conflict = await expectDialogTitle(page, /更新|冲突|差异|revision/i);
    await expect(conflict.getByText(/字段级 diff|diff|提交|当前/).first()).toBeVisible();
    await expect(conflict.getByRole("button", { name: /加载服务端版本/ })).toBeVisible();
    await conflict.getByRole("button", { name: /加载服务端版本/ }).click();
    await expectDialogTitle(page, /已加载服务端版本/);
    await closeDialog(page);
    await expect(page.getByText(new RegExp(`r${canvas.revision + 1}`))).toBeVisible({ timeout: 10_000 });
    await expectNoHorizontalOverflow(page);
  });

  test("USER-011 移动端 Canvas 列表视图可关闭 Dialog 且无横向溢出", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "user-mobile", "移动端专项");
    test.skip(!CANVAS_ID || !FLOW_ADDRESS, "需要 E2E_S5_CANVAS_ID");
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    await page.goto(`/app-v2/canvases/${CANVAS_ID}`, { waitUntil: "domcontentloaded" });
    await expect(page.getByText(/Lens/)).toBeVisible({ timeout: 20_000 });
    await expect(page.getByRole("button", { name: /展开图谱|收起图谱/ })).toBeVisible();
    await page.getByRole("button", { name: /加入地址资金流/ }).last().click();
    await expectDialogTitle(page, /加入地址资金流（1\/2）/);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0, { timeout: 5_000 });
    await expectNoHorizontalOverflow(page);
    const svgToggle = page.getByRole("button", { name: /展开图谱|收起图谱/ });
    if ((await svgToggle.textContent())?.includes("展开")) {
      await svgToggle.click();
    }
    /* 画布自 R6 起由 @chain/graph-engine 在 WebGL 上绘制（§65），选择器随之更新；
       断言的意图不变：展开后画布必须真的可见。 */
    await expect(page.locator(".cs-canvas-stage")).toBeVisible({ timeout: 15_000 });
    await expectNoHorizontalOverflow(page);
  });

  test("USER-012 键盘完成搜索并进入结果，Dialog 焦点可逃逸", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "user-desktop", "键盘流程桌面专项");
    test.skip(!SEARCH_Q || !ADDRESS || !CHAIN, "需要搜索夹具");
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    await page.goto("/app-v2/search", { waitUntil: "domcontentloaded" });
    // 只用键盘把焦点移动到搜索框。
    let focused = "";
    for (let i = 0; i < 40; i += 1) {
      await page.keyboard.press("Tab");
      focused = await page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? "");
      if (focused === "搜索关键词") break;
    }
    expect(focused).toBe("搜索关键词");
    await page.keyboard.type(SEARCH_Q);
    await page.keyboard.press("Enter");
    await expect(page.getByText(/案件|调查|任务|报告|地址|交易/).first()).toBeVisible({ timeout: 15_000 });
    // 继续 Tab 到第一个结果链接并回车。
    let isLink = false;
    for (let i = 0; i < 80; i += 1) {
      await page.keyboard.press("Tab");
      isLink = await page.evaluate(() => document.activeElement?.tagName === "A");
      if (isLink) break;
    }
    expect(isLink).toBe(true);
    await page.keyboard.press("Enter");
    await expect(page).not.toHaveURL(/\/search$/);
    // Dialog 可通过键盘关闭，焦点不困在遮罩里。
    await page.keyboard.press("Shift+Tab");
    // 详情页在数据到达过程中会重渲染（活动/运行态/覆盖率是多个独立查询）。
    // 2026-09-16 实测：CI 全量跑测时 ClickHouse 正被同一环境的下载任务拖慢，
    // `focus()` 解析到的按钮被后续渲染替换，Playwright 报 "element was detached from the DOM,
    // retrying" 直到 60 s 超时；单独复跑与整套复跑各一次都是 PASS（3.6 s）。
    // 这里等页面自己稳定下来再定位，判据（焦点陷阱、Escape 关闭）一个都没放宽。
    await page.waitForLoadState("networkidle").catch(() => {});
    const trigger = page.getByRole("button", { name: /说明|数据口径/ }).first();
    if (await trigger.count()) {
      await expect(trigger).toBeVisible({ timeout: 10_000 });
      await trigger.focus();
      await trigger.press("Enter");
      const dialog = await page.getByRole("dialog").waitFor({ state: "visible", timeout: 5_000 });
      const trapped = await page.evaluate(() => {
        const active = document.activeElement;
        const root = document.querySelector('[role="dialog"]');
        return Boolean(active && root && root.contains(active));
      });
      expect(trapped).toBe(true);
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog")).toHaveCount(0, { timeout: 5_000 });
    }
    await expectNoHorizontalOverflow(page);
  });

  test("UX-004 10k 真实长表：虚拟滚动 DOM 有界、滚动可交互", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "user-desktop", "10k 长表桌面专项");
    test.skip(!BIG10K_ADDRESS || !BIG10K_CHAIN, "需要 E2E_S5_10K_ADDRESS / E2E_S5_10K_CHAIN");
    test.setTimeout(420_000);
    await uiLogin(page, INV_EMAIL!, INV_PASSWORD!);
    await page.goto(`/app-v2/addresses/${BIG10K_CHAIN}/${BIG10K_ADDRESS}?tab=transfers`, { waitUntil: "domcontentloaded" });
    await page.waitForResponse((r) => r.url().includes("/activity") && r.status() === 200, { timeout: 30_000 });
    const t0 = Date.now();
    await page.getByRole("button", { name: /加载全量（最多 10,000 行）/ }).click();
    const confirm = await expectDialogTitle(page, /加载全量活动行/);
    await confirm.getByRole("button", { name: "开始加载" }).click();
    await expect(page.getByRole("dialog").filter({ hasText: /全量活动已加载|加载全量活动失败/ })).toBeVisible({ timeout: 360_000 });
    const elapsed = Date.now() - t0;
    await closeDialog(page);
    const summary = page.getByTestId("table-summary");
    await expect(summary).toBeVisible();
    const text = (await summary.textContent()) ?? "";
    const total = Number((text.match(/共\s*(\d+)\s*行/) ?? [])[1] ?? "0");
    expect(total, `10k 夹具加载行数不足：${text}`).toBeGreaterThanOrEqual(10_000);
    const domRows = await page.locator("table.table--virtual tbody tr").count();
    expect(domRows, "虚拟滚动 DOM 行数必须远小于 10k").toBeLessThanOrEqual(100);
    const scrollStart = Date.now();
    await page.locator("[data-virtualized=true]").evaluate((el) => {
      el.scrollTop = el.scrollHeight / 2;
      el.dispatchEvent(new Event("scroll"));
    });
    await page.waitForTimeout(100);
    await page.locator("[data-virtualized=true]").evaluate((el) => {
      el.scrollTop = el.scrollHeight;
      el.dispatchEvent(new Event("scroll"));
    });
    await page.waitForTimeout(100);
    const domAfter = await page.locator("table.table--virtual tbody tr").count();
    expect(domAfter).toBeLessThanOrEqual(100);
    expect(Date.now() - scrollStart, `滚动交互耗时 ${Date.now() - scrollStart}ms`).toBeLessThan(1500);
    expect(elapsed, `加载 10k 行耗时 ${elapsed}ms`).toBeLessThan(180_000);
    await expectNoHorizontalOverflow(page);
  });
});

test.describe("S5-ADMIN-01 后台全局任务与固定诊断链", () => {
  test.beforeEach(({}, testInfo) => {
    test.skip(!testInfo.project.name.startsWith("admin"), "后台页面只在 admin project 验证");
  });

  test("全局任务列表跨组织可见，诊断链按固定顺序渲染", async ({ page }) => {
    await uiLogin(page, ADMIN_EMAIL!, ADMIN_PASSWORD!, "/admin");
    await page.goto("/admin/jobs", { waitUntil: "domcontentloaded" });
    await expect(page.getByText(/组织|工作区|诊断|任务/).first()).toBeVisible({ timeout: 20_000 });
    await expectNoHorizontalOverflow(page);
    const list = await page.request.get(`${originOf(page)}/api/admin/v1/jobs?limit=5`, {
      headers: { "X-Auth-Audience": "admin" },
    });
    expect(list.status()).toBe(200);
    const listBody = await list.json();
    expect(listBody.data.items.length).toBeGreaterThan(0);
    const job = listBody.data.items[0] as { id: string };
    await page.goto(`/admin/jobs/${job.id}`, { waitUntil: "domcontentloaded" });
    await expect(page.getByText("DownloadJob", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    const order = ["DownloadJob", "SubjectJob", "DatasetJob", "ShardCheckpoint", "ProviderUsage", "RawObject", "ClickHouseCoverage"];
    for (const layer of order) {
      await expect(page.getByText(layer, { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    }
    const bodyText = (await page.locator("body").innerText()).toLowerCase();
    expect(bodyText.includes("apikey="), "Provider 错误不得泄露明文密钥参数").toBe(false);
    await expectNoHorizontalOverflow(page);
  });
});
