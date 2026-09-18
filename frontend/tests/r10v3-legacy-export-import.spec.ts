import { expect, test, type Browser, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { artifactsDir, authHeaders, dialog, dump, login, saveDownload, startCapture, unwrap, type NetRecord } from "./r10-helpers";

/**
 * R10 v3 — legacy SPA、登录态列表契约回归、导出跨工作区作用域（v1/v2 文件原样保留）。
 *
 * v2 -> v3 变化（六类真实缺陷中的 3 类在此闭合，另 3 类在 crosscheck v4）：
 *   F1 R10-07 探针修正：`[aria-label='数据筛选与导出']` 只存在于地址详情页；/data-explorer 的真实探针是
 *      `[aria-label='地址目录筛选']`（DataExplorerPage.tsx:164，无条件渲染），/fund-flow 用
 *      `[aria-label='案件画布工具栏']`，/ 用 `#download-addresses`。
 *   F2 R10-07 契约层真值变更：服务端已修 nil slice -> []（backend/internal/api/v12.go），
 *      因此真实接口现在返回 []，**不能再靠真实接口制造 null**。v3 拆成三块并分别留证：
 *      (a) 真实接口契约：200 且 cases/tags 是数组（记录真实长度与原文）；
 *      (b) 真实浏览器：三个 legacy 页面在真实响应下渲染且无 pageerror；
 *      (c) **明确标注 injected** 的客户端防线回归：浏览器层拦截 /api/v1/cases|tags 返回
 *          {"cases":null}/{"tags":null}（修复前的服务端行为），三个页面仍必须渲染且无 pageerror。
 *          该块是故障注入，**不是**服务端证据，evidence 里 injected=true 原样记录。
 *   F3 R10-08 身份修正：改用**有 export:create 的跨工作区身份**（wsB，p1gate-inv-b），
 *      使作用域闸门独立成立（404），而不是被权限闸门（403）挡住；
 *      并新增只读身份（p1gate-view-b）的列表回归（itemCount 必须 = 0）
 *      与 wsA 导出明细 404 断言（旧 v3 轮 itemCount=5/leakedJobItems=1 作为修复前对照保留在旧证据里）。
 *      POST 必须带 Idempotency-Key（P1 轮真实 400 缺陷的回归）。
 */

const EMAIL = process.env.R10_EMAIL ?? "";
const PASSWORD = process.env.R10_PASSWORD ?? "";
const VIEWER_EMAIL = process.env.R10_VIEWER_EMAIL ?? "";
const VIEWER_PASSWORD = process.env.R10_VIEWER_PASSWORD ?? "";
const VIEWONLY_EMAIL = process.env.R10_VIEWONLY_EMAIL ?? "";
const VIEWONLY_PASSWORD = process.env.R10_VIEWONLY_PASSWORD ?? "";
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
  return { sheetNames, header: (matrix[0] ?? []).map((v) => String(v ?? "")), dataRowCount: records.length, firstRow: records[0] ?? {}, lastRow: records[records.length - 1] ?? {}, matrixRows: matrix.length };
}

const LEGACY_ROUTES = [
  { name: "DownloadPage", url: "/", probe: "#download-addresses" },
  { name: "DataExplorerPage", url: "/data-explorer", probe: "[aria-label='地址目录筛选']" },
  { name: "FundFlowPage", url: "/fund-flow", probe: "[aria-label='案件画布工具栏']" },
];

test.describe("R10 v3 legacy SPA：客户端 xlsx 导出、导入、列表契约与跨工作区作用域", () => {
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
      cellProbe: { column: "交易哈希", value: String(readback.firstRow["交易哈希"] ?? "") },
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

  test("R10-07 列表契约与 legacy 三页面渲染：真实 [] 契约 + injected null 客户端防线", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "R10_EMAIL/R10_PASSWORD not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);
    const pageErrors: string[] = [];
    page.on("pageerror", (e) => pageErrors.push(String(e).slice(0, 300)));
    await login(page, EMAIL, PASSWORD);

    const headers = { "X-Auth-Audience": "user" };
    const casesResp = await page.request.get(BASE + "/api/v1/cases", { headers });
    const casesBody = await casesResp.text();
    const tagsResp = await page.request.get(BASE + "/api/v1/tags", { headers });
    const tagsBody = await tagsResp.text();
    expect(casesResp.status(), "登录态 /api/v1/cases 必须成功").toBe(200);
    expect(tagsResp.status(), "登录态 /api/v1/tags 必须成功").toBe(200);

    const casesJson = JSON.parse(casesBody) as { cases?: unknown };
    const tagsJson = JSON.parse(tagsBody) as { tags?: unknown };
    const contract = {
      casesIsArray: Array.isArray(casesJson.cases),
      tagsIsArray: Array.isArray(tagsJson.tags),
      casesIsNull: casesJson.cases === null,
      tagsIsNull: tagsJson.tags === null,
      casesLength: Array.isArray(casesJson.cases) ? (casesJson.cases as unknown[]).length : -1,
      tagsLength: Array.isArray(tagsJson.tags) ? (tagsJson.tags as unknown[]).length : -1,
      casesBody: casesBody.slice(0, 200),
      tagsBody: tagsBody.slice(0, 200),
    };
    // 契约层真值：服务端已把 nil slice 序列化为 []；真实接口**不得**再返回 null。
    expect(contract.casesIsArray, "真实 /api/v1/cases 必须返回数组（服务端 nil slice -> []，不能是 null）。body=" + casesBody.slice(0, 200)).toBe(true);
    expect(contract.tagsIsArray, "真实 /api/v1/tags 必须返回数组（服务端 nil slice -> []，不能是 null）。body=" + tagsBody.slice(0, 200)).toBe(true);

    // (b) 真实响应下的三页面渲染
    const realRoutes: Record<string, unknown>[] = [];
    for (const route of LEGACY_ROUTES) {
      const errors: string[] = [];
      const onError = (e: Error) => errors.push(String(e).slice(0, 300));
      page.on("pageerror", onError);
      await page.goto(route.url, { waitUntil: "domcontentloaded" });
      let visible = true;
      try {
        await expect(page.locator(route.probe)).toBeVisible({ timeout: 30_000 });
      } catch {
        visible = false;
      }
      realRoutes.push({ route: route.name, url: route.url, probe: route.probe, rendered: visible, pageErrors: errors.slice(0, 3), bodyTextLength: (await page.locator("body").innerText()).length, injected: false });
      page.off("pageerror", onError);
    }

    const brokenReal = realRoutes.filter((r) => !r.rendered || (r.pageErrors as string[]).length > 0);
    expect(brokenReal, "登录态真实响应下 legacy 三个页面必须全部正常渲染且无 pageerror").toEqual([]);

    // (c) injected：浏览器层注入修复前的服务端行为（{"cases":null} / {"tags":null}），验证客户端 fail-closed 防线。
    await page.route("**/api/v1/cases**", (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ cases: null }) }));
    await page.route("**/api/v1/tags**", (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ tags: null }) }));
    const injectedRoutes: Record<string, unknown>[] = [];
    for (const route of LEGACY_ROUTES) {
      const errors: string[] = [];
      const onError = (e: Error) => errors.push(String(e).slice(0, 300));
      page.on("pageerror", onError);
      await page.goto("about:blank");
      await page.goto(route.url, { waitUntil: "domcontentloaded" });
      let visible = true;
      try {
        await expect(page.locator(route.probe)).toBeVisible({ timeout: 30_000 });
      } catch {
        visible = false;
      }
      injectedRoutes.push({ route: route.name, url: route.url, probe: route.probe, rendered: visible, pageErrors: errors.slice(0, 3), bodyTextLength: (await page.locator("body").innerText()).length, injected: true });
      page.off("pageerror", onError);
    }
    await page.unroute("**/api/v1/cases**");
    await page.unroute("**/api/v1/tags**");

    const brokenInjected = injectedRoutes.filter((r) => !r.rendered || (r.pageErrors as string[]).length > 0);

    dump(testInfo, "r10-07-evidence.json", {
      api: { casesStatus: casesResp.status(), casesBody: casesBody.slice(0, 200), tagsStatus: tagsResp.status(), tagsBody: tagsBody.slice(0, 200) },
      contract,
      casesIsNull: contract.casesIsNull,
      tagsIsNull: contract.tagsIsNull,
      routes: realRoutes,
      injected: { note: "浏览器层故障注入（拦截 /api/v1/cases|tags 模拟修复前服务端返回 null），仅验证客户端防线，不是服务端证据", routes: injectedRoutes, broken: brokenInjected },
      pageErrors,
    });

    expect(brokenInjected, "客户端 fail-closed 防线：服务端返回 null 时 legacy 三个页面仍必须渲染且无 pageerror").toEqual([]);
  });

  test("R10-08 导出作用域：跨工作区创建/明细必须 404，只读与可写身份列表必须为空", async ({ browser, page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !JOB_ID, "R10_EMAIL/R10_PASSWORD/R10_JOB_ID not provided");
    test.skip(!VIEWER_EMAIL || !VIEWER_PASSWORD, "R10_VIEWER_EMAIL/R10_VIEWER_PASSWORD (wsB with export:create) not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);

    const userHeaders = { "X-Auth-Audience": "user" };

    // 正向对照：wsA 调查员自己必须看得见导出行（否则「空列表」断言毫无意义）。
    await login(page, EMAIL, PASSWORD);
    const wsAHeaders = await authHeaders(page);
    const wsASession = unwrap(await (await page.request.get(BASE + "/api/auth/v1/session", { headers: userHeaders })).text());
    const wsAListResp = await page.request.get(BASE + "/api/v2/exports?limit=50", { headers: userHeaders });
    const wsAList = unwrap(await wsAListResp.text());
    const wsAItems = (wsAList.items as Record<string, unknown>[]) ?? [];
    expect(wsAItems.length, "wsA 调查员自己的导出列表必须非空（正向对照）").toBeGreaterThan(0);
    const wsAExportId = String(wsAItems[0].id ?? "");
    expect(wsAExportId, "wsA 导出 id").toMatch(/^[0-9a-f-]{36}$/);

    async function isolatedContext(email: string, password: string) {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
      const p: Page = await ctx.newPage();
      await login(p, email, password);
      const session = unwrap(await (await p.request.get(BASE + "/api/auth/v1/session", { headers: userHeaders })).text());
      return { ctx, p, session };
    }

    const writer = await isolatedContext(VIEWER_EMAIL, VIEWER_PASSWORD);
    const readonly = VIEWONLY_EMAIL && VIEWONLY_PASSWORD ? await isolatedContext(VIEWONLY_EMAIL, VIEWONLY_PASSWORD) : null;
    try {
      // (1) 可写跨工作区身份（有 export:create）：用 wsA 的 job 创建导出 -> 作用域闸门必须 404。
      const writerHeaders = await authHeaders(writer.p);
      const cross = await writer.p.request.post(BASE + "/api/v2/exports", {
        headers: { ...writerHeaders, "Idempotency-Key": "r10v3-xscope-" + Date.now() },
        data: { job_id: JOB_ID, format: "xlsx" },
      });
      const crossBody = await cross.text();
      const crossCreate = {
        status: cross.status(), body: crossBody.slice(0, 600),
        requestId: cross.headers()["x-request-id"] ?? null, traceId: cross.headers()["x-trace-id"] ?? null,
      };

      // (2) 同一身份读 wsA 导出明细 -> 404
      const detailResp = await writer.p.request.get(BASE + "/api/v2/exports/" + encodeURIComponent(wsAExportId), { headers: userHeaders });
      const writerDetail = { status: detailResp.status(), body: (await detailResp.text()).slice(0, 600), requestId: detailResp.headers()["x-request-id"] ?? null, traceId: detailResp.headers()["x-trace-id"] ?? null };

      // (3) 可写跨工作区身份的列表 -> 必须为空（修复前 v3 轮为 itemCount=5 / leakedJobItems=1）
      const writerListResp = await writer.p.request.get(BASE + "/api/v2/exports?limit=50", { headers: userHeaders });
      const writerListData = unwrap(await writerListResp.text());
      const writerItems = (writerListData.items as Record<string, unknown>[]) ?? [];
      const writerList = {
        status: writerListResp.status(), itemCount: writerItems.length,
        leakedJobItems: writerItems.filter((i) => String(i.job_id ?? "") === JOB_ID).length,
        leakedWsAIds: writerItems.filter((i) => wsAItems.some((w) => String(w.id) === String(i.id))).length,
        ids: writerItems.map((i) => String(i.id)),
        raw: JSON.stringify(writerItems).slice(0, 2000),
        requestId: writerListResp.headers()["x-request-id"] ?? null, traceId: writerListResp.headers()["x-trace-id"] ?? null,
      };

      let readonlyList: Record<string, unknown> = { skipped: true };
      let readonlyDetail: Record<string, unknown> = { skipped: true };
      if (readonly) {
        const rl = await readonly.p.request.get(BASE + "/api/v2/exports?limit=50", { headers: userHeaders });
        const rlData = unwrap(await rl.text());
        const rlItems = (rlData.items as Record<string, unknown>[]) ?? [];
        readonlyList = {
          status: rl.status(), itemCount: rlItems.length,
          leakedJobItems: rlItems.filter((i) => String(i.job_id ?? "") === JOB_ID).length,
          leakedWsAIds: rlItems.filter((i) => wsAItems.some((w) => String(w.id) === String(i.id))).length,
          ids: rlItems.map((i) => String(i.id)), raw: JSON.stringify(rlItems).slice(0, 2000),
          requestId: rl.headers()["x-request-id"] ?? null, traceId: rl.headers()["x-trace-id"] ?? null,
        };
        const rd = await readonly.p.request.get(BASE + "/api/v2/exports/" + encodeURIComponent(wsAExportId), { headers: userHeaders });
        readonlyDetail = { status: rd.status(), body: (await rd.text()).slice(0, 600), requestId: rd.headers()["x-request-id"] ?? null, traceId: rd.headers()["x-trace-id"] ?? null };
      }

      dump(testInfo, "r10-08-evidence.json", {
        job_id: JOB_ID,
        wsAExport: { id: wsAExportId, job_id: String(wsAItems[0].job_id ?? ""), itemCount: wsAItems.length, http: { status: wsAListResp.status() }, ids: wsAItems.map((i) => String(i.id)) },
        sessions: {
          investigator: { identity: EMAIL, organization_id: String(wsASession.organization_id ?? ""), workspace_id: String(wsASession.workspace_id ?? "") },
          writerB: { identity: VIEWER_EMAIL, organization_id: String(writer.session.organization_id ?? ""), workspace_id: String(writer.session.workspace_id ?? "") },
          readonlyB: readonly ? { identity: VIEWONLY_EMAIL, organization_id: String(readonly.session.organization_id ?? ""), workspace_id: String(readonly.session.workspace_id ?? "") } : { skipped: true },
        },
        crossCreate, writerDetail, list: writerList, readonlyDetail, readonlyList,
        network: [],
      });

      expect(crossCreate.status, "跨工作区用他区 job_id 创建导出必须 404（作用域闸门，非权限闸门）。body=" + crossBody.slice(0, 300)).toBe(404);
      expect(writerDetail.status, "跨工作区读取他区导出明细必须 404。body=" + String(writerDetail.body).slice(0, 300)).toBe(404);
      expect(writerList.itemCount, "跨工作区身份的导出列表必须为空（修复前实测 5 条，含 wsA 任务）").toBe(0);
      expect(writerList.leakedWsAIds, "列表不得含任何 wsA 导出 id").toBe(0);
      if (readonly) {
        expect(readonlyList.itemCount, "只读跨工作区身份的导出列表必须为空").toBe(0);
        expect(readonlyDetail.status, "只读跨工作区身份读取他区导出明细必须 404").toBe(404);
      }
    } finally {
      await writer.ctx.close();
      if (readonly) await readonly.ctx.close();
    }
  });
});
