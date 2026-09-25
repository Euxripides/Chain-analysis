#!/usr/bin/env node
/** 地址与交易查询页面：真实构建产物 + 固定 API 夹具的桌面/移动视觉与交互验收。 */
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { extname, resolve, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const frontend = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const dist = resolve(frontend, process.env.EXPLORER_DIST ?? "apps/user-console/dist");
const out = resolve(frontend, process.env.EXPLORER_OUT ?? "acceptance-evidence/explorer-visual");
const port = Number(process.env.EXPLORER_PORT ?? 5412);
const base = `http://127.0.0.1:${port}/app-v2`;
const mime = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".png": "image/png", ".svg": "image/svg+xml", ".woff2": "font/woff2" };
const address = "0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649";
const hash = "0x" + "a".repeat(64);
const chains = { items: [
  { slug: "arbitrum", name: "Arbitrum One", type: "evm", enabled: true },
  { slug: "ethereum", name: "Ethereum", type: "evm", enabled: true },
  { slug: "polygon", name: "Polygon PoS", type: "evm", enabled: true },
  { slug: "tron", name: "TRON", type: "tron", enabled: true },
] };
const session = {
  user: { id: "qa-user", display_name: "视觉验收", email: "qa@local" },
  email: "qa@local", display_name: "视觉验收", organization_id: "org-1", workspace_id: "ws-1",
  audience: "user", session_id: "qa-session", roles: ["investigator"],
  permissions: ["dashboard:read", "search:read", "explorer:read", "case:list", "profile:read", "notification:list"],
  expires_at: new Date(Date.now() + 3600000).toISOString(), absolute_expires_at: new Date(Date.now() + 86400000).toISOString(),
  must_change_password: false, mfa_satisfied: true, mfa_enrolled: true, csrf_token: "qa-csrf",
};
const envelope = (data) => JSON.stringify({ success: true, data, meta: { request_id: "qa-request", trace_id: "qa-trace", server_time: new Date().toISOString() } });

async function startServer() {
  const server = createServer(async (req, res) => {
    const pathname = decodeURIComponent(new URL(req.url ?? "/", base).pathname);
    const relative = pathname.startsWith("/app-v2/") ? pathname.slice("/app-v2/".length) : "";
    const requested = resolve(dist, normalize(relative));
    if (!requested.startsWith(dist + "/") && requested !== dist) { res.writeHead(403).end(); return; }
    const asset = extname(requested) ? requested : join(dist, "index.html");
    try {
      const bytes = await readFile(asset);
      res.writeHead(200, { "Content-Type": mime[extname(asset)] ?? "application/octet-stream" }).end(bytes);
    } catch { res.writeHead(404).end(); }
  });
  await new Promise((done) => server.listen(port, "127.0.0.1", done));
  return server;
}

async function installRoutes(page) {
  await page.route("**/api/**", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({}) }));
  await page.route("**/api/auth/v1/session", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(session) }));
  await page.route(/\/api\/v2\/meta\/chains(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(chains) }));
  await page.route(/\/api\/v2\/notifications(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }));
}

async function run() {
  await mkdir(out, { recursive: true });
  const server = await startServer();
  const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL ?? "chromium" });
  const report = { ok: true, problems: [], viewports: {}, navigation: {}, dialogs: {}, console_errors: [], page_errors: [] };
  try {
    for (const viewport of [{ name: "desktop", width: 1440, height: 900 }, { name: "tablet", width: 768, height: 1024 }, { name: "mobile", width: 390, height: 844 }, { name: "compact", width: 320, height: 800 }]) {
      const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } });
      await context.addInitScript(({ address, hash }) => {
        localStorage.setItem("chainsight.explorer.recent.v1", JSON.stringify([
          { chain: "polygon", value: address, kind: "address", at: new Date(Date.now() - 3600000).toISOString() },
          { chain: "ethereum", value: hash, kind: "transaction", at: new Date(Date.now() - 86400000).toISOString() },
        ]));
      }, { address, hash });
      const page = await context.newPage();
      page.on("console", (message) => { if (message.type() === "error") report.console_errors.push(message.text()); });
      page.on("pageerror", (error) => report.page_errors.push(error.message));
      await installRoutes(page);
      await page.goto(base + "/explorer", { waitUntil: "domcontentloaded" });
      await page.getByRole("heading", { name: "地址与交易查询" }).waitFor();
      await page.waitForFunction(() => {
        const native = document.querySelector(".cs-search-hero select");
        const custom = document.querySelector(".cs-explorer-query__chain .cs-chainselect");
        return (native && native.options.length > 0 && native.value) || (custom && custom.textContent?.includes("Arbitrum"));
      });
      await page.waitForTimeout(550);
      const geometry = await page.evaluate(() => {
        const rect = (selector) => {
          const node = document.querySelector(selector);
          if (!node) return null;
          const box = node.getBoundingClientRect();
          return { x: Math.round(box.x), y: Math.round(box.y), width: Math.round(box.width), height: Math.round(box.height) };
        };
        return {
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
          header: rect(".cs-pageheader"), query: rect(".cs-search-hero"), recent: rect(".cs-recentlist"),
          chain: rect(".cs-search-hero select, .cs-explorer-query__chain .cs-chainselect"),
          input: rect(".cs-explorer-query__input.ant-input-affix-wrapper, .cs-explorer-query__input-wrap .input, .cs-search-hero > input"), submit: rect(".cs-search-hero button[type=submit]"),
        };
      });
      if (geometry.scrollWidth > geometry.clientWidth + 1) report.problems.push(`${viewport.name}: horizontal overflow ${geometry.scrollWidth - geometry.clientWidth}px`);
      if (!geometry.query || !geometry.recent || !geometry.chain || !geometry.input || !geometry.submit) report.problems.push(`${viewport.name}: missing component`);
      if (viewport.width > 599 && geometry.chain && geometry.input && geometry.chain.y !== geometry.input.y) report.problems.push(`${viewport.name}: chain/input not aligned`);
      if (geometry.input && geometry.submit && geometry.input.y !== geometry.submit.y) report.problems.push(`${viewport.name}: input/submit not aligned`);
      report.viewports[viewport.name] = geometry;
      await page.screenshot({ path: join(out, `${viewport.name}.png`), fullPage: true });
      if (viewport.name === "compact") {
        await page.locator(".cs-explorer-query__chain .cs-chainselect").click();
        const popup = page.locator(".cs-select-dropdown-wizard:visible");
        await popup.waitFor();
        await page.waitForTimeout(250);
        const box = await popup.boundingBox();
        report.viewports.compact.dropdown = box;
        if (box && (box.x < -1 || box.x + box.width > viewport.width + 1)) report.problems.push("compact: dropdown outside viewport");
        await page.screenshot({ path: join(out, "compact-dropdown.png"), fullPage: true });
      }
      if (viewport.name === "desktop") {
        await page.getByRole("button", { name: "说明" }).click();
        const dialog = page.locator(".cs-dialog:visible");
        if (await dialog.count() === 0) report.problems.push("help dialog did not open");
        else {
          await page.keyboard.press("Escape");
          await page.waitForTimeout(1000);
          report.dialogs.help_escape_closed = await dialog.count() === 0;
          if (!report.dialogs.help_escape_closed) await dialog.getByRole("button", { name: "关闭对话框" }).click();
        }
        await page.getByRole("button", { name: "清空" }).click();
        const confirm = page.locator(".cs-dialog:visible");
        if (await confirm.count() === 0) report.problems.push("clear confirm did not open");
        else {
          await confirm.getByRole("button", { name: "关闭对话框" }).click();
        }
        report.navigation.recent_after_cancel = await page.locator(".cs-recentlist__item").count();
        await page.locator(".cs-explorer-query__chain .cs-chainselect").click();
        await page.locator(".cs-select-dropdown-wizard:visible").waitFor();
        await page.waitForTimeout(250);
        await page.screenshot({ path: join(out, "chain-dropdown.png"), fullPage: true });
        await page.locator(".cs-select-dropdown-wizard .ant-select-item-option").filter({ hasText: "Polygon PoS" }).click();
        report.navigation.selected_chain = await page.locator(".cs-explorer-query__chain .ant-select-selection-item").innerText();
        if (!report.navigation.selected_chain.includes("Polygon PoS")) report.problems.push("chain selection failed");
        await page.getByRole("textbox", { name: "地址或交易哈希" }).fill(address);
        await page.getByRole("button", { name: "查询", exact: true }).click();
        report.navigation.address_url = new URL(page.url()).pathname;
        if (!report.navigation.address_url.endsWith(`/addresses/polygon/${address}`)) report.problems.push("address navigation changed");
        await page.goBack();
        await page.getByRole("heading", { name: "地址与交易查询" }).waitFor();
        await page.getByRole("textbox", { name: "地址或交易哈希" }).fill(hash);
        await page.getByRole("button", { name: "查询", exact: true }).click();
        report.navigation.transaction_url = new URL(page.url()).pathname;
        if (!report.navigation.transaction_url.endsWith(`/transactions/polygon/${hash.slice(2)}`)) report.problems.push("transaction navigation changed");
      }
      await context.close();
    }
  } finally { await browser.close(); server.close(); }
  if (report.console_errors.length || report.page_errors.length) report.problems.push("browser console/page errors");
  report.ok = report.problems.length === 0;
  await writeFile(join(out, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ ok: report.ok, problems: report.problems, viewports: report.viewports, navigation: report.navigation }, null, 2));
  if (!report.ok) process.exitCode = 1;
}

await run();
