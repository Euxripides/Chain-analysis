import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";

/**
 * U-DL-01 v5（历史基线，语义已同步） — 导入解析的**隔离 Worker / 有界超时 / 可取消 / 大文件 / 恶意 ZIP**真实浏览器验收。
 *
 * 历史基线说明：旧 v5 断言 /20000[01]/（BIFF8 不可达）已删除，断言与 v5b 同步，仓库不再保留一个必失败测试；权威执行体是 v5b，最终 release gate 是 v6。
 *
 * 本轮（《新建数据下载页面全量整改实施规范》§13 / §14 / §15）修正的测试侧真实缺陷：
 *   R1 行数语义（§13.1/§13.2）：旧 v5 断言 /20000[01]/。BIFF8 .xls 单表上限 65536 行（含表头），
 *      请求写 200000 行时实际只落 65535 个以内的数据行，该断言**不可达**。现在拆成两个意图：
 *        A1（UDL01-A1）真实 .xls 格式上限：断言行数等于夹具**读回文件**得到的真实数据行数
 *           （manifest.actualDataRowsOnDisk），manifest 同时记录 requestedRows 与 actualFormatCapacity；
 *           并用浏览器里真实存在的 import-parse Worker 证明解析在 Worker 中执行。
 *        A2（UDL01-A2a / UDL01-A2b）.xlsx（< 16MiB）走超时与取消路径——不把 200000 行硬塞进 .xls。
 *   R2 ZIP64（§14.3）：夹具布局（ZIP64 EOCD → locator → EOCD）由生成器机器自检；浏览器侧必须命中
 *      ZIP64 拒绝分支，且**不触发 SheetJS 解析、不触发服务端验证、不创建任务**。
 *   R3 稳定语义（§15.3）：UDL01-E 不再依赖“本次送校验 1 个地址”整句文案，改用 local-parse-* 稳定锚点。
 *
 * 与 R10 相同的纪律：不做 mock、不用占位值；每一条断言都对应真实文件、真实页面交互与真实度量。
 * 度量方式（主线程是否被解析阻塞）：requestAnimationFrame 间隔最大值 + PerformanceObserver("longtask")。
 */

const EMAIL = process.env.UDL01_EMAIL ?? "";
const PASSWORD = process.env.UDL01_PASSWORD ?? "";
const FIX = process.env.UDL01_FIXTURES ?? ".";
const OUT = process.env.UDL01_OUT ?? ".";
const LOGIN_PATH = "/app-v2/login";
const PAGE_PATH = "/app-v2/downloads/new";
const SPEC_BASENAME = "udl01-worker-isolation-v5.spec.ts";
/** v6 才要求 U5 预算文案判据（300ms → “300 毫秒”、180000ms → “180 秒”）。 */
const WANT_U5_BUDGET_PROBE = false;

const fixture = (name: string) => path.join(FIX, name);

function loadJson(file: string): Record<string, any> {
  try {
    return JSON.parse(readFileSync(file, "utf8")) as Record<string, any>;
  } catch (error) {
    throw new Error("夹具元数据缺失或不可解析（先用 frontend/scripts/r9-make-fixtures-v5c.mjs 生成）：" + file + " -> " + String(error));
  }
}

/**
 * 夹具 manifest（r9-make-fixtures-v5c.mjs 生成）：同时记录 requestedRows 与 actualFormatCapacity，
 * 并把写出的 .xls 读回，给出该格式真实落盘的数据行数（§13.3 第 4 条）。
 */
const MANIFEST = loadJson(path.join(FIX, "udl01-fixtures-manifest.json"));
const XLS_REQUESTED_ROWS = Number(MANIFEST.requestedRows?.xls ?? 0);
const XLS_MAX_DATA_ROWS = Number(MANIFEST.actualFormatCapacity?.maxDataRows ?? 0);
const XLS_ACTUAL_ROWS = Number(MANIFEST.actualDataRowsOnDisk ?? 0);
/** ZIP64 生成器的机器自检（§14.3 第 3 条）。 */
const FIXTURE_SELFCHECK = loadJson(path.join(FIX, "udl01-fixtures-selfcheck.json"));

/** 登录导航的传输层重试记录（并发部署窗口会短暂 404/5xx；真实鉴权错误仍会失败）。 */
const loginMeta = { attempts: 0, retriedNavigation: false };

function dump(testInfo: { project: { name: string } }, name: string, value: unknown) {
  const dir = path.join(OUT, testInfo.project.name);
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, name), JSON.stringify(value, null, 2) + "\n", "utf8");
}

function trackPageErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error).slice(0, 400)));
  return errors;
}

/** 主线程可交互性监视器：帧间隔 + 长任务。由测试显式 start/stop。 */
async function installMonitor(page: Page) {
  await page.addInitScript(() => {
    const state = { running: false, last: 0, maxGap: 0, gapSamples: 0, maxLongTask: 0, longTasks: [] as number[] };
    (window as unknown as { __udl01: typeof state }).__udl01 = state;
    const tick = (t: number) => {
      if (state.running) {
        const gap = t - state.last;
        if (gap > state.maxGap) state.maxGap = gap;
        state.gapSamples += 1;
      }
      state.last = t;
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          if (!state.running) continue;
          if (entry.duration > state.maxLongTask) state.maxLongTask = entry.duration;
          state.longTasks.push(Math.round(entry.duration));
        }
      }).observe({ entryTypes: ["longtask"] });
    } catch {
      /* longtask 观测不可用时只依赖帧间隔（不假装有数据） */
    }
  });
}

async function startMonitor(page: Page) {
  return page.evaluate(() => {
    const s = (window as unknown as { __udl01: { running: boolean; last: number; maxGap: number; gapSamples: number; maxLongTask: number; longTasks: number[] } }).__udl01;
    s.running = true; s.last = performance.now(); s.maxGap = 0; s.gapSamples = 0; s.maxLongTask = 0; s.longTasks = [];
    return true;
  });
}

async function stopMonitor(page: Page) {
  return page.evaluate(() => {
    const s = (window as unknown as { __udl01: { running: boolean; maxGap: number; gapSamples: number; maxLongTask: number; longTasks: number[] } }).__udl01;
    s.running = false;
    return { maxGap: Math.round(s.maxGap), gapSamples: s.gapSamples, maxLongTask: Math.round(s.maxLongTask), longTasks: s.longTasks.slice(0, 20) };
  });
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

async function openImportPage(page: Page) {
  await page.goto(PAGE_PATH, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#download-addresses"), "地址输入框（导入页已渲染）").toBeVisible({ timeout: 30_000 });
}

/** 本地解析摘要本体（Flowline 版放在「本地解析摘要」Dialog 里）。 */
const summary = (page: Page) => page.getByTestId("file-parse-summary");
const summaryDialog = (page: Page) => page.locator('[role="dialog"]').filter({ hasText: "本地解析摘要" });
const noticeDialog = (page: Page) => page.locator('[role="dialog"]');
/**
 * 文件导入字段：唯一触发器（含正在解析的文件名）+ 局部进度 + 取消入口都在同一个
 * .dl-address__file-field 里 —— 页面只有 1 个原生 input 和 1 个可见触发器（规范 §7.5.3）。
 */
const progressBox = (page: Page) => page.locator(".dl-address__file-field");

async function importFile(page: Page, name: string) {
  await page.locator('input[aria-label="导入地址文件"]').setInputFiles(fixture(name));
}

/** 真实网络证据：记录每个案例发出的 API 请求与静态 chunk 请求（不得请求 SheetJS chunk）。 */
type RequestLog = { api: string[]; chunks: string[] };

function captureRequests(page: Page): RequestLog {
  const log: RequestLog = { api: [], chunks: [] };
  page.on("request", (request) => {
    const url = request.url();
    if (url.includes("/api/")) log.api.push(request.method() + " " + url);
    else if (/\.js(\?|$)/.test(url)) log.chunks.push(url);
  });
  return log;
}

const countApi = (log: RequestLog, needle: string) => log.api.filter((entry) => entry.includes(needle)).length;
/** 精确匹配 create：POST /api/v2/downloads（不含 preview / validate-addresses）。 */
const countCreate = (log: RequestLog) => log.api.filter((entry) => /^POST .*\/api\/v2\/downloads$/.test(entry)).length;
const countSheetjsChunks = (log: RequestLog) => log.chunks.filter((url) => /\/xlsx-[^/]*\.js(\?|$)/.test(url)).length;
const normalize = (text: string) => text.replace(/[ \t\r\n]+/g, " ").trim();

/* --------------------------------------------------- v6 专用：预算文案探测（U5） */

type BudgetProbeRow = { input: string; expected: string; actual: string; ok: boolean };
type BudgetProbe = { chunk: string; chunk_sha256: string; rows: BudgetProbeRow[]; no_zero_second_token: boolean };

/**
 * 从**服务端实际服务的** page chunk 里提取真正的 formatParseBudget 实现并求值。
 *
 * 为什么不是直接在进度区读文案：Flowline 页面的局部进度只如实显示“正在解析哪个文件”，
 * 预算文案只出现在超时弹窗里（300ms 的真实超时已在 UDL01-A2a 端到端验证）。
 * 180000ms → “180 秒” 因此用同一份**已部署字节**里的同一个函数求值来验证：
 * 提取的源码来自 served chunk，其 sha256 必须等于 bundle-binding.json 里 served/disk 的 page chunk SHA。
 */
function extractBudgetFormatter(source: string): (ms: number) => string {
  const marker = source.indexOf('"未知时长"');
  if (marker < 0) throw new Error("served page chunk 里找不到 formatParseBudget 的标记字符串");
  const head = source.lastIndexOf("function", marker);
  const open = head >= 0 ? source.indexOf("{", head) : -1;
  let depth = 0;
  let end = -1;
  for (let i = open; i >= 0 && i < source.length; i += 1) {
    const ch = source.charAt(i);
    if (ch === "{") depth += 1;
    else if (ch === "}") {
      depth -= 1;
      if (depth === 0) { end = i + 1; break; }
    }
  }
  if (head < 0 || open < 0 || end < 0) throw new Error("无法从 served chunk 提取 formatParseBudget 源码");
  const factory = new Function("return (" + source.slice(head, end) + ")") as () => (ms: number) => string;
  return factory();
}

async function probeBudgetCopy(request: APIRequestContext, outDir: string): Promise<BudgetProbe> {
  const binding = loadJson(path.join(outDir, "bundle-binding.json"));
  const chunk = String(binding.page_chunk ?? "");
  if (!chunk) throw new Error("bundle-binding.json 缺少 page_chunk");
  const response = await request.get("/app-v2/assets/" + chunk);
  expect(response.status(), "served page chunk 必须可读").toBe(200);
  const source = await response.text();
  const chunkSha = createHash("sha256").update(source).digest("hex");
  const format = extractBudgetFormatter(source);
  const cases: Array<[number, string]> = [[300, "300 毫秒"], [180000, "180 秒"], [2000, "2 秒"], [1500, "1.5 秒"], [0, "未知时长"]];
  const rows: BudgetProbeRow[] = cases.map(([ms, expected]) => {
    const actual = String(format(ms));
    return { input: String(ms), expected, actual, ok: actual === expected };
  });
  return { chunk, chunk_sha256: chunkSha, rows, no_zero_second_token: rows.every((row) => !/(?:^|[^0-9])0\s*秒/.test(row.actual)) };
}

test.describe("U-DL-01 v5（历史基线，语义与 v5b 同步）隔离解析 / 有界超时 / 可取消 / 恶意 ZIP", () => {
  test("UDL01-A A1 真实 .xls 格式上限：请求 200000 行落盘 65535 行，由隔离 Worker 解析且主线程无秒级阻塞", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await login(page);
    await openImportPage(page);
    const log = captureRequests(page);
    const pageErrors = trackPageErrors(page);
    await startMonitor(page);

    const started = Date.now();
    await importFile(page, "udl01-big.xls");
    const dlg = summary(page);
    await expect(dlg, "解析数据行（大 .xls）").toBeVisible({ timeout: 300_000 });
    const elapsedMs = Date.now() - started;
    const dialogText = normalize(await summaryDialog(page).innerText());
    const metrics = await stopMonitor(page);
    const rowCountText = normalize(await page.getByTestId("local-parse-row-count").innerText());
    const rowCountReportedByPage = Number(rowCountText);
    const workerUrls = page.workers().map((worker) => worker.url());
    const isolatedWorkerUsed = workerUrls.some((url) => url.includes("import-parse.worker"));

    dump(testInfo, "udl01-a-evidence.json", {
      fixture: "udl01-big.xls",
      spec: SPEC_BASENAME,
      bytes: statSync(fixture("udl01-big.xls")).size,
      elapsedMs,
      dialogText,
      metrics,
      pageErrors: pageErrors.slice(),
      login: loginMeta,
      requestedRows: XLS_REQUESTED_ROWS,
      actualFormatCapacity: MANIFEST.actualFormatCapacity,
      actualDataRowsOnDisk: XLS_ACTUAL_ROWS,
      rowCountReportedByPage,
      rowCountText,
      workerUrls,
      isolatedWorkerUsed,
      oldUnreachableAssertion: "旧 v5 的 /20000[01]/ 在 BIFF8 下不可达，已移除",
      workerChunkRequested: log.chunks.filter((url) => url.includes("import-parse.worker")).length,
      apiCalls: log.api,
    });

    expect(XLS_REQUESTED_ROWS, "夹具确实按 200000 行请求生成").toBe(200000);
    expect(XLS_MAX_DATA_ROWS, "BIFF8 单表数据行上限").toBe(65535);
    expect(XLS_ACTUAL_ROWS, "夹具写盘后读回的数据行数必须为正").toBeGreaterThan(0);
    expect(rowCountReportedByPage, "页面扫描行数必须等于夹具读回文件后的真实数据行数").toBe(XLS_ACTUAL_ROWS);
    expect(rowCountReportedByPage, "不得超过 BIFF8 单表数据行上限").toBeLessThanOrEqual(XLS_MAX_DATA_ROWS);
    expect(rowCountReportedByPage, "必须接近该格式上限（真实的大文件路径）").toBeGreaterThan(XLS_MAX_DATA_ROWS - 8);
    expect(rowCountReportedByPage, "不得再出现不可达的 200000/200001 行断言").toBeLessThan(XLS_REQUESTED_ROWS);
    expect(isolatedWorkerUsed, "解析必须发生在 import-parse Worker 中：" + JSON.stringify(workerUrls)).toBe(true);
    expect(metrics.maxLongTask, "解析期间不得出现秒级长任务（否则说明解析仍在主线程）。metrics=" + JSON.stringify(metrics)).toBeLessThan(1000);
    expect(metrics.maxGap, "解析期间帧间隔不得出现秒级卡顿。metrics=" + JSON.stringify(metrics)).toBeLessThan(1000);

    /* A1 只证明“该格式真实上限 + Worker 隔离”；本地摘要不做服务端提交（取消后不得有请求）。 */
    await summaryDialog(page).getByRole("button", { name: "取消" }).click();
    await expect(dlg, "取消后本地摘要必须关闭").toBeHidden({ timeout: 20_000 });
    await expect.poll(() => countApi(log, "validate-addresses"), { timeout: 5000 }).toBe(0);
    expect(pageErrors, "A1 不得产生页面异常").toEqual([]);
  });

  test("UDL01-B A2a 有界超时：60000 行 .xlsx（< 16MiB）在 300ms 预算内被终止，不导入任何地址", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await page.addInitScript(() => { (window as unknown as { __IMPORT_PARSE_TIMEOUT_MS__: number }).__IMPORT_PARSE_TIMEOUT_MS__ = 300; });
    await login(page);
    await openImportPage(page);
    const log = captureRequests(page);
    const pageErrors = trackPageErrors(page);
    await startMonitor(page);

    const started = Date.now();
    await importFile(page, "udl01-big.xlsx");
    const notice = noticeDialog(page).filter({ hasText: "解析超时已中止" });
    await expect(notice, "超时弹窗必须出现").toBeVisible({ timeout: 120_000 });
    const elapsedMs = Date.now() - started;
    const noticeText = normalize(await notice.innerText());
    const metrics = await stopMonitor(page);
    const addressValue = await page.locator("#download-addresses").inputValue();
    const fixtureBytes = statSync(fixture("udl01-big.xlsx")).size;

    dump(testInfo, "udl01-b-evidence.json", {
      fixture: "udl01-big.xlsx",
      spec: SPEC_BASENAME,
      fixtureBytes,
      timeoutMs: 300,
      elapsedMs,
      noticeText,
      addressValueLength: addressValue.length,
      metrics,
      pageErrors: pageErrors.slice(),
      login: loginMeta,
      apiCalls: log.api,
      validateCalls: countApi(log, "validate-addresses"),
      createCalls: countCreate(log),
    });

    expect(fixtureBytes, "A2 用 .xlsx，且必须小于 16MiB 导入上限").toBeLessThan(16 * 1024 * 1024);
    expect(fixtureBytes, "A2 的 .xlsx 必须是真实大文件").toBeGreaterThan(1024 * 1024);
    expect(elapsedMs, "有界超时必须在预算附近生效（不得无限等待）").toBeLessThan(60_000);
    expect(noticeText, "必须说明 Worker 已被终止且未导入地址").toMatch(/终止/);
    expect(addressValue.trim(), "超时后不得导入任何地址").toBe("");
    expect(countApi(log, "validate-addresses"), "超时后不得提交服务端校验").toBe(0);
    expect(countCreate(log), "超时后不得创建任务").toBe(0);
    expect(pageErrors, "超时路径不得产生页面异常").toEqual([]);

    await notice.getByRole("button", { name: "知道了" }).click();
  });

  test("UDL01-C A2b 可取消：60000 行 .xlsx 解析中点取消真停止，Worker 重建后小文件仍可解析", async ({ page, request }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await page.addInitScript(() => { (window as unknown as { __IMPORT_PARSE_TIMEOUT_MS__: number }).__IMPORT_PARSE_TIMEOUT_MS__ = 180_000; });
    await login(page);
    await openImportPage(page);
    const log = captureRequests(page);
    const pageErrors = trackPageErrors(page);

    await startMonitor(page);
    await importFile(page, "udl01-big.xlsx");
    const progress = progressBox(page);
    await expect(progress, "解析局部进度（含取消入口）").toBeVisible({ timeout: 60_000 });
    await expect(progress.locator(".dl-address__progress"), "不定进度条必须出现").toBeVisible({ timeout: 60_000 });
    const progressText = normalize(await progress.innerText());
    expect(progressText, "局部进度区必须指出正在解析的文件名：" + progressText).toMatch(/udl01-big\.xlsx/);
    const cancelStarted = Date.now();
    await progress.getByRole("button", { name: "取消解析" }).click();
    const cancelNotice = noticeDialog(page).filter({ hasText: "解析已取消" });
    await expect(cancelNotice, "取消后必须给出取消弹窗").toBeVisible({ timeout: 60_000 });
    const cancelMs = Date.now() - cancelStarted;
    const cancelText = normalize(await cancelNotice.innerText());
    await cancelNotice.getByRole("button", { name: "知道了" }).click();
    const metricsAfterCancel = await stopMonitor(page);
    const addressAfterCancel = await page.locator("#download-addresses").inputValue();

    /* 取消后同一个 Worker 已被 terminate；下一次解析必须新建并成功（真实回归）。 */
    await importFile(page, "udl01-small-ok.xlsx");
    const dlg = summary(page);
    await expect(dlg, "取消之后的下一次解析必须成功").toBeVisible({ timeout: 120_000 });
    const followUpText = normalize(await summaryDialog(page).innerText());
    const followUpRowCount = normalize(await page.getByTestId("local-parse-row-count").innerText());
    await summaryDialog(page).getByRole("button", { name: "取消" }).click();
    await expect(dlg).toBeHidden({ timeout: 20_000 });

    let budgetFormatProbe: BudgetProbe | null = null;
    if (WANT_U5_BUDGET_PROBE) {
      budgetFormatProbe = await probeBudgetCopy(request, OUT);
      const binding = loadJson(path.join(OUT, "bundle-binding.json"));
      expect(budgetFormatProbe.chunk_sha256, "预算文案探测必须绑定到服务端实际服务的 page chunk").toBe(String(binding.page_chunk_served_sha));
      for (const row of budgetFormatProbe.rows) {
        expect(row.actual, "formatParseBudget(" + row.input + ") 必须是 " + row.expected + "，实际 " + row.actual).toBe(row.expected);
      }
      expect(budgetFormatProbe.no_zero_second_token, "预算文案不得出现无意义的 0 秒").toBe(true);
    }

    dump(testInfo, "udl01-c-evidence.json", {
      spec: SPEC_BASENAME,
      fixture: "udl01-big.xlsx",
      timeoutMs: 180_000,
      progressText,
      cancelMs,
      cancelText,
      metricsAfterCancel,
      addressValueAfterCancel: addressAfterCancel.length,
      followUpDialogText: followUpText,
      followUpRowCount,
      pageErrors: pageErrors.slice(),
      login: loginMeta,
      budgetFormatProbe,
      apiCallsAfterCancel: log.api,
      validateCallsAfterCancel: countApi(log, "validate-addresses"),
    });

    expect(cancelMs, "取消必须在有界时间内生效").toBeLessThan(30_000);
    expect(cancelText, "取消弹窗必须说明 Worker 已停止且未导入").toMatch(/终止|停止/);
    expect(addressAfterCancel.trim(), "取消后不得导入任何地址").toBe("");
    expect(countApi(log, "validate-addresses"), "取消后不得提交服务端校验").toBe(0);
    expect(followUpText, "取消后的下一次解析必须真实成功（含扫描行数）").toMatch(/扫描行数/);
    expect(followUpRowCount, "取消后的小文件扫描行数必须是稳定业务值").toBe("1");
    expect(pageErrors, "取消/重建路径不得产生页面异常").toEqual([]);
  });

  test("UDL01-D 恶意 ZIP 全部被格式防线拒绝（真实字节结构，逐条留证）", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await login(page);
    await openImportPage(page);
    const pageErrors = trackPageErrors(page);

    const cases: Array<{ file: string; expect: RegExp; why: string }> = [
      { file: "udl01-bomb.xlsx", expect: /炸弹|上限|超过/, why: "压缩炸弹：声明解压总大小超上限" },
      { file: "udl01-encrypted.xlsx", expect: /加密/, why: "加密条目" },
      { file: "udl01-stored-mismatch.xlsx", expect: /不一致/, why: "stored 声明与实际不符" },
      { file: "udl01-method-mismatch.xlsx", expect: /压缩方法|本地头/, why: "本地头与中央目录方法不一致" },
      { file: "udl01-data-descriptor.xlsx", expect: /data descriptor/, why: "data descriptor" },
      { file: "udl01-zip64.xlsx", expect: /ZIP64/, why: "ZIP64 布局（locator 位于 EOCD 之前）" },
    ];

    /* §14.3：浏览器之前先机器核对 ZIP64 夹具字节布局（生成器已自检，这里在轮次内再断言一次）。 */
    const zip64Layout = FIXTURE_SELFCHECK.zip64Layout as Record<string, any>;
    expect(zip64Layout.eocd_present, "ZIP64 夹具必须有 EOCD").toBe(true);
    expect(zip64Layout.locator_is_20_bytes_before_eocd, "ZIP64 locator 必须正好位于 EOCD 前 20 字节").toBe(true);
    expect(zip64Layout.locator_signature, "ZIP64 locator signature").toBe("0x07064b50");
    expect(zip64Layout.zip64_eocd_signature, "ZIP64 EOCD signature").toBe("0x06064b50");
    expect(zip64Layout.locator_points_at_zip64_eocd, "locator 声明的偏移必须指向 ZIP64 EOCD").toBe(true);
    expect(zip64Layout.layout, "ZIP64 布局顺序").toBe("ZIP64 EOCD -> ZIP64 locator -> classic EOCD");

    const results: Record<string, any>[] = [];
    for (const item of cases) {
      const log = captureRequests(page);
      await startMonitor(page);
      const started = Date.now();
      await importFile(page, item.file);
      const notice = noticeDialog(page).filter({ hasText: "文件解析失败" });
      await expect(notice, item.file + " 必须被拒绝并弹窗").toBeVisible({ timeout: 120_000 });
      const text = normalize(await notice.innerText());
      const metrics = await stopMonitor(page);
      const summaryVisible = await page.getByTestId("local-parse-summary").isVisible().catch(() => false);
      const serverCalls = {
        validate: countApi(log, "validate-addresses"),
        preview: countApi(log, "/api/v2/downloads/preview"),
        create: countCreate(log),
      };
      const sheetjsChunkRequests = countSheetjsChunks(log);
      results.push({
        file: item.file, expect: String(item.expect), why: item.why,
        elapsedMs: Date.now() - started, noticeText: text, matched: item.expect.test(text),
        metrics, summaryVisible, serverCalls, sheetjsChunkRequests, apiCalls: log.api, chunkRequests: log.chunks,
      });
      expect(text, item.file + " 的拒绝原因必须命中预期分支（" + item.why + "）：" + text).toMatch(item.expect);
      expect(summaryVisible, item.file + " 不得进入 SheetJS 解析路径（不得出现本地解析摘要）").toBe(false);
      expect(sheetjsChunkRequests, item.file + " 不得请求 SheetJS chunk（格式防线必须在解析之前返回）").toBe(0);
      expect(serverCalls.validate + serverCalls.preview + serverCalls.create, item.file + " 不得触发任何业务请求").toBe(0);
      await notice.getByRole("button", { name: "知道了" }).click();
    }

    const zip64 = results.find((row) => row.file === "udl01-zip64.xlsx") ?? {};
    dump(testInfo, "udl01-d-evidence.json", {
      spec: SPEC_BASENAME,
      cases: results,
      pageErrors: pageErrors.slice(),
      login: loginMeta,
      zip64Layout,
      zip64Branch: {
        file: "udl01-zip64.xlsx",
        matched: zip64.matched === true,
        reason: zip64.noticeText ?? null,
        sheetjsChunkRequests: zip64.sheetjsChunkRequests ?? null,
        serverCalls: zip64.serverCalls ?? null,
        taskCreated: Number((zip64.serverCalls ?? {}).create ?? 0) > 0,
      },
    });

    expect(pageErrors, "恶意 ZIP 不得造成页面异常").toEqual([]);
    for (const row of results) {
      expect((row.metrics as { maxLongTask: number }).maxLongTask, String(row.file) + " 拒绝路径不得阻塞主线程").toBeLessThan(1000);
    }
    expect(zip64.matched, "ZIP64 夹具必须命中 ZIP64 拒绝分支，而不是被当成普通损坏 ZIP").toBe(true);
    expect(String(zip64.noticeText), "ZIP64 拒绝原因必须点名 ZIP64").toContain("ZIP64");
    expect(zip64.sheetjsChunkRequests, "ZIP64 分支不得触发 SheetJS 解析").toBe(0);
    expect((zip64.serverCalls ?? {}).validate, "ZIP64 不得触发服务端验证").toBe(0);
    expect((zip64.serverCalls ?? {}).preview, "ZIP64 不得触发预览").toBe(0);
    expect((zip64.serverCalls ?? {}).create, "ZIP64 不得创建任务").toBe(0);
  });

  test("UDL01-E 正向回归：小工作簿解析成功，稳定业务语义锚点正确且取消后无服务端请求", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(300_000);
    await installMonitor(page);
    await login(page);
    await openImportPage(page);
    const log = captureRequests(page);
    const pageErrors = trackPageErrors(page);
    await startMonitor(page);

    await importFile(page, "udl01-small-ok.xlsx");
    const dlg = summary(page);
    await expect(dlg, "正常文件必须给出解析摘要").toBeVisible({ timeout: 120_000 });
    const dialogText = normalize(await summaryDialog(page).innerText());
    const metrics = await stopMonitor(page);

    /* §15.3 稳定业务语义断言：不依赖“本次送校验 1 个地址”这一整句易变文案。 */
    await expect(page.getByTestId("local-parse-row-count"), "扫描行数是纯数字 1").toHaveText("1");
    await expect(page.getByTestId("local-parse-address-count"), "待校验地址数是纯数字 1").toHaveText("1");
    await expect(page.getByTestId("local-parse-address-header"), "表头识别必须是 data-detected=true").toHaveAttribute("data-detected", "true");
    await expect(page.getByTestId("local-parse-submit"), "提交服务端校验按钮必须可用").toBeEnabled();
    const stableSemantics = {
      rowCount: normalize(await page.getByTestId("local-parse-row-count").innerText()),
      addressCount: normalize(await page.getByTestId("local-parse-address-count").innerText()),
      headerDetected: await page.getByTestId("local-parse-address-header").getAttribute("data-detected"),
      submitEnabled: await page.getByTestId("local-parse-submit").isEnabled(),
    };

    await summaryDialog(page).getByRole("button", { name: "取消" }).click();
    await expect(dlg, "取消后本地摘要必须关闭").toBeHidden({ timeout: 20_000 });
    const serverCallsAfterCancel = {
      validate: countApi(log, "validate-addresses"),
      preview: countApi(log, "/api/v2/downloads/preview"),
      create: countCreate(log),
    };

    dump(testInfo, "udl01-e-evidence.json", {
      spec: SPEC_BASENAME,
      fixture: "udl01-small-ok.xlsx",
      dialogText,
      stableSemantics,
      metrics,
      pageErrors: pageErrors.slice(),
      login: loginMeta,
      serverCallsAfterCancel,
      apiCalls: log.api,
    });

    expect(stableSemantics.rowCount, "扫描行数 = 1").toBe("1");
    expect(stableSemantics.addressCount, "待校验地址数 = 1").toBe("1");
    expect(stableSemantics.headerDetected, "address 表头已识别").toBe("true");
    expect(stableSemantics.submitEnabled, "可以提交服务端校验").toBe(true);
    expect(dialogText, "摘要必须仍报出 address 表头这一业务事实").toContain("address 表头");
    expect(serverCallsAfterCancel.validate + serverCallsAfterCancel.preview + serverCallsAfterCancel.create, "取消后不得有服务端请求").toBe(0);
    expect(pageErrors, "正向路径不得产生页面异常").toEqual([]);
  });
});
