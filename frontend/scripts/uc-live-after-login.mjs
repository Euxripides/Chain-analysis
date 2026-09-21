import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || undefined });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(process.env.SWEEP_EMAIL ?? "admin@local");
await page.locator("#password").fill(process.env.SWEEP_PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
await page.waitForTimeout(3500);
const d = page.getByRole("dialog").first();
if (await d.isVisible().catch(() => false)) { await page.keyboard.press("Escape"); await page.waitForTimeout(300); }

const home = await page.evaluate(() => ({
  url: location.pathname,
  /* 新版 Shell 独有 */
  csShellRail: !!document.querySelector(".cs-shell__rail"),
  csNavGroup: !!document.querySelector(".cs-nav__group"),
  csCmdTrigger: !!document.querySelector(".cs-cmdtrigger"),
  csUserMenu: !!document.querySelector(".cs-usermenu"),
  csBento: !!document.querySelector(".cs-bento"),
  csMetricCard: !!document.querySelector(".metric__info"),
  /* 旧版 Shell 独有 */
  oldShellNav: !!document.querySelector(".shell__nav"),
  oldMobileToggle: !!document.querySelector(".shell__mobile-toggle"),
  oldMetricGrid: !!document.querySelector(".metric-grid") && !document.querySelector(".cs-bento"),
  pageTitle: document.title,
}));
console.log("=== 登录后 /home 的界面标记 ===");
console.log(JSON.stringify(home, null, 1));
console.log();
const nav = await page.locator(".cs-nav__link, .shell__link").allInnerTexts();
console.log("=== 侧栏导航项 ===");
console.log(nav.map((t) => "- " + t.replace(/\s+/g, " ").trim()).join("\n"));
console.log();
const body = (await page.locator("main").innerText()).replace(/\s+/g, " ").slice(0, 420);
console.log("=== 首页正文开头 ===");
console.log(body);
await browser.close();
