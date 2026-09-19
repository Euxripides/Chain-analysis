import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || undefined });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await ctx.newPage();
/* 在导航之前注入：CSP 违规事件带 sourceFile/lineNumber，能直接定位到出错的资源。 */
await page.addInitScript(() => {
  window.__csp = [];
  document.addEventListener("securitypolicyviolation", (e) => {
    window.__csp.push({
      directive: e.violatedDirective,
      blockedURI: e.blockedURI,
      sourceFile: e.sourceFile,
      line: e.lineNumber,
      column: e.columnNumber,
      sample: (e.sample || "").slice(0, 120),
    });
  });
});
await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(4000);
const csp = await page.evaluate(() => window.__csp || []);
console.log("violations on /app-v2/login:", JSON.stringify(csp, null, 1));
const dom = await page.evaluate(() => ({
  imgs: document.querySelectorAll("img").length,
  icons: Array.from(document.querySelectorAll('link[rel*="icon"]')).map((l) => l.getAttribute("href")),
  bgImagesWithEmptyUrl: Array.from(document.querySelectorAll("body *")).flatMap((el) => {
    const bg = getComputedStyle(el).backgroundImage;
    return bg && bg.includes('url("")') ? [(el.className || el.tagName).toString().slice(0, 50)] : [];
  }).slice(0, 5),
}));
console.log("dom:", JSON.stringify(dom));
await browser.close();
