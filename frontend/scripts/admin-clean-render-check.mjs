#!/usr/bin/env node
/* Browser coverage for the admin presentation layer, with deterministic API fixtures. */
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { existsSync } from "node:fs";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { extname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const frontend = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const dist = join(frontend, "apps/admin-console/dist");
const out = join(frontend, "acceptance-evidence/admin-clean-20260929");
const port = Number(process.env.ADMIN_RENDER_PORT ?? 5497);
const base = `http://127.0.0.1:${port}`;
const mime = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".woff2": "font/woff2" };
const now = new Date().toISOString();
const paths = [
  "overview", "observability", "system", "organizations", "users", "roles", "sessions",
  "login-events", "audit", "audit/integrity", "jobs", "jobs/fixture-job", "storage",
  "data-quality", "risk", "account/security", "providers", "providers/routing",
  "providers/fixture-provider", "runtime/performance", "api-keys", "requests", "costs",
  "budgets", "alerts", "settings", "feature-flags", "control", "control/kill-switch",
  "system/backups", "data/retention", "system/version",
];
const permissions = [
  "admin:dashboard:read", "admin:system:read", "admin:org:read", "admin:user:read",
  "admin:role:read", "admin:session:read", "admin:login-event:read", "admin:audit:read",
  "admin:audit:verify", "admin:job:list", "admin:job:read", "admin:storage:read",
  "admin:coverage:read", "admin:risk:read", "security:update", "admin:provider:read",
  "admin:routing:read", "admin:performance:read", "admin:apikey:read", "admin:request:read",
  "admin:cost:read", "admin:budget:read", "admin:alert:read", "admin:config:read",
  "admin:flag:read", "admin:killswitch:read", "admin:backup:read", "admin:retention:read",
  "admin:version:read", "admin:metrics:read", "admin:workspace:read", "admin:member:read",
];
const session = {
  user: { id: "admin-fixture", display_name: "管理验收", email: "admin@example.test" },
  id: "admin-fixture", email: "admin@example.test", display_name: "管理验收",
  organization_id: "org-fixture", workspace_id: "ws-fixture", audience: "admin",
  session_id: "admin-session-fixture", roles: ["platform_admin"], permissions,
  expires_at: new Date(Date.now() + 3600_000).toISOString(),
  absolute_expires_at: new Date(Date.now() + 86400_000).toISOString(),
  must_change_password: false, mfa_satisfied: true, mfa_enrolled: true, csrf_token: "fixture",
};
const envelope = (data) => JSON.stringify({ success: true, data, meta: { total: 0, next_cursor: null, request_id: "render-fixture", server_time: now } });

function fixture(path) {
  if (path === "/api/admin/v1/users") return [{
    id: "identity-1234567890", display_name: "运营管理员", email: "operator@example.test",
    status: "active", mfa_enrolled: true, mfa_required: true, roles: ["platform_admin"],
    last_login_at: now, organization_id: "org-fixture",
  }];
  if (path === "/api/admin/v1/jobs") return { items: [{
    id: "job-fixture-1234567890", job_no: "JOB-20260929-001", status: "RUNNING", chain: "ethereum",
    datasets: ["transactions"], organization_id: "org-fixture", workspace_id: "ws-fixture",
    progress: .62, shards: { total: 12, completed: 8, failed: 0 }, downloaded_rows: 154892,
    downloaded_bytes: 14680064, effective_mode: "FAST", requested_mode: "FAST", created_at: now,
  }] };
  if (path === "/api/admin/v1/providers") return { items: [fixture("/api/admin/v1/providers/fixture-provider")] };
  if (path === "/api/admin/v1/overview") return {
    as_of: now, status: "ok", my_roles: ["platform_admin"], data_source: {},
    cards: [
      { key: "users", label: "管理账号", value: 18, unit: "人" },
      { key: "jobs_running", label: "运行中任务", value: 7, unit: "个" },
      { key: "jobs_failed", label: "失败任务", value: 0, unit: "个" },
      { key: "login_failures_24h", label: "登录失败", value: 2, unit: "次" },
    ],
  };
  if (path === "/api/admin/v1/observability/overview") return {
    as_of: now, window_start: now, health: { checked_at: now, healthy: true, open_alerts: 0,
      checks: { postgres: "ok", clickhouse: "ok", nats: "ok", minio: "ok" } },
    api: { requests_total: 4281, error_rate: 0.001, inflight: 3, window: "60m" },
    queue: { running: 7, waiting: 4, failed: 0, paused: 0 },
    alerts: { P0: 0, P1: 1, P2: 3, P3: 2, open_total: 6 }, throughput: {}, recent_high_risk: [], data_source: {},
  };
  if (/\/api\/admin\/v1\/(users|roles|sessions|login-events|audit\/events|organizations|workspaces|members|jobs|risk\/events)$/.test(path)) return [];
  if (path === "/api/auth/v1/sessions" || path === "/api/auth/v1/devices") return [];
  if (path === "/api/admin/v1/audit/integrity") return {
    chain: "audit", observed: { has_prefix_gap: false, detected_at: now }, incident: null,
    integrity_status: "intact", prefix_status: "intact",
    chain_verification: { checked: 840, valid: true, verified_from_seq: 1, to_seq: 840, head_hash: "fixture-hash", integrity_status: "intact", prefix_status: "intact" },
    anchors: [], anchors_verified_count: 0, anchors_legacy_derivation_count: 0,
    anchors_sealed_count: 0, anchors_unexplained_count: 0, all_anchors_accounted: true,
    truth_statement: "完整", as_of: now,
  };
  if (path === "/api/admin/v1/storage/overview") return {
    as_of: now, buckets: [], export_total: 0, export_ready: 0, export_failed: 0,
    export_rows: 0, recent_exports: [], coverage_subjects: 0, provider_raw_bytes: 0, list_cap: 20000,
  };
  if (path === "/api/admin/v1/risk/summary") return {
    as_of: now, window_hours: 168, levels: [],
    totals: { events: 0, critical: 0, high: 0, medium: 0, blocked: 0, denied: 0, failure: 0, affected_identities: 0, affected_ips: 0 },
    by_reason: [], by_hour: [], top_ips: [],
  };
  if (path === "/api/admin/v1/providers/fixture-provider") return {
    key: "fixture-provider", display_name: "Provider 运行节点", kind: "rpc", status: "connected",
    chains: ["ethereum"], datasets: ["transactions"], capabilities: [], endpoint: "https://fixture.invalid",
    concurrency: 8, rate_limit_per_minute: 120, priority: 1, revision: 3, updated_at: now,
    runtime_health: { state: "ok", last_check: now }, usage: { calls_24h: 1270, errors_24h: 2 },
    active_secret: { configured: true, prefix: "example" },
  };
  return { items: [], data: [], rows: [], events: [], workspaces: [], members: [], roles: [],
    backups: [], requests: [], incidents: [], checks: {}, metrics: [], providers: [],
    coverage: [], gaps: [], chains: [], shards: [], tables: [], summary: {}, status: "ok",
    as_of: now, total: 0 };
}

async function startServer() {
  const server = createServer(async (req, res) => {
    const pathname = decodeURIComponent(new URL(req.url ?? "/", base).pathname);
    if (pathname !== "/admin" && !pathname.startsWith("/admin/")) { res.writeHead(404).end(); return; }
    const target = resolve(dist, "." + pathname.slice(6));
    if (target !== dist && !target.startsWith(dist + sep)) { res.writeHead(403).end(); return; }
    const file = existsSync(target) && extname(target) ? target : join(dist, "index.html");
    try { res.writeHead(200, { "content-type": mime[extname(file)] ?? "application/octet-stream" }).end(await readFile(file)); }
    catch { res.writeHead(404).end(); }
  });
  await new Promise((done) => server.listen(port, "127.0.0.1", done));
  return server;
}

if (!existsSync(join(dist, "index.html"))) throw new Error("Build admin console first");
await mkdir(out, { recursive: true });
const server = await startServer();
const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL ?? "msedge" });
const report = { generated_at: now, pages: [], problems: [] };
try {
  for (const viewport of [{ name: "desktop", width: 1440, height: 900 }, { name: "mobile", width: 390, height: 844 }]) {
    const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height }, deviceScaleFactor: 1 });
    await context.route("**/api/**", async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === "/api/auth/v1/session") {
        await route.fulfill({ status: 200, contentType: "application/json", body: envelope(session) });
      } else {
        await route.fulfill({ status: 200, contentType: "application/json", body: envelope(fixture(path)) });
      }
    });
    const loginPage = await context.newPage();
    await loginPage.route("**/api/auth/v1/session", (route) => route.fulfill({
      status: 401, contentType: "application/json",
      body: JSON.stringify({ success: false, error: { code: "UNAUTHENTICATED", message: "未登录" } }),
    }));
    await loginPage.goto(`${base}/admin/login`, { waitUntil: "domcontentloaded" });
    await loginPage.locator("#identifier").waitFor({ timeout: 8000 });
    const loginInfo = await loginPage.evaluate(() => ({
      width: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth,
      form: Boolean(document.querySelector(".auth-card form")),
      inputs: document.querySelectorAll(".auth-card input").length,
    }));
    report.pages.push({ viewport: viewport.name, path: "login", ...loginInfo });
    if (!loginInfo.form || loginInfo.inputs < 2 || loginInfo.width > loginInfo.clientWidth + 1) report.problems.push({ viewport: viewport.name, path: "login", ...loginInfo });
    await loginPage.screenshot({ path: join(out, `login-${viewport.name}.png`), fullPage: true });
    await loginPage.route("**/api/auth/v1/login", (route) => route.fulfill({
      status: 401, contentType: "application/json",
      body: JSON.stringify({ success: false, error: { code: "AUTHN_INVALID_CREDENTIALS", message: "账号或密码错误" } }),
    }));
    await loginPage.locator("#identifier").fill("admin@example.test");
    await loginPage.locator("#password").fill("invalid-password");
    await loginPage.getByRole("button", { name: "登录后台" }).click();
    const loginDialog = loginPage.getByRole("dialog");
    await loginDialog.waitFor({ state: "visible", timeout: 5000 });
    report.pages.push({ viewport: viewport.name, path: "login-error-dialog", visible: await loginDialog.isVisible() });
    await loginPage.close();
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()); });
    for (const path of paths) {
      errors.length = 0;
      await page.goto(`${base}/admin/${path}`, { waitUntil: "domcontentloaded" });
      await page.locator(".shell__content").waitFor({ timeout: 8000 });
      await page.waitForTimeout(450);
      const info = await page.evaluate(() => ({
        pathname: location.pathname, title: document.querySelector("h1")?.textContent?.trim() ?? "",
        shell: Boolean(document.querySelector(".shell__nav")),
        width: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth,
        hasDialog: Boolean(document.querySelector('[role="dialog"]')),
      }));
      const row = { viewport: viewport.name, path, ...info, errors: [...errors] };
      report.pages.push(row);
      if (!info.shell || !info.title || info.width > info.clientWidth + 1 || info.pathname.endsWith("/login") || errors.length) report.problems.push(row);
      if (viewport.name === "mobile" && ["users", "jobs", "providers"].includes(path)) {
        const table = await page.locator(".table-scroll").first().evaluate((node) => ({
          scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, overflowX: getComputedStyle(node).overflowX,
        }));
        const tableRow = { viewport: viewport.name, path: `${path}-table-scroll`, ...table };
        report.pages.push(tableRow);
        if (table.scrollWidth > table.clientWidth + 1 && !["auto", "scroll"].includes(table.overflowX)) report.problems.push(tableRow);
      }
      await page.screenshot({ path: join(out, `${path.replaceAll("/", "-")}-${viewport.name}.png`), fullPage: true });
    }
    await page.goto(`${base}/admin/overview`, { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "指标口径" }).click();
    const infoDialog = page.getByRole("dialog");
    await infoDialog.waitFor({ state: "visible", timeout: 5000 });
    await page.keyboard.press("Escape");
    await infoDialog.waitFor({ state: "hidden", timeout: 5000 });
    report.pages.push({ viewport: viewport.name, path: "overview-dialog", openedAndClosed: true });
    if (viewport.name === "mobile") {
      await page.getByRole("button", { name: "打开主导航" }).click();
      const navOpen = await page.locator(".shell__nav").getAttribute("data-open");
      await page.locator(".shell__scrim").click({ position: { x: 350, y: 100 } });
      const navClosed = await page.locator(".shell__nav").getAttribute("data-open");
      report.pages.push({ viewport: viewport.name, path: "mobile-navigation", navOpen, navClosed });
      if (navOpen !== "true" || navClosed !== "false") report.problems.push({ path: "mobile-navigation", navOpen, navClosed });
    }
    await context.close();
  }
} finally {
  await browser.close();
  await new Promise((done) => server.close(done));
}
report.ok = report.problems.length === 0;
await writeFile(join(out, "report.json"), JSON.stringify(report, null, 2));
console.log(JSON.stringify({ ok: report.ok, pages: report.pages.length, problems: report.problems.slice(0, 10) }, null, 2));
if (!report.ok) process.exitCode = 1;
