import { expect, test, type Locator, type Page, type Response } from "@playwright/test";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

/**
 * R9 v4 — U-DL-01 /downloads/new（app-v2 用户控制台）CSV/XLSX 导入真实浏览器验收。
 *
 * v2 修正 v1 的真实缺陷（v1 与其失败运行原样保留，未修改）：
 *   D1 夹具生成用 XLSX.writeFile，SheetJS 的 ESM 构建未注入 fs(set_fs) → "cannot save file"，
 *      整套用例 ENOENT 失败；v2 用 XLSX.write(type:"buffer") + fs.writeFileSync。
 *   D2 R9-05 用 page.request POST 时未带 X-CSRF-Token / X-Auth-Audience，被服务端 403 拒绝
 *      （真实鉴权行为，不是产品缺陷）；v2 从浏览器 cookie 取 csrf 并带受众头。
 *
 * v3 修正 v2 的真实缺陷（v1/v2 文件与其运行证据原样保留，未修改）：
 *   D3 错的选择器：真实按钮文案是“预览 Coverage 与成本”（DownloadNewPage source），
 *      v2 等 “Coverage 预览” 直接把整轮拖到中断；v3 用真实文案。
 *   D4 响应解包：V2 接口是 envelope，job_id 在 body.data 里；直接读 body.job_id 会误报。
 *   D5 格式防线：v2 实测 45 字节垃圾 bad.xlsx 被 SheetJS 宽松嗅探当表格读入，
 *      浏览器弹“解析摘要”而不是“文件解析失败”。产品已改为扩展名+魔数+OOXML 条目+
 *      条目数/解压尺寸/压缩比校验（§23.7 plan 1852）；v3 新增 R9-07 真实负向。
 *
 * v4（《新建数据下载 Flowline 界面重构实施规范》落地后重跑，v4 原文件即本文件）：
 *   交互链路从"一个按钮串起 校验→预览→创建"改成两段：
 *     ① 文件导入 →「本地解析摘要」弹窗 →「提交服务端校验」→「服务端地址校验结果」→「使用服务端结果」；
 *     ② 「预览 Coverage 与成本」→ Coverage 弹窗 →「创建下载任务」→「确认创建下载任务？」→ 结果弹窗。
 *   业务断言（服务端接受/拒绝计数、被移除的案件/标签、preview/create 请求体、幂等、
 *   坏文件与压缩炸弹负向）逐条保留，未放宽。本地摘要 data-testid="file-parse-summary" 不变。
 *
 * 范围声明：R9-05 只证明 **API 幂等**（同 key 同 payload 不重复建任务），
 * 不等于“UI 多链第二组失败后重试保持已成功组”的回归——后者需要独立授权的故障注入夹具。
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

/** V2 接口是 envelope：真实业务字段在 body.data 下。 */
function unwrap(body: string | null | undefined): Record<string, unknown> {
  try {
    const parsed = JSON.parse(body ?? "{}");
    if (parsed && typeof parsed === "object" && "data" in parsed) {
      return ((parsed as { data: Record<string, unknown> }).data ?? {}) as Record<string, unknown>;
    }
    return (parsed ?? {}) as Record<string, unknown>;
  } catch {
    return {};
  }
}

function dump(testInfo: { project: { name: string } }, name: string, value: unknown) {
  if (!OUT) return;
  const dir = path.join(OUT, testInfo.project.name);
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, name), JSON.stringify(value, null, 2) + "\n", "utf8");
}

/** 与服务端真实约定一致：状态变更请求必须带 CSRF + 受众头（packages/api-client/client.ts）。 */
async function authHeaders(page: Page, audience = "user"): Promise<Record<string, string>> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === `cs_${audience}_session_csrf`)?.value ?? "";
  return { "Content-Type": "application/json", "X-CSRF-Token": csrf, "X-Auth-Audience": audience };
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
 * Flowline 重构后的两段交互（《新建数据下载 Flowline 界面重构实施规范》§11.2/§11.3）：
 *   ① 解析导入 / 文件导入 → 「本地解析摘要」/「服务端地址校验结果」弹窗 → 「使用服务端结果」
 *   ② 「预览 Coverage 与成本」→ Coverage 弹窗 → 「创建下载任务」→ 「确认创建下载任务？」→ 结果弹窗
 * 校验与预览不再由同一个按钮串起；本文件因此把两步拆成独立助手。
 */

/**
 * 链选择器仍是 .cs-chainselect（Ant Design Select）。
 * 选项列表是**虚拟滚动**：未过滤时 DOM 里只有前 8 项，靠后的链（例如 tron）永远等不到；
 * 展开后搜索框自动获得焦点，先按 slug 过滤（filterOption 同时匹配链名与 slug）再点选项。
 */
async function selectChain(page: Page, slug: string) {
  await expect(page.locator(".cs-chainselect"), "链选择器").not.toHaveClass(/ant-select-disabled/, { timeout: 30_000 });
  await page.locator(".cs-chainselect").click();
  await page.locator(".cs-chainselect input").first().fill(slug);
  await page.locator(".cs-select-dropdown-wizard .ant-select-item-option", { hasText: new RegExp(slug, "i") }).first().click();
  await page.keyboard.press("Escape");
}

/**
 * 范围类型是分段控件（Segmented，fieldset + radio）：点 <label> 本体。
 * 直接点里面的 <span> 会被覆盖其上的 radio input 拦截（元素的命中目标必须是目标本身或后代）。
 */
async function selectRange(page: Page, label: string) {
  await page.locator('#download-range-type label:has(input[name="download-range-type"])').filter({ hasText: label }).click();
}

const dialog = (page: Page) => page.locator('[role="dialog"]');
const PREVIEW_TITLE = /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/;

/**
 * 文件导入的第 ① 段：选文件 → 「本地解析摘要」弹窗。
 * 摘要本体 data-testid="file-parse-summary" 仍挂在同一个 <dl> 上，只是现在位于弹窗内。
 */
async function openFileSummary(page: Page, file: string) {
  await importFile(page, file);
  const summaryDialog = dialog(page).filter({ hasText: "本地解析摘要" });
  await expect(summaryDialog, "本地解析摘要弹窗").toBeVisible({ timeout: 30_000 });
  const summary = summaryDialog.locator('[data-testid="file-parse-summary"]');
  await expect(summary, "本地解析摘要数据行").toBeVisible({ timeout: 30_000 });
  return { summaryDialog, summary };
}

/** 摘要弹窗 → 「提交服务端校验」→ 「服务端地址校验结果」弹窗。 */
async function submitToServerValidation(page: Page, summaryDialog: Locator) {
  await summaryDialog.getByRole("button", { name: "提交服务端校验" }).click();
  const serverDialog = dialog(page).filter({ hasText: "服务端地址校验结果" });
  await expect(serverDialog, "服务端地址校验结果弹窗").toBeVisible({ timeout: 60_000 });
  return serverDialog;
}

/** 点「使用服务端结果」采用服务端 accepted 的规范化地址（唯一权威集合）。 */
async function useServerVerdict(page: Page, serverDialog: Locator) {
  await serverDialog.getByRole("button", { name: "使用服务端结果" }).click();
  await expect(serverDialog).toBeHidden({ timeout: 20_000 });
}

/** 第 ② 段预览：点「预览 Coverage 与成本」→ 等真实 preview 响应 → Coverage 弹窗。 */
async function previewCoverage(page: Page) {
  const responsePromise = page.waitForResponse(
    (response) => response.url().includes("/api/v2/downloads/preview") && response.request().method() === "POST",
    { timeout: 120_000 },
  );
  await page.getByRole("button", { name: "预览 Coverage 与成本" }).first().click();
  const response = await responsePromise;
  expect(response.status(), "preview 必须成功").toBeLessThan(300);
  const previewDialog = dialog(page).filter({ hasText: PREVIEW_TITLE });
  await expect(previewDialog, "Coverage 预览弹窗").toBeVisible({ timeout: 90_000 });
  return { previewDialog, response };
}

/** 第 ② 段创建：点「创建下载任务」→ 「确认创建下载任务？」→ 结果弹窗。 */
async function createAndConfirm(page: Page) {
  await page.getByRole("button", { name: "创建下载任务" }).first().click();
  const confirmDialog = dialog(page).filter({ hasText: "确认创建下载任务？" });
  await expect(confirmDialog, "创建确认弹窗").toBeVisible({ timeout: 30_000 });
  await confirmDialog.getByRole("button", { name: "确认创建" }).click();
  const resultDialog = dialog(page).filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ });
  await expect(resultDialog, "创建结果弹窗").toBeVisible({ timeout: 90_000 });
  return resultDialog;
}

test.describe("R9 U-DL-01 CSV/XLSX import (app-v2)", () => {
  test("R9-01 最小 .xlsx 导入：浏览器摘要 → 服务端再次验证 → 未解析案件/标签被移除 → 真实创建", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R9_EMAIL/R9_PASSWORD/R9_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page);
    await openDownloadsNew(page);
    /* 校验是**按链作用域**的（chain + normalized address 才是去重键），且入参显式带 chain：
       链必须在提交校验之前选定（旧链路由「预览并创建任务」一键串起，新链路拆成两段）。 */
    await selectChain(page, CHAIN);

    // 1) 本地解析摘要（数据行在「本地解析摘要」弹窗里）
    const { summaryDialog, summary } = await openFileSummary(page, path.join(FIX, "min.xlsx"));
    const localText = await summary.innerText();
    expect(localText, "本地摘要必须报出 chain 列与案件/标签列被识别").toContain("chain");
    expect(localText, "本地摘要必须报出 case 列").toContain("case");
    expect(localText, "本地摘要必须报出 tags 列").toContain("tags");

    // 2) 服务端再次验证（「提交服务端校验」→ 「服务端地址校验结果」）
    const serverDialog = await submitToServerValidation(page, summaryDialog);
    const serverText = await serverDialog.innerText();
    expect(serverText, "服务端必须返回接受/拒绝计数").toContain("服务端接受");
    expect(serverText, "不存在的案件必须被明确标注为已移除，而不是只写不发生绑定").toContain("已从本次提交中移除");
    expect(serverText, "必须列出不存在的案件名").toContain(MISSING_CASE);
    expect(serverText, "必须列出不存在的标签名").toContain(MISSING_TAG);
    expect(serverText, "必须说明名称已从 payload 中移除").toContain("已从创建请求的 payload 中移除");
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "01-server-verdict.png"), fullPage: false });
    await useServerVerdict(page, serverDialog);

    // 3) 预览（真实服务端 Coverage）：独立按钮，独立弹窗
    const { previewDialog } = await previewCoverage(page);
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "02-preview.png"), fullPage: false });
    await previewDialog.getByRole("button", { name: "知道了" }).click();

    // 4) 真实创建：「创建下载任务」→「确认创建下载任务？」→ 结果弹窗
    const resultDialog = await createAndConfirm(page);
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
    const createResponse = unwrap(createCalls[0].responseBody);
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
    await selectChain(page, CHAIN);
    const { summaryDialog, summary } = await openFileSummary(page, path.join(FIX, "large.xlsx"));
    const localText = await summary.innerText();
    expect(localText, "本地摘要必须报出超上限行数").toContain("超出单次上限");

    const serverDialog = await submitToServerValidation(page, summaryDialog);
    const serverText = await serverDialog.innerText();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "05-large-file.png"), fullPage: false });
    const validateCall = net.filter((r) => r.url.includes("validate-addresses")).pop();
    const verdict = unwrap(validateCall?.responseBody);
    dump(testInfo, "r9-03-evidence.json", {
      local_summary_text: localText, server_verdict_text: serverText,
      server_summary: verdict.summary, cap_rejected_sample: (verdict.rejected ?? []).slice(0, 3),
    });
    // 浏览器本地按上限截断到 100 条后才提交，因此服务端看到的就是 100 条；
    // 服务端自己的上限由下面同一用例的 API 直连负向证明（两层都要有真实证据）。
    expect(verdict.summary?.accepted, "浏览器提交的 100 条必须全部被接受").toBe(100);
    expect(verdict.summary?.submitted, "服务端实际收到的条数").toBe(100);
    expect(serverText, "Dialog 必须显示服务端接受/拒绝计数").toContain("服务端接受");
    // 服务端上限的独立负向：直接提交 150 条（绕过浏览器截断）
    const rawItems = Array.from({ length: 150 }, (_, i) => ({ address: "0x" + String(i + 1).padStart(40, "0"), chain: CHAIN, case: "", tags: [] as string[] }));
    const direct = await page.request.post("/api/v2/downloads/validate-addresses", {
      data: { chain: CHAIN, items: rawItems, create_missing_meta: false },
      headers: await authHeaders(page),
    });
    const directBody = unwrap(await direct.text());
    const directSummary = (directBody.summary ?? {}) as Record<string, number>;
    dump(testInfo, "r9-03-direct-api.json", { status: direct.status(), summary: directSummary });
    expect(direct.status(), "direct validate").toBe(200);
    expect(directSummary.accepted, "服务端直连也必须只接受 100 条").toBe(100);
    expect(directSummary.cap_rejected, "服务端必须显式拒绝其余 50 条").toBe(50);
    await serverDialog.getByRole("button", { name: "取消" }).click();
    await expect(serverDialog).toBeHidden({ timeout: 20_000 });
    expect(net.filter((r) => r.url.includes("/api/v2/downloads/preview")).length, "取消后不得预览").toBe(0);
  });

  test("R9-04 混合文件：合法/非法/重复 → 服务端判定与最终 payload 只含 accepted", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page);
    await openDownloadsNew(page);
    await selectChain(page, CHAIN);
    const { summaryDialog } = await openFileSummary(page, path.join(FIX, "mixed.xlsx"));
    const serverDialog = await submitToServerValidation(page, summaryDialog);
    const serverText = await serverDialog.innerText();
    const verdict = unwrap(net.filter((r) => r.url.includes("validate-addresses")).pop()?.responseBody);
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "06-mixed.png"), fullPage: false });
    await useServerVerdict(page, serverDialog);

    const { previewDialog } = await previewCoverage(page);
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
    const headers = { ...(await authHeaders(page)), "Idempotency-Key": key };
    const first = await page.request.post("/api/v2/downloads", { data: payload, headers });
    const firstBody = unwrap(await first.text());
    const second = await page.request.post("/api/v2/downloads", { data: payload, headers });
    const secondBody = unwrap(await second.text());
    dump(testInfo, "r9-05-evidence.json", {
      first: { status: first.status(), body: firstBody }, second: { status: second.status(), body: secondBody }, payload, key,
    });
    expect(first.status(), "first create").toBeLessThan(300);
    expect(second.status(), "second create").toBeLessThan(300);
    expect(firstBody.job_id, "first create must return a job id").toBeTruthy();
    expect(secondBody.job_id, "retry must reuse the same job, not create a new one").toBe(firstBody.job_id);
    const jobs = await page.request.get("/api/v2/jobs?limit=100", { headers: { "X-Auth-Audience": "user" } });
    const jobsBody = unwrap(await jobs.text()) as { items?: Array<{ id: string }> };
    const matching = (jobsBody.items ?? []).filter((j) => j.id === firstBody.job_id);
    expect(matching.length, "the reused job must exist exactly once in the job list").toBe(1);
  });

  test("R9-07 格式防线：改名文件与三个压缩炸弹必须逐条命中各自的安全分支（真实上限），且不发出任何请求", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    /*
     * 每个夹具断言**自己的**安全分支（§11.3 / §11.6），不是笼统的"被拒绝"：
     *   renamed     → 扩展名/魔数分支（不是 ZIP/OOXML）
     *   bomb-size   → 中央目录声明解压总大小分支（> 64MiB）
     *   bomb-ratio  → 中央目录声明压缩比分支（> 200）
     *   bomb-lie    → 真实 deflate 解压分支（声明值不可信 / 超过剩余预算）
     * 三个炸弹都带齐 OOXML 必需条目（[Content_Types].xml + xl/workbook.xml），
     * 恶意内容在 xl/worksheets/sheet1.xml —— 由 frontend/scripts/r9-make-fixtures-v4.mjs
     * 生成并在生成时做机器自检（r9-07-fixture-selfcheck.json）。
     * 生产校验顺序一行未改；这里只证明每个分支真实可达。
     */
    const cases: Array<{ file: string; expectAll: string[]; why: string }> = [
      { file: "renamed.xlsx", expectAll: ["不是 ZIP/OOXML"], why: "改名文件：扩展名与真实魔数不符" },
      { file: "bomb-size.xlsx", expectAll: ["声明解压后总大小", "超过上限"], why: "声明解压总大小 > 64MiB 分支" },
      { file: "bomb-ratio.xlsx", expectAll: ["声明压缩比", "超过上限"], why: "声明压缩比 > 200 分支" },
      { file: "bomb-lie.xlsx", expectAll: ["真实解压", "声明值不可信"], why: "真实 deflate 解压超出剩余预算分支" },
    ];
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(String(error).slice(0, 400)));
    const observed: Array<{ file: string; why: string; expect_all: string[]; reason: string; all_matched: boolean; sha256: string; bytes: number; summaryVisible: boolean; validateCalls: number; previewCalls: number; createCalls: number; totalApiCalls: number; pageErrors: string[] }> = [];
    await login(page);
    for (const item of cases) {
      const net: NetRecord[] = [];
      startCapture(page, net);
      await openDownloadsNew(page);
      const filePath = path.join(FIX, item.file);
      const fixtureBytes = readFileSync(filePath);
      await importFile(page, filePath);
      const errDialog = dialog(page).filter({ hasText: "文件解析失败" });
      await expect(errDialog, item.file + " 必须走文件解析失败 Dialog").toBeVisible({ timeout: 60_000 });
      const text = await errDialog.innerText();
      await page.screenshot({ path: path.join(OUT, testInfo.project.name, "09-guard-" + item.file + ".png"), fullPage: false });
      /* 安全分支命中后不得再弹"本地解析摘要"（summary 是解析**成功**才会出现的数据行）：
         这同时证明 SheetJS 没有被用来宽松嗅探这些文件。 */
      const summaryVisible = await page.getByTestId("local-parse-summary").isVisible().catch(() => false);
      await errDialog.getByRole("button", { name: "知道了" }).click();
      const validateCalls = net.filter((r) => r.url.includes("validate-addresses")).length;
      const previewCalls = net.filter((r) => r.url.includes("/api/v2/downloads/preview")).length;
      const createCalls = net.filter((r) => /\/api\/v2\/downloads$/.test(r.url)).length;
      const matched = item.expectAll.filter((needle) => text.includes(needle));
      observed.push({
        file: item.file, why: item.why, expect_all: item.expectAll, reason: text,
        all_matched: matched.length === item.expectAll.length,
        sha256: createHash("sha256").update(fixtureBytes).digest("hex"), bytes: fixtureBytes.length,
        summaryVisible, validateCalls, previewCalls, createCalls,
        totalApiCalls: net.length, pageErrors: pageErrors.slice(),
      });
      for (const needle of item.expectAll) {
        expect(text, item.file + " 的拒绝原因必须命中「" + needle + "」（" + item.why + "）：" + text).toContain(needle);
      }
      expect(text, item.file + " 的拒绝原因必须点名文件").toContain(item.file);
      expect(summaryVisible, item.file + " 不得出现本地解析摘要（说明它进了 SheetJS 解析路径）").toBe(false);
      expect(validateCalls, item.file + " 不得触发服务端校验").toBe(0);
      expect(previewCalls, item.file + " 不得触发预览").toBe(0);
      expect(createCalls, item.file + " 不得触发创建").toBe(0);
      expect(pageErrors, item.file + " 不得产生页面异常").toEqual([]);
    }
    dump(testInfo, "r9-07-evidence.json", {
      cases: observed,
      pageErrors,
      limits: { maxFileBytes: 16 * 1024 * 1024, maxZipEntries: 512, maxUncompressedBytes: 64 * 1024 * 1024, maxCompressionRatio: 200 },
      fixture_self_check: path.join(FIX, "r9-07-fixture-selfcheck.json"),
    });
    expect(pageErrors, "四个夹具全轮不得产生页面异常").toEqual([]);
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
    /* 移动端（<640px）非当前步骤折叠成摘要卡：链选择器在第 ② 步折叠态里不渲染。
       本用例只验证导入弹窗的可读性与断点溢出，链由页面按服务端元数据自动预选，
       因此不再点击 .cs-chainselect（旧链路的「预览并创建任务」已拆成两段）。 */
    const { summaryDialog, summary } = await openFileSummary(page, path.join(FIX, "min.csv"));
    const box = await summary.boundingBox();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "07-mobile-import.png"), fullPage: true });
    const serverDialog = await submitToServerValidation(page, summaryDialog);
    const serverBox = await serverDialog.boundingBox();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "08-mobile-verdict.png"), fullPage: true });
    dump(testInfo, "r9-06-evidence.json", { viewport: vp, document_overflow: overflow, local_dialog_box: box, server_dialog_box: serverBox, api_calls: net.map((c) => ({ method: c.method, url: c.url, status: c.status })) });
    expect(vp?.width, "mobile project viewport").toBe(390);
    expect(overflow.scrollWidth, "page must not overflow horizontally at 390px").toBeLessThanOrEqual(overflow.clientWidth + 1);
    expect(box?.width ?? 0, "local dialog must fit the 390px viewport").toBeLessThanOrEqual(390);
    expect(serverBox?.width ?? 0, "server verdict dialog must fit the 390px viewport").toBeLessThanOrEqual(390);
  });
});