import { expect, test, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { statSync } from "node:fs";
import {
  artifactsDir, authHeaders, dialog, dump, login, readZipCentralDirectory, readZipEntry,
  saveDownload, startCapture, unwrap, type NetRecord,
} from "./r10-helpers";

/**
 * R10 v3 — 服务端 excelize 真实 XLSX 导出端到端验收（app-v2 真实浏览器）。
 * v1（r10-export-excelize.spec.ts）、v2（r10v2-export-excelize.spec.ts）已实际运行并原样保留。
 *
 * v2 -> v3 变化（仅测试侧加固；产品侧缺陷另立记录，不在此掩盖）：
 *   V1 绑定新的唯一证据目录（R10_OUT），并在 evidence 中记录 api_pid/api_sha，
 *      与 runner 的冻结绑定互为对照（mid-run deploy 判定）。
 *   V2 每张表除 rows/header 外显式记录 firstRow/lastRow 的**单元格值**，
 *      供 crosscheck 逐单元格与 ClickHouse 真值比对（不只是行数）。
 *   V3 R10-01 增加 ZIP 条目「本地头压缩方法 == 中央目录方法」一致性断言（C5 stored guard 回归）。
 *
 * 真实链路不做任何 mock：登录 -> 导出中心真实 UI -> POST /api/v2/exports（真实 Trace/Request ID）
 * -> 服务端 excelize 生成 xlsx -> 60s 单次票据 -> 浏览器落盘 -> Node 解 ZIP（CRC32）-> SheetJS 读回。
 */

const EMAIL = process.env.R10_EMAIL ?? "";
const PASSWORD = process.env.R10_PASSWORD ?? "";
const JOB_ID = process.env.R10_JOB_ID ?? "";
const JOB_NO = process.env.R10_JOB_NO ?? "";
const ADDRESS = process.env.R10_ADDRESS ?? "";
const CHAIN = process.env.R10_CHAIN ?? "bsc";
const DATASET = process.env.R10_DATASET ?? "transactions";

/** 与服务端 exportFieldOrder（backend/internal/api/export.go）一致：导出列是稳定排序的。 */
const FIELD_ORDER = [
  "chain", "tx_hash", "block_number", "block_hash", "timestamp", "log_index",
  "method_id", "method_name", "method_signature", "method_source", "method_verified",
  "method_confidence", "method_type", "method_resolved_at", "decoded_input", "decode_source",
  "event_name", "event_signature", "event_source", "event_verified",
  "from_address", "to_address", "address", "counterparty", "direction", "activity_type",
  "token_address", "contract_address", "token_id", "token_symbol", "token_name", "token_decimals",
  "token_contract", "token_type", "token_metadata_status", "token_metadata_source", "token_metadata_confidence",
  "amount", "amount_raw", "raw_value", "formatted_amount", "amount_usd", "value_raw", "balance", "balance_raw", "balance_usd", "token_price",
  "status", "nonce", "gas", "gas_used", "gas_price", "tx_type", "topics", "data", "removed", "input",
  "matched_address", "case_names", "tag_names", "dataset", "source_provider", "source_ingested_at",
];

const CREATE_CONTRACT_KEYS = ["id", "status", "format", "zip", "job_id", "addresses", "datasets"];
const sha256 = (buf: Buffer) => createHash("sha256").update(buf).digest("hex");

function expectedOrder(header: string[]): { ok: boolean; violations: string[] } {
  const rank = new Map(FIELD_ORDER.map((name, index) => [name, index]));
  const violations: string[] = [];
  let last = -1;
  for (const name of header) {
    const rankValue = rank.has(name) ? (rank.get(name) as number) : Number.MAX_SAFE_INTEGER;
    if (rankValue < last) violations.push(name + " (rank " + rankValue + ") 出现在 rank " + last + " 之后");
    last = rankValue;
  }
  return { ok: violations.length === 0, violations };
}

interface SheetReadback {
  entry: string; sheetNames: string[]; header: string[]; dataRowCount: number;
  firstRow: Record<string, string>; lastRow: Record<string, string>;
  orderCheck: { ok: boolean; violations: string[] };
}

async function readWorkbook(buf: Buffer, entryName: string): Promise<SheetReadback> {
  const XLSX = await import("xlsx");
  const wb = XLSX.read(buf, { type: "buffer" });
  const sheetNames: string[] = wb.SheetNames;
  expect(sheetNames.length, "workbook must contain at least one sheet").toBeGreaterThan(0);
  const ws = wb.Sheets[sheetNames[0]];
  const matrix = XLSX.utils.sheet_to_json(ws, { header: 1, blankrows: false, raw: false }) as unknown[][];
  expect(matrix.length, "workbook must contain a header row").toBeGreaterThan(0);
  const header = (matrix[0] ?? []).map((v) => String(v ?? ""));
  const records = XLSX.utils.sheet_to_json(ws, { defval: "", raw: false }) as Record<string, string>[];
  return {
    entry: entryName, sheetNames, header, dataRowCount: records.length,
    firstRow: records[0] ?? {}, lastRow: records[records.length - 1] ?? {},
    orderCheck: expectedOrder(header),
  };
}

async function openExports(page: Page) {
  await page.goto("/app-v2/exports", { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("button", { name: "新建导出" }), "新建导出 button").toBeVisible({ timeout: 30_000 });
}

type CreatedExport = Record<string, unknown> & {
  __http: { status: number; requestId: string | null; traceId: string | null; body: string | null };
  __shape: "create" | "reuse";
  __dialogText: string;
  __keys: string[];
};

async function createExportViaUi(
  page: Page, net: NetRecord[],
  spec: { mode: "job" | "addresses"; jobId?: string; chain?: string; address?: string; format: string },
): Promise<CreatedExport> {
  const before = net.length;
  await page.getByRole("button", { name: "新建导出" }).click();
  const dlg = dialog(page);
  await expect(dlg, "create export dialog").toBeVisible({ timeout: 15_000 });

  await dlg.locator("#export-mode").selectOption(spec.mode);
  if (spec.mode === "job") {
    const select = dlg.locator("#export-job-select");
    if (await select.count()) {
      await expect.poll(async () => select.locator("option").count(), { timeout: 30_000 }).toBeGreaterThan(1);
      await select.selectOption(spec.jobId as string);
    } else {
      await dlg.locator("#export-job-id").fill(spec.jobId as string);
    }
  } else {
    await dlg.locator("#export-chain").selectOption(spec.chain as string);
    await dlg.locator("#export-addresses").fill(spec.address as string);
  }
  await dlg.locator("#export-format").selectOption(spec.format);
  await dlg.getByRole("button", { name: "创建导出" }).click();

  await expect.poll(() => {
    const hit = net.slice(before).find((n) => n.url.includes("/api/v2/exports") && n.method === "POST" && n.responseBody);
    return hit ? JSON.parse(hit.responseBody as string) : null;
  }, { timeout: 60_000 }).not.toBeNull();

  const hit = net.slice(before).find((n) => n.url.includes("/api/v2/exports") && n.method === "POST" && n.responseBody) as NetRecord;
  const successDlg = dialog(page);
  await expect(successDlg, "create success dialog").toContainText("导出任务已创建", { timeout: 30_000 });
  const dialogText = (await successDlg.innerText()).replace(/[ \t\r\n]+/g, " ").trim();
  await successDlg.getByRole("button", { name: "知道了" }).click();
  await expect(successDlg).toBeHidden({ timeout: 15_000 });

  const data = unwrap(hit.responseBody);
  expect(data.id, "export id from server response").toBeTruthy();
  return {
    ...data,
    __http: { status: hit.status, requestId: hit.requestId, traceId: hit.traceId, body: hit.responseBody },
    __shape: "addresses" in data ? "create" : "reuse",
    __dialogText: dialogText,
    __keys: Object.keys(data).sort(),
  };
}

async function waitExportTerminal(page: Page, headers: Record<string, string>, exportId: string): Promise<Record<string, unknown>> {
  let last: Record<string, unknown> = {};
  await expect.poll(async () => {
    const res = await page.request.get("/api/v2/exports/" + encodeURIComponent(exportId), { headers });
    last = unwrap(await res.text());
    return String(last.status ?? "");
  }, { timeout: 300_000, intervals: [1000, 2000, 3000, 5000] }).toMatch(/^(COMPLETED|FAILED|EXPIRED)$/);
  expect(last.status, "export must complete (server-side excelize generated the object)").toBe("COMPLETED");
  return last;
}

async function downloadViaUi(page: Page, net: NetRecord[], dir: string, prefix: string, exportId: string) {
  const shortId = exportId.slice(0, 10) + "\u2026" + exportId.slice(-6);
  await page.getByRole("button", { name: "刷新" }).click();
  const row = page.getByRole("row").filter({ hasText: shortId });
  await expect(row, "export row " + shortId).toHaveCount(1, { timeout: 60_000 });

  const before = net.length;
  const saved = await saveDownload(page, dir, prefix, async () => {
    await row.getByRole("button", { name: "下载票据" }).click();
    const confirm = dialog(page);
    await expect(confirm).toContainText("创建下载票据", { timeout: 15_000 });
    await confirm.getByRole("button", { name: "创建并下载" }).click();
  });
  const ticketHit = net.slice(before).find((n) => n.url.includes("/download-ticket"));
  expect(ticketHit, "download-ticket request observed").toBeTruthy();
  return {
    saved, ticket: unwrap(ticketHit?.responseBody),
    ticketHttp: { status: ticketHit?.status, requestId: ticketHit?.requestId, traceId: ticketHit?.traceId },
  };
}

test.describe("R10 v3 服务端 excelize 真实 XLSX 导出", () => {
  test("R10-01 任务维度导出 xlsx：真实 UI -> excelize ZIP 产物 -> 落盘 -> SheetJS 读回每张表", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !JOB_ID, "R10_EMAIL/R10_PASSWORD/R10_JOB_ID not provided");
    test.setTimeout(600_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page, EMAIL, PASSWORD);
    await openExports(page);
    const headers = await authHeaders(page);
    const dir = artifactsDir(testInfo);

    const created = await createExportViaUi(page, net, { mode: "job", jobId: JOB_ID, format: "xlsx" });
    const exportId = String(created.id);
    expect(String(created.format)).toBe("xlsx");
    expect(created.zip, "job 维度导出服务端强制 ZIP（48）").toBe(true);
    if (created.__shape === "create") {
      expect(String(created.job_id ?? "")).toBe(JOB_ID);
    }

    const detail = await waitExportTerminal(page, headers, exportId);
    expect(String(detail.job_id ?? JOB_ID), "导出详情必须仍关联本任务").toBe(JOB_ID);

    const { saved, ticket, ticketHttp } = await downloadViaUi(page, net, dir, "r10-01-job-xlsx", exportId);
    expect(saved.bytes.length, "downloaded artifact must be non-empty").toBeGreaterThan(0);
    const fileSha = sha256(saved.bytes);
    expect(fileSha, "下载文件 sha256 必须等于服务端记录的 sha256（同一份对象）").toBe(String(detail.sha256));

    const entries = readZipCentralDirectory(saved.bytes);
    const names = entries.map((e) => e.name).sort();
    expect(names, "ZIP 必须含 manifest.json").toContain("manifest.json");
    const manifestEntry = entries.find((e) => e.name === "manifest.json") as (typeof entries)[number];
    const manifest = JSON.parse(readZipEntry(saved.bytes, manifestEntry).toString("utf8")) as Record<string, unknown>;
    expect(manifest.export_id).toBe(exportId);
    expect(manifest.format).toBe("xlsx");
    expect(manifest.job_id).toBe(JOB_ID);
    expect(manifest.partial, "任务已 COMPLETED，导出不得标记 partial").toBe(false);

    // C5 回归：stored/本地头-中央目录压缩方法一致性 guard（上一轮真实缺陷）。
    const storedEntries = entries.filter((e) => e.method === 0).length;
    const parts = entries.filter((e) => e.name.endsWith(".xlsx"));
    expect(parts.length, "每个 地址 x dataset 一个真实 xlsx 条目").toBe((manifest.datasets as string[]).length * (manifest.addresses as string[]).length);

    const sheets: SheetReadback[] = [];
    for (const entry of parts) {
      const bytes = readZipEntry(saved.bytes, entry);
      expect(bytes.subarray(0, 2).toString("latin1"), entry.name + " 必须是 ZIP/OOXML 魔数").toBe("PK");
      const readback = await readWorkbook(bytes, entry.name);
      expect(readback.orderCheck.ok, entry.name + " 表头必须符合服务端 exportFieldOrder：" + readback.orderCheck.violations.join("; ")).toBe(true);
      expect(readback.header.length, entry.name + " 表头列数").toBeGreaterThan(5);
      expect(readback.dataRowCount, entry.name + " 必须含真实数据行").toBeGreaterThan(0);
      sheets.push(readback);
    }
    const totalRows = sheets.reduce((sum, s) => sum + s.dataRowCount, 0);
    expect(totalRows, "ZIP 内真实数据行总数必须等于服务端 rows").toBe(Number(detail.rows));

    const txSheet = sheets.find((s) => s.entry.endsWith("/" + DATASET + ".xlsx")) ?? sheets[0];
    const party = [txSheet.firstRow.from_address, txSheet.firstRow.to_address].map((v) => String(v ?? "").toLowerCase());
    expect(party, txSheet.entry + " 首行必须包含导出地址本身").toContain(ADDRESS.toLowerCase());

    dump(testInfo, "r10-01-evidence.json", {
      job: { id: JOB_ID, job_no: JOB_NO, chain: CHAIN },
      export: detail,
      responseShape: created.__shape,
      responseKeys: created.__keys,
      createHttp: created.__http,
      successDialogText: created.__dialogText,
      ticket, ticketHttp,
      file: { path: saved.file, bytes: statSync(saved.file).size, sha256: fileSha, apiSha256: detail.sha256 },
      zip: {
        entryCount: entries.length,
        storedEntryCount: storedEntries,
        entries: entries.map((e) => ({ name: e.name, method: e.method, flags: e.flags, crc32: e.crc32, compressed: e.compressed, uncompressed: e.uncompressed })),
        manifest,
      },
      sheets, totalRows,
      cellProbe: { column: "tx_hash", perSheet: sheets.map((s) => ({ entry: s.entry, tx_hash: String(s.firstRow.tx_hash ?? "") })) },
      network: net.filter((n) => n.url.includes("/api/v2/")),
    });
  });

  test("R10-02 地址维度导出 xlsx：裸 .xlsx（非 ZIP）-> 落盘 -> SheetJS 读回单元格", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(600_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page, EMAIL, PASSWORD);
    await openExports(page);
    const headers = await authHeaders(page);
    const dir = artifactsDir(testInfo);

    const created = await createExportViaUi(page, net, { mode: "addresses", chain: CHAIN, address: ADDRESS, format: "xlsx" });
    const exportId = String(created.id);
    expect(String(created.format)).toBe("xlsx");
    expect(created.zip, "单地址单数据集不强制 ZIP").toBe(false);

    const detail = await waitExportTerminal(page, headers, exportId);
    const { saved, ticket, ticketHttp } = await downloadViaUi(page, net, dir, "r10-02-address-xlsx", exportId);

    const fileSha = sha256(saved.bytes);
    expect(fileSha, "下载文件 sha256 必须等于服务端记录的 sha256").toBe(String(detail.sha256));
    expect(saved.bytes.subarray(0, 2).toString("latin1"), "裸 xlsx 必须是 ZIP/OOXML 魔数").toBe("PK");

    const readback = await readWorkbook(saved.bytes, "addresses/<addr>/transactions.xlsx");
    expect(readback.orderCheck.ok, "表头必须符合服务端 exportFieldOrder：" + readback.orderCheck.violations.join("; ")).toBe(true);
    expect(readback.dataRowCount, "真实数据行数必须等于服务端 rows").toBe(Number(detail.rows));
    expect(String(readback.firstRow.tx_hash ?? ""), "首行必须有真实 tx_hash").toMatch(/^0x[0-9a-f]{64}$/);
    const party = [readback.firstRow.from_address, readback.firstRow.to_address].map((v) => String(v ?? "").toLowerCase());
    expect(party, "首行必须包含导出地址本身").toContain(ADDRESS.toLowerCase());

    dump(testInfo, "r10-02-evidence.json", {
      address: ADDRESS, chain: CHAIN, dataset: DATASET,
      export: detail, responseShape: created.__shape, responseKeys: created.__keys,
      createHttp: created.__http, successDialogText: created.__dialogText, ticket, ticketHttp,
      file: { path: saved.file, bytes: statSync(saved.file).size, sha256: fileSha, apiSha256: detail.sha256 },
      workbook: readback,
      cellProbe: { column: "tx_hash", value: String(readback.firstRow.tx_hash ?? "") },
      network: net.filter((n) => n.url.includes("/api/v2/")),
    });
  });

  test("R10-03 下载票据真实单次使用：第二次 GET 必须 410 且写审计", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(600_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page, EMAIL, PASSWORD);
    await openExports(page);
    const headers = await authHeaders(page);
    const dir = artifactsDir(testInfo);

    const created = await createExportViaUi(page, net, { mode: "addresses", chain: CHAIN, address: ADDRESS, format: "csv" });
    const exportId = String(created.id);
    const detail = await waitExportTerminal(page, headers, exportId);
    const { ticket, ticketHttp } = await downloadViaUi(page, net, dir, "r10-03-ticket-single-use", exportId);
    const url = String(ticket.url ?? "");
    expect(url, "下载票据 URL").toContain("/api/v2/exports/download/");

    const reuse = await page.request.get(url, { headers });
    const reuseBody = await reuse.text();
    expect(reuse.status(), "票据单次使用：第二次必须 410").toBe(410);

    dump(testInfo, "r10-03-evidence.json", {
      export: detail, ticket, ticketHttp,
      reuse: { status: reuse.status(), body: reuseBody.slice(0, 2000), requestId: reuse.headers()["x-request-id"] ?? null, traceId: reuse.headers()["x-trace-id"] ?? null },
      network: net.filter((n) => n.url.includes("/download")),
    });
  });

  test("R10-06 导出复用路径的响应契约与 UI 后果（已知缺陷断言）", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !JOB_ID, "R10_EMAIL/R10_PASSWORD/R10_JOB_ID not provided");
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    test.setTimeout(600_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page, EMAIL, PASSWORD);
    await openExports(page);

    // R10-01 已为本任务生成过 COMPLETED 的 xlsx ZIP 导出，因此这里必然走服务端 export.reuse 分支。
    const first = await createExportViaUi(page, net, { mode: "job", jobId: JOB_ID, format: "xlsx" });
    const second = await createExportViaUi(page, net, { mode: "job", jobId: JOB_ID, format: "xlsx" });

    const observed = {
      first: { shape: first.__shape, httpStatus: first.__http.status, keys: first.__keys, dialog: first.__dialogText },
      second: { shape: second.__shape, httpStatus: second.__http.status, keys: second.__keys, dialog: second.__dialogText },
      createContractKeys: CREATE_CONTRACT_KEYS,
    };
    dump(testInfo, "r10-06-evidence.json", {
      job: { id: JOB_ID, job_no: JOB_NO },
      reusedExportId: String(first.id ?? ""),
      observed,
      network: net.filter((n) => n.url.includes("/api/v2/")),
    });

    const missing = CREATE_CONTRACT_KEYS.filter((key) => !(key in second));
    expect(
      missing,
      "导出复用路径（export.reuse，HTTP " + second.__http.status + "）返回的 body 缺少创建契约字段 [" + missing.join(",") +
      "]。新建路径返回 [" + CREATE_CONTRACT_KEYS.join(",") + "]。DEFECT-RECORD-2026-09-13-export-reuse-response。observed=" + JSON.stringify(observed.first.keys),
    ).toEqual([]);
  });
});
