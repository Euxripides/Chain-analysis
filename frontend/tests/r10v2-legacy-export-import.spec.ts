import { expect, test, type Browser, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { artifactsDir, dump, login, saveDownload, startCapture, unwrap, type NetRecord } from "./r10-helpers";

/**
 * R10 v2 — legacy SPA 与登录态 null 列表回归（v1 文件原样保留）。
 *
 * 2026-09-13 真实缺陷（R10 v1 实测复现 + 根 10:56 定位）：
 *   已认证用户在空工作区请求 /api/v1/cases、/api/v1/tags 得到 200 {"cases":null} / {"tags":null}
 *   （Go nil slice 序列化为 null）。DownloadPage 直接 setCases(null)，随后 cases.map 抛
 *   TypeError: Cannot read properties of null (reading 'map')，整页白屏；DataExplorerPage 同样，
 *   FundFlowPage 用 casesQ.data?.cases.map 也挡不住 null。
 *   未认证时反而正常（401 走 catch 分支），所以只有**登录态**才暴露。
 *
 * 修复（两层 fail-closed）：
 *   - 契约层 frontend/src/api/client.ts：Array.isArray(d.cases) ? d.cases : []
 *   - 消费层 DownloadPage / DataExplorerPage / FundFlowPage：Array.isArray 判定后才接收
 * 本文件是对应的真实浏览器回归（R10-07），不是只改选择器。
 */

const EMAIL = process.env.R10_EMAIL ?? "";
const PASSWORD = process.env.R10_PASSWORD ?? "";
const VIEWER_EMAIL = process.env.R10_VIEWER_EMAIL ?? "";
const VIEWER_PASSWORD = process.env.R10_VIEWER_PASSWORD ?? "";
const ADDRESS = process.env.R10_ADDRESS ?? "";
const CHAIN = process.env.R10_CHAIN ?? "bsc";
const JOB_ID = process.env.R10_JOB_ID ?? "";
const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const sha256 = (buf: Buffer) => createHash("sha256").update(buf).digest("hex");

async function readXlsx(buf: Buffer) {
  const XLSX = await import("xlsx");
  const wb = XLSX.read(buf, { type: "buffer" });
  const sheetNames: string[] = wb.SheetNames;
  const ws = wb.Sheets[sheetNames[0]];
  const matrix = XLSX.utils.sheet_to_json(ws, { header: 1, blankrows: false, raw: false }) as unknown[][];
  const records = XLSX.utils.sheet_to_json(ws, { defval: "", raw: false }) as Record<string, string>[];
  return { sheetNames, header: (matrix[0] ?? []).map((v) => String(v ?? "")), dataRowCount: records.length, firstRow: records[0] ?? {}, matrixRows: matrix.length };
}

test.describe("R10 v2 legacy SPA：客户端 xlsx 导出、导入与登录态 null 列表回归", () => {
  test("R10-04 legacy 地址详情客户端 SheetJS xlsx 导出：真实落盘并读回", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page, EMAIL, PASSWORD);
    const dir = artifactsDir(testInfo);

    await page.goto("/data-explorer/" + encodeURIComponent(CHAIN) + "/" + encodeURIComponent(ADDRESS), { waitUntil: "domcontentloaded" });
    await expect(page.locator('[aria-label="导出格式"]'), "legacy 导出格式选择器").toBeVisible({ timeout: 30_000 });
    await expect.poll(async () => page.locator("table.detail-table tbody tr").count(), { timeout: 60_000 }).toBeGreaterThan(0);

    const tableRowCount = await page.locator("table.detail-table tbody tr").count();
    const firstRowText = (await page.locator("table.detail-table tbody tr").first().innerText()).replace(/[ \t\r\n]+/g, " ").trim();

    await page.locator('[aria-label="导出格式"]').selectOption("xlsx");
    await page.locator('[aria-label="导出范围"]').selectOption("all");

    const { file, bytes } = await saveDownload(page, dir, "r10-04-legacy-client-xlsx", async () => {
      await page.getByRole("button", { name: /^导出$/ }).click();
    });
    expect(bytes.length, "legacy 客户端导出的 xlsx 必须非空").toBeGreaterThan(0);
    expect(bytes.subarray(0, 2).toString("latin1"), "必须是真实 OOXML 包").toBe("PK");

    const readback = await readXlsx(bytes);
    expect(readback.sheetNames, "客户端导出的工作表名").toContain("数据");
    expect(readback.header.length, "客户端导出的表头列数").toBeGreaterThan(0);
    expect(readback.dataRowCount, "客户端导出的数据行数").toBeGreaterThan(0);
    const flat = Object.values(readback.firstRow).map((v) => String(v ?? "").toLowerCase());
    expect(flat.some((v) => v.includes(ADDRESS.toLowerCase())), "首行必须含被导出的地址（客户端导出的是页面上真实行）").toBe(true);

    dump(testInfo, "r10-04-evidence.json", {
      chain: CHAIN, address: ADDRESS,
      page: { tableRowCount, firstRowText },
      file: { path: file, bytes: bytes.length, sha256: sha256(bytes) },
      workbook: readback,
      network: net.filter((n) => n.url.includes("/api/")),
    });
  });

  test("R10-05 legacy 下载页导入：XLSX 地址/案件/标签真实带入；负向如实记录", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    const pageErrors: string[] = [];
    page.on("pageerror", (e) => pageErrors.push(String(e).slice(0, 400)));
    await login(page, EMAIL, PASSWORD);
    const dir = artifactsDir(testInfo);

    const XLSX = await import("xlsx");
    const addr2 = "0x00000000000000000000000000000000000000d1";
    const sheet = XLSX.utils.json_to_sheet([
      { address: ADDRESS, chain: CHAIN, case: "R10-LEGACY-CASE", tags: "R10-LEGACY-TAG|R10-LEGACY-TAG2" },
      { address: addr2, chain: CHAIN, case: "", tags: "" },
    ]);
    const wb = XLSX.utils.book_new();
    XLSX.utils.book_append_sheet(wb, sheet, "Sheet1");
    const goodBuf = XLSX.write(wb, { type: "buffer", bookType: "xlsx" }) as Buffer;
    const goodPath = path.join(dir, "r10-05-legacy-import.xlsx");
    writeFileSync(goodPath, goodBuf);

    const badBuf = Buffer.concat([Buffer.from([0x50, 0x4b, 0x03, 0x04]), Buffer.alloc(40, 0xa5)]);
    const badPath = path.join(dir, "r10-05-legacy-broken.xlsx");
    writeFileSync(badPath, badBuf);

    await page.goto("/", { waitUntil: "domcontentloaded" });
    const textarea = page.locator("#download-addresses");
    let rendered = true;
    try {
      await expect(textarea).toBeVisible({ timeout: 30_000 });
    } catch {
      rendered = false;
    }
    const diagnostics = {
      rendered,
      pageErrors,
      bodyTextLength: (await page.locator("body").innerText()).length,
      casesBody: null as string | null,
      tagsBody: null as string | null,
    };
    if (!rendered) {
      const r1 = await page.request.get(BASE + "/api/v1/cases", { headers: { "X-Auth-Audience": "user" } });
      diagnostics.casesBody = (await r1.text()).slice(0, 300);
      const r2 = await page.request.get(BASE + "/api/v1/tags", { headers: { "X-Auth-Audience": "user" } });
      diagnostics.tagsBody = (await r2.text()).slice(0, 300);
      dump(testInfo, "r10-05-evidence.json", { diagnostics, network: net.filter((n) => n.url.includes("/api/")) });
      expect(rendered, "登录态下 legacy 下载页必须可渲染（cases/tags 为 null 时不得白屏）。diagnostics=" + JSON.stringify(diagnostics)).toBe(true);
      return;
    }

    await page.locator('input[aria-label="导入地址文件"]').setInputFiles(goodPath);
    await expect.poll(async () => textarea.inputValue(), { timeout: 30_000 }).toContain(ADDRESS);
    const value = await textarea.inputValue();
    const metaText = (await page.locator(".input-meta").first().innerText()).replace(/[ \t\r\n]+/g, " ").trim();
    expect(value.toLowerCase(), "XLSX 第二行地址也要带入").toContain(addr2);
    expect(metaText, "有效地址计数").toMatch(/2 个有效/);

    let negative: Record<string, unknown> = { dialogAppeared: false, dialogText: null, textareaAfter: null };
    await page.locator('input[aria-label="导入地址文件"]').setInputFiles(badPath);
    try {
      const alert = page.locator('[role="alertdialog"]');
      await expect(alert).toBeVisible({ timeout: 8_000 });
      negative = { dialogAppeared: true, dialogText: (await alert.innerText()).replace(/[ \t\r\n]+/g, " ").trim(), textareaAfter: await textarea.inputValue() };
      await page.getByRole("button", { name: "关闭" }).click();
    } catch {
      negative = { dialogAppeared: false, dialogText: null, textareaAfter: await textarea.inputValue() };
    }

    dump(testInfo, "r10-05-evidence.json", {
      diagnostics,
      fixtures: { good: { path: goodPath, sha256: sha256(goodBuf) }, broken: { path: badPath, sha256: sha256(badBuf) } },
      positive: { textarea: value, metaText },
      negative,
      legacyFormatGuard: negative.dialogAppeared
        ? "legacy 页面对损坏 xlsx 弹出了 NoticeDialog"
        : "legacy 页面没有 23.7 格式防线：损坏 xlsx 未弹窗（app-v2 已修，legacy 未修；只记录实测行为，不计为通过）",
      network: net.filter((n) => n.url.includes("/api/")),
    });
  });

  test("R10-07 登录态 null 列表回归：cases/tags 为 null 时 legacy 三个页面不得白屏", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "R10_EMAIL/R10_PASSWORD not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);
    await login(page, EMAIL, PASSWORD);

    const headers = { "X-Auth-Audience": "user" };
    const casesResp = await page.request.get(BASE + "/api/v1/cases", { headers });
    const casesBody = await casesResp.text();
    const tagsResp = await page.request.get(BASE + "/api/v1/tags", { headers });
    const tagsBody = await tagsResp.text();
    expect(casesResp.status(), "登录态 /api/v1/cases 必须成功").toBe(200);
    expect(tagsResp.status(), "登录态 /api/v1/tags 必须成功").toBe(200);

    const routes = [
      { name: "DownloadPage", url: "/", probe: "#download-addresses" },
      { name: "DataExplorerPage", url: "/data-explorer", probe: "[aria-label='数据筛选与导出']" },
      { name: "FundFlowPage", url: "/fund-flow", probe: "[aria-label='案件画布工具栏']" },
    ];
    const results: Record<string, unknown>[] = [];
    for (const route of routes) {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(String(e).slice(0, 300)));
      await page.goto(route.url, { waitUntil: "domcontentloaded" });
      let visible = true;
      try {
        await expect(page.locator(route.probe)).toBeVisible({ timeout: 30_000 });
      } catch {
        visible = false;
      }
      results.push({ route: route.name, url: route.url, probe: route.probe, rendered: visible, pageErrors: errors.slice(0, 3), bodyTextLength: (await page.locator("body").innerText()).length });
    }

    dump(testInfo, "r10-07-evidence.json", {
      api: { casesStatus: casesResp.status(), casesBody: casesBody.slice(0, 200), tagsStatus: tagsResp.status(), tagsBody: tagsBody.slice(0, 200) },
      casesIsNull: JSON.parse(casesBody).cases === null,
      tagsIsNull: JSON.parse(tagsBody).tags === null,
      routes: results,
    });

    const broken = results.filter((r) => !r.rendered || (r.pageErrors as string[]).length > 0);
    expect(broken, "登录态下 legacy 页面在 cases/tags 为 null 时必须全部正常渲染且无 pageerror").toEqual([]);
  });

  test("R10-08 导出作用域：跨工作区访问他区导出必须 404（不得泄露）", async ({ browser }, testInfo) => {
    test.skip(!VIEWER_EMAIL || !VIEWER_PASSWORD || !JOB_ID, "R10_VIEWER_EMAIL/R10_VIEWER_PASSWORD/R10_JOB_ID not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);

    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
    const viewer: Page = await context.newPage();
    const net: NetRecord[] = [];
    startCapture(viewer, net);
    try {
      await login(viewer, VIEWER_EMAIL, VIEWER_PASSWORD);
      const headers = { "Content-Type": "application/json", "X-Auth-Audience": "user" };

      // 工作区 B 的用户拿工作区 A 的 job_id 去创建导出：必须 404（requireJobScope 作用域校验）。
      const cross = await viewer.request.post(BASE + "/api/v2/exports", {
        headers: { ...headers, "X-CSRF-Token": (await viewer.context().cookies()).find((c) => c.name === "cs_user_session_csrf")?.value ?? "" },
        data: { job_id: JOB_ID, format: "xlsx" },
      });
      const crossBody = await cross.text();

      // 同一路径下的导出列表必须是空/不含该 job（作用域隔离）。
      const list = await viewer.request.get(BASE + "/api/v2/exports?limit=50", { headers: { "X-Auth-Audience": "user" } });
      const listData = unwrap(await list.text());
      const items = (listData.items as Record<string, unknown>[]) ?? [];
      const leaked = items.filter((i) => String(i.job_id ?? "") === JOB_ID);

      dump(testInfo, "r10-08-evidence.json", {
        job_id: JOB_ID,
        crossCreate: { status: cross.status(), body: crossBody.slice(0, 400), requestId: cross.headers()["x-request-id"] ?? null, traceId: cross.headers()["x-trace-id"] ?? null },
        list: { status: list.status(), itemCount: items.length, leakedJobItems: leaked.length },
        network: net.filter((n) => n.url.includes("/api/v2/")),
      });

      expect(cross.status(), "跨工作区用他区 job_id 创建导出必须 404").toBe(404);
      expect(leaked.length, "他区导出列表不得含本区 job 的导出").toBe(0);
    } finally {
      await context.close();
    }
  });
});
