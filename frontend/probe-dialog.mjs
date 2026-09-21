import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL || "https://127.0.0.1:8448";
const EMAIL = process.env.EMAIL, PASSWORD = process.env.PASSWORD;
const WS = process.env.WS_SLUG || "acme-lab", ADDRESS = process.env.ADDRESS;
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || "/usr/bin/chromium-browser" });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
page.on("pageerror", (e) => console.log("pageerror:", String(e).slice(0, 200)));
await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(EMAIL);
await page.locator("#password").fill(PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
const wsDialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
await wsDialog.waitFor({ state: "visible", timeout: 8000 }).catch(() => {});
if (await wsDialog.isVisible().catch(() => false)) await wsDialog.getByRole("button", { name: new RegExp(WS) }).first().click();
await page.locator(".auth-card").waitFor({ state: "detached", timeout: 20000 }).catch(() => {});
await page.goto(BASE + "/app-v2/downloads/new", { waitUntil: "domcontentloaded" });
await page.locator("#download-addresses").fill(ADDRESS);
await page.getByRole("button", { name: "解析导入" }).click();
await page.waitForTimeout(4000);
let dialogs = await page.getByRole("dialog").all();
console.log("STEP1 dialogs:", dialogs.length);
for (const d of dialogs) {
  console.log("STEP1 text >>>", (await d.innerText().catch(() => "")).replace(/\n+/g, " | ").slice(0, 500));
  console.log("STEP1 buttons:", JSON.stringify(await d.getByRole("button").allInnerTexts().catch(() => [])));
}
const step2 = page.getByRole("button", { name: "提交服务端校验" });
console.log("step2 button count:", await step2.count());
if (await step2.count()) {
  await step2.first().click();
  await page.waitForTimeout(6000);
  dialogs = await page.getByRole("dialog").all();
  console.log("STEP2 dialogs:", dialogs.length);
  for (const d of dialogs) {
    console.log("STEP2 text >>>", (await d.innerText().catch(() => "")).replace(/\n+/g, " | ").slice(0, 700));
    console.log("STEP2 buttons:", JSON.stringify(await d.getByRole("button").allInnerTexts().catch(() => [])));
  }
  console.log("addresses field:", (await page.locator("#download-addresses").inputValue().catch(() => "<n/a>")).slice(0, 160));
}
await page.screenshot({ path: "/tmp/dialog-probe.png" });
await browser.close();
