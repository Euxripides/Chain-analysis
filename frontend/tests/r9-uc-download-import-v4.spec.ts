import { expect, test, type Page, type Request, type Response } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
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
  // 链与 Dataset 来自服务端 registry，等它们加载完成再操作。
  await expect.poll(async () => page.locator("#download-chain option").count(), { timeout: 30_000 }).toBeGreaterThan(1);
}

async function importFile(page: Page, file: string) {
  await page.locator('input[aria-label="导入地址文件"]').setInputFiles(file);
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
    await page.locator("#download-chain").selectOption(CHAIN);
    await importFile(page, path.join(FIX, "min.xlsx"));

    // 1) 浏览器解析摘要（本地）
    const localDialog = dialog(page).filter({ hasText: "地址导入解析摘要（浏览器）" });
    await expect(localDialog).toBeVisible({ timeout: 30_000 });
    const localText = await localDialog.innerText();
    expect(localText, "本地摘要必须报出 chain 列与案件/标签列被识别").toContain("chain");
    expect(localText, "本地摘要必须报出 case 列").toContain("case");
    expect(localText, "本地摘要必须报出 tags 列").toContain("tags");
    await page.getByRole("button", { name: "提交服务端校验" }).click();

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
    await page.getByRole("button", { name: "预览 Coverage 与成本" }).click();
    const previewDialog = dialog(page).filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ });
    await expect(previewDialog).toBeVisible({ timeout: 60_000 });
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "02-preview.png"), fullPage: false });
    await previewDialog.getByRole("button", { name: "知道了" }).click();

    // 4) 真实创建
    await page.getByRole("button", { name: /创建下载任务|正在创建/ }).click();
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
    await page.locator("#download-chain").selectOption(CHAIN);
    await importFile(page, path.join(FIX, "large.xlsx"));

    const localDialog = dialog(page).filter({ hasText: "地址导入解析摘要（浏览器）" });
    await expect(localDialog).toBeVisible({ timeout: 30_000 });
    const localText = await localDialog.innerText();
    expect(localText, "本地摘要必须报出超上限行数").toContain("超出单次上限");
    await page.getByRole("button", { name: "提交服务端校验" }).click();

    const serverDialog = dialog(page).filter({ hasText: "服务端地址校验结果" });
    await expect(serverDialog).toBeVisible({ timeout: 60_000 });
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
    await page.locator("#download-chain").selectOption(CHAIN);
    await importFile(page, path.join(FIX, "mixed.xlsx"));

    const localDialog = dialog(page).filter({ hasText: "地址导入解析摘要（浏览器）" });
    await expect(localDialog).toBeVisible({ timeout: 30_000 });
    await page.getByRole("button", { name: "提交服务端校验" }).click();
    const serverDialog = dialog(page).filter({ hasText: "服务端地址校验结果" });
    await expect(serverDialog).toBeVisible({ timeout: 60_000 });
    const serverText = await serverDialog.innerText();
    const verdict = unwrap(net.filter((r) => r.url.includes("validate-addresses")).pop()?.responseBody);
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "06-mixed.png"), fullPage: false });
    await page.getByRole("button", { name: "使用服务端结果" }).click();

    await page.getByRole("button", { name: "预览 Coverage 与成本" }).click();
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

  test("R9-07 格式防线：改名文件与压缩炸弹必须在浏览器被拒（真实上限），且不发出任何请求", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "creds not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    const cases: Array<{ file: string; expectReason: string }> = [
      { file: "renamed.xlsx", expectReason: "不是 ZIP/OOXML" },
      { file: "bomb-size.xlsx", expectReason: "超过上限" },
      { file: "bomb-ratio.xlsx", expectReason: "压缩比" },
      { file: "bomb-lie.xlsx", expectReason: "声明值不可信" },
    ];
    const observed: Array<{ file: string; reason: string; validateCalls: number; createCalls: number }> = [];
    await login(page);
    for (const item of cases) {
      const net: NetRecord[] = [];
      startCapture(page, net);
      await openDownloadsNew(page);
      await importFile(page, path.join(FIX, item.file));
      const errDialog = dialog(page).filter({ hasText: "文件解析失败" });
      await expect(errDialog, item.file + " 必须走文件解析失败 Dialog").toBeVisible({ timeout: 30_000 });
      const text = await errDialog.innerText();
      await page.screenshot({ path: path.join(OUT, testInfo.project.name, "09-guard-" + item.file + ".png"), fullPage: false });
      await errDialog.getByRole("button", { name: "知道了" }).click();
      observed.push({
        file: item.file, reason: text,
        validateCalls: net.filter((r) => r.url.includes("validate-addresses")).length,
        createCalls: net.filter((r) => r.url.includes("/api/v2/downloads")).length,
      });
      expect(text, item.file + " 的拒绝原因必须点名真实格式问题").toContain(item.expectReason);
      expect(text).toContain(item.file);
      expect(observed[observed.length - 1].validateCalls, item.file + " 不得触发服务端校验").toBe(0);
      expect(observed[observed.length - 1].createCalls, item.file + " 不得触发创建").toBe(0);
    }
    dump(testInfo, "r9-07-evidence.json", { cases: observed, limits: { maxFileBytes: 16 * 1024 * 1024, maxZipEntries: 512, maxUncompressedBytes: 64 * 1024 * 1024, maxCompressionRatio: 200 } });
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
    const localDialog = dialog(page).filter({ hasText: "地址导入解析摘要（浏览器）" });
    await expect(localDialog).toBeVisible({ timeout: 30_000 });
    const box = await localDialog.boundingBox();
    await page.screenshot({ path: path.join(OUT, testInfo.project.name, "07-mobile-import.png"), fullPage: true });
    await page.getByRole("button", { name: "提交服务端校验" }).click();
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
