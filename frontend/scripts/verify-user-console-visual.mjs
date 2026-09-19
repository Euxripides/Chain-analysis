#!/usr/bin/env node
/**
 * 用户端真实渲染度量（蓝图 §13 / §14 / §20.2）
 *
 * 为什么不用截图：截图只能证明"看起来像"，不能证明"没有溢出、对比度达标、触摸目标够大"。
 * 本脚本在真实 Chromium 里**度量实际值**并输出 JSON 报告，可作为验收证据存档。
 *
 * 用法：
 *   node scripts/verify-user-console-visual.mjs --base http://127.0.0.1:5199/app-v2 --out <dir>
 *   node scripts/verify-user-console-visual.mjs --base $E2E_BASE_URL/app-v2 --login   # 带真实登录
 *
 * 不带 --login 时只检查匿名可见的路由（登录页），用于本地与无后端环境的冒烟验证。
 */
import { chromium } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const args = process.argv.slice(2);
const arg = (name, fallback) => {
  const index = args.indexOf(name);
  return index >= 0 && args[index + 1] ? args[index + 1] : fallback;
};
const has = (name) => args.includes(name);

const BASE = arg("--base", process.env.E2E_BASE_URL ? process.env.E2E_BASE_URL.replace(/\/$/, "") + "/app-v2" : "http://127.0.0.1:5199/app-v2");
const OUT = resolve(arg("--out", "acceptance-evidence/user-console-redesign"));
const DO_LOGIN = has("--login");
const EMAIL = process.env.E2E_USER_EMAIL ?? "admin@local";
const PASSWORD = process.env.E2E_USER_PASSWORD;

const BREAKPOINTS = [
  { name: "390x844", width: 390, height: 844 },
  { name: "768x1024", width: 768, height: 1024 },
  { name: "1024x768", width: 1024, height: 768 },
  { name: "1440x900", width: 1440, height: 900 },
  { name: "1920x1080", width: 1920, height: 1080 },
  { name: "2560x1440", width: 2560, height: 1440 },
];

const ANON_ROUTES = ["/login"];
const AUTH_ROUTES = [
  "/home", "/search", "/cases", "/cases/new", "/investigations", "/investigations/new",
  "/graph", "/explorer", "/downloads/new", "/jobs", "/exports", "/inbox", "/reports",
  "/notifications", "/profile",
];

/** 与 tests/dual-system-quality.spec.ts 同一套对比度算法，保证判定口径一致。 */
const MEASURE = `(() => {
  const parse = (value) => {
    const m = String(value).match(/rgba?\\(([^)]+)\\)/);
    if (!m) return null;
    const parts = m[1].split(',').map((v) => parseFloat(v.trim()));
    return { rgb: parts.slice(0, 3), alpha: parts.length > 3 ? parts[3] : 1 };
  };
  const lum = (rgb) => {
    const [r, g, b] = rgb.map((v) => { const c = v / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const ratio = (a, b) => { const la = lum(a), lb = lum(b); const hi = Math.max(la, lb), lo = Math.min(la, lb); return (hi + 0.05) / (lo + 0.05); };

  const doc = document.documentElement;
  const contrastFailures = [];
  const skippedGradient = [];
  const touchTargets = [];
  const smallFonts = [];
  let unnamedButtons = 0;
  let progressbarsWithoutAria = 0;

  for (const el of Array.from(document.querySelectorAll('body *'))) {
    const rect = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    if (style.visibility === 'hidden' || style.display === 'none' || style.opacity === '0') continue;
    if (rect.width < 2 || rect.height < 2) continue;

    const text = (el.textContent || '').trim();
    if (text && el.children.length === 0) {
      const fg = parse(style.color);
      if (fg) {
        let bg = null;
        let gradient = false;
        let node = el;
        while (node && node !== doc) {
          const nodeStyle = getComputedStyle(node);
          const candidate = parse(nodeStyle.backgroundColor);
          // 渐变背景（品牌标记、深色侧栏等）无法用 backgroundColor 还原实际颜色。
          // 强行取父级颜色会把"白字 + 靛蓝渐变"误判成"白字 + 白底"（比值 1.0），
          // 产生假失败。这类元素单独计入 skippedGradient，不做对比度判定。
          if (String(nodeStyle.backgroundImage).includes("gradient")) { gradient = true; break; }
          if (candidate && candidate.alpha >= 0.999) { bg = candidate; break; }
          node = node.parentElement;
        }
        if (gradient) { skippedGradient.push(String(el.className || el.tagName).slice(0, 40)); }
        if (!bg && !gradient) bg = { rgb: [255, 255, 255], alpha: 1 };
        const fontSize = parseFloat(style.fontSize);
        const bold = parseInt(style.fontWeight, 10) >= 700;
        const large = fontSize >= 24 || (fontSize >= 18.66 && bold);
        const r = bg ? ratio(fg.rgb, bg.rgb) : null;
        if (r !== null && r < (large ? 3 : 4.5)) {
          contrastFailures.push({ cls: String(el.className || el.tagName).slice(0, 60), ratio: Number(r.toFixed(2)), sample: text.slice(0, 30) });
        }
        if (fontSize < 13) smallFonts.push({ cls: String(el.className || el.tagName).slice(0, 40), fontSize, sample: text.slice(0, 20) });
      }
    }

    if (el.tagName === 'BUTTON') {
      const name = el.getAttribute('aria-label') || (el.textContent || '').trim() || el.getAttribute('title') || '';
      if (!name) unnamedButtons += 1;
    }
    if (el.getAttribute('role') === 'progressbar') {
      const ok = el.getAttribute('aria-valuemin') === '0' && el.getAttribute('aria-valuemax') === '100' &&
        (el.getAttribute('aria-valuenow') !== null || el.getAttribute('aria-valuetext') !== null);
      if (!ok) progressbarsWithoutAria += 1;
    }
  }

  const coarse = window.matchMedia('(pointer: coarse)').matches;
  if (coarse) {
    for (const el of Array.from(document.querySelectorAll('.cs-iconbtn, .cs-copybtn, .btn--sm, .cs-chip'))) {
      const rect = el.getBoundingClientRect();
      if (rect.width < 2 || rect.height < 2) continue;
      if (rect.width < 44 || rect.height < 44) {
        touchTargets.push({ cls: String(el.className).slice(0, 48), w: Math.round(rect.width), h: Math.round(rect.height) });
      }
    }
  }

  const cs = getComputedStyle(doc);
  return {
    scrollWidth: doc.scrollWidth,
    clientWidth: doc.clientWidth,
    bodyOverflowX: getComputedStyle(document.body).overflowX,
    appScope: doc.dataset.app ?? null,
    tokens: {
      cobalt: cs.getPropertyValue('--c-cobalt').trim(),
      ink: cs.getPropertyValue('--c-ink').trim(),
      ice: cs.getPropertyValue('--c-ice').trim(),
      graphite: cs.getPropertyValue('--c-graphite').trim(),
      slate: cs.getPropertyValue('--c-slate').trim(),
      border: cs.getPropertyValue('--c-border').trim(),
    },
    contrastFailures: contrastFailures.slice(0, 20),
    skippedGradient: Array.from(new Set(skippedGradient)).slice(0, 10),
    smallFonts: smallFonts.slice(0, 20),
    unnamedButtons,
    progressbarsWithoutAria,
    touchTargets: touchTargets.slice(0, 20),
    inlineNotices: ['[role="alert"]', '.banner', '.alert', '.alert-banner', '.notice', '.callout', '.toast-inline']
      .flatMap((s) => Array.from(document.querySelectorAll(s)).map((el) => s + ':' + el.className)),
  };
})()`;

async function main() {
  const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || undefined });
  /*
   * 验收入口 https://127.0.0.1:8448 是自签名的回环 TLS（socat → nginx），
   * 与 playwright.dual.config.ts 的 ignoreHTTPSErrors 保持一致。
   */
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  /*
   * 分开记录两类控制台输出：
   *   pageErrors     —— 运行时异常 / console.error，**必须为 0**；
   *   resourceErrors —— 浏览器对失败网络请求的固有记录（4xx/5xx）。
   * 后者在"未登录访问 401 端点"这类合法场景下也会出现，属于环境事实，
   * 不能被当成代码缺陷，也不能被静默忽略——单独列出并带上状态码。
   */
  const pageErrors = [];
  const resourceErrors = [];
  page.on("console", (message) => {
    if (message.type() !== "error") return;
    const text = message.text();
    if (text.startsWith("Failed to load resource")) resourceErrors.push(text);
    else pageErrors.push(text);
  });
  page.on("pageerror", (error) => pageErrors.push(String(error)));

  if (DO_LOGIN) {
    if (!PASSWORD) throw new Error("--login 需要 E2E_USER_PASSWORD");
    await page.goto(BASE + "/login", { waitUntil: "domcontentloaded" });
    await page.locator("#identifier").fill(EMAIL);
    await page.locator("#password").fill(PASSWORD);
    await page.getByRole("button", { name: /^登录$/ }).click();
    const wsDialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
    await wsDialog.waitFor({ state: "visible", timeout: 5000 }).catch(() => {});
    if (await wsDialog.isVisible().catch(() => false)) {
      await wsDialog.getByRole("button").nth(1).click();
    }
    await page.waitForTimeout(2500);
  }

  const routes = DO_LOGIN ? AUTH_ROUTES : ANON_ROUTES;
  const report = { base: BASE, login: DO_LOGIN, generatedAt: new Date().toISOString(), results: [], pageErrors, resourceErrors };

  for (const bp of BREAKPOINTS) {
    await page.setViewportSize({ width: bp.width, height: bp.height });
    for (const route of routes) {
      const url = BASE + route;
      try {
        /*
         * 必须等网络静默后再量：只等 domcontentloaded 会在数据回来**之前**测量，
         * 那时元信息条、表格、指标卡都还是骨架，长值（UUID / 数据源 / 完整哈希）
         * 还没进 DOM，横向溢出会被漏掉。这个坑是 dual-system-quality 的 390px 用例
         * 先发现的——自检脚本必须和它一样严格。
         */
        await page.goto(url, { waitUntil: "networkidle", timeout: 45000 });
        await page.waitForTimeout(700);
        const measured = await page.evaluate(MEASURE);
        report.results.push({
          breakpoint: bp.name,
          route,
          finalUrl: page.url(),
          ...measured,
          overflow: measured.scrollWidth > measured.clientWidth + 1,
        });
      } catch (error) {
        report.results.push({ breakpoint: bp.name, route, error: String(error).slice(0, 200) });
      }
    }
  }

  mkdirSync(OUT, { recursive: true });
  const file = resolve(OUT, DO_LOGIN ? "visual-report-authenticated.json" : "visual-report-anonymous.json");
  writeFileSync(file, JSON.stringify(report, null, 2), "utf-8");

  const overflows = report.results.filter((r) => r.overflow);
  const contrast = report.results.flatMap((r) => (r.contrastFailures ?? []).map((f) => r.breakpoint + " " + r.route + " " + JSON.stringify(f)));
  const notices = report.results.flatMap((r) => r.inlineNotices ?? []);
  const touch = report.results.flatMap((r) => (r.touchTargets ?? []).map((t) => r.breakpoint + " " + r.route + " " + JSON.stringify(t)));
  const progress = report.results.filter((r) => (r.progressbarsWithoutAria ?? 0) > 0);

  console.log("用户端渲染度量报告：" + file);
  console.log("  检查组合数        " + report.results.length);
  console.log("  横向溢出          " + overflows.length + (overflows.length ? " → " + overflows.map((o) => o.breakpoint + o.route).join(", ") : ""));
  console.log("  对比度不达标      " + contrast.length);
  console.log("  页面内提示选择器  " + notices.length + (notices.length ? " → " + notices.slice(0, 5).join(", ") : ""));
  console.log("  触摸目标不足      " + touch.length);
  console.log("  进度条缺少 ARIA   " + progress.length);
  console.log("  无名称按钮        " + report.results.reduce((sum, r) => sum + (r.unnamedButtons ?? 0), 0));
  console.log("  运行时 console 错误 " + pageErrors.length + (pageErrors.length ? " → " + pageErrors.slice(0, 3).join(" | ") : ""));
  console.log("  网络资源错误      " + Array.from(new Set(resourceErrors)).length + "（4xx/5xx 请求，未登录场景下属正常）");
  const sample = report.results[0];
  if (sample) console.log("  令牌快照(" + (sample.finalUrl || "") + ") " + JSON.stringify(sample.tokens));

  await browser.close();
  const failed = overflows.length + contrast.length + notices.length + touch.length + progress.length + pageErrors.length > 0;
  process.exit(failed ? 1 : 0);
}

main().catch((error) => {
  console.error(error);
  process.exit(2);
});
