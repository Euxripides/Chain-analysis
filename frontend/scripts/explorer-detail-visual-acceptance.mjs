#!/usr/bin/env node
/** 查询结果详情页：真实构建产物 + 契约形状夹具的桌面/移动视觉与 Tab 验收。 */
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { extname, resolve, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const frontend = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const dist = resolve(frontend, process.env.EXPLORER_DIST ?? "apps/user-console/dist");
const out = resolve(frontend, process.env.EXPLORER_DETAIL_OUT ?? "acceptance-evidence/explorer-detail-visual");
const port = Number(process.env.EXPLORER_DETAIL_PORT ?? 5413);
const base = `http://127.0.0.1:${port}/app-v2`;
const mime = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".png": "image/png", ".svg": "image/svg+xml", ".woff2": "font/woff2" };
const address = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t";
const hash = "9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c8d0b2a4e6f8c0d2b4a6e8f0c";

const session = {
  user: { id: "qa-user", display_name: "视觉验收", email: "qa@local" },
  email: "qa@local", display_name: "视觉验收", organization_id: "org-1", workspace_id: "ws-1",
  audience: "user", session_id: "qa-session", roles: ["investigator"],
  permissions: ["dashboard:read", "search:read", "explorer:read", "explorer:fetch", "case:list", "profile:read", "notification:list"],
  expires_at: new Date(Date.now() + 3600000).toISOString(), absolute_expires_at: new Date(Date.now() + 86400000).toISOString(),
  must_change_password: false, mfa_satisfied: true, mfa_enrolled: true, csrf_token: "qa-csrf",
};

const overview = {
  subject: { type: "address", chain: "tron", address, normalized_address: address },
  summary: {
    transaction_count: 1284,
    token_transfer_count: 436,
    counterparty_count: 98,
    first_seen: "2024-05-18T12:20:00Z",
    last_seen: "2026-09-22T12:40:00Z",
    current_balance: {
      source: "latest balance",
      is_current_balance: true,
      observed_at: "2026-09-22T12:39:12Z",
      items: [
        { token_symbol: "USDT", token_name: "Tether USD", decimals: 6, raw_amount: "12345678900", display_amount: "12345.6789", token_price: "1.0000", amount_usd: "12345.68", observed_at: "2026-09-22T12:39:12Z", source_provider: "local", is_current_balance: true },
        { token_symbol: "TRX", token_name: "TRON", decimals: 6, raw_amount: "8765432100", display_amount: "8765.4321", token_price: "0.0793", amount_usd: "695.14", observed_at: "2026-09-22T12:39:12Z", source_provider: "local", is_current_balance: true },
      ],
    },
    historical_net_flow: {
      source: "indexed flow",
      is_current_balance: false,
      items: [
        { asset: "USDT", token_symbol: "USDT", decimals: 6, in_raw: "35000000000", out_raw: "22654321100", net_raw: "12345678900", transaction_count: 436, source: "indexed", is_current_balance: false },
      ],
    },
  },
  coverage: {
    source: "local",
    items: [
      { dataset: "transactions", status: "complete", source: "local" },
      { dataset: "token_transfers", status: "complete", source: "local" },
      { dataset: "balances", status: "complete", source: "local" },
      { dataset: "internal_transactions", status: "partial", source: "local" },
    ],
  },
  caliber: { current_balance: "latest balance", historical_net_flow: "indexed flow" },
  source: { provider: "本地索引", tables: ["address_overview"], as_of: "2026-09-22T12:40:00Z" },
};

const tx = {
  items: {
    chain: "tron",
    tx_hash: hash,
    transaction: {
      chain: "tron", tx_hash: hash, block_number: 76543210, block_hash: "a".repeat(64),
      timestamp: "2026-09-22T12:40:00Z", from_address: address,
      to_address: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", value_raw: "1234567", nonce: 18,
      gas: 120000, gas_used: 65210, gas_price: "420", tx_type: "transfer", status: 1,
      input: "0x", source_provider: "local", method_name: "transfer", method_signature: "transfer(address,uint256)",
    },
    input: "0x", method: { name: "transfer", signature: "transfer(address,uint256)", source: "local ABI", verified: true, confidence: "high" },
    decoded_params: [], decode_source: "local", decode_verified: true, decode_cache_hit: true, decoded_at: "2026-09-22T12:40:01Z",
  },
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
  await page.route(/\/api\/v2\/notifications(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }));
  await page.route(/\/api\/v2\/explorer\/addresses\/tron\/[^/]+\/(assets|activity|counterparties|semantic|flow-profile)(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }));
  await page.route(/\/api\/v2\/explorer\/addresses\/tron\/[^/]+\/overview(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(overview) }));
  await page.route(new RegExp(`/api/v2/explorer/transactions/tron/${hash}$`), (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(tx) }));
  await page.route(new RegExp(`/api/v2/explorer/transactions/tron/${hash}/logs`), (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }));
  await page.route(new RegExp(`/api/v2/explorer/transactions/tron/${hash}/trace`), (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: null }) }));
}

async function run() {
  await mkdir(out, { recursive: true });
  const server = await startServer();
  const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL ?? "chromium" });
  const report = { ok: true, problems: [], viewports: {}, tabs: {}, console_errors: [], page_errors: [] };
  try {
    for (const viewport of [{ name: "desktop", width: 1440, height: 900 }, { name: "mobile", width: 390, height: 844 }]) {
      const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } });
      const page = await context.newPage();
      page.on("console", (message) => { if (message.type() === "error") report.console_errors.push(`${viewport.name}: ${message.text()}`); });
      page.on("pageerror", (error) => report.page_errors.push(`${viewport.name}: ${error.message}`));
      await installRoutes(page);

      await page.goto(`${base}/addresses/tron/${address}`, { waitUntil: "domcontentloaded" });
      await page.getByRole("heading", { name: "地址详情" }).waitFor();
      await page.locator(".cs-detail-tabs .ant-tabs-nav").waitFor();
      await page.locator(".cs-statistic-group .metric").first().waitFor();
      await page.waitForTimeout(450);

      const geometry = await page.evaluate(() => {
        const rect = (selector) => {
          const node = document.querySelector(selector);
          if (!node) return null;
          const box = node.getBoundingClientRect();
          return { x: Math.round(box.x), y: Math.round(box.y), width: Math.round(box.width), height: Math.round(box.height) };
        };
        const metrics = [...document.querySelectorAll(".cs-statistic-group .metric")].map((node) => Math.round(node.getBoundingClientRect().width));
        return {
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
          tabs: rect(".cs-detail-tabs .ant-tabs-nav"),
          metricGroup: rect(".cs-statistic-group"),
          inspector: rect(".cs-detail-split .cs-inspector"),
          metricWidths: metrics,
          contentBackground: getComputedStyle(document.querySelector("#main-content")).backgroundColor,
        };
      });
      if (geometry.scrollWidth > geometry.clientWidth + 1) report.problems.push(`${viewport.name}: address horizontal overflow ${geometry.scrollWidth - geometry.clientWidth}px`);
      if (!geometry.tabs || !geometry.metricGroup) report.problems.push(`${viewport.name}: address components missing`);
      if (geometry.contentBackground !== "rgb(255, 255, 255)") report.problems.push(`${viewport.name}: content background is ${geometry.contentBackground}`);
      report.viewports[viewport.name] = geometry;
      const labels = (await page.locator(".cs-detail-tabs .cs-tab").allInnerTexts()).map((value) => value.trim());
      if (labels.join("|") !== "概览|资产|普通交易|代币转账|内部交易|对手方|语义事件|分析|Coverage") report.problems.push(`${viewport.name}: address tabs changed ${JSON.stringify(labels)}`);
      report.tabs[`${viewport.name}_address`] = labels;
      await page.screenshot({ path: join(out, `address-${viewport.name}.png`), fullPage: true });

      if (viewport.name === "desktop") {
        await page.locator(".cs-detail-tabs .cs-tab", { hasText: "资产" }).click();
        if (!new URL(page.url()).searchParams.get("tab")?.includes("assets")) report.problems.push("desktop: address tab URL not updated");
        await page.locator(".cs-detail-tabs .cs-tab", { hasText: "概览" }).click();
      }

      await page.goto(`${base}/transactions/tron/${hash}`, { waitUntil: "domcontentloaded" });
      await page.getByRole("heading", { name: "交易详情" }).waitFor();
      await page.locator(".cs-detail-tabs .ant-tabs-nav").waitFor();
      const txLabels = (await page.locator(".cs-detail-tabs .cs-tab").allInnerTexts()).map((value) => value.trim());
      if (txLabels.join("|") !== "概览|Logs|Trace") report.problems.push(`${viewport.name}: transaction tabs changed ${JSON.stringify(txLabels)}`);
      report.tabs[`${viewport.name}_transaction`] = txLabels;
      const txOverflow = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth }));
      if (txOverflow.scrollWidth > txOverflow.clientWidth + 1) report.problems.push(`${viewport.name}: transaction horizontal overflow ${txOverflow.scrollWidth - txOverflow.clientWidth}px`);
      await page.screenshot({ path: join(out, `transaction-${viewport.name}.png`), fullPage: true });
      await context.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
  if (report.console_errors.length || report.page_errors.length) report.problems.push("browser console/page errors");
  report.ok = report.problems.length === 0;
  await writeFile(join(out, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  if (!report.ok) process.exitCode = 1;
}

await run();
