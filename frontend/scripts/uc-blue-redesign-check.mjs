import { chromium, expect } from "@playwright/test";
import { mkdir, writeFile, readFile } from "node:fs/promises";
import { resolve } from "node:path";
process.env.UC_CHECK_PORT = "5407";
const F = await import("./uc-v4-render-check.mjs");
const out = resolve("acceptance-evidence/ui-blue-redesign-20261003");
await mkdir(out, { recursive: true });
const server = await F.startStaticServer();
const browser = await chromium.launch();
const base = "http://127.0.0.1:5407/app-v2";
const address = "0x1111111111111111111111111111111111111111";
const cp = "0x2222222222222222222222222222222222222222";
const contract = "0x55d398326f99059ff775485246999027b3197955";
const hash = "0x" + "b".repeat(64);
const exportId = "c523aa6e-2dcb-4e53-a49a-0b435208b010";
const total = { dataset: "token_transfers", token_address: contract, token_symbol: "USDT", token_decimals: 18, in_raw: "100000000000000000001", out_raw: "20000000000000000000", net_raw: "80000000000000000001", internal_raw: "0", self_raw: "0", event_count: 12, transaction_count: 11, missing_amount_rows: 0, failed_event_count: 0, first_seen: "2026-09-01T00:00:00Z", last_seen: "2026-09-30T00:00:00Z" };
const asset = { key: "asset|token_transfers|" + contract + "|18|", total };
const activity = Array.from({ length: 12 }, (_, i) => ({ chain: "bsc", address, counterparty: cp, direction: i % 2 ? "in" : "out", activity_type: "transfer", timestamp: "2026-09-28T12:00:00Z", block_number: 12345678, tx_hash: hash, token_address: contract, token_symbol: "USDT", token_decimals: 18, amount_raw: "1000000000000000001", log_index: i, dataset: "token_transfers", source_provider: "local_index" }));
const exportRow = { id: exportId, job_id: F.JOB_ID, dataset: "transactions+token_transfers", format: "csv", zip: true, status: "COMPLETED", rows: 12, bytes: 2048, sha256: "a".repeat(64), error_safe: "", object_ref: "exports/example.zip", created_at: "2026-09-30T12:00:00Z", updated_at: "2026-09-30T12:00:00Z", finished_at: "2026-09-30T12:00:00Z", expires_at: "2026-10-30T12:00:00Z", download_available: true };
const report = { source: "Built UI with API fixtures; backend/database acceptance is recorded separately", tests: [], errors: [], screenshots: [] };
async function install(page, state) {
  await page.route("**/api/**", async route => {
    const request = route.request(), u = new URL(request.url()), path = u.pathname;
    state.requests.push({ path, query: u.search, method: request.method(), body: request.postDataJSON?.(), csrf: request.headers()["x-csrf-token"], idempotencyKey: request.headers()["idempotency-key"] });
    let data = {}, next = null;
    if (path.endsWith("/logo")) return route.fulfill({ status: 404, body: "" });
    if (path === "/api/auth/v1/session") data = { ...F.SESSION, permissions: [...F.SESSION.permissions, "export:create", "export:read", "export:download"] };
    else if (path === "/api/v2/meta/chains") data = {items:[...F.DOWNLOAD_CHAINS.items,{slug:"bsc",name:"BNB Chain",enabled:true},{slug:"solana",name:"Solana",enabled:true}]};
    else if (path === "/api/v1/cases") return route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({cases:[{id:"case-qa",name:"验收案件"}]})});
    else if (path === "/api/v1/tags") return route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({tags:[{id:"tag-qa",name:"验收标签"}]})});
    else if (path === "/api/v2/meta/datasets") data = F.DOWNLOAD_DATASETS;
    else if (path === "/api/v2/jobs") data = { items: [F.JOB_ROW] };
    else if (path === "/api/v2/notifications") data = [];
    else if (path === "/api/v2/downloads/validate-addresses") data = F.VALIDATE_RESULT;
    else if (path === "/api/v2/downloads/preview") { state.previewCalls++; data = F.PREVIEW; }
    else if (path === "/api/v2/downloads") { state.creates.push(request.postDataJSON()); data = { job_id: F.JOB_ID, job_no: "DL-QA", status: "planned", created:true, reused: false, provider_work_required:true,coverage:0.75 }; }
    else if (path.includes("/files/") && path.endsWith("/download-ticket")) {state.fileTickets++;data={url:"/api/v2/exports/download/ui-file-ticket"};}
    else if (path === "/api/v2/exports/download/ui-file-ticket") return route.fulfill({status:200,contentType:"text/csv",headers:{"content-disposition":"attachment; filename=token_transfers.csv"},body:`tx_hash,amount_raw\n${hash},100000000000000000001\n`});
    else if (path === "/api/v2/exports" && request.method() === "POST") { state.exports.push(request.postDataJSON()); data = { id: exportId, status: "QUEUED", format: "csv", zip: true, datasets: ["token_transfers"], addresses: 1 }; }
    else if (path === "/api/v2/exports") { data = { items: u.searchParams.get("q") === "no-match" ? [] : [exportRow] }; }
    else if (path === `/api/v2/exports/${exportId}`) data = { ...exportRow, files: [{ dataset: "token_transfers", name: "token_transfers.csv", rows: 12, bytes: 1850, sha256: "b".repeat(64), download_available: true }] };
    else if (path.endsWith("/overview")) data = { subject: { type: "address", chain: "bsc", address, normalized_address: address }, summary: { transaction_count: 11, token_transfer_count: 12, counterparty_count: 2, first_seen: total.first_seen, last_seen: total.last_seen, current_balance: { observed_at: total.last_seen, items: [{ token_symbol: "BNB", token_address: "", raw_amount: "120000000000000001", decimals: 18 }] }, historical_net_flow: { items: [] } }, coverage: { items: [{ dataset: "token_transfers", status: "complete", from_timestamp: total.first_seen, to_timestamp: total.last_seen }] }, source: { provider: "local_index", as_of: total.last_seen }, caliber: {} };
    else if (path.endsWith("/activity")) { data = { items: activity }; next = "page-two"; }
    else if (path.endsWith("/statistics")) data = { statistics: { filter: { datasets: ["token_transfers"], as_of: "2026-10-01T00:00:00Z", interval: "day", timezone: "Asia/Shanghai" }, data_version: "fixture-version", transaction_count: 11, event_count: 12, counterparty_count: 2, first_seen: total.first_seen, last_seen: total.last_seen, assets: [asset], trend: [{ key: "trend-1", bucket: "2026-09-28T00:00:00+08:00", total }, { key: "trend-2", bucket: "2026-09-29T00:00:00+08:00", total }], hours: [], distribution: [], addresses: [{ key: "address-1", address, total }], counterparties: [{ key: "cp-1", counterparty: cp, share_percent: "50.000000", common_addresses: [address], total }], top_transfers: [] }, source: { provider: "local_index", as_of: total.last_seen }, coverage: [{ address, coverage: { items: [] } }], calculation: {} };
    else if (path.includes("/blocks/")) data = { summary: { block_number: 12345678, block_hash: hash, timestamp: total.last_seen }, items: [{ tx_hash: hash, from_address: address, to_address: cp, block_number: 12345678, block_hash: hash, timestamp: total.last_seen, value_raw: "1000000000000000000" }], source: "local_index" };
    else if (path.includes("/tokens/") && path.endsWith("/transfers")) data = { items: [{ tx_hash: hash, log_index: 1, from_address: address, to_address: cp, block_number: 12345678, timestamp: total.last_seen, token_symbol: "USDT", token_decimals: 18, amount_raw: "1000000000000000001" }] };
    else if (path.includes("/tokens/")) data = { metadata: { name: "Tether USD", symbol: "USDT", decimals: 18, verified: true, metadata_source: "catalog", last_resolved_at: total.last_seen } };
    else if (path.includes("/transactions/")) data = { items: { tx_hash: hash, transaction: { tx_hash: hash, block_number: 12345678, block_hash: hash, timestamp: total.last_seen, from_address: address, to_address: cp, value_raw: "1000000000000000000", status: 1 } }, source: {} };
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ success: true, data, meta: { request_id: "ui-blue-qa", trace_id: "ui-blue-qa", next_cursor: next, server_time: "2026-10-03T00:00:00Z" } }) });
  });
}
async function shot(page, name, width) { await page.evaluate(async () => { await document.fonts.ready; await Promise.race([Promise.all(document.getAnimations().filter(a => a.effect?.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {}))), new Promise(resolve => setTimeout(resolve, 1500))]); }); const path = resolve(out, `${name}-${width}.png`); await page.screenshot({ path, fullPage: false }); report.screenshots.push(path); }
try {
  for (const width of [1440, 390]) {
    const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 900 }, locale: "zh-CN" });
    const page = await context.newPage(); const state = { requests: [], exports: [], creates: [], previewCalls: 0, fileTickets:0 };
    page.on("pageerror", error => report.errors.push(`${width}: ${error.message}`)); await install(page, state);
    await context.addCookies([{ name: "cs_user_session_csrf", value: F.SESSION.csrf_token, url: "http://127.0.0.1:5407" }]);
    const check = async name => { expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), name + " overflow").toBeLessThanOrEqual(1); report.tests.push({ name, width, status: "PASS" }); };
    await page.goto(base + "/downloads/new", { waitUntil: "networkidle" });
    await expect(page.locator(".cs-download-layout")).toBeVisible(); await check("download-layout"); await shot(page, "download", width);
    await page.getByRole("button", { name: "高级设置" }).click(); await expect(page.getByText("优先复用本地", { exact: true })).toBeVisible(); await page.getByRole("button", { name: "高级设置" }).click();
    await page.locator("#download-addresses").fill(F.ADDRESS_INPUT);
    await page.getByRole("button",{name:"解析导入",exact:true}).click();
    await page.getByRole("button",{name:"使用服务端结果",exact:true}).click();
    await expect(page.getByTestId("server-verdict-summary")).toContainText("1 / 100");
    expect(state.requests.some(r=>r.path==="/api/v2/meta/datasets"&&new URLSearchParams(r.query).get("chain")==="tron")).toBe(true);
    await page.getByRole("button",{name:"预览数据覆盖与成本",exact:true}).click();
    await expect(page.getByRole("dialog")).toContainText("Provider 配额接近上限");
    await page.getByRole("button",{name:"知道了",exact:true}).click();
    await page.getByRole("button",{name:"高级设置"}).click();
    await page.locator(".ant-select:has(#download-performance) .ant-select-selector").click();
    await page.getByText("快速",{exact:true}).last().click();
    await expect(page.locator(".cs-download-summary")).toContainText("已失效");
    await page.getByRole("button",{name:/创建下载任务/}).click();
    await expect(page.getByRole("dialog")).toContainText("需要先完成真实预览");
    expect(state.creates).toHaveLength(0);
    await page.getByRole("button",{name:"知道了",exact:true}).click();
    await expect(page.getByRole("dialog")).toContainText("Provider 配额接近上限");
    await page.getByRole("button",{name:"知道了",exact:true}).click();
    await page.getByRole("button",{name:/创建下载任务/}).click();
    await expect(page.getByRole("dialog")).toContainText("确认创建下载任务");
    expect(state.creates).toHaveLength(0);
    await page.getByRole("button",{name:"取消",exact:true}).click();
    expect(state.creates).toHaveLength(0);
    await page.getByRole("button",{name:/创建下载任务/}).click();
    await page.getByRole("button",{name:"确认创建",exact:true}).click();
    await expect(page.getByRole("dialog")).toContainText("下载任务已创建");
    expect(state.creates).toHaveLength(1);
    const createdRequest=state.requests.find(r=>r.path==="/api/v2/downloads");
    expect(createdRequest.csrf).toBe(F.SESSION.csrf_token);expect(createdRequest.idempotencyKey).toBeTruthy();
    expect(createdRequest.body.create_missing_meta).toBe(false);
    await expect.poll(()=>page.evaluate(()=>sessionStorage.getItem("cs.download-draft.v1:sess-1:org-1:ws-1"))).toBe(null);
    await page.keyboard.press("Escape");
    report.tests.push({name:"download-validate-stale-preview-confirm-csrf-idempotency",width,status:"PASS"});
    await page.goto(base + "/exports", { waitUntil: "networkidle" });
    await expect(page.locator(".cs-export-filter-form")).toBeVisible(); await expect(page.locator("#export-addresses")).toHaveCount(0); await check("export-list-first"); await shot(page, "exports", width);
    await page.getByRole("button", { name: "新建导出", exact: true }).click(); const dialog = page.getByRole("dialog").last(); await expect(dialog.locator("#export-addresses")).toBeVisible(); await shot(page, "export-dialog", width); await page.keyboard.press("Escape"); await expect(dialog).not.toBeVisible();
    if(width===390) await page.getByRole("button",{name:"文件清单",exact:true}).click();
    else await page.locator(".ant-table-row-expand-icon").first().click();
    await expect(page.getByText("token_transfers.csv",{exact:true})).toBeVisible();
    expect(state.fileTickets).toBe(0);
    await (width === 390 ? page.getByRole("dialog").getByRole("button",{name:"下载文件",exact:true}) : page.getByRole("button",{name:"下载文件",exact:true})).click();
    await expect(page.getByRole("dialog").last()).toContainText("下载数据集文件");
    expect(state.fileTickets).toBe(0);
    const downloading=page.waitForEvent("download");
    await page.getByRole("button",{name:"下载",exact:true}).last().click();
    const file=await downloading;const filePath=resolve(out,`ui-token-transfers-${width}.csv`);await file.saveAs(filePath);
    expect((await readFile(filePath,"utf8"))).toContain("100000000000000000001");
    expect(state.fileTickets).toBe(1);
    report.tests.push({name:"export-file-list-confirm-bound-ticket-browser-download",width,status:"PASS"});
    const search = page.getByPlaceholder("输入导出 ID、任务编号"); await search.fill("no-match"); await search.press("Enter"); await expect(page.getByText("没有符合筛选条件的导出")).toBeVisible(); expect(state.requests.some(request => request.path === "/api/v2/exports" && request.query.includes("q=no-match"))).toBe(true); report.tests.push({ name: "export-server-search", width, status: "PASS" });
    await page.goto(base + `/addresses/bsc/${address}?tab=transfers`, { waitUntil: "networkidle" }); await expect(page.locator(".cs-query-full-address")).toContainText(address); await check("query-layout"); await shot(page, "query", width);
    expect(state.requests.some(request => request.path.endsWith("/activity") && request.query.includes("dataset=token_transfers"))).toBe(true);
    await page.getByRole("button", { name: "地址二维码" }).click(); await expect(page.getByRole("dialog")).toBeVisible(); await shot(page, "query-qr", width); await page.keyboard.press("Escape");
    const beforeStatistics = state.requests.length;
    await page.getByRole("link", { name: "统计分析", exact: true }).click(); await expect(page.locator(".cs-statistics-summary")).toContainText("12"); await check("statistics-layout"); await shot(page, "statistics", width);
    expect(state.requests.slice(beforeStatistics).some(request => request.path.endsWith("/overview") || request.path.endsWith("/counterparties"))).toBe(false);
    await expect(page.getByRole("button", { name: "数据覆盖", exact: true })).toBeVisible();
    report.tests.push({name:"statistics-no-duplicate-global-query-or-assumed-zero-coverage",width,status:"PASS"});
    await page.getByRole("button", { name: "查看交易", exact: true }).first().click(); await expect(page).toHaveURL(new RegExp("counterparty=" + cp)); await expect.poll(() => state.requests.some(request => request.path.endsWith("/activity") && request.query.includes("counterparty=" + cp))).toBe(true); report.tests.push({ name: "statistics-drilldown", width, status: "PASS" });
    const exportScope=new URLSearchParams({chain:"bsc",address,addresses:address+",0x3333333333333333333333333333333333333333",datasets:"token_transfers",from_time:"2026-09-01T00:00:00Z",to_time:"2026-09-30T23:59:59Z",as_of:"2026-10-01T00:00:00Z",token:contract,direction:"in",counterparty:cp,from_address:cp,to_address:address,hide_zero:"1",min_amount:"0.000000000000000001",max_amount:"100.000000000000000001",timezone:"Asia/Shanghai",interval:"day"});
    await page.goto(base+"/exports?"+exportScope,{waitUntil:"networkidle"});
    await expect(page.getByRole("dialog").locator("#export-addresses")).toHaveValue(new RegExp(address));
    await page.getByRole("button",{name:"创建导出",exact:true}).click();
    await expect(page.getByRole("dialog")).toContainText("导出任务已创建");
    const exported=state.exports.at(-1);expect(exported.file_layout).toBe("dataset");expect(exported.addresses).toHaveLength(2);
    for(const key of ["from_time","to_time","as_of","token","direction","counterparty","from_address","to_address","min_amount","max_amount","timezone","interval"]) expect(exported.filters[key],key).toBe(exportScope.get(key));
    expect(exported.filters.hide_zero).toBe(true);
    report.tests.push({name:"export-complete-filter-snapshot-address-group-and-precision",width,status:"PASS"});
    await page.keyboard.press("Escape");
    await page.goto(base + "/blocks/bsc/12345678", { waitUntil: "networkidle" }); await check("block-layout"); await shot(page, "block", width);
    await page.goto(base + `/tokens/bsc/${contract}`, { waitUntil: "networkidle" }); await check("token-layout"); await shot(page, "token", width);
    await context.close();
  }
  expect(report.errors).toEqual([]); report.status = "PASS";
} catch (error) { report.status = "FAIL"; report.errors.push(error.stack || String(error)); for (const context of browser.contexts()) for (const page of context.pages()) { await page.screenshot({path: resolve(out, "failure.png")}); report.debug = await page.evaluate(() => ({focus: document.activeElement?.outerHTML, dialogs: [...document.querySelectorAll('[role=dialog]')].map(e => ({text:e.textContent, rect: e.getBoundingClientRect().toJSON(), style: {opacity:getComputedStyle(e).opacity, z:getComputedStyle(e).zIndex}})), bodyStyle: getComputedStyle(document.body).fontFamily})); } process.exitCode = 1; }
finally { await browser.close(); server.close(); await writeFile(resolve(out, "browser-report.json"), JSON.stringify(report, null, 2)); console.log(JSON.stringify({ status: report.status, tests: report.tests, errors: report.errors }, null, 2)); }
