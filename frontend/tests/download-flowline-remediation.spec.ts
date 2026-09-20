import { expect, test, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";

/**
 * DL-FLOW-REM —— 新建数据下载整改：Dataset 上限三层验证（《全量整改实施规范》§16 / REM-015）。
 *
 * 背景：UI 上限是 6，但**真实链白名单当前最多只返回 5 项**，所以"选择第 7 项"在真实环境不可达。
 * 不允许为了让测试可达而扩大生产白名单，也不允许用 annotation 代替执行（§16.3）。因此拆成三层：
 *
 *   第一层 DL-FLOW-REM-002（真实接口）：真实 /api/v2/meta/datasets 白名单逐项可选择，
 *                                      preview 请求体只包含真实支持的 Dataset。
 *   第二层 DL-FLOW-REM-001（组件确定性）：用 page.route 注入 7 项夹具（第 7 项
 *                                      dataset_test_only_7 只存在于测试，不进生产枚举），
 *                                      验证前 6 项可选、第 7 项进不了 selected、上限说明走 Dialog、
 *                                      键盘与鼠标一致、已选项可取消、被挡住的第 7 项不会进入请求体。
 *   第三层 DL-FLOW-REM-003（服务端约束）：直接用真实服务端接口提交 7 个 Dataset，
 *                                      必须 400 明确拒绝、不创建任务（记录状态码与错误结构）。
 *
 * 不做任何 mock 之外的产品代码改动；只有 meta 元数据与 preview 请求在组件层被夹具替代，
 * 服务端层与真实接口层都打真实后端。
 */

const EMAIL = process.env.DLF_EMAIL ?? process.env.UDL01_EMAIL ?? "";
const PASSWORD = process.env.DLF_PASSWORD ?? process.env.UDL01_PASSWORD ?? "";
const OUT = process.env.DLF_OUT ?? "";
const LOGIN_PATH = "/app-v2/login";
const PAGE_PATH = "/app-v2/downloads/new";
const ADDRESS = process.env.DLF_ADDRESS ?? process.env.R9_ADDRESS ?? "0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649";
const CHAIN_PREFERENCE = process.env.DLF_CHAIN ?? "bsc";

/** 7 项组件夹具：前 6 项与生产枚举同名，第 7 项**只存在于测试**（§16.2 第二层）。 */
const REAL_DATASET_IDS = ["transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions"];
const FIXTURE_ONLY_DATASET_ID = "dataset_test_only_7";
const FIXTURE_DATASET_IDS = REAL_DATASET_IDS.concat([FIXTURE_ONLY_DATASET_ID]);
const DATASET_LABELS: Record<string, string> = {
  transactions: "交易",
  token_transfers: "代币转账",
  nft_transfers: "NFT 转账",
  logs: "事件日志",
  balances: "余额",
  internal_transactions: "内部交易",
  dataset_test_only_7: "测试专用第 7 项",
};
const FIXTURE_DATASETS = FIXTURE_DATASET_IDS.map((id) => ({
  id,
  label: DATASET_LABELS[id],
  status: "available",
  provider: "fixture",
  api_channel: "fixture",
}));
const FIXTURE_CHAINS = [
  { slug: CHAIN_PREFERENCE, name: "BNB Smart Chain", enabled: true },
  { slug: "eth", name: "Ethereum", enabled: true },
];

const TARGET = Number(process.env.DLF_MAX_DATASETS ?? 6);

const loginMeta = { attempts: 0, retriedNavigation: false };

const envelope = (data: unknown) =>
  JSON.stringify({
    success: true,
    data,
    meta: { request_id: "fixture", trace_id: "fixture", server_time: new Date().toISOString(), next_cursor: null },
  });

function dump(testInfo: { project: { name: string } }, name: string, value: unknown) {
  const dir = path.join(OUT, testInfo.project.name);
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, name), JSON.stringify(value, null, 2) + "\n", "utf8");
}

async function login(page: Page) {
  for (;;) {
    loginMeta.attempts += 1;
    try {
      await page.goto(LOGIN_PATH, { waitUntil: "domcontentloaded" });
      break;
    } catch (error) {
      if (loginMeta.attempts >= 3) throw error;
      loginMeta.retriedNavigation = true;
      await page.waitForTimeout(1000 * loginMeta.attempts);
    }
  }
  await expect(page.locator("#identifier"), "login identifier").toBeVisible({ timeout: 30_000 });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect.poll(() => page.url(), { timeout: 30_000 }).not.toContain(LOGIN_PATH);
}

async function openDownloadsNew(page: Page) {
  await page.goto(PAGE_PATH, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#download-addresses"), "地址输入框").toBeVisible({ timeout: 30_000 });
}

const cards = (page: Page) => page.locator(".dl-dataset-card:not(.dl-dataset-card--skeleton)");
const verdictDialog = (page: Page) => page.locator('[role="dialog"]').filter({ hasText: "服务端地址校验结果" });

/**
 * 粘贴地址 → 点「预览 Coverage 与成本」：第一次预览会先弹「服务端地址校验结果」
 * （服务端再次验证是冻结契约），必须显式采用服务端结果才会发出真实 preview 请求。
 */
async function previewAndCapture(page: Page, previewBodies: string[]) {
  await page.locator("#download-addresses").fill(ADDRESS);
  await page.getByRole("button", { name: "预览 Coverage 与成本" }).first().click();
  const verdict = verdictDialog(page);
  await expect(verdict, "服务端地址校验结果弹窗").toBeVisible({ timeout: 60_000 });
  await verdict.getByRole("button", { name: "使用服务端结果" }).click();
  await expect(verdict).toBeHidden({ timeout: 20_000 });
  await expect.poll(() => previewBodies.length, { timeout: 60_000 }).toBeGreaterThan(0);
}
const boxes = (page: Page) => page.locator('.dl-dataset-card:not(.dl-dataset-card--skeleton) input[type="checkbox"]');
const datasetMeta = (page: Page) => page.locator("#download-step-datasets .dl-section-head__meta");
const limitDialog = (page: Page) => page.locator('[role="dialog"]').filter({ hasText: "Dataset 数量超出上限" });

async function waitForDatasets(page: Page, expected: number) {
  await expect(cards(page), "Dataset 卡片必须来自（夹具或真实）元数据").toHaveCount(expected, { timeout: 30_000 });
}

async function authHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((cookie) => cookie.name === "cs_user_session_csrf")?.value ?? "";
  return { "Content-Type": "application/json", "X-CSRF-Token": csrf, "X-Auth-Audience": "user" };
}

async function jobCount(page: Page): Promise<number> {
  const response = await page.request.get("/api/v2/jobs?limit=100", { headers: { "X-Auth-Audience": "user" } });
  const body = (await response.json()) as { data?: { items?: unknown[] }; items?: unknown[] };
  const items = body.data?.items ?? body.items ?? [];
  return Array.isArray(items) ? items.length : -1;
}

test.describe("DL-FLOW-REM Dataset 上限三层验证（§16 / REM-015 / REM-E2E-013）", () => {
  test("DL-FLOW-REM-001 组件确定性：注入 7 项 Dataset 夹具，前 6 项可选、第 7 项进不了 selected、上限说明走 Dialog、键盘与鼠标一致、已选可取消", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "DLF_EMAIL/DLF_PASSWORD not provided");
    const metaRequests: string[] = [];
    const previewBodies: string[] = [];
    await page.route("**/api/v2/meta/datasets*", (route) => {
      metaRequests.push(route.request().url());
      return route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: FIXTURE_DATASETS }) });
    });
    await page.route("**/api/v2/meta/chains*", (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: FIXTURE_CHAINS }) }),
    );
    /* preview 的请求体由页面真实构造；为避免触发真实 Provider 工作，这里只记录请求体并中止响应。 */
    await page.route("**/api/v2/downloads/preview", async (route) => {
      previewBodies.push(route.request().postData() ?? "");
      await route.abort();
    });

    await login(page);
    await openDownloadsNew(page);
    await waitForDatasets(page, 7);
    await expect(datasetMeta(page), "页面必须声明 6 项上限").toContainText("上限 6");
    const metaRequestRecord = metaRequests.slice();

    /* 1) 前 6 项可选（页面默认会预选 transactions/token_transfers，已选的不再重复点击） */
    for (let index = 0; index < TARGET; index += 1) {
      const box = boxes(page).nth(index);
      if (!(await box.isChecked())) await box.click();
      await expect(box, "第 " + (index + 1) + " 项必须被选中").toBeChecked();
      await expect(cards(page).nth(index), "卡片必须给出 data-selected").toHaveAttribute("data-selected", "true");
    }
    await expect(datasetMeta(page), "已选计数").toContainText("已选 " + TARGET + " / 上限 6");
    const selectedBefore = await page.locator(".dl-dataset-card[data-selected='true']").count();
    expect(selectedBefore, "前 6 项必须全部进入 selected").toBe(TARGET);

    /* 2) 第 7 项不能加入 selected：鼠标点击 → Dialog 说明上限 */
    const seventh = boxes(page).nth(6);
    await expect(cards(page).nth(6), "达到上限后第 7 项必须标记 data-blocked").toHaveAttribute("data-blocked", "true");
    await seventh.click();
    const mouseDialog = limitDialog(page);
    await expect(mouseDialog, "上限说明必须走 Dialog").toBeVisible({ timeout: 10_000 });
    const mouseDialogText = (await mouseDialog.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
    expect(mouseDialogText, "Dialog 必须说清上限与原因").toContain("最多选择 6 种 Dataset");
    expect(mouseDialogText, "Dialog 必须点名被挡住的项").toContain(DATASET_LABELS[FIXTURE_ONLY_DATASET_ID]);
    await mouseDialog.getByRole("button", { name: "知道了" }).click();
    await expect(page.locator(".dl-dataset-card[data-selected='true']")).toHaveCount(TARGET);
    await expect(cards(page).nth(6), "第 7 项不得进入 selected").not.toHaveAttribute("data-selected", "true");

    /* 3) 键盘与鼠标一致：焦点在第 7 项按 Space → 同一 Dialog，同样进不了 selected */
    await seventh.focus();
    await page.keyboard.press("Space");
    const keyboardDialog = limitDialog(page);
    await expect(keyboardDialog, "键盘操作必须给出同一个上限 Dialog").toBeVisible({ timeout: 10_000 });
    await keyboardDialog.getByRole("button", { name: "知道了" }).click();
    await expect(page.locator(".dl-dataset-card[data-selected='true']")).toHaveCount(TARGET);
    await expect(cards(page).nth(6)).not.toHaveAttribute("data-selected", "true");

    /* 4) 被挡住的第 7 项不得进入真实请求体：地址 → 服务端校验 → 预览（请求体被记录，响应被中止） */
    await previewAndCapture(page, previewBodies);
    const previewBody = JSON.parse(previewBodies[previewBodies.length - 1] || "{}") as { datasets?: string[] };
    const errorDialog = page.locator('[role="dialog"]');
    if (await errorDialog.first().isVisible().catch(() => false)) {
      const dismiss = errorDialog.first().getByRole("button", { name: /知道了|关闭/ });
      if (await dismiss.count()) await dismiss.first().click();
    }

    /* 5) 已选项可取消：取消一个后上限让位，第 7 项此时可以加入 */
    expect(await boxes(page).nth(TARGET - 1).isChecked(), "第 6 项此时应当是已选状态").toBe(true);
    await boxes(page).nth(TARGET - 1).click();
    await expect(page.locator(".dl-dataset-card[data-selected='true']")).toHaveCount(TARGET - 1);
    await expect(boxes(page).nth(TARGET - 1)).not.toBeChecked();
    await expect(datasetMeta(page)).toContainText("已选 " + (TARGET - 1) + " / 上限 6");
    await expect(cards(page).nth(6), "未满上限时第 7 项不再 blocked").not.toHaveAttribute("data-blocked", "true");
    await boxes(page).nth(6).click();
    await expect(cards(page).nth(6), "让位后第 7 项必须可以加入").toHaveAttribute("data-selected", "true");
    await expect(datasetMeta(page), "重新选满 6 项").toContainText("已选 " + TARGET + " / 上限 6");

    dump(testInfo, "dlflow-rem-001-component-dataset-limit.json", {
      layer: "component-7-dataset-fixture",
      fixture_dataset_ids: FIXTURE_DATASET_IDS,
      fixture_only_id: FIXTURE_ONLY_DATASET_ID,
      ui_max_datasets: TARGET,
      meta_requests: metaRequests,
      selected_before_block_attempt: selectedBefore,
      mouse_limit_dialog_text: mouseDialogText,
      preview_request_datasets: previewBody.datasets ?? null,
      preview_request_contains_fixture_only: (previewBody.datasets ?? []).includes(FIXTURE_ONLY_DATASET_ID),
      final_selected_count: await page.locator(".dl-dataset-card[data-selected='true']").count(),
      login: loginMeta,
    });

    expect(previewBody.datasets, "预览请求体必须来自页面真实构造").toBeTruthy();
    expect(previewBody.datasets, "被上限挡住的第 7 项绝不能进入请求体").not.toContain(FIXTURE_ONLY_DATASET_ID);
    expect(previewBody.datasets, "请求体必须正是已选的 6 项").toEqual(FIXTURE_DATASET_IDS.slice(0, TARGET));
  });

  test("DL-FLOW-REM-002 真实接口层：真实链白名单项全部可选择（EVM 链 <=5），preview 只发送真实支持的 Dataset", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "DLF_EMAIL/DLF_PASSWORD not provided");
    /* 只记录**请求**顺序（同步、无竞态）：最后一个带 chain= 的请求就是页面当前链的作用域请求。
       然后由本用例自己用同一 URL 走真实接口取白名单 —— 不依赖 response 异步回调的到达顺序。 */
    const datasetRequests: string[] = [];
    const previewBodies: string[] = [];
    page.on("request", (request) => {
      if (request.url().includes("/api/v2/meta/datasets")) datasetRequests.push(request.url());
    });
    await page.route("**/api/v2/downloads/preview", async (route) => {
      previewBodies.push(route.request().postData() ?? "");
      await route.abort();
    });

    await login(page);
    await openDownloadsNew(page);
    await expect.poll(() => datasetRequests.filter((url) => url.includes("chain=")).length, { timeout: 30_000 }).toBeGreaterThan(0);
    const chainScopedUrl = datasetRequests.filter((url) => url.includes("chain=")).pop() as string;
    const pageChain = new URL(chainScopedUrl).searchParams.get("chain") ?? "";
    const realResponse = await page.request.get(chainScopedUrl.replace(/^https?:\/\/[^/]+/, ""), { headers: { "X-Auth-Audience": "user" } });
    const realBody = (await realResponse.json()) as { data?: { items?: Array<{ id: string; label: string; status: string }> } };
    const realItems = realBody.data?.items ?? [];
    const realIds = realItems.map((item) => item.id);
    await waitForDatasets(page, realIds.length);

    /* 页面渲染的 Dataset 顺序/身份必须与服务端返回逐项一致（id 从 aria-label 的 （id） 取出）。 */
    const domIds: string[] = [];
    for (let index = 0; index < realIds.length; index += 1) {
      const label = (await boxes(page).nth(index).getAttribute("aria-label")) ?? "";
      /* 第一个括号是 （<dataset id>）；不可用项的 aria-label 后面还会追加原因（含括号）。 */
      const match = label.match(/（([^）]+)）/);
      domIds.push(match ? match[1] : label);
    }
    expect(domIds, "页面 Dataset 顺序与身份必须来自服务端白名单").toEqual(realIds);

    /* 页面当前链的真实元数据：/api/v2/meta/datasets?chain=… 对每条链返回**全部** Dataset 行
       （6 行），用 status 表达该链是否支持（unsupported = 不可选）。因此 §16.1 的"最多 5 项"
       指的是**可用（available）项 <= 5**，不是行数。第 7 项在任何链上都不可达，
       上限行为必须由组件夹具（DL-FLOW-REM-001）与服务端约束层（DL-FLOW-REM-003）证明。 */
    expect(realResponse.status(), "真实 meta/datasets 必须 200").toBe(200);
    expect(realIds.length, "真实链元数据行数必须 <= UI 上限 6").toBeLessThanOrEqual(6);
    expect(realIds.length, "真实链元数据必须非空").toBeGreaterThan(0);
    const availableIds = realItems.filter((item) => item.status === "available").map((item) => item.id);
    const unavailableIds = realItems.filter((item) => item.status !== "available").map((item) => item.id);
    expect(availableIds.length, "真实链可用 Dataset（可选择）必须 <= 5：" + JSON.stringify(realItems.map((i) => [i.id, i.status]))).toBeLessThanOrEqual(5);
    expect(availableIds.length, "真实链必须有可用 Dataset（可选择）").toBeGreaterThan(0);

    /* EVM 链（bsc）交叉核对：同一接口对 bsc 返回的行数 <= 6，其中可用项 <= 5。 */
    const bscResponse = await page.request.get("/api/v2/meta/datasets?chain=bsc", { headers: { "X-Auth-Audience": "user" } });
    const bscBody = (await bscResponse.json()) as { data?: { items?: Array<{ id: string; status: string }> } };
    const bscItems = bscBody.data?.items ?? [];
    const bscIds = bscItems.map((item) => item.id);
    const bscAvailableIds = bscItems.filter((item) => item.status === "available").map((item) => item.id);

    /* 真实项逐项校验：available 必须可选择；非 available 必须整格禁用、进不了 selected */
    for (let index = 0; index < realItems.length; index += 1) {
      const box = boxes(page).nth(index);
      const item = realItems[index];
      if (item.status === "available") {
        if (!(await box.isChecked())) await box.click();
        await expect(box, "真实可用第 " + (index + 1) + " 项必须可选中").toBeChecked();
        await expect(cards(page).nth(index), "真实可用项卡片必须给出 data-selected").toHaveAttribute("data-selected", "true");
      } else {
        await expect(box, "非 available 的真实项必须禁用：" + item.id).toBeDisabled();
        await expect(cards(page).nth(index), "非 available 的真实项不得进入 selected").not.toHaveAttribute("data-selected", "true");
      }
    }
    await expect(datasetMeta(page)).toContainText("已选 " + availableIds.length + " / 上限 6");

    /* preview 请求体只包含真实支持的可用 Dataset */
    await previewAndCapture(page, previewBodies);
    const previewBody = JSON.parse(previewBodies[previewBodies.length - 1] || "{}") as { chain?: string; datasets?: string[]; reuse_policy?: string };
    const errorDialog = page.locator('[role="dialog"]');
    if (await errorDialog.first().isVisible().catch(() => false)) {
      const dismiss = errorDialog.first().getByRole("button", { name: /知道了|关闭/ });
      if (await dismiss.count()) await dismiss.first().click();
    }

    dump(testInfo, "dlflow-rem-002-real-api-dataset-limit.json", {
      layer: "real-api-dataset-whitelist",
      meta_url: chainScopedUrl,
      page_chain: pageChain,
      dataset_request_urls: datasetRequests,
      real_ids: realIds,
      real_count: realIds.length,
      real_items: realItems,
      dom_ids: domIds,
      available_ids: availableIds,
      unavailable_ids: unavailableIds,
      available_count: availableIds.length,
      unavailable_count: unavailableIds.length,
      evm_probe: { chain: "bsc", status: bscResponse.status(), ids: bscIds, items: bscItems, available_ids: bscAvailableIds },
      ui_selected: await page.locator(".dl-dataset-card[data-selected='true']").count(),
      preview_request_chain: previewBody.chain ?? null,
      preview_request_datasets: previewBody.datasets ?? null,
      preview_request_reuse_policy: previewBody.reuse_policy ?? null,
      login: loginMeta,
    });

    expect(bscResponse.status(), "bsc 白名单探测必须 200").toBe(200);
    expect(bscIds.length, "bsc 元数据行数不得超过 UI 上限 6").toBeLessThanOrEqual(6);
    expect(bscAvailableIds.length, "EVM 链（bsc）真实可选项数必须 <= 5").toBeLessThanOrEqual(5);
    expect(bscAvailableIds.length, "bsc 必须有可用 Dataset").toBeGreaterThan(0);
    expect(previewBody.datasets, "preview 必须只发送真实支持的可用 Dataset").toEqual(availableIds);
    expect(previewBody.datasets, "不得出现任何非真实 Dataset").not.toContain(FIXTURE_ONLY_DATASET_ID);
    expect(previewBody.reuse_policy, "preview 必须继续携带 prefer_local").toBe("prefer_local");
  });

  test("DL-FLOW-REM-003 服务端约束层：提交 7 个 Dataset 必须被明确拒绝且不创建任务", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "DLF_EMAIL/DLF_PASSWORD not provided");
    await login(page);
    const headers = await authHeaders(page);
    const body = {
      chain: CHAIN_PREFERENCE,
      addresses: [ADDRESS.toLowerCase()],
      datasets: FIXTURE_DATASET_IDS,
      range: { type: "full" },
      reuse_policy: "prefer_local",
      performance_mode: "safe",
    };
    const jobsBefore = await jobCount(page);

    const previewResponse = await page.request.post("/api/v2/downloads/preview", { data: body, headers });
    const previewStatus = previewResponse.status();
    const previewBody = (await previewResponse.json()) as Record<string, any>;

    const createResponse = await page.request.post("/api/v2/downloads", {
      data: body,
      headers: { ...headers, "Idempotency-Key": "dlflow-rem-003-seven-datasets-" + Date.now() },
    });
    const createStatus = createResponse.status();
    const createBody = (await createResponse.json()) as Record<string, any>;

    const jobsAfter = await jobCount(page);

    dump(testInfo, "dlflow-rem-003-server-dataset-limit.json", {
      layer: "server-constraint",
      submitted_datasets: FIXTURE_DATASET_IDS,
      preview: { status: previewStatus, body: previewBody },
      create: { status: createStatus, body: createBody },
      jobs_before: jobsBefore,
      jobs_after: jobsAfter,
      login: loginMeta,
    });

    expect(previewStatus, "preview 必须明确拒绝 7 个 Dataset（400）").toBe(400);
    expect(previewBody.success, "错误响应必须是 success=false").toBe(false);
    expect(previewBody.error?.code, "错误码").toBe("VALIDATION_INVALID_ARGUMENT");
    expect(String(previewBody.error?.message ?? ""), "错误信息必须点名 Dataset").toContain("Dataset");
    const fieldErrors = (previewBody.error?.field_errors ?? []) as Array<{ field: string; code: string; message: string }>;
    const datasetField = fieldErrors.find((item) => item.field === "datasets");
    expect(datasetField, "必须给出 datasets 字段级错误：" + JSON.stringify(fieldErrors)).toBeTruthy();
    expect(String(datasetField?.message ?? ""), "字段级错误必须说明 6 项上限").toContain("最多选择 6");
    expect(previewBody.data ?? null, "拒绝响应不得返回任何计划数据").toBeNull();

    expect(createStatus, "create 同样必须拒绝（400）").toBe(400);
    expect(createBody.success).toBe(false);
    expect(JSON.stringify(createBody), "拒绝响应不得包含 job_id（不得创建任务）").not.toContain("job_id");
    expect(jobsAfter, "拒绝后不得新增任务（jobs 计数 " + jobsBefore + " -> " + jobsAfter + "）").toBeLessThanOrEqual(jobsBefore);
  });
});
