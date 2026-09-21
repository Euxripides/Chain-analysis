import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL || "https://127.0.0.1:8448";
const ADDR = process.env.ADDR, CHAIN = process.env.CHAIN;
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || "/usr/bin/chromium-browser" });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
let reqs = 0, oks = 0, fails = 0;
page.on("request", (r) => { if (r.url().includes("/activity")) { reqs++; console.log("REQ", reqs, r.url().split("activity")[0].slice(-40)); } });
page.on("response", (r) => { if (r.url().includes("/activity")) { oks++; console.log("RESP", r.status(), r.url().includes("from_time") ? "with-from_time" : "no-from_time"); } });
page.on("requestfailed", (r) => { if (r.url().includes("/activity")) { fails++; console.log("FAIL", r.failure()?.errorText); } });
page.on("pageerror", (e) => console.log("pageerror:", String(e).slice(0, 200)));

await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(process.env.EMAIL);
await page.locator("#password").fill(process.env.PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
const wsDialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
await wsDialog.waitFor({ state: "visible", timeout: 8000 }).catch(() => {});
if (await wsDialog.isVisible().catch(() => false)) await wsDialog.getByRole("button", { name: /acme-lab/ }).first().click();
await page.locator(".auth-card").waitFor({ state: "detached", timeout: 20000 }).catch(() => {});

await page.goto(`${BASE}/app-v2/addresses/${CHAIN}/${ADDR}?tab=transfers`, { waitUntil: "domcontentloaded" });
for (let i = 0; i < 6; i++) {
  await page.waitForTimeout(5000);
  console.log(`t=${(i + 1) * 5}s reqs=${reqs} resp=${oks} fails=${fails} summary=${await page.getByTestId("table-summary").count()} loadAllBtn=${await page.getByRole("button", { name: /加载全量（最多 10,000 行）/ }).count()}`);
}
console.log("main:", (await page.locator("main").innerText().catch(() => "")).replace(/\n+/g, " | ").slice(0, 260));
await browser.close();
