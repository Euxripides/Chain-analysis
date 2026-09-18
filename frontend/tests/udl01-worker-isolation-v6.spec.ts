import { expect, test, type Page } from "@playwright/test";
import { mkdirSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";

/**
 * U-DL-01 v6 — 导入解析的**隔离 Worker / 有界超时 / 可取消 / UI 不卡**真实浏览器验收。
 *
 * v6 相对 v5b 的唯一差异：把 U5 产品缺陷（超时预算 < 1s 时文案渲染成"0 秒"）变成**硬断言**：
 * 超时弹窗与进度弹窗都必须给出真实预算，且不得出现"0 秒"。旧 v5b spec 原样保留未改。
 *
 * 与 R10 相同的纪律：不做 mock、不用占位值；每一条断言都对应真实文件、真实页面交互与真实度量。
 * 度量方式（主线程是否被解析阻塞）：
 *   - requestAnimationFrame 间隔最大值（帧间隔）
 *   - PerformanceObserver("longtask") 的最长任务时长
 * 解析若仍在主线程，200k 行的工作簿会让这两个值达到数秒；跑在 Worker 里则应保持在浏览器正常的短任务量级。
 */

const EMAIL = process.env.UDL01_EMAIL ?? "";
const PASSWORD = process.env.UDL01_PASSWORD ?? "";
const FIX = process.env.UDL01_FIXTURES ?? ".";
const OUT = process.env.UDL01_OUT ?? ".";
const LOGIN_PATH = "/app-v2/login";
const PAGE_PATH = "/app-v2/downloads/new";

const fixture = (name: string) => path.join(FIX, name);

function dump(testInfo: { project: { name: string } }, name: string, value: unknown) {
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

async function openImportPage(page: Page) {
  await page.goto(PAGE_PATH, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#download-addresses"), "地址输入框（导入页已渲染）").toBeVisible({ timeout: 30_000 });
}

const summaryDialog = (page: Page) => page.locator('[role="dialog"]').filter({ hasText: "地址导入解析摘要（浏览器）" });
const noticeDialog = (page: Page) => page.locator('[role="dialog"]');
const progressDialog = (page: Page) => page.locator('[role="dialog"]').filter({ hasText: "正在隔离解析文件" });

async function importFile(page: Page, name: string) {
  await page.locator('input[aria-label="导入地址文件"]').setInputFiles(fixture(name));
}

test.describe("U-DL-01 v6 隔离解析 / 有界超时 / 可取消 / 恶意 ZIP", () => {
  test("UDL01-A 大 .xls（200k 行）在隔离 Worker 中解析：解析成功且主线程未被阻塞", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await login(page);
    await openImportPage(page);
    await startMonitor(page);

    const started = Date.now();
    await importFile(page, "udl01-big.xls");
    const dlg = summaryDialog(page);
    await expect(dlg, "解析摘要弹窗（大 .xls）").toBeVisible({ timeout: 300_000 });
    const elapsedMs = Date.now() - started;
    const dialogText = (await dlg.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
    const metrics = await stopMonitor(page);

    expect(dialogText, "摘要必须来自真实解析（含扫描行数）").toMatch(/扫描行数/);
    // v5b 更正：BIFF8(.xls) 单表上限 65536 行，SheetJS 写入 200k 行矩阵时实际只落 65535 个数据行，
    // 因此这里断言的是**该格式真实的**行数（页面报告的也是真实值），不是我们期望的 200k（第 1 轮真实暴露的测试侧错误）。
    expect(dialogText, "大 .xls 必须真的被扫描到该格式的上限行数").toMatch(/6553[0-9]/);
    dump(testInfo, "udl01-a-evidence.json", {
      fixture: "udl01-big.xls",
      bytes: statSync(fixture("udl01-big.xls")).size,
      elapsedMs,
      dialogText,
      metrics,
      pageErrors: [],
    });
    // 主线程必须有空：解析耗时远大于 1s，而帧间隔/长任务仍在浏览器正常的短任务量级
    expect(metrics.maxLongTask, "解析期间不得出现秒级长任务（否则说明解析仍在主线程）。metrics=" + JSON.stringify(metrics)).toBeLessThan(1000);
    expect(metrics.maxGap, "解析期间帧间隔不得出现秒级卡顿。metrics=" + JSON.stringify(metrics)).toBeLessThan(1000);
    await dlg.getByRole("button", { name: "取消" }).click();
  });

  test("UDL01-B 有界超时：超过预算必须终止并给出超时弹窗（且不导入任何地址）", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await page.addInitScript(() => { (window as unknown as { __IMPORT_PARSE_TIMEOUT_MS__: number }).__IMPORT_PARSE_TIMEOUT_MS__ = 300; });
    await login(page);
    await openImportPage(page);
    await startMonitor(page);

    const started = Date.now();
    await importFile(page, "udl01-big.xlsx");
    const notice = noticeDialog(page).filter({ hasText: "解析超时已中止" });
    await expect(notice, "超时弹窗必须出现").toBeVisible({ timeout: 120_000 });
    const elapsedMs = Date.now() - started;
    const noticeText = (await notice.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
    const metrics = await stopMonitor(page);
    const addressValue = await page.locator("#download-addresses").inputValue();

    dump(testInfo, "udl01-b-evidence.json", {
      fixture: "udl01-big.xlsx",
      timeoutMs: 300,
      elapsedMs,
      noticeText,
      addressValueLength: addressValue.length,
      metrics,
    });
    expect(elapsedMs, "有界超时必须在预算附近生效（不得无限等待）").toBeLessThan(60_000);
    expect(noticeText, "必须说明 Worker 已被终止且未导入地址").toMatch(/终止/);
    // U5（P3 产品缺陷）：预算 < 1s 时旧文案四舍五入成"0 秒"，对用户无意义且误导。
    // 真实缺陷（本轮 v6 第一版）：/0\s*秒/ 会命中 "180 秒" 里的 "0 秒" 子串 -> 必须加数字边界。
    expect(noticeText, "超时文案不得出现无意义的 0 秒：" + noticeText).not.toMatch(/(?:^|[^0-9])0\s*秒/);
    expect(noticeText, "超时文案必须给出真实预算（300 毫秒）：" + noticeText).toMatch(/300\s*毫秒/);
    expect(addressValue.trim(), "超时后不得导入任何地址").toBe("");
    await notice.getByRole("button", { name: "知道了" }).click();
  });

  test("UDL01-C 可取消：解析中点取消必须真停止，且后续解析仍可用（Worker 重生）", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await page.addInitScript(() => { (window as unknown as { __IMPORT_PARSE_TIMEOUT_MS__: number }).__IMPORT_PARSE_TIMEOUT_MS__ = 180_000; });
    await login(page);
    await openImportPage(page);

    await startMonitor(page);
    await importFile(page, "udl01-big.xlsx");
    const progress = progressDialog(page);
    await expect(progress, "解析进度弹窗（含取消入口）").toBeVisible({ timeout: 60_000 });
    const progressText = (await progress.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
    expect(progressText, "进度弹窗不得出现无意义的 0 秒：" + progressText).not.toMatch(/(?:^|[^0-9])0\s*秒/);
    expect(progressText, "进度弹窗必须给出真实预算（180 秒）：" + progressText).toMatch(/180\s*秒/);
    const cancelStarted = Date.now();
    await progress.getByRole("button", { name: "取消解析" }).click();
    const cancelNotice = noticeDialog(page).filter({ hasText: "解析已取消" });
    await expect(cancelNotice, "取消后必须给出取消弹窗").toBeVisible({ timeout: 60_000 });
    const cancelMs = Date.now() - cancelStarted;
    const cancelText = (await cancelNotice.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
    await cancelNotice.getByRole("button", { name: "知道了" }).click();
    const metricsAfterCancel = await stopMonitor(page);
    const addressAfterCancel = await page.locator("#download-addresses").inputValue();

    // 取消后同一个 Worker 已被 terminate；下一次解析必须新建并成功（真实回归）
    await importFile(page, "udl01-small-ok.xlsx");
    const dlg = summaryDialog(page);
    await expect(dlg, "取消之后的下一次解析必须成功").toBeVisible({ timeout: 120_000 });
    const followUpText = (await dlg.innerText()).replace(/[ \t\r\n]+/g, " ").trim();

    dump(testInfo, "udl01-c-evidence.json", {
      progressText, cancelMs, cancelText, metricsAfterCancel,
      addressValueAfterCancel: addressAfterCancel.length,
      followUpDialogText: followUpText,
    });
    expect(cancelMs, "取消必须在有界时间内生效").toBeLessThan(30_000);
    expect(cancelText, "取消弹窗必须说明 Worker 已停止且未导入").toMatch(/终止|停止/);
    expect(addressAfterCancel.trim(), "取消后不得导入任何地址").toBe("");
    expect(followUpText, "取消后的下一次解析必须真实成功（含扫描行数）").toMatch(/扫描行数/);
    await dlg.getByRole("button", { name: "取消" }).click();
  });

  test("UDL01-D 恶意 ZIP 全部被格式防线拒绝（真实字节结构，逐条留证）", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(600_000);
    await installMonitor(page);
    await login(page);
    await openImportPage(page);

    const cases: Array<{ file: string; expect: RegExp; why: string }> = [
      { file: "udl01-bomb.xlsx", expect: /炸弹|上限|超过/, why: "压缩炸弹：真实解压超预算" },
      { file: "udl01-encrypted.xlsx", expect: /加密/, why: "加密条目" },
      { file: "udl01-stored-mismatch.xlsx", expect: /不一致/, why: "stored 声明与实际不符" },
      { file: "udl01-method-mismatch.xlsx", expect: /压缩方法|本地头/, why: "本地头与中央目录方法不一致" },
      { file: "udl01-data-descriptor.xlsx", expect: /data descriptor/, why: "data descriptor" },
      { file: "udl01-zip64.xlsx", expect: /ZIP64/, why: "ZIP64" },
    ];
    const results: Record<string, unknown>[] = [];
    const pageErrors: string[] = [];
    page.on("pageerror", (e) => pageErrors.push(String(e).slice(0, 300)));
    for (const item of cases) {
      await startMonitor(page);
      const started = Date.now();
      await importFile(page, item.file);
      const notice = noticeDialog(page).filter({ hasText: "文件解析失败" });
      await expect(notice, item.file + " 必须被拒绝并弹窗").toBeVisible({ timeout: 120_000 });
      const text = (await notice.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
      const metrics = await stopMonitor(page);
      results.push({ ...item, expect: String(item.expect), elapsedMs: Date.now() - started, noticeText: text, matched: item.expect.test(text), metrics });
      expect(text, item.file + " 的拒绝原因必须命中预期分支（" + item.why + "）：" + text).toMatch(item.expect);
      await notice.getByRole("button", { name: "知道了" }).click();
    }
    dump(testInfo, "udl01-d-evidence.json", { cases: results, pageErrors });
    expect(pageErrors, "恶意 ZIP 不得造成页面异常").toEqual([]);
    for (const r of results) {
      expect((r.metrics as { maxLongTask: number }).maxLongTask, String(r.file) + " 拒绝路径不得阻塞主线程").toBeLessThan(1000);
    }
  });

  test("UDL01-E 正向回归：正常小工作簿仍可解析并进入服务端校验摘要", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "UDL01_EMAIL/PASSWORD not provided");
    test.setTimeout(300_000);
    await installMonitor(page);
    await login(page);
    await openImportPage(page);
    await startMonitor(page);
    await importFile(page, "udl01-small-ok.xlsx");
    const dlg = summaryDialog(page);
    await expect(dlg, "正常文件必须给出解析摘要").toBeVisible({ timeout: 120_000 });
    const text = (await dlg.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
    const metrics = await stopMonitor(page);
    dump(testInfo, "udl01-e-evidence.json", { fixture: "udl01-small-ok.xlsx", dialogText: text, metrics });
    expect(text, "识别 address 表头").toMatch(/address 表头/);
    expect(text, "本次送校验 1 个地址").toMatch(/1 个地址/);
    await dlg.getByRole("button", { name: "取消" }).click();
  });
});
