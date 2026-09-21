import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || undefined });
/* 全新 context，没有任何缓存 */
const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await ctx.newPage();
const html = await (await ctx.request.get(BASE + "/app-v2/")).text();
console.log("=== 服务端返回的 index.html ===");
console.log(html.split("\n").filter((l) => l.includes("assets/") || l.includes("icon")).join("\n"));
console.log();
await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
const markers = await page.evaluate(() => ({
  appScope: document.documentElement.dataset.app ?? null,
  cobalt: getComputedStyle(document.documentElement).getPropertyValue("--c-cobalt").trim(),
  ink: getComputedStyle(document.documentElement).getPropertyValue("--c-ink").trim(),
  hasShellRail: !!document.querySelector(".cs-shell__rail"),
  hasAuthAside: !!document.querySelector(".auth-aside"),
  hasFlowGraph: !!document.querySelector(".auth-flow"),
  hasOtp: !!document.querySelector(".cs-otp"),
  hasTopToast: !!document.querySelector(".top-toast"),
  classes: Array.from(document.querySelectorAll("[class]")).slice(0, 0).length,
}));
console.log("=== 登录页实际渲染标记 ===");
console.log(JSON.stringify(markers, null, 1));
console.log();
/* 未登录时登录页的可见文案 */
const text = (await page.locator("body").innerText()).replace(/\s+/g, " ").slice(0, 300);
console.log("=== 登录页可见文案 ===");
console.log(text);
await browser.close();
