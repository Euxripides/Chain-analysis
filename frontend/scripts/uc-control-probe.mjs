import { chromium } from "@playwright/test";
import { writeFileSync, mkdirSync } from "node:fs";

const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const EMAIL = process.env.SWEEP_EMAIL ?? "admin@local";
const PASSWORD = process.env.SWEEP_PASSWORD;
const ROUTES = (process.env.DIAG_ROUTES ?? "").split(",").filter(Boolean);

const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || undefined });
const context = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await context.newPage();

await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(EMAIL);
await page.locator("#password").fill(PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
const ws = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
await ws.waitFor({ state: "visible", timeout: 6000 }).catch(() => {});
if (await ws.isVisible().catch(() => false)) await ws.getByRole("button").nth(1).click();
await page.waitForTimeout(2500);

const out = [];
for (const route of ROUTES) {
  await page.goto(BASE + route, { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(1200);

  /* 1. 页面加载后是否自动弹出了 Dialog？ */
  const dialogs = await page.getByRole("dialog").count();
  const dialogText = dialogs > 0 ? (await page.getByRole("dialog").first().innerText().catch(() => "")).slice(0, 160) : "";
  const backdrops = await page.locator(".dialog-backdrop").count();

  /* 2. 逐个控件做「可点击性」体检。 */
  const probe = await page.evaluate(() => {
    const loc = document.querySelectorAll(
      'main button, main a[href], main [role="button"], main [role="tab"], main [role="checkbox"], main [role="switch"], main summary',
    );
    const results = [];
    for (let i = 0; i < loc.length; i += 1) {
      const el = loc[i];
      const rect = el.getBoundingClientRect();
      const cx = rect.left + rect.width / 2;
      const cy = rect.top + rect.height / 2;
      const top = document.elementFromPoint(Math.max(0, Math.min(window.innerWidth - 1, cx)), Math.max(0, Math.min(window.innerHeight - 1, cy)));
      const style = getComputedStyle(el);
      /* 找出第一个把该元素裁掉的祖先 */
      let clipper = null;
      let node = el.parentElement;
      while (node && node !== document.body) {
        const s = getComputedStyle(node);
        if ((s.overflow === "hidden" || s.overflowX === "hidden") && node.scrollWidth > node.clientWidth + 1) {
          const nr = node.getBoundingClientRect();
          if (rect.right > nr.right + 1 || rect.left < nr.left - 1) { clipper = (node.className || node.tagName).toString().slice(0, 50); break; }
        }
        node = node.parentElement;
      }
      results.push({
        i,
        tag: el.tagName.toLowerCase(),
        name: (el.getAttribute("aria-label") || el.textContent || "").replace(/\s+/g, " ").trim().slice(0, 40),
        w: Math.round(rect.width),
        h: Math.round(rect.height),
        inViewport: rect.top < window.innerHeight && rect.bottom > 0 && rect.left < window.innerWidth && rect.right > 0,
        coveredBy: top && !el.contains(top) && top !== el ? (top.className || top.tagName).toString().slice(0, 50) : null,
        visibility: style.visibility,
        display: style.display,
        clipper,
      });
    }
    return results;
  });

  out.push({ route, dialogsOnLoad: dialogs, backdropsOnLoad: backdrops, dialogTextOnLoad: dialogText, total: probe.length, problems: probe.filter((p) => p.coveredBy || p.clipper || p.visibility !== "visible" || p.display === "none" || p.w < 2 || p.h < 2) });
}

mkdirSync("/tmp/uc-diag", { recursive: true });
writeFileSync("/tmp/uc-diag/probe.json", JSON.stringify(out, null, 2));
for (const r of out) {
  console.log("== " + r.route + "  controls=" + r.total + " dialogsOnLoad=" + r.dialogsOnLoad + (r.dialogTextOnLoad ? " [" + r.dialogTextOnLoad.replace(/\n/g, " ").slice(0, 90) + "]" : ""));
  for (const p of r.problems.slice(0, 12)) {
    console.log("   #" + p.i + " [" + p.tag + "] " + p.name + "  " + p.w + "x" + p.h +
      (p.coveredBy ? "  COVERED_BY=" + p.coveredBy : "") + (p.clipper ? "  CLIPPED_BY=" + p.clipper : "") +
      (p.visibility !== "visible" ? "  visibility=" + p.visibility : "") + (p.display === "none" ? "  display=none" : "") +
      (p.w < 2 || p.h < 2 ? "  ZERO_SIZE" : "") + (p.inViewport ? "" : "  OFFSCREEN"));
  }
  if (r.problems.length > 12) console.log("   ... +" + (r.problems.length - 12) + " more");
}
await browser.close();
