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

await page.goto(BASE + "/app-v2/investigations", { waitUntil: "networkidle" });
await page.waitForTimeout(700);

const experiment = async (label, css) => {
  const r = await page.evaluate((cssText) => {
    const style = document.createElement("style");
    style.textContent = cssText;
    document.head.appendChild(style);
    const out = { scrollWidth: document.documentElement.scrollWidth, bodyScroll: document.body.scrollWidth };
    style.remove();
    return out;
  }, css);
  console.log(label.padEnd(52) + " rootScrollW=" + r.scrollWidth + "  bodyScrollW=" + r.bodyScroll);
};

await experiment("baseline", "");
await experiment("+ .table-scroll { overflow-x: hidden }", ".table-scroll{overflow-x:hidden !important}");
await experiment("+ frozen cells static", ".table__th--frozen,.table__td--frozen{position:static !important}");
await experiment("+ both", ".table-scroll{overflow-x:hidden !important}.table__th--frozen,.table__td--frozen{position:static !important}");
await experiment("+ .table-scroll { contain: paint }", ".table-scroll{contain:paint}");
await experiment("+ table min-width:0", ".table{min-width:0 !important}");
const detail = await page.evaluate(() => {
  const s = document.querySelector(".table-scroll");
  const t = document.querySelector(".table table--virtual") || document.querySelector("table");
  const frozen = document.querySelector(".table__td--frozen");
  return {
    scrollClientW: s.clientWidth, scrollWidth: s.scrollWidth, scrollLeft: s.scrollLeft,
    tableW: t ? Math.round(t.getBoundingClientRect().width) : null,
    tableMinWidth: t ? getComputedStyle(t).minWidth : null,
    frozenW: frozen ? Math.round(frozen.getBoundingClientRect().width) : null,
    frozenLeft: frozen ? Math.round(frozen.getBoundingClientRect().left) : null,
    frozenPosition: frozen ? getComputedStyle(frozen).position : null,
  };
});
console.log("detail:", JSON.stringify(detail));
await browser.close();
