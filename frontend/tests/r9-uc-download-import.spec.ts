import { expect, test, type Page, type Request, type Response } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";

/**
 * R9 — U-DL-01 /downloads/new（app-v2 用户控制台）CSV/XLSX 导入真实浏览器验收。
 *
 * 覆盖契约 docs/dual-system-development-plan.md 1042-1043：
 *   "地址支持粘贴和 CSV/XLSX 导入；导入先在浏览器显示解析摘要，服务端再次验证。"
 *   "每个地址可单独选择链；重复判断键为 chain + normalized address。"
 *
 * 断言绑定真实 HTTP：localhost/API 请求体、DB 由 runner 另行核对；本文件只断言
 * 浏览器实际发出的请求与页面上真实出现的 Dialog 文案，不做任何 mock。
 */

const OUT = process.env.R9_OUT ?? "";
const EMAIL = process.env.R9_EMAIL ?? "";
const PASSWORD = process.env.R9_PASSWORD ?? "";
const ADDRESS = process.env.R9_ADDRESS ?? "";
const CHAIN = process.env.R9_CHAIN ?? "bsc";
const FIX = process.env.R9_FIXTURES ?? "";
const MISSING_CASE = process.env.R9_MISSING_CASE ?? "R9-MISSING-CASE";
const MISSING_TAG = process.env.R9_MISSING_TAG ?? "R9-MISSING-TAG";
const LOGIN_PATH = process.env.R9_LOGIN_PATH ?? "/app-v2/login";

test.use({ ignoreHTTPSErrors: true });

type NetRecord = { method: string; url: string; status: number; requestId: string | null; traceId: string | null; requestBody?: string | null; responseBody?: string | null };

function startCapture(page: Page, sink: NetRecord[]) {
  page.on("response", (response: Response) => {
    void (async () => {
      const url = response.url();
      if (!url.includes("/api/")) return;
      const request = response.request();
      let requestBody: string | null = null;
      try { requestBody = request.postData(); } catch { requestBody = null; }
      let responseBody: string | null = null;
      try { responseBody = (await response.text()).slice(0, 20000); } catch { responseBody = null; }
      sink.push({
        method: request.method(), url, status: response.status(),
        requestId: response.headers()["x-request-id"] ?? null,
        traceId: response.headers()["x-trace-id"] ?? null,
        requestBody, responseBody,
      });
    })();
  });
}

function dump(testInfo: { project: { name: string } }, name: string, value: unknown) {
  if (!OUT) return;
  const dir = path.join(OUT, testInfo.project.name);
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, name), JSON.stringify(value, null, 2) + "\n", "utf8");
}

async function login(page: Page) {
  await page.goto(LOGIN_PATH, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#identifier"), "login identifier").toBeVisible({ timeout: 30_000 });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect.poll(() => page.url(), { timeout: 30_000 }).not.toContain(LOGIN_PATH);
}

async function openDownloadsNew(page: Page) {
  await page.goto("/app-v2/downloads/new", { waitUntil: "domcontentloaded" });
  await expect(page.locator("#download-addresses"), "address textarea").toBeVisible({ timeout: 30_000 });
  // 链与 Dataset 来自服务端 registry；链选择器在第 ② 步（Ant Select）才渲染，
  // 这里只等地址输入框就绪，展开下拉见 selectChain()。
}

async function importFile(page: Page, file: string) {
  await page.locator('input[aria-label="导入地址文件"]').setInputFiles(file);
}

/**
 * 新建数据下载是**单页四区**版面（效果图）：
 *   ① 地址与对象 ② 链与范围 ③ 数据类型 ④ 预览与创建 同屏可见；
 * 正文一次只渲染当前步骤，底部操作栏固定在工作区底部（上一步 / 下一步 / 创建任务）。
 */
/** 范围类型是紧凑分段控件（Segmented），按可见名称点选即可。 */
async function selectRangeSegment(page: Page, label: string) {
  await page.locator("#download-range-type").getByText(label, { exact: true }).click();
}

/** 链选择器是第 ② 步的 Ant Design Select：点 .ant-select 根节点展开，再点可见选项。 */
async function selectChain(page: Page, slug: string) {
  await page.locator(".cs-chainselect").click();
  await page.locator(".cs-select-dropdown-wizard .ant-select-item-option", { hasText: new RegExp(slug, "i") }).first().click();
}

/** 范围类型是紧凑分段控件（Segmented）：按可见名称点选。 */
async function selectRange(page: Page, label: string) {
  await page.locator("#download-range-type").getByText(label, { exact: true }).click();
}

const dialog = (page: Page) => page.locator('[role="dialog"]');

test.describe("R9 U-DL-01 CSV/XLSX import (app-v2)", () => {
  test("R9-01 最小 .xlsx 导入：浏览器摘要 → 服务端再次验证 → 未解析案件/标签被移除 → 真实创建", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R9_EMAIL/R9_PASSWORD/R9_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page);
    await openDownloadsNew(page);
    await importFile(page, path.join(FIX, "min.xlsx"));

    // 1) 浏览器解析摘要（本地）
    const fileSummary = page.locator('[data-testid="file-parse-summary"]');
    await expect(fileSummary).toBeVisible({ timeout: 30_000 });
    const localText = await fileSummary.innerText();
    expect(localText, "本地摘要必须报出 chain 列与案件/标签列被识别").toContain("chain");
    expect(localText, "本地摘要必须报出 case 列").toContain("case");
    expect(localText, "本地摘要必须报出 tags 列").toContain("tags");
    /* 规范 §8.4：本地解析数据以内联数据行呈现，不再弹摘要窗；
       服务端校验在第 ② 步出口执行（validate-addresses 的入参含 chain）。 */
    /* 单屏工作台：三块同屏，链/范围直接选，右栏主按钮一次串起 校验 → 预览 → 创建确认。 */
    await selectChain(page, CHAIN);
    await page.getByRole("button", { name: "预览并创建任务" }).click();

    // 2) 服务端再次验证
    const serverDialog = dialog(page).filter({ hasText: "服务端地址校验结果" });
    await expect(serverDialog).toBeVisible({ timeout: 30_000 });
    const serverText = await serverDialog.innerText();
    expect(serverText, "服务端必须返回接受/拒绝计数").toContain("服务端接受");
    expect(serverText, "不存在的案件必须被明确标注为已移除，而不是只写不发生绑定").toContain("已从本次提交中移除");
    expect(serverText, "必须列出不存在的案件名").toContain(MISSING_CASE);
    expect(serverText, "必须列出不存在的标签名").toContain(MISSING_TAG);
    expect(serverText, "必须说明名称已从 payload 中移除").toContain("已从创建请求的 payload 中移除");
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "01-server-verdict.png"), fullPage: false });
    await page.getByRole("button", { name: "使用服务端结果" }).click();

    // 3) 预览（真实服务端 Coverage）
    const previewDialog = dialog(page).filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ });
    await expect(previewDialog).toBeVisible({ timeout: 60_000 });
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "02-preview.png"), fullPage: false });
    await previewDialog.getByRole("button", { name: "知道了" }).click();

    // 4) 真实创建：预览弹窗关闭后，主按钮会继续带出创建确认弹窗
    const confirmDialog = dialog(page).filter({ hasText: "确认创建下载任务？" });
    await expect(confirmDialog).toBeVisible({ timeout: 30_000 });
    await page.getByRole("button", { name: "确认创建" }).click();
    const resultDialog = dialog(page).filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ });
    await expect(resultDialog).toBeVisible({ timeout: 90_000 });
    const resultText = await resultDialog.innerText();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "03-created.png"), fullPage: false });

    // 5) 断言真实请求体：只提交服务端 accepted 的 normalized 地址，且不带未解析元数据
    const previewCalls = net.filter((r) => r.url.includes("/api/v2/downloads/preview") && r.method === "POST");
    const createCalls = net.filter((r) => /\/api\/v2\/downloads$/.test(r.url) && r.method === "POST");
    expect(previewCalls.length, "必须真实调用过 preview").toBeGreaterThan(0);
    expect(createCalls.length, "必须真实调用过 create").toBe(1);
    const previewBody = JSON.parse(previewCalls[previewCalls.length - 1].requestBody ?? "{}");
    const createBody = JSON.parse(createCalls[0].requestBody ?? "{}");
    expect(previewBody.chain).toBe(CHAIN);
    expect(previewBody.addresses).toEqual([ADDRESS.toLowerCase()]);
    expect(Object.keys(previewBody.subject_meta ?? {}), "未解析的案件/标签不得出现在 payload 中").toEqual([]);
    expect(createBody.addresses).toEqual([ADDRESS.toLowerCase()]);
    expect(createBody.subject_meta ?? {}, "create 也不得携带未解析元数据").toEqual({});
    const createResponse = JSON.parse(createCalls[0].responseBody ?? "{}");
    dump(testInfo, "r9-01-evidence.json", {
      local_summary_text: localText, server_verdict_text: serverText, result_text: resultText,
      preview_request_body: previewBody, create_request_body: createBody, create_response: createResponse,
      preview_trace_ids: previewCalls.map((c) => ({ request_id: c.requestId, trace_id: c.traceId, status: c.status })),
      create_trace_ids: createCalls.map((c) => ({ request_id: c.requestId, trace_id: c.traceId, status: c.status })),
      all_api_calls: net.map((c) => ({ method: c.method, url: c.url, status: c.status, request_id: c.requestId, trace_id: c.traceId })),
    });
    expect(createCalls[0].status, "create 必须 200/201/202").toBeLessThan(300);
  });

  test("R9-02 坏文件：解析失败必须走 Dialog，且不得发出任何校验/创建请求", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page);
    await openDownloadsNew(page);
    await importFile(page, path.join(FIX, "bad.xlsx"));

    const errDialog = dialog(page).filter({ hasText: "文件解析失败" });
    await expect(errDialog).toBeVisible({ timeout: 30_000 });
    const text = await errDialog.innerText();
    expect(text).toContain("bad.xlsx");
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "04-bad-file.png"), fullPage: false });
    await errDialog.getByRole("button", { name: "知道了" }).click();

    const validateCalls = net.filter((r) => r.url.includes("validate-addresses"));
    const createCalls = net.filter((r) => r.url.includes("/api/v2/downloads"));
    dump(testInfo, "r9-02-evidence.json", { dialog_text: text, validate_calls: validateCalls.length, create_calls: createCalls.length, all_api_calls: net.map((c) => ({ method: c.method, url: c.url, status: c.status })) });
    expect(validateCalls.length, "坏文件不得触发服务端校验").toBe(0);
    expect(createCalls.length, "坏文件不得触发创建").toBe(0);
  });

  test("R9-03 大文件 150 行：上限必须显式报出（接受 100 / 拒绝 50），取消后不得预览", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page);
    await openDownloadsNew(page);
    await importFile(page, path.join(FIX, "large.xlsx"));

    const fileSummary = page.locator('[data-testid="file-parse-summary"]');
    await expect(fileSummary).toBeVisible({ timeout: 30_000 });
    const localText = await fileSummary.innerText();
    expect(localText, "本地摘要必须报出超上限行数").toContain("超出单次上限");
    /* 规范 §8.4：本地解析数据以内联数据行呈现，不再弹摘要窗；
       服务端校验在第 ② 步出口执行（validate-addresses 的入参含 chain）。 */
    /* 单屏工作台：三块同屏，链/范围直接选，右栏主按钮一次串起 校验 → 预览 → 创建确认。 */
    await selectChain(page, CHAIN);
    await page.getByRole("button", { name: "预览并创建任务" }).click();

    const serverDialog = dialog(page).filter({ hasText: "服务端地址校验结果" });
    await expect(serverDialog).toBeVisible({ timeout: 60_000 });
    const serverText = await serverDialog.innerText();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "05-large-file.png"), fullPage: false });
    const validateCall = net.filter((r) => r.url.includes("validate-addresses")).pop();
    const verdict = JSON.parse(validateCall?.responseBody ?? "{}");
    dump(testInfo, "r9-03-evidence.json", {
      local_summary_text: localText, server_verdict_text: serverText,
      server_summary: verdict.summary, cap_rejected_sample: (verdict.rejected ?? []).slice(0, 3),
    });
    expect(verdict.summary?.accepted, "服务端必须恰好接受 100 条").toBe(100);
    expect(verdict.summary?.cap_rejected, "服务端必须显式拒绝其余 50 条").toBe(50);
    expect(serverText, "Dialog 必须显示被拒 50 条").toContain("服务端拒绝");
    await page.getByRole("button", { name: "取消" }).click();
    await expect(serverDialog).toBeHidden();
    expect(net.filter((r) => r.url.includes("/api/v2/downloads/preview")).length, "取消后不得预览").toBe(0);
  });

  test("R9-04 混合文件：合法/非法/重复 → 服务端判定与最终 payload 只含 accepted", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page);
    await openDownloadsNew(page);
    await importFile(page, path.join(FIX, "mixed.xlsx"));

    const fileSummary = page.locator('[data-testid="file-parse-summary"]');
    await expect(fileSummary).toBeVisible({ timeout: 30_000 });
    /* 规范 §8.4：本地解析数据以内联数据行呈现，不再弹摘要窗；
       服务端校验在第 ② 步出口执行（validate-addresses 的入参含 chain）。 */
    /* 单屏工作台：三块同屏，链/范围直接选，右栏主按钮一次串起 校验 → 预览 → 创建确认。 */
    await selectChain(page, CHAIN);
    await page.getByRole("button", { name: "预览并创建任务" }).click();
    const serverDialog = dialog(page).filter({ hasText: "服务端地址校验结果" });
    await expect(serverDialog).toBeVisible({ timeout: 60_000 });
    const serverText = await serverDialog.innerText();
    const verdict = JSON.parse(net.filter((r) => r.url.includes("validate-addresses")).pop()?.responseBody ?? "{}");
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "06-mixed.png"), fullPage: false });
    await page.getByRole("button", { name: "使用服务端结果" }).click();

    const previewDialog = dialog(page).filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ });
    await expect(previewDialog).toBeVisible({ timeout: 60_000 });
    await previewDialog.getByRole("button", { name: "知道了" }).click();

    const previewCall = net.filter((r) => r.url.includes("/api/v2/downloads/preview")).pop();
    const previewBody = JSON.parse(previewCall?.requestBody ?? "{}");
    const codes = (verdict.rejected ?? []).map((r: { code: string }) => r.code).sort();
    dump(testInfo, "r9-04-evidence.json", {
      server_verdict_text: serverText, server_summary: verdict.summary,
      rejected_codes: codes, preview_request_body: previewBody,
    });
    expect(verdict.summary?.accepted, "混合文件只应有 1 条 accepted").toBe(1);
    expect(codes, "必须显式拒绝非法地址与重复地址").toEqual(["DUPLICATE_ADDRESS", "INVALID_ADDRESS"]);
    expect(previewBody.addresses, "最终 payload 只能包含服务端 accepted 的 normalized 地址").toEqual([ADDRESS.toLowerCase()]);
  });

  test("R9-05 重试不重复：同一幂等键 + 同一 payload 连续创建，必须复用同一任务", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    await login(page);
    const payload = { chain: CHAIN, addresses: [ADDRESS.toLowerCase()], datasets: ["transactions"], range: { type: "full" }, reuse_policy: "prefer_local", performance_mode: "safe" };
    const key = "r9-retry-stable-key-0001";
    const first = await page.request.post("/api/v2/downloads", { data: payload, headers: { "Idempotency-Key": key, "Content-Type": "application/json" } });
    const firstBody = await first.json().catch(() => ({}));
    const second = await page.request.post("/api/v2/downloads", { data: payload, headers: { "Idempotency-Key": key, "Content-Type": "application/json" } });
    const secondBody = await second.json().catch(() => ({}));
    dump(testInfo, "r9-05-evidence.json", {
      first: { status: first.status(), body: firstBody }, second: { status: second.status(), body: secondBody }, payload, key,
    });
    expect(first.status(), "first create").toBeLessThan(300);
    expect(second.status(), "second create").toBeLessThan(300);
    expect(firstBody.job_id, "first create must return a job id").toBeTruthy();
    expect(secondBody.job_id, "retry must reuse the same job, not create a new one").toBe(firstBody.job_id);
    const jobs = await page.request.get("/api/v2/jobs?limit=100", { headers: { "X-Auth-Audience": "user" } });
    const jobsBody = await jobs.json().catch(() => ({ items: [] }));
    const matching = (jobsBody.items ?? []).filter((j: { id: string }) => j.id === firstBody.job_id);
    expect(matching.length, "the reused job must exist exactly once in the job list").toBe(1);
  });

  test("R9-06 手机断点：导入 Dialog 在 390x844 可读且无横向溢出", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-mobile", "mobile-only assertion set");
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page);
    await openDownloadsNew(page);
    const vp = page.viewportSize();
    const overflow = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth }));
    await importFile(page, path.join(FIX, "min.csv"));
    const fileSummary = page.locator('[data-testid="file-parse-summary"]');
    await expect(fileSummary).toBeVisible({ timeout: 30_000 });
    const box = await fileSummary.boundingBox();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "07-mobile-import.png"), fullPage: true });
    /* 规范 §8.4：本地解析数据以内联数据行呈现，不再弹摘要窗；
       服务端校验在第 ② 步出口执行（validate-addresses 的入参含 chain）。 */
    /* 单屏工作台：三块同屏，链/范围直接选，右栏主按钮一次串起 校验 → 预览 → 创建确认。 */
    await selectChain(page, CHAIN);
    await page.getByRole("button", { name: "预览并创建任务" }).click();
    const serverDialog = dialog(page).filter({ hasText: "服务端地址校验结果" });
    await expect(serverDialog).toBeVisible({ timeout: 60_000 });
    const serverBox = await serverDialog.boundingBox();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "08-mobile-verdict.png"), fullPage: true });
    dump(testInfo, "r9-06-evidence.json", { viewport: vp, document_overflow: overflow, local_dialog_box: box, server_dialog_box: serverBox, api_calls: net.map((c) => ({ method: c.method, url: c.url, status: c.status })) });
    expect(vp?.width, "mobile project viewport").toBe(390);
    expect(overflow.scrollWidth, "page must not overflow horizontally at 390px").toBeLessThanOrEqual(overflow.clientWidth + 1);
    expect(box?.width ?? 0, "local dialog must fit the 390px viewport").toBeLessThanOrEqual(390);
    expect(serverBox?.width ?? 0, "server verdict dialog must fit the 390px viewport").toBeLessThanOrEqual(390);
  });
});
