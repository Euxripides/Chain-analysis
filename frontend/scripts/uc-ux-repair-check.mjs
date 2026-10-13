#!/usr/bin/env node
/** 用户端 UX 修复的交互验收：生产构建页面 + 真实 Dialog 组件。API 夹具与真实 PostgreSQL 查询测试分开记录。 */
import { chromium, expect } from "@playwright/test";
import { createServer as createViteServer } from "vite";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

process.env.UC_CHECK_PORT = process.env.UC_CHECK_PORT ?? "5402";
const F = await import("./uc-v4-render-check.mjs");
const frontend = resolve(fileURLToPath(new URL("..", import.meta.url)));
const outArg = process.argv.indexOf("--out");
const out = resolve(frontend, outArg >= 0 ? process.argv[outArg + 1] : "acceptance-evidence/ui-ux-repair-20261002");
const origin = `http://127.0.0.1:${process.env.UC_CHECK_PORT}`;
const report = { at: new Date().toISOString(), apiMode: "browser fixtures; backend filters separately verified with isolated PostgreSQL", cases: [], errors: [], expectedErrors: [] };
const ok = (viewport, name) => report.cases.push({ viewport, name, result: "PASS" });
const sleep = ms => new Promise(r => setTimeout(r, ms));
await mkdir(out, { recursive: true });
const server = await F.startStaticServer();
const vite = await createViteServer({
  configFile: resolve(frontend, "apps/user-console/vite.config.ts"),
  root: frontend,
  base: "/",
  server: { port: 5403, strictPort: true, host: "127.0.0.1" },
  plugins: [{ name: "dialog-qa-page", configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (!req.url?.startsWith("/__dialog-qa")) return next();
      const html = await server.transformIndexHtml(req.url, `<!doctype html><html><head><link rel="icon" href="data:,"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module" src="/scripts/fixtures/user-dialog-harness.tsx"></script></body></html>`);
      res.setHeader("Content-Type", "text/html"); res.end(html);
    });
  } }],
});
await vite.listen();
const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL ?? "msedge" });

async function measureDialog(page, vp) {
  await expect(page.getByRole("dialog")).toHaveCSS("opacity", "1");
  await sleep(260);
  const bounds = await page.locator(".cs-dialog .ant-modal-content").evaluate(el => {
    const b = el.getBoundingClientRect();
    const footer = el.querySelector(".cs-dialog__foot").getBoundingClientRect();
    const body = el.querySelector(".cs-dialog__body");
    return { left: b.left, right: b.right, top: b.top, bottom: b.bottom, footerBottom: footer.bottom, overflow: el.scrollWidth - el.clientWidth, bodyScrollable: body.scrollHeight > body.clientHeight };
  });
  expect(bounds.left).toBeGreaterThanOrEqual(15);
  expect(bounds.right).toBeLessThanOrEqual(vp.width - 15);
  expect(bounds.top).toBeGreaterThanOrEqual(0);
  expect(bounds.bottom).toBeLessThanOrEqual(vp.height);
  expect(bounds.footerBottom).toBeLessThanOrEqual(vp.height);
  expect(bounds.overflow).toBeLessThanOrEqual(1);
  return bounds;
}
async function noPageOverflow(page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
}
async function installApi(page, state) {
  const rows = Array.from({ length: 75 }, (_, i) => ({ ...F.JOB_ROW, id: `job-${i}`, job_no: i === 70 ? "DL-SEARCH-OUTSIDE-FIRST-PAGE" : `DL-QA-${String(i).padStart(3, "0")}`, created_at: "2026-09-28T12:00:00Z", updated_at: "2026-09-28T12:00:00Z" }));
  const envelope = (data, next = null) => JSON.stringify({ success: true, data, meta: { request_id: "ui-qa", trace_id: "ui-qa", server_time: new Date().toISOString(), next_cursor: next } });
  await page.route("**/api/**", async route => {
    const req = route.request(), u = new URL(req.url()), path = u.pathname;
    state.requests.push({ path, query: u.search, method: req.method() });
    let data = {}, next = null;
    if (path === "/api/auth/v1/session") data = { ...F.SESSION, workspace_id: state.workspace ?? F.SESSION.workspace_id, permissions: state.permissions ?? [...F.SESSION.permissions, "job:list:workspace", "canvas:create"] };
    else if (path === "/api/v2/dashboard") data = F.DASHBOARD;
    else if (path === "/api/v2/jobs") {
      let filtered = rows;
      const q = u.searchParams.get("q");
      if (q) filtered = filtered.filter(r => (r.job_no + " " + r.id + " " + r.datasets.join(" ")).toLowerCase().includes(q.toLowerCase()));
      const from = u.searchParams.get("created_from"), to = u.searchParams.get("created_to");
      if (from) filtered = filtered.filter(r => r.created_at >= from);
      if (to) filtered = filtered.filter(r => r.created_at <= to);
      const offset = Number(u.searchParams.get("cursor") ?? 0), limit = Number(u.searchParams.get("limit") ?? 50);
      next = offset + limit < filtered.length ? String(offset + limit) : null;
      data = { items: filtered.slice(offset, offset + limit), next_cursor: next, status_counts: { RUNNING: filtered.length } };
    } else if (/\/jobs\/[^/]+\/events$/.test(path)) return route.fulfill({ status: 200, contentType: "text/event-stream", body: "" });
    else if (/\/jobs\/[^/]+$/.test(path)) data = F.DETAIL;
    else if (path === "/api/v2/cases") data = F.CASE_ROWS;
    else if (path === "/api/v2/investigations") data = F.INVESTIGATION_ROWS;
    else if (/\/investigations\/[^/]+\/canvases$/.test(path)) data = { items: [{ ...F.CANVAS_RECORD, case_id: "case-0001", investigation_id: "inv-0001", created_at: new Date().toISOString(), updated_at: new Date().toISOString() }] };
    else if (/\/investigations\/[^/]+\/(findings|evidence)$/.test(path)) data = [];
    else if (/\/investigations\/[^/]+\/runtime$/.test(path)) data = { runs: [] };
    else if (path === "/api/v2/inbox") data = [];
    else if (path === "/api/v2/notifications") data = [];
    else if (path === "/api/v2/meta/chains") data = F.DOWNLOAD_CHAINS;
    else if (path === "/api/v2/meta/datasets") data = F.DOWNLOAD_DATASETS;
    else if (path === "/api/v2/downloads/validate-addresses") {
      state.validations++;
      await sleep(state.delay ?? 250);
      if (state.validationError) return route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ success: false, error: { code: "AUTH007", message: "当前工作区没有下载权限" }, meta: { request_id: "ui-qa", trace_id: "trace-denied" } }) });
      data = state.verdict ?? F.VALIDATE_RESULT;
    } else if (path === "/api/v2/downloads/preview") { state.previews++; data = F.PREVIEW; }
    else if (path === "/api/v2/downloads" && req.method() === "POST") {
      state.creates = [...(state.creates ?? []), { payload: req.postDataJSON(), idempotencyKey: req.headers()["idempotency-key"], csrf: req.headers()["x-csrf-token"] }];
      data = { status: "planned", created: true, reused: false, provider_work_required: true, coverage: 0.75, job_id: "job-created", job_no: "DL-CREATED" };
    }
    else if (path === "/api/v2/canvases") data = { items: [F.CANVAS_RECORD] };
    else if (/\/canvases\/[^/]+$/.test(path)) data = F.CANVAS_RECORD;
    else if (/\/addresses\/[^/]+\/[^/]+\/overview$/.test(path)) data = {};
    else if (/\/addresses\/[^/]+\/[^/]+\/activity$/.test(path)) {
      const offset = Number(u.searchParams.get("cursor") ?? 0), limit = Number(u.searchParams.get("limit") ?? 200), count = state.activityCount ?? 0;
      data = { items: Array.from({ length: Math.max(0, Math.min(limit, count - offset)) }, (_, i) => ({ chain: "tron", address: F.ADDRESS_INPUT, counterparty: "TXk9Kv1QzB7mN4pR2sD8fG3hJ6kL0aZ5yU", direction: "out", activity_type: "transaction", timestamp: "2026-09-28T12:00:00Z", block_number: offset + i, tx_hash: (offset + i).toString(16).padStart(64, "0"), amount_raw: "1000000", dataset: "transactions", source_provider: "local" })) };
      next = offset + limit < count ? String(offset + limit) : null;
      if (u.searchParams.get("full") === "1") state.bulkPages = (state.bulkPages ?? 0) + 1;
    }
    else if (/\/addresses\/[^/]+\/[^/]+\//.test(path)) data = { items: [] };
    else if (path === "/api/v2/search") data = { query: u.searchParams.get("q"), items: state.searchHits ?? [{ type: "job", id: "job-70", title: "目标任务", subtitle: "DL-SEARCH-OUTSIDE-FIRST-PAGE", url: "/jobs/job-70" }], groups: {}, scopes: [], caliber: {} };
    await route.fulfill({ status: 200, contentType: "application/json", body: envelope(data, next) });
  });
}
try {
  for (const vp of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    const name = `${vp.width}x${vp.height}`;
    const context = await browser.newContext({ viewport: vp, deviceScaleFactor: 1 });
    await context.addCookies([{ name: "cs_user_session_csrf", value: F.SESSION.csrf_token, url: origin }]);
    const page = await context.newPage();
    const state = { requests: [], validations: 0, previews: 0 };
    page.setDefaultTimeout(10_000);
    page.on("pageerror", e => report.errors.push({ viewport: name, type: "pageerror", message: String(e) }));
    page.on("response", r => { if (r.status() >= 400) console.log(`HTTP ${r.status()} ${r.url()}`); });
    page.on("console", msg => {
      if (msg.type() !== "error") return;
      const expected = state.validationError && msg.text().includes("403 (Forbidden)");
      (expected ? report.expectedErrors : report.errors).push({ viewport: name, type: "console", message: msg.text() });
    });
    page.on("dialog", d => d.accept());
    await page.goto("http://127.0.0.1:5403/__dialog-qa");
    for (const kind of ["success", "info", "warning", "error"]) {
      const trigger = page.getByRole("button", { name: kind, exact: true });
      await trigger.click();
      await expect(page.getByRole("dialog")).toBeVisible();
      if (kind === "info") {
        expect(await page.locator(".cs-dialog__detail").count()).toBe(16);
        await expect(page.getByText("字段 16", { exact: true })).not.toBeVisible();
        await page.getByText("更多详情", { exact: false }).click();
      }
      const bounds = await measureDialog(page, vp);
      if (["warning", "error"].includes(kind)) expect(bounds.bodyScrollable).toBe(true);
      await page.screenshot({ path: resolve(out, `dialog-${kind}-${name}.png`) });
      await page.getByRole("button", { name: "关闭对话框" }).click();
      await expect(trigger).toBeFocused();
      ok(name, `${kind} dialog bounds, scrolling and focus return`);
    }
    await page.getByRole("button", { name: "confirm", exact: true }).click();
    await expect(page.getByRole("button", { name: "确认取消", exact: true })).toBeDisabled();
    await page.getByRole("textbox").fill("错误");
    await expect(page.getByRole("button", { name: "确认取消", exact: true })).toBeDisabled();
    await page.getByRole("textbox").fill("取消");
    await page.getByRole("button", { name: "确认取消", exact: true }).click();
    await expect(page.getByTestId("result")).toHaveText("cancel-job");
    ok(name, "confirmation word gates destructive action");
    await page.getByRole("button", { name: "ack", exact: true }).click();
    await expect(page.getByRole("button", { name: "关闭对话框" })).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.mouse.click(2, 2);
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.getByRole("checkbox", { name: "我已保存恢复码" }).check();
    await page.getByRole("button", { name: "关闭", exact: true }).click();
    ok(name, "acknowledgement prevents ESC, mask and close until saved");
    await page.getByRole("button", { name: "form", exact: true }).click();
    await expect(page.getByRole("button", { name: "提交", exact: true })).toBeDisabled();
    await page.getByLabel("申请原因").fill("核对数据");
    await expect(page.getByRole("button", { name: "提交", exact: true })).toBeDisabled();
    await page.getByLabel("已核对操作影响").check();
    await page.getByRole("button", { name: "提交", exact: true }).click();
    await expect(page.getByTestId("result")).toContainText('form:{"reason":"核对数据","checked":"true"}');
    ok(name, "required fields preserve encoded form result");
    await page.getByRole("button", { name: "queue", exact: true }).click();
    await expect(page.getByRole("heading", { name: "第一条", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "下一条", exact: true }).click();
    await expect(page.getByRole("heading", { name: "第二条已更新", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "关闭对话框" }).click();
    await page.getByRole("button", { name: "dismiss-session", exact: true }).click();
    await expect(page.getByRole("heading", { name: "保留当前弹窗", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "关闭对话框" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByTestId("result")).toHaveText("null");
    ok(name, "session update and dismiss target their own queued request");
    await page.getByRole("button", { name: "info", exact: true }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await measureDialog(page, vp);
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => Boolean(document.activeElement?.closest('[role="dialog"]')))).toBe(true);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "info", exact: true })).toBeFocused();
    ok(name, "keyboard focus stays within dialog and ESC restores trigger");
    await page.goto("http://127.0.0.1:5403/__dialog-qa?legacy=1");
    await page.getByRole("button", { name: "confirm", exact: true }).click();
    await expect(page.getByRole("button", { name: "确认取消", exact: true })).toBeDisabled();
    await page.getByRole("textbox").fill("取消");
    await page.getByRole("button", { name: "确认取消", exact: true }).click();
    await expect(page.getByTestId("result")).toHaveText("cancel-job");
    await page.getByRole("button", { name: "ack", exact: true }).click();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.getByRole("checkbox", { name: "我已保存恢复码" }).check();
    await page.getByRole("button", { name: "关闭", exact: true }).last().click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    ok(name, "admin legacy renderer preserves confirmation and acknowledgement");

    if (process.env.UC_UX_DIALOG_ONLY === "1") { await context.close(); continue; }

    await installApi(page, state);
    await page.goto(origin + "/app-v2/home");
    await expect(page.getByRole("heading", { name: "进行中的下载任务", exact: true })).toBeVisible();
    await expect.poll(() => state.requests.some(r => r.path === "/api/v2/jobs" && new URLSearchParams(r.query).get("status") === "ACTIVE")).toBe(true);
    ok(name, "home requests active jobs directly");
    await page.goto(origin + "/app-v2/jobs?view=all");
    await expect(page.getByText("DL-QA-000", { exact: true }).first()).toBeVisible();
    await page.getByRole("button", { name: "下一页", exact: true }).click();
    await expect(page.getByText("DL-SEARCH-OUTSIDE-FIRST-PAGE", { exact: true }).first()).toBeVisible();
    expect(new URL(page.url()).searchParams.get("page")).toBe("1");
    await page.evaluate(() => window.scrollTo({ top: 1200, behavior: "instant" }));
    await sleep(150);
    const returnUrl = new URL(page.url());
    const positionKey = "cs.return-position.v1:sess-1:org-1:ws-1:" + returnUrl.pathname.replace("/app-v2", "") + returnUrl.search;
    await page.getByRole("link", { name: "DL-QA-055", exact: true }).click();
    await expect(page).toHaveURL(/\/jobs\/job-55$/);
    await sleep(400);
    await page.goBack();
    await expect(page.getByText("DL-SEARCH-OUTSIDE-FIRST-PAGE", { exact: true }).first()).toBeVisible();
    const savedPosition = await page.evaluate(key => JSON.parse(sessionStorage.getItem(key))?.value, positionKey);
    expect(savedPosition).toBeGreaterThan(0);
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(savedPosition);
    await page.reload();
    await expect(page.getByText("DL-SEARCH-OUTSIDE-FIRST-PAGE", { exact: true }).first()).toBeVisible();
    await page.getByRole("button", { name: "上一页", exact: true }).click();
    await expect(page.getByText("DL-QA-000", { exact: true }).first()).toBeVisible();
    ok(name, "task cursor survives details, back and refresh");
    await page.getByPlaceholder(/任务号/).fill("SEARCH-OUTSIDE");
    await expect(page.getByText("DL-SEARCH-OUTSIDE-FIRST-PAGE", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("DL-QA-000", { exact: true })).toHaveCount(0);
    expect(new URL(page.url()).searchParams.get("q")).toBe("SEARCH-OUTSIDE");
    expect(new URL(page.url()).searchParams.has("cursor")).toBe(false);
    expect(state.requests.some(r => r.path === "/api/v2/jobs" && r.query.includes("q=SEARCH-OUTSIDE"))).toBe(true);
    await noPageOverflow(page);
    await page.screenshot({ path: resolve(out, `task-search-${name}.png`) });
    ok(name, "search is sent to server and finds later-page job");
    await page.goto(origin + "/app-v2/search?q=任务&type=job");
    await expect(page.getByPlaceholder("案件 / 调查 / 任务 / 报告 / 地址 / 交易")).toHaveValue("任务");
    await expect(page.getByText("目标任务", { exact: true }).first()).toBeVisible();
    await page.getByText("目标任务", { exact: true }).first().click();
    await page.goBack();
    await expect(page.getByPlaceholder("案件 / 调查 / 任务 / 报告 / 地址 / 交易")).toHaveValue("任务");
    expect(new URL(page.url()).searchParams.get("type")).toBe("job");
    ok(name, "search query and type survive navigation");
    const addressUrl = origin + `/app-v2/addresses/tron/${F.ADDRESS_INPUT}?tab=overview`;
    await page.goto(addressUrl);
    const dl = page.getByRole("link", { name: "创建下载", exact: true });
    await expect(dl).toBeVisible();
    expect(new URL(await dl.getAttribute("href"), origin).searchParams.get("address")).toBe(F.ADDRESS_INPUT);
    await dl.click();
    await expect(page.locator("#download-addresses")).toHaveValue(F.ADDRESS_INPUT);
    await page.getByRole("button", { name: "解析导入", exact: true }).click();
    await expect(page.getByRole("heading", { name: "地址导入校验结果", exact: true })).toBeVisible();
    expect(state.validations).toBe(1);
    await measureDialog(page, vp);
    await page.screenshot({ path: resolve(out, `import-review-${name}.png`) });
    await page.locator(".cs-dialog .ant-modal-content").screenshot({ path: resolve(out, `import-review-panel-${name}.png`) });
    await page.getByRole("button", { name: "使用服务端结果", exact: true }).click();
    await expect(page.getByText("地址列表（已导入 1 个）", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "任务中心", exact: true }).first().click();
    await expect(page.getByRole("heading", { name: /离开/ })).toBeVisible();
    await page.getByRole("button", { name: "继续填写", exact: true }).click();
    await expect(page.locator("#download-addresses")).toHaveValue(F.ADDRESS_INPUT);
    await page.getByRole("button", { name: "任务中心", exact: true }).first().click();
    await page.getByRole("button", { name: /离开/, exact: false }).click();
    await page.goBack();
    await expect(page.locator("#download-addresses")).toHaveValue(F.ADDRESS_INPUT);
    await page.getByRole("button", { name: "解析导入", exact: true }).click();
    await expect(page.getByRole("heading", { name: "地址导入校验结果", exact: true })).toBeVisible();
    expect(state.validations).toBe(2);
    await page.getByRole("button", { name: "使用服务端结果", exact: true }).click();
    ok(name, "address download link, single import dialog, leave confirmation, draft and revalidation");
    state.validationError = true;
    await page.locator("#download-addresses").fill(F.ADDRESS_INPUT + " ");
    await page.getByRole("button", { name: "解析导入", exact: true }).click();
    await expect(page.getByRole("heading", { name: "操作失败", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "关闭对话框" }).click();
    state.validationError = false;
    state.delay = 1200;
    await page.getByRole("button", { name: "解析导入", exact: true }).click();
    await expect(page.getByRole("heading", { name: /正在校验/ })).toBeVisible();
    await page.getByRole("button", { name: "取消导入", exact: true }).click();
    await sleep(1400);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    ok(name, "validation errors replace progress; cancellation cannot apply late response");
    await noPageOverflow(page);
    // 文件解析、拒绝项和未授权案件/标签必须保留在最终确认弹窗内。
    state.delay = 50;
    state.verdict = structuredClone(F.VALIDATE_RESULT);
    state.verdict.accepted[0].case_name = "未授权案件";
    state.verdict.accepted[0].tags = ["未授权标签"];
    state.verdict.rejected = [{ index: 1, row: 3, input: "invalid-address", code: "invalid_address" }];
    state.verdict.meta = { cases: [{ name: "未授权案件", status: "denied", required_permission: "case:create" }], tags: [{ name: "未授权标签", status: "denied", required_permission: "tag:create" }] };
    state.verdict.summary.submitted = 2;
    state.verdict.summary.rejected = 1;
    await page.getByLabel("导入地址文件").setInputFiles({ name: "addresses.csv", mimeType: "text/csv", buffer: Buffer.from(`address,chain,case,tags\n${F.ADDRESS_INPUT},tron,未授权案件,未授权标签\ninvalid-address,tron,,\n`) });
    await expect(page.getByRole("heading", { name: "地址导入校验结果", exact: true })).toBeVisible();
    await expect(page.getByText("invalid-address", { exact: true })).toBeVisible();
    await expect(page.getByText("本次不绑定的案件", { exact: true })).toBeVisible();
    await expect(page.getByText("本次不绑定的标签", { exact: true })).toBeVisible();
    expect(await page.locator(".cs-dialog__more").count()).toBe(0);
    await measureDialog(page, vp);
    await page.screenshot({ path: resolve(out, `import-warning-${name}.png`) });
    await page.getByRole("button", { name: "使用服务端结果", exact: true }).click();
    const draft = await page.evaluate(() => JSON.parse(sessionStorage.getItem("cs.download-draft.v1:sess-1:org-1:ws-1")).value);
    expect(draft.imported[0].caseName).toBe("");
    expect(draft.imported[0].tags).toEqual([]);
    state.verdict = undefined;
    ok(name, "CSV worker import keeps rejections visible and drops unauthorized metadata");

    await page.getByRole("button", { name: "预览数据覆盖与成本", exact: true }).first().click();
    await expect(page.getByRole("heading", { name: "预览完成，存在风险提示", exact: true })).toBeVisible();
    await expect(page.getByText("Provider 配额接近上限", { exact: true })).toBeVisible();
    expect(state.previews).toBe(1);
    await page.getByRole("button", { name: "知道了", exact: true }).click();
    await expect(page.getByText("本次预览可提交", { exact: true })).toBeVisible();
    state.workspace = "ws-2";
    await page.goto(origin + "/app-v2/downloads/new");
    await expect(page.locator("#download-addresses")).toHaveValue("");
    state.workspace = "ws-1";
    await page.goto(origin + "/app-v2/downloads/new");
    await expect(page.locator("#download-addresses")).toHaveValue(F.ADDRESS_INPUT);
    await page.getByRole("button", { name: "预览数据覆盖与成本", exact: true }).first().click();
    await expect(page.getByRole("heading", { name: "地址导入校验结果", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "使用服务端结果", exact: true }).click();
    await expect(page.getByRole("heading", { name: "预览完成，存在风险提示", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "知道了", exact: true }).click();
    ok(name, "preview retains server risks; another workspace cannot restore the draft");
    await page.getByRole("button", { name: "创建下载任务", exact: true }).first().click();
    await expect(page.getByRole("heading", { name: "确认创建下载任务？", exact: true })).toBeVisible();
    expect(state.creates ?? []).toHaveLength(0);
    await page.getByRole("button", { name: "确认创建", exact: true }).click();
    await expect(page.getByRole("heading", { name: "下载任务已创建", exact: true })).toBeVisible();
    expect(state.creates).toHaveLength(1);
    expect(state.creates[0].idempotencyKey).toBeTruthy();
    expect(state.creates[0].csrf).toBe(F.SESSION.csrf_token);
    expect(JSON.stringify(state.creates[0].payload)).not.toContain("未授权");
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem("cs.download-draft.v1:sess-1:org-1:ws-1"))).toBe(null);
    await page.getByRole("button", { name: "查看任务", exact: true }).click();
    await expect(page).toHaveURL(/\/jobs\/job-created$/);
    ok(name, "creation requires explicit confirmation, sends CSRF/idempotency and clears successful draft");

    await page.goto(addressUrl);
    await page.getByRole("link", { name: "发起调查", exact: true }).click();
    await page.locator("#inv-case").selectOption("case-0001");
    await page.locator("#inv-goal").fill("核查资金流向");
    await page.getByRole("button", { name: "下一步", exact: true }).click();
    await expect(page.locator("#inv-value")).toHaveValue(F.ADDRESS_INPUT);
    await expect(page.locator("#inv-chain")).toHaveValue("tron");
    ok(name, "investigation quick link preserves full address and chain");
    await page.goto(addressUrl);
    const graphLink = page.locator(".cs-pageheader").getByRole("link", { name: "资金图谱", exact: true });
    expect(new URL(await graphLink.getAttribute("href"), origin).searchParams.get("address")).toBe(F.ADDRESS_INPUT);
    await graphLink.click();
    await expect(page).toHaveURL(/\/graph\?.*address=/);
    await expect(page.getByRole("combobox", { name: "案件", exact: true })).toHaveValue("case-0001");
    await page.getByRole("button", { name: "进入画布", exact: true }).first().click();
    await expect(page).toHaveURL(/\/canvases\/canvas-demo\?.*address=/);
    expect(new URL(page.url()).searchParams.get("address")).toBe(F.ADDRESS_INPUT);
    await page.getByRole("button", { name: "加入地址资金流", exact: true }).first().click();
    await expect(page.getByRole("heading", { name: "从本地索引加入资金流", exact: true })).toBeVisible();
    await expect(page.getByText(F.ADDRESS_INPUT, { exact: true }).last()).toBeVisible();
    await page.getByRole("button", { name: "取消", exact: true }).click();
    expect(state.requests.filter(r => r.method === "POST" && r.path.includes("canvases"))).toHaveLength(0);
    ok(name, "graph quick link follows case/investigation/canvas and requires explicit add confirmation");

    state.activityCount = 10200;
    state.bulkPages = 0;
    await page.goto(addressUrl.replace("tab=overview", "tab=transactions"));
    await page.getByRole("button", { name: "批量加载（最多 10,000 行）", exact: true }).click();
    await expect(page.getByRole("heading", { name: "批量加载活动记录？", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "开始加载", exact: true }).click();
    await expect(page.getByRole("heading", { name: "批量活动记录已加载", exact: true })).toBeVisible({ timeout: 30000 });
    await expect(page.getByText(/已达到 10,000 行上限，后面仍有记录/)).toBeVisible();
    expect(state.bulkPages).toBe(50);
    await page.getByRole("button", { name: "关闭对话框" }).click();
    await expect(page.getByRole("button", { name: "返回分页视图（10000 行）", exact: true })).toBeVisible();
    await noPageOverflow(page);
    ok(name, "10,000-row batch limit reports remaining records accurately");
    state.searchHits = [
      { type: "case", id: "case-0001", title: "案件成员", subtitle: "案件", url: "/cases/case-0001/members" },
      { type: "case", id: "case-0001-overview", title: "案件概览", subtitle: "案件", url: "/cases/case-0001/overview" },
    ];
    await page.goto(origin + "/app-v2/home");
    await expect(page.getByRole("heading", { name: "进行中的下载任务", exact: true })).toBeVisible();
    await page.keyboard.press("Control+k");
    await page.locator(".cs-command input").fill("案件");
    await expect(page.locator(".cs-command").getByText("案件成员", { exact: true })).toBeVisible();
    await expect(page.locator(".cs-command").getByText("案件概览", { exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    ok(name, "command palette allows authorized case-member result");
    state.permissions = F.SESSION.permissions.filter(p => p !== "case:member:list");
    await page.reload();
    await expect(page.getByRole("heading", { name: "进行中的下载任务", exact: true })).toBeVisible();
    await page.keyboard.press("Control+k");
    await page.locator(".cs-command input").fill("案件");
    await expect(page.locator(".cs-command").getByText("案件概览", { exact: true })).toBeVisible();
    await expect(page.locator(".cs-command").getByText("案件成员", { exact: true })).toHaveCount(0);
    await page.keyboard.press("Escape");
    ok(name, "command palette removes case-member result when permission is revoked");
    await context.close();
  }
} catch (e) {
  report.failure = String(e.stack ?? e);
  const contexts = browser.contexts();
  const page = contexts.at(-1)?.pages().at(-1);
  if (page) await page.screenshot({ path: resolve(out, "failure.png"), fullPage: true }).catch(() => {});
  process.exitCode = 1;
} finally {
  report.result = !report.failure && report.errors.length === 0 ? "PASS" : "FAIL";
  if (report.result === "FAIL") process.exitCode = 1;
  await writeFile(resolve(out, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  await browser.close();
  await vite.close();
  await new Promise(r => server.close(r));
}
