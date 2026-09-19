import { chromium } from "@playwright/test";

const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const EMAIL = process.env.SWEEP_EMAIL ?? "admin@local";
const PASSWORD = process.env.SWEEP_PASSWORD;
const ROUTES = (process.env.PROBE_ROUTES ?? "/app-v2/cases,/app-v2/investigations,/app-v2/exports").split(",");

const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || undefined });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await ctx.newPage();
const consoleErrors = [];
page.on("console", (m) => { if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) consoleErrors.push(m.text().slice(0, 200)); });

await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(EMAIL);
await page.locator("#password").fill(PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
const ws = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
await ws.waitFor({ state: "visible", timeout: 6000 }).catch(() => {});
if (await ws.isVisible().catch(() => false)) await ws.getByRole("button").nth(1).click();
await page.waitForTimeout(2500);
await page.keyboard.press("Escape").catch(() => {});

for (const route of ROUTES) {
  await page.goto(BASE + route, { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(1200);
  const dialog = page.getByRole("dialog").first();
  if (await dialog.isVisible().catch(() => false)) { await page.keyboard.press("Escape"); await page.waitForTimeout(200); }

  const before = await page.evaluate(() => {
    const scroll = document.querySelector(".table-scroll");
    const total = document.querySelectorAll('tr[data-data-row="true"]').length;
    return { scrollHeight: scroll ? scroll.scrollHeight : 0, clientHeight: scroll ? scroll.clientHeight : 0, renderedRows: total };
  });

  /* 滚到表格底部——这正是之前会让窗口落到数据之外的操作。 */
  await page.evaluate(() => { const s = document.querySelector(".table-scroll"); if (s) s.scrollTop = s.scrollHeight; });
  await page.waitForTimeout(700);

  const after = await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('tr[data-data-row="true"]'));
    const last = rows[rows.length - 1];
    const r = last ? last.getBoundingClientRect() : null;
    return {
      renderedRows: rows.length,
      lastRowVisible: r ? r.height > 4 && r.bottom > 0 && r.top < window.innerHeight : false,
      lastRowHeight: r ? Math.round(r.height) : 0,
      blank: rows.length === 0,
    };
  });

  /* 真实点击最后一行的「复制」按钮：修复前这里必然超时。 */
  let clickResult = "no-button";
  const copyBtns = page.locator(".table-scroll .cs-copybtn");
  const n = await copyBtns.count();
  if (n > 0) {
    try { await copyBtns.nth(n - 1).click({ timeout: 6000 }); clickResult = "OK"; }
    catch (e) { clickResult = "FAIL: " + String(e).slice(0, 90); }
  }

  console.log(route.padEnd(24) + " rows=" + String(before.renderedRows).padStart(3) +
    " -> afterScroll=" + String(after.renderedRows).padStart(3) +
    " lastRowH=" + after.lastRowHeight +
    " blank=" + after.blank +
    " lastRowVisible=" + after.lastRowVisible +
    " lastCopyClick=" + clickResult);
}
console.log("console errors (non-network):", consoleErrors.length ? JSON.stringify(consoleErrors) : "0");
await browser.close();
