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
 * v6（本文件）：补上 R10-03 的**创建请求证据**（唯一增量，其余逐字节与 v5 相同）。
 *   严格门（crosscheck v10）要求：pre-snapshot 中不存在的**每个** genuinely_new 导出，都必须记录
 *   UI 创建请求的 request_id/trace_id，以证明 audit_events_v2 的 export.completed 与创建是**同一次发生**。
 *   v5 轮里 R10-03 的导出 3d97973f 是真实新建，但没有 dump createHttp -> 严格门如实判 1 项 FAIL（证据缺口，
 *   不是产品缺陷）。v6 把 R10-03 的 createHttp/createToggles 一并落盘，闭合该缺口。
 *
 * v5（本文件）：v4 的**测量侧**更正 —— 由 v9 SELFTEST 轮的真实结果驱动，只改一处期望值。
 *   v4 实测（轮 r10-export-excelize-v9-SELFTEST-20260913T070254Z）暴露：我把"全新导出"的状态码
 *   写成 202（照 export_v2.go 的 writeJSON(w, 202, exp) 读出来的），但**真实测量值是 201**：
 *     r10-02 uc-desktop createHttp.status = 201（v8 轮与 v9 自测轮一致）
 *     r10-01 uc-desktop createHttp.status = 201（v9 自测轮的真实新建导出）
 *     r10-01 uc-mobile  createHttp.status = 200（复用同一个 export id）
 *   => v5 断言 201=新建、200=复用（**以实测为准，不以读码为准**）。其余逐字节与 v4 相同。
 *
 * v4（本文件）：R10 v9 施工单（1420 交接 §5）。
 *   v3 实测（轮 r10-export-excelize-v8-20260913T060647Z）暴露两个**测量侧**缺陷：
 *     V8-1 R10-01 的任务维度导出命中了服务端复用分支，复用的是 02:49 产出的**修复前**导出
 *          （f62e4308，早于 05:55 审计修复部署），因此它的 export.completed 永远只存在于 legacy 表
 *          —— 结构上无法观测修复后的新完成事件（不是产品回归，但也绝不能记 PASS）。
 *     V8-2 crosscheck 的 SELECT seq::text ... ORDER BY seq 被 PostgreSQL 解析为按**输出列**（text）排序。
 *   本版只做"让证据可被观测"的测试侧更正，不放宽任何断言：
 *     F1 createExportViaUi 支持通过 UI 真实勾选 include_meta / force_zip / include_decoded_input
 *        （三者都进入服务端 query_snapshot，因此可产生新的快照 -> 不再复用旧导出）；
 *     F2 R10-01 打开 include_meta，使任务维度导出**每轮真实新建**（首轮 202）；
 *     F3 新增 R10-09：**结构上不可能复用**的地址维度全新导出认证用例（附 export_v2.go 的静态依据），
 *        并落地 Export/Job/Request/Trace/文件 全部证据，供 crosscheck 做同一次完成事件的硬断言。
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
  __toggles: { includeMeta: boolean; forceZip: boolean; includeDecodedInput: boolean };
};

async function createExportViaUi(
  page: Page, net: NetRecord[],
  spec: {
    mode: "job" | "addresses"; jobId?: string; chain?: string; address?: string; format: string;
    toggles?: { includeMeta?: boolean; forceZip?: boolean; includeDecodedInput?: boolean };
  },
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
  // v4：把导出选项（include_meta / force_zip / include_decoded_input）**真的**通过 UI 勾选。
  // 这三个字段都会进入服务端 query_snapshot，因此打开其中任意一个都会产生**新的快照**，
  // 从而绕开 export_v2.go 的复用分支（复用要求快照逐字节相同）。
  const toggles = { includeMeta: false, forceZip: false, includeDecodedInput: false };
  const toggleSpec: Array<[keyof typeof toggles, string]> = [
    ["includeMeta", "include_meta"],
    ["forceZip", "force_zip"],
    ["includeDecodedInput", "include_decoded_input"],
  ];
  for (const [key, label] of toggleSpec) {
    if (spec.toggles?.[key] !== true) continue;
    const box = dlg.getByLabel(label);
    await expect(box, "导出选项 " + label + " 必须唯一存在").toHaveCount(1);
    if (!(await box.isChecked())) await box.check();
    expect(await box.isChecked(), label + " 必须真的被勾选").toBe(true);
    toggles[key] = true;
  }
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
    __toggles: toggles,
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

    // v4：任务维度导出必须**真实新建**。服务端复用要求 query_snapshot 逐字节相同；
    // 打开 include_meta 会改变快照（include_meta 在 snapshot 里），因此不会命中 02:49 那个修复前的旧导出。
    // 注意：本轮之后再次运行本 spec 时，桌面端会复用**本轮新建的**导出（它已有 v2 完成事件，
    // 仍可被硬断言证明），移动端的复用响应为 200 —— 因此这里不断言状态码，由证据+crosscheck 判定。
    const created = await createExportViaUi(page, net, { mode: "job", jobId: JOB_ID, format: "xlsx", toggles: { includeMeta: true } });
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
      createToggles: created.__toggles,
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
      export: detail,
      createHttp: created.__http,
      createToggles: created.__toggles,
      ticket, ticketHttp,
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

  test("R10-09 全新导出（不复用）完成认证：真实 UI 新建 -> 202 -> COMPLETED -> 文件落盘 -> SheetJS 读回", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.setTimeout(600_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page, EMAIL, PASSWORD);
    await openExports(page);
    const headers = await authHeaders(page);
    const dir = artifactsDir(testInfo);

    // 为什么本用例结构上**不可能复用**（静态依据，不是"跑一次看看"）：
    //   backend/internal/api/export_v2.go 的复用分支写在 "if job != nil { ... }" 之内，
    //   而 job 只在请求携带 job_id 时才被加载（var job *model.DownloadJob; if req.JobID != "" {...}）。
    //   地址维度导出不携带 job_id => job == nil => 复用分支不可达 => 每轮必然真实新建一行 exports。
    // 另叠加 include_meta=true（R10-02 未使用该快照），使快照唯一性不依赖单一路径。
    const created = await createExportViaUi(page, net, {
      mode: "addresses", chain: CHAIN, address: ADDRESS, format: "xlsx", toggles: { includeMeta: true },
    });
    const exportId = String(created.id);
    expect(
      created.__http.status,
      "全新导出必须走新建路径（201，实测值；v4 曾误写 202）。复用路径（200，export_v2.go 命中 query_snapshot 完全一致的历史导出）" +
      "不得作为本用例证据；它的完成事件属于**当初那次**创建，不是本轮。",
    ).toBe(201);
    expect(String(created.format)).toBe("xlsx");
    expect(created.zip, "单地址单数据集不强制 ZIP").toBe(false);
    expect(created.__toggles.includeMeta, "include_meta 必须真的通过 UI 勾选（快照唯一性来源之一）").toBe(true);

    const detail = await waitExportTerminal(page, headers, exportId);
    expect(String(detail.id), "导出详情必须就是本次新建的 export_id").toBe(exportId);
    expect(String(detail.status)).toBe("COMPLETED");

    const { saved, ticket, ticketHttp } = await downloadViaUi(page, net, dir, "r10-09-fresh-xlsx", exportId);
    expect(saved.bytes.length, "下载文件必须非空").toBeGreaterThan(0);
    const fileSha = sha256(saved.bytes);
    expect(fileSha, "下载文件 sha256 必须等于服务端记录 sha256（同一份对象）").toBe(String(detail.sha256));
    expect(saved.bytes.subarray(0, 2).toString("latin1"), "裸 xlsx 必须是 ZIP/OOXML 魔数").toBe("PK");

    const readback = await readWorkbook(saved.bytes, "addresses/<addr>/transactions.xlsx");
    expect(readback.orderCheck.ok, "表头必须符合服务端 exportFieldOrder：" + readback.orderCheck.violations.join("; ")).toBe(true);
    expect(readback.dataRowCount, "真实数据行数必须等于服务端 rows").toBe(Number(detail.rows));
    // include_meta=true 的真实后果（不是文案）：服务端 annotateRows 会给每行加三列。
    expect(
      readback.header,
      "include_meta=true 必须在表头真实产生案件/标签列（证明该 UI 选项真的进了服务端快照）",
    ).toEqual(expect.arrayContaining(["matched_address", "case_names", "tag_names"]));
    const party = [readback.firstRow.from_address, readback.firstRow.to_address].map((v) => String(v ?? "").toLowerCase());
    expect(party, "首行必须包含导出地址本身").toContain(ADDRESS.toLowerCase());
    const txHash = String(readback.firstRow.tx_hash ?? "");
    expect(txHash, "首行必须有真实 tx_hash").toMatch(/^0x[0-9a-f]{64}$/);

    dump(testInfo, "r10-09-evidence.json", {
      job: {
        id: null, job_no: null, chain: CHAIN,
        note: "地址维度导出（无 job 绑定）：export_v2.go 的复用分支位于 if job != nil 之内，对地址维度结构上不可达。",
      },
      address: ADDRESS, dataset: DATASET, chain: CHAIN,
      export: detail,
      createHttp: created.__http,
      createToggles: created.__toggles,
      responseShape: created.__shape,
      responseKeys: created.__keys,
      successDialogText: created.__dialogText,
      ticket, ticketHttp,
      file: { path: saved.file, bytes: statSync(saved.file).size, sha256: fileSha, apiSha256: detail.sha256 },
      workbook: readback,
      cellProbe: { column: "tx_hash", value: txHash },
      network: net.filter((n) => n.url.includes("/api/v2/")),
    });
  });
});
