import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || undefined });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 } });
const page = await ctx.newPage();
await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(process.env.SWEEP_EMAIL ?? "admin@local");
await page.locator("#password").fill(process.env.SWEEP_PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
await page.waitForTimeout(2500);
const d0 = page.getByRole("dialog").first();
if (await d0.isVisible().catch(() => false)) { await page.keyboard.press("Escape"); await page.waitForTimeout(200); }

for (const route of (process.env.PROBE_ROUTES ?? "/app-v2/investigations").split(",")) {
  await page.goto(BASE + route, { waitUntil: "networkidle" });
  await page.waitForTimeout(700);
  const info = await page.evaluate(() => {
    const doc = document.documentElement;
    const vw = doc.clientWidth;
    /* 判断元素是否处于一个可横向滚动的祖先之内——这种元素自身的宽度不算页面溢出 */
    const inScroller = (el) => {
      let n = el.parentElement;
      while (n && n !== doc) {
        const s = getComputedStyle(n);
        if (s.overflowX === "auto" || s.overflowX === "scroll" || s.overflowX === "hidden") {
          if (n.scrollWidth > n.clientWidth + 1) return (n.className || n.tagName).toString().slice(0, 40);
        }
        n = n.parentElement;
      }
      return null;
    };
    const offenders = [];
    for (const el of Array.from(document.querySelectorAll("body *"))) {
      const r = el.getBoundingClientRect();
      if (r.right <= vw + 1 && r.width <= vw + 1) continue;
      const scroller = inScroller(el);
      if (scroller) continue;
      offenders.push({
        tag: el.tagName,
        cls: (el.className || "").toString().slice(0, 55),
        w: Math.round(r.width),
        right: Math.round(r.right),
        parent: (el.parentElement?.className || el.parentElement?.tagName || "").toString().slice(0, 45),
        text: (el.textContent || "").replace(/\s+/g, " ").trim().slice(0, 34),
      });
    }
    return { scrollWidth: doc.scrollWidth, clientWidth: vw, offenders: offenders.slice(0, 16) };
  });
  console.log("== " + route + " scrollW=" + info.scrollWidth + " clientW=" + info.clientWidth + " trueOffenders=" + info.offenders.length);
  for (const o of info.offenders) console.log("   " + o.w + "px right=" + o.right + " <" + o.tag + " class=" + o.cls + "> parent=" + o.parent + " text=" + JSON.stringify(o.text));
}
await browser.close();
