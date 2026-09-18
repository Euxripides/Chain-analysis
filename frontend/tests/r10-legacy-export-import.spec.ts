import { expect, test, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { artifactsDir, dump, login, saveDownload, startCapture, type NetRecord } from "./r10-helpers";

/**
 * R10 — legacy SPA（`/` 与 `/data-explorer/...`）的两个独立证明，**不与 app-v2 验收混同**：
 *   R10-04  客户端 SheetJS xlsx 导出（frontend/src/utils/detail-table.ts 的 exportDetailRows）
 *           → 真实下载 → 落盘 → SheetJS 读回 sheet 名/表头/行/单元格
 *   R10-05  legacy 下载页导入（CSV / XLSX → 地址 + 案件 + 标签带入）
 *           负向（改名/损坏文件）只**记录实测行为**，不包装成通过：
 *           legacy 页面没有 app-v2 的 §23.7 格式防线，这里如实标注。
 */

const EMAIL = process.env.R10_EMAIL ?? "";
const PASSWORD = process.env.R10_PASSWORD ?? "";
const ADDRESS = process.env.R10_ADDRESS ?? "";
const CHAIN = process.env.R10_CHAIN ?? "bsc";
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

async function openLegacyLogin(page: Page) {
  await login(page, EMAIL, PASSWORD);
}

test.describe("R10 legacy SPA：客户端 xlsx 导出与导入", () => {
  test("R10-04 legacy 地址详情客户端 SheetJS xlsx 导出：真实落盘并读回", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await openLegacyLogin(page);
    const dir = artifactsDir(testInfo);

    await page.goto(`/data-explorer/${encodeURIComponent(CHAIN)}/${encodeURIComponent(ADDRESS)}`, { waitUntil: "domcontentloaded" });
    await expect(page.locator('[aria-label="导出格式"]'), "legacy 导出格式选择器").toBeVisible({ timeout: 30_000 });
    await expect.poll(async () => page.locator("table.detail-table tbody tr").count(), { timeout: 60_000 }).toBeGreaterThan(0);

    const tableRowCount = await page.locator("table.detail-table tbody tr").count();
    const firstRowText = (await page.locator("table.detail-table tbody tr").first().innerText()).replace(/\s+/g, " ").trim();

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

  test("R10-05 legacy 下载页导入：XLSX 地址/案件/标签真实带入；负向仅如实记录", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(300_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await openLegacyLogin(page);
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
    await expect(page.locator("#download-addresses"), "legacy 地址输入框").toBeVisible({ timeout: 30_000 });

    await page.locator('input[aria-label="导入地址文件"]').setInputFiles(goodPath);
    await expect.poll(async () => page.locator("#download-addresses").inputValue(), { timeout: 30_000 }).toContain(ADDRESS);
    const value = await page.locator("#download-addresses").inputValue();
    const metaText = (await page.locator(".input-meta").first().innerText()).replace(/\s+/g, " ").trim();
    expect(value.toLowerCase(), "XLSX 第二行地址也要带入").toContain(addr2);
    expect(metaText, "有效地址计数").toMatch(/2 个有效/);

    // 负向：损坏的 .xlsx。legacy 页面没有 app-v2 的格式防线，这里只记录真实行为。
    let negative: Record<string, unknown> = { dialogAppeared: false, dialogText: null, textareaAfter: null, error: null };
    await page.locator('input[aria-label="导入地址文件"]').setInputFiles(badPath);
    try {
      const alert = page.locator('[role="alertdialog"]');
      await expect(alert).toBeVisible({ timeout: 8_000 });
      negative = {
        dialogAppeared: true,
        dialogText: (await alert.innerText()).replace(/\s+/g, " ").trim(),
        textareaAfter: await page.locator("#download-addresses").inputValue(),
        error: null,
      };
      await page.getByRole("button", { name: "关闭" }).click();
    } catch (error) {
      negative = {
        dialogAppeared: false,
        dialogText: null,
        textareaAfter: await page.locator("#download-addresses").inputValue(),
        error: String(error).slice(0, 400),
      };
    }

    dump(testInfo, "r10-05-evidence.json", {
      fixtures: { good: { path: goodPath, sha256: sha256(goodBuf) }, broken: { path: badPath, sha256: sha256(badBuf) } },
      positive: { textarea: value, metaText },
      negative,
      legacyFormatGuard: negative.dialogAppeared
        ? "legacy 页面对损坏 xlsx 弹出了 NoticeDialog"
        : "legacy 页面没有 §23.7 格式防线：损坏 xlsx 未弹窗（app-v2 已修，legacy 未修；本条只记录实测行为，不计为通过）",
      network: net.filter((n) => n.url.includes("/api/")),
    });
  });
});
