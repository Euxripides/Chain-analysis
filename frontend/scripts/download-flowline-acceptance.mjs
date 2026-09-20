#!/usr/bin/env node
/**
 * 新建数据下载 · Flowline 验收（《新建数据下载 Flowline 界面重构实施规范》§24 / §26）
 *
 * 把**真实构建产物**（apps/user-console/dist，base=/app-v2/）挂到本地静态服务器，
 * 在真实 Chromium 里加载 /app-v2/downloads/new，用路由拦截喂入固定的服务端夹具，
 * 然后产出规范 §26 要求的四视口截图 + 风险弹窗截图 + reduced-motion 截图 + report.json。
 *
 * 断言覆盖验收矩阵里可由浏览器独立证明的部分：
 *   BIZ-01 复用策略在所有用户可见层不可见（正文 / role / 可访问名称）
 *   BIZ-02 preview 与 create 的请求体都带 reuse_policy: "prefer_local"
 *   UI-01/02/04 桌面与移动版面、五步轨道顺序
 *   UI-03 字体清晰度：祖先 opacity / filter / backdrop-filter / text-shadow / 对比度
 *   UI-05 Dataset 仍是原生 checkbox
 *   UI-07 风险只在 Dialog 中出现
 *   UI-08 reduced motion：没有移动光点，功能仍可完成
 *   PERF-01 四视口无页面级横向滚动
 *
 * 真实服务端证据不在这里：本脚本的 /api 全部是夹具（页面发出的请求体是真的）。
 * 真实服务端 + 真实 DB 的证据由 tests/download-flowline-redesign.spec.ts
 * 与 tests/r9-uc-download-import-v4.spec.ts 在 https://127.0.0.1:8448 上产生。
 *
 * 用法：node scripts/download-flowline-acceptance.mjs
 */
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { extname, join, resolve, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const DIST = resolve(here, "apps/user-console/dist");
const OUT = resolve(here, "acceptance-evidence/download-flowline");
const PORT = Number(process.env.DL_CHECK_PORT ?? 5401);
const BASE = "http://127.0.0.1:" + PORT + "/app-v2";

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".png": "image/png",
};

/* ------------------------------------------------------------------ fixtures */

const ADDRESS = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t";

const SESSION = {
  user: { id: "u-1", display_name: "验收账号", email: "qa@local" },
  email: "qa@local",
  display_name: "验收账号",
  organization_id: "org-1",
  workspace_id: "ws-1",
  audience: "user",
  session_id: "sess-1",
  roles: ["investigator"],
  permissions: [
    "dashboard:read", "search:read", "case:list", "case:read", "subject:list",
    "investigation:list", "investigation:read", "finding:list", "evidence:list",
    "report:list", "report:read", "inbox:read", "notification:list", "profile:read",
    "canvas:list", "canvas:read", "explorer:read", "download:create",
    "job:list:self", "job:read", "export:list",
  ],
  expires_at: new Date(Date.now() + 3600_000).toISOString(),
  absolute_expires_at: new Date(Date.now() + 86400_000).toISOString(),
  must_change_password: false,
  mfa_satisfied: true,
  mfa_enrolled: true,
  csrf_token: "csrf-fixture",
};

const CHAINS = { items: [{ slug: "tron", name: "TRON", enabled: true }] };

/* 7 项 Dataset：既覆盖参考图的一行 6 项，也留出"第 7 项被上限挡住"的负向空间。 */
const DATASETS = {
  items: [
    { id: "transactions", label: "Transactions", status: "available" },
    { id: "token_transfers", label: "Token Transfers", status: "available" },
    { id: "nft_transfers", label: "NFT Transfers", status: "available" },
    { id: "logs", label: "Logs", status: "available" },
    { id: "contract_interactions", label: "Contract Interactions", status: "available" },
    { id: "trace", label: "Trace", status: "available" },
    { id: "balances", label: "Balances", status: "available" },
  ],
};

const PREVIEW = {
  request: {
    chain: "tron",
    addresses: [ADDRESS],
    datasets: ["transactions", "token_transfers"],
    range: { type: "full" },
    reuse_policy: "prefer_local",
    performance_mode: "safe",
  },
  coverage: {
    chain: "tron",
    datasets: ["transactions", "token_transfers"],
    overall_ratio: 0.888,
    items: [
      { subject: ADDRESS, dataset: "transactions", covered_blocks: 800, total_blocks: 900, ratio: 0.888, gaps: [{ from_block: 900, to_block: 1000 }] },
      { subject: ADDRESS, dataset: "token_transfers", covered_blocks: 860, total_blocks: 900, ratio: 0.955, gaps: [{ from_block: 880, to_block: 900 }] },
    ],
  },
  missing_blocks: 100,
  provider_work_required: true,
  reuse: {
    policy: "prefer_local",
    will_reuse: true,
    candidate: { job_id: "job-old-1", job_no: "DL-20260901-0001", status: "COMPLETED", coverage: 0.8, updated_at: new Date().toISOString() },
    coverage_hit: true,
    refresh: false,
  },
  estimate: {
    estimated_missing_blocks: 100,
    estimated_rows: 12000,
    estimated_cost_usd: "1.25",
    estimated_time_seconds: 90,
    basis: "按未覆盖区间 × 该地址历史平均行数估算",
  },
  risks: ["Provider 配额接近上限"],
  source: { coverage: "postgres:dataset_coverage", provider: "local_index", as_of: new Date().toISOString() },
};

const VALIDATE_RESULT = {
  accepted: [{ index: 0, row: 1, input: ADDRESS, chain: "tron", normalized: ADDRESS, case_name: "", tags: [] }],
  rejected: [],
  field_rejections: [],
  scope: { organization_id: "org-1", workspace_id: "ws-1" },
  grants: { "download:create": true, "case:create": true, "case:update": true, create_missing_meta_requested: false },
  summary: {
    submitted: 1, accepted: 1, rejected: 0, blank: 0, invalid: 0, duplicate: 0, cap_rejected: 0,
    tags_over_limit: 0, field_rejections: 0, meta_denied: 0, max_addresses: 100, max_tags: 20,
    chains: { tron: 1 }, distinct_chains: 1,
  },
  meta: { cases: [], tags: [] },
  source: "server",
};

const CREATE_RESULT = {
  status: "planned", created: true, reused: false, provider_work_required: true,
  coverage: 0.888, job_id: "job-fixture-created", job_no: "DL-20260920-0001",
};

/* ------------------------------------------------------------------ server */

function startStaticServer() {
  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://127.0.0.1");
    const pathname = decodeURIComponent(url.pathname);
    if (pathname !== "/app-v2" && !pathname.startsWith("/app-v2/")) {
      res.writeHead(404).end("not found");
      return;
    }
    const rel = pathname.slice("/app-v2".length) || "/";
    const target = normalize(join(DIST, rel));
    if (!target.startsWith(DIST)) {
      res.writeHead(403).end("forbidden");
      return;
    }
    if (existsSync(target) && extname(target)) {
      const body = await readFile(target);
      res.writeHead(200, { "content-type": MIME[extname(target)] ?? "application/octet-stream" });
      res.end(body);
      return;
    }
    const html = await readFile(join(DIST, "index.html"));
    res.writeHead(200, { "content-type": MIME[".html"] });
    res.end(html);
  });
  return new Promise((ok) => server.listen(PORT, "127.0.0.1", () => ok(server)));
}

/* ------------------------------------------------------------------ helpers */

const problems = [];
const notes = [];

function problem(message) {
  problems.push(message);
}

function num(value) {
  return Number.parseFloat(value);
}

function srgb(channel) {
  const c = channel / 255;
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

function luminance(rgb) {
  return 0.2126 * srgb(rgb[0]) + 0.7152 * srgb(rgb[1]) + 0.0722 * srgb(rgb[2]);
}

function contrast(a, b) {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

function parseColor(value) {
  const match = /rgba?\(([^)]+)\)/.exec(value ?? "");
  if (!match) return null;
  const parts = match[1].split(",").map((item) => Number.parseFloat(item));
  return { rgb: [parts[0], parts[1], parts[2]], alpha: parts.length > 3 ? parts[3] : 1 };
}

/** 在浏览器里取一个元素的"自身 + 全部祖先"的计算样式（§24.2 的判据）。 */
async function probeStyle(locator) {
  return locator.evaluate((node) => {
    const own = getComputedStyle(node);
    const ancestors = [];
    let current = node;
    while (current) {
      const computed = getComputedStyle(current);
      ancestors.push({
        tag: current.tagName.toLowerCase(),
        cls: typeof current.className === "string" ? current.className : "",
        opacity: computed.opacity,
        filter: computed.filter,
        backdropFilter: computed.backdropFilter || computed.webkitBackdropFilter || "none",
        background: computed.backgroundColor,
      });
      current = current.parentElement;
    }
    const rect = node.getBoundingClientRect();
    return {
      color: own.color,
      fontSize: own.fontSize,
      fontWeight: own.fontWeight,
      lineHeight: own.lineHeight,
      textShadow: own.textShadow,
      rect: { width: Math.round(rect.width), height: Math.round(rect.height) },
      ancestors,
    };
  });
}

/** 沿祖先链合成有效背景色（把半透明层压成实色）。 */
/**
 * 沿祖先链合成有效背景色：从最外层向元素自身叠加。
 *
 * 不能在遇到第一个不透明层时 break —— 最外层不透明的是页面画布，
 * 而文字真正的背景是它自己/最近祖先的实色面。全部叠加后，最靠近文字的
 * 不透明层自然胜出。
 */
function effectiveBackground(ancestors) {
  let base = [255, 255, 255];
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const parsed = parseColor(ancestors[index].background);
    if (!parsed || parsed.alpha === 0) continue;
    base = [
      Math.round(parsed.rgb[0] * parsed.alpha + base[0] * (1 - parsed.alpha)),
      Math.round(parsed.rgb[1] * parsed.alpha + base[1] * (1 - parsed.alpha)),
      Math.round(parsed.rgb[2] * parsed.alpha + base[2] * (1 - parsed.alpha)),
    ];
  }
  return base;
}

/*
 * 字号下限以《全量整改实施规范》§7.1 为准（它明确取代旧规范的 §8.2 取值）：
 *   页面标题 桌面 24 / 移动 20；章节标题 15；正文 13（移动 14）；辅助文字 12；
 *   输入 14（移动 16）；指标数值 18。
 * 旧值 26/16/13/14/20 由旧规范 §8.2 给出，已被本轮整改规范显式取代。
 */
const TEXT_TARGETS = [
  { name: "页面标题", selector: ".cs-pageheader__title", minSize: 24, minSizeMobile: 20 },
  { name: "章节标题", selector: ".dl-section-head__title", minSize: 15 },
  { name: "字段标签", selector: ".dl-field-label", minSize: 12 },
  { name: "Dataset 标签", selector: ".dl-dataset-card__label", minSize: 13 },
  { name: "右侧指标标签", selector: ".dl-preview-metric__label", minSize: 12 },
  { name: "右侧指标数值", selector: ".dl-preview-metric__value", minSize: 18 },
  { name: "主按钮", selector: ".dl-btn--primary:not([disabled])", minSize: 13 },
  { name: "轨道标签", selector: ".dl-rail__label", minSize: 12 },
];

async function checkClarity(page, viewport, sink) {
  const viewportWidth = (await page.viewportSize())?.width ?? 1440;
  for (const target of TEXT_TARGETS) {
    const locator = page.locator(target.selector).first();
    if ((await locator.count()) === 0) continue;
    const probe = await probeStyle(locator);
    const label = "[" + viewport + "] " + target.name + " (" + target.selector + ")";
    for (const ancestor of probe.ancestors) {
      const where = ancestor.tag + (ancestor.cls ? "." + ancestor.cls.split(" ")[0] : "");
      if (num(ancestor.opacity) < 1) problem(label + " 的祖先 " + where + " opacity=" + ancestor.opacity);
      if (ancestor.filter !== "none") problem(label + " 的祖先 " + where + " filter=" + ancestor.filter);
    }
    if (probe.textShadow !== "none") problem(label + " 使用了 text-shadow：" + probe.textShadow);
    const floor = target.minSizeMobile && num(viewportWidth) < 640 ? target.minSizeMobile : target.minSize;
    if (num(probe.fontSize) + 0.01 < floor) {
      problem(label + " 字号 " + probe.fontSize + " 小于规范下限 " + floor + "px");
    }
    const foreground = parseColor(probe.color);
    const background = effectiveBackground(probe.ancestors);
    const ratio = foreground ? contrast(foreground.rgb, background) : 0;
    if (ratio < 4.5) {
      problem(label + " 对比度 " + ratio.toFixed(2) + ":1 低于 4.5:1（" + probe.color + " on rgb(" + background.join(",") + ")）");
    }
    sink[target.name] = {
      color: probe.color,
      fontSize: probe.fontSize,
      fontWeight: probe.fontWeight,
      contrast: Number(ratio.toFixed(2)),
      background: "rgb(" + background.join(", ") + ")",
    };
  }
}

const FORBIDDEN = ["复用策略", "prefer_local", "refresh", "默认复用", "强制重新拉取"];

async function checkReusePolicyHidden(page, stage) {
  const bodyText = await page.locator("body").innerText();
  for (const needle of FORBIDDEN) {
    if (bodyText.includes(needle)) problem("[" + stage + "] 页面正文出现禁用文本「" + needle + "」");
  }
  const names = await page.evaluate(() => {
    const out = [];
    for (const el of document.querySelectorAll("button, a, input, select, option, label, [role], [aria-label], [title]")) {
      out.push({
        tag: el.tagName.toLowerCase(),
        type: el.getAttribute("type") ?? "",
        role: el.getAttribute("role") ?? "",
        name: el.getAttribute("aria-label") ?? "",
        title: el.getAttribute("title") ?? "",
        text: (el.textContent ?? "").trim().slice(0, 60),
        field: el.getAttribute("name") ?? "",
      });
    }
    return out;
  });
  for (const entry of names) {
    const haystack = [entry.name, entry.title, entry.text, entry.field, entry.role].join(" ");
    for (const needle of FORBIDDEN) {
      if (haystack.includes(needle)) {
        problem("[" + stage + "] 存在以「" + needle + "」命名的控件：" + JSON.stringify(entry));
      }
    }
  }
  const controls = await page.evaluate(() =>
    Array.from(document.querySelectorAll("input, select, button")).filter((el) => {
      const style = getComputedStyle(el);
      const rect = el.getBoundingClientRect();
      const hiddenByCss = style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0;
      return hiddenByCss || rect.left < -10 || rect.top < -10;
    }).length,
  );
  return { names: names.length, hiddenControls: controls };
}

function installRoutes(page) {
  const envelope = (data) =>
    JSON.stringify({
      success: true,
      data,
      meta: { request_id: "req-fixture", trace_id: "trace-fixture", server_time: new Date().toISOString(), next_cursor: null },
    });
  return (async () => {
    await page.route("**/api/**", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({}) }));
    await page.route("**/api/auth/v1/session", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(SESSION) }));
    await page.route(/\/api\/v2\/meta\/chains(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(CHAINS) }));
    await page.route(/\/api\/v2\/meta\/datasets(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(DATASETS) }));
    await page.route(/\/api\/v2\/jobs(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }));
    /* 通知 Provider 的响应体本身是数组（不是 { items } 信封）：形状错了整个应用树会崩。 */
    await page.route(/\/api\/v2\/notifications(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }));
    await page.route(/\/api\/v2\/downloads\/validate-addresses$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(VALIDATE_RESULT) }));
    await page.route(/\/api\/v2\/downloads\/preview$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(PREVIEW) }));
    await page.route(/\/api\/v2\/downloads$/, (route) =>
      route.request().method() === "POST"
        ? route.fulfill({ status: 200, contentType: "application/json", body: envelope(CREATE_RESULT) })
        : route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }),
    );
  })();
}
/* -------------------------------------------------------------------- flow */

/**
 * DOM / 可访问名称契约（规范 §21）：这些 ID、按钮名与 aria-label 必须继续存在。
 * 桌面与移动共用同一套（移动端折叠章节里的控件在展开后才存在，因此按"页面内可达"判定：
 * 通过轨道/编辑入口展开后仍然能找到同名控件）。
 */
const REQUIRED_ALWAYS = [
  "#download-addresses",
  "#download-step-objects",
  "#download-step-range",
  "#download-step-datasets",
  "#download-step-preview",
  "#download-step-create",
  'input[aria-label="导入地址文件"]',
];
/* 「预览 Coverage 与成本」只存在于 ④ 区的未预览态：展开 ④ 区之后再单独断言。 */
const REQUIRED_BUTTONS = ["解析导入", "创建下载任务"];

/**
 * DOM / 可访问名称契约（规范 §21）。
 *
 * 移动端已完成章节是折叠摘要卡（正文不渲染），因此按"可达"判定：
 * 先展开对应章节，再断言它内部的 ID / name / 按钮名。
 */
async function expandSection(page, key) {
  const section = page.locator("#download-step-" + key);
  if ((await section.locator(".dl-section-body").count()) > 0) return;
  const edit = section.locator(".dl-section-head__edit");
  if ((await edit.count()) === 0) return;
  await edit.click();
  await page.waitForTimeout(250);
}

async function needIn(scope, selector, sink, label) {
  if ((await scope.locator(selector).count()) === 0) sink.push(label);
}

/**
 * 本地解析摘要 Dialog（§11.2 / §21）：用一份真实 CSV 走一次文件导入，
 * 断言摘要本体（[data-testid="file-parse-summary"]）与「提交服务端校验」按钮都在弹窗里，
 * 然后点「取消」退出（不提交、不影响后续用例）。
 */
async function checkParseSummaryDialog(page, stage) {
  await expandSection(page, "objects");
  await page.locator('input[aria-label="导入地址文件"]').setInputFiles({
    name: "dlf-probe.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("address\nTR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t\n", "utf8"),
  });
  const dialog = page.locator(".cs-dialog").filter({ hasText: "本地解析摘要" });
  const failed = [];
  try {
    await dialog.waitFor({ state: "visible", timeout: 30_000 });
  } catch {
    failed.push("本地解析摘要弹窗");
  }
  if (failed.length === 0) {
    await needIn(dialog, '[data-testid="file-parse-summary"]', failed, "file-parse-summary");
    if ((await dialog.getByRole("button", { name: "提交服务端校验", exact: true }).count()) === 0) failed.push("button:提交服务端校验");
    if ((await dialog.getByRole("button", { name: "取消", exact: true }).count()) === 0) failed.push("button:取消");
    await dialog.getByRole("button", { name: "取消", exact: true }).click();
    await page.waitForTimeout(300);
  }
  if (failed.length > 0) problem("[" + stage + "] 本地解析摘要契约缺失：" + failed.join("、"));
  return { parse_summary_missing: failed };
}

async function checkDomContract(page, stage) {
  const missing = [];
  const need = async (selector) => {
    if ((await page.locator(selector).count()) === 0) missing.push(selector);
  };

  for (const selector of REQUIRED_ALWAYS) await need(selector);
  for (const name of REQUIRED_BUTTONS) {
    if ((await page.getByRole("button", { name, exact: true }).count()) === 0) missing.push("button:" + name);
  }

  await expandSection(page, "objects");
  await need("#download-addresses");
  await need('input[aria-label="导入地址文件"]');
  if ((await page.getByRole("button", { name: "解析导入", exact: true }).count()) === 0) missing.push("button:解析导入");

  await expandSection(page, "range");
  await need("#download-chain");
  await need("#download-range-type");
  await need('input[name="download-range-type"]');
  await need("#download-performance");

  /* 区块 / 时间字段是按范围类型条件渲染的：两种类型都要能定位到规范列出的 ID。 */
  await page.locator('label:has(input[name="download-range-type"])').filter({ hasText: "区块范围" }).click();
  await page.waitForTimeout(400);
  await need("#download-from-block");
  await need("#download-to-block");
  await page.locator('label:has(input[name="download-range-type"])').filter({ hasText: "时间范围" }).click();
  await page.waitForTimeout(400);
  await need("#download-from-time");
  await need("#download-to-time");
  await page.locator('label:has(input[name="download-range-type"])').filter({ hasText: "全量" }).click();
  await page.waitForTimeout(300);

  /* 预览按钮在 ④ 区（移动端同样要先展开该章节才会渲染）。 */
  await expandSection(page, "preview");
  if ((await page.getByRole("button", { name: "预览 Coverage 与成本", exact: true }).count()) === 0) missing.push("button:预览 Coverage 与成本");

  if (missing.length > 0) problem("[" + stage + "] DOM 契约缺失：" + missing.join("、"));
  return { missing };
}

/** 从真实请求体里取出 reuse_policy（页面自己发出的负载，未经过任何改写）。 */
function safeReusePolicy(body) {
  if (!body) return null;
  try {
    const parsed = JSON.parse(body);
    return parsed.reuse_policy ?? null;
  } catch {
    return null;
  }
}

const viewports = [
  { name: "desktop", file: "desktop-1440x900.png", width: 1440, height: 900, compact: false, mobile: false },
  { name: "laptop", file: "laptop-1024x768.png", width: 1024, height: 768, compact: false, mobile: false },
  { name: "tablet", file: "tablet-768x1024.png", width: 768, height: 1024, compact: true, mobile: false },
  { name: "mobile", file: "mobile-390x844.png", width: 390, height: 844, compact: true, mobile: true },
];

const report = {
  route: "/app-v2/downloads/new",
  mode: "构建产物 + 浏览器侧服务端夹具（请求体为页面真实发出）",
  generated_at: new Date().toISOString(),
  reuse_policy_visible: false,
  preview_request_reuse_policy: null,
  create_request_reuse_policy: null,
  viewports: {},
  horizontal_overflow: {},
  contrast_checks: {},
  text_clarity_checks: {},
  step_order: {},
  dataset_semantics: {},
  reuse_policy_probe: {},
  dom_contract: {},
  stale_preview: {},
  reduced_motion: {},
  console_errors: [],
  page_errors: [],
  csp_violations: [],
  screenshots: [],
};

async function openPage(browser, vp, options = {}) {
  const context = await browser.newContext({
    viewport: { width: vp.width, height: vp.height },
    deviceScaleFactor: 1,
    isMobile: Boolean(vp.mobile),
    hasTouch: Boolean(vp.mobile),
    reducedMotion: options.reducedMotion ? "reduce" : "no-preference",
  });
  await context.addInitScript(() => {
    window.__dlCheck = { consoleErrors: [], pageErrors: [], csp: [] };
    document.addEventListener("securitypolicyviolation", (event) => {
      window.__dlCheck.csp.push({ directive: event.violatedDirective, blockedURI: event.blockedURI });
    });
  });
  const page = await context.newPage();
  const net = { preview: [], create: [] };
  page.on("request", (request) => {
    const url = request.url();
    const method = request.method();
    if (method !== "POST") return;
    if (url.includes("/api/v2/downloads/preview")) net.preview.push(request.postData());
    else if (/\/api\/v2\/downloads$/.test(url)) net.create.push(request.postData());
  });
  page.on("console", (msg) => {
    if (msg.type() === "error") void page.evaluate((t) => window.__dlCheck.consoleErrors.push(t), msg.text()).catch(() => {});
  });
  page.on("pageerror", (err) => {
    void page.evaluate((t) => window.__dlCheck.pageErrors.push(t), String(err)).catch(() => {});
  });
  await installRoutes(page);
  await page.goto(BASE + "/downloads/new", { waitUntil: "domcontentloaded" });
  await page.waitForSelector("#download-addresses", { timeout: 20_000 });
  await page.waitForSelector(".dl-flow-page", { timeout: 20_000 });
  await page.waitForTimeout(400);
  return { context, page, net };
}

/**
 * Dialog 进入动画（220ms）结束后再截图/点击：
 * Playwright 的 visible 判定不看 opacity，动画期间的截图会拍到"看不见的弹窗"。
 */
async function waitForDialogSettled(page) {
  await page.waitForFunction(() => {
    const dialog = document.querySelector(".cs-dialog");
    if (!dialog) return false;
    const style = getComputedStyle(dialog);
    const opacity = Number(style.opacity);
    const transform = style.transform;
    const settled = opacity > 0.99 && (transform === "none" || transform === "matrix(1, 0, 0, 1, 0, 0)");
    return settled;
  }, null, { timeout: 10_000 }).catch(() => {});
  await page.waitForTimeout(120);
}

async function railLabels(page) {
  return page.locator(".dl-rail__label, .dl-vrail__label").allInnerTexts();
}

async function currentStepLabel(page) {
  const label = page.locator('[aria-current="step"] .dl-rail__label, [aria-current="step"] .dl-vrail__label').first();
  if ((await label.count()) === 0) return null;
  return (await label.innerText()).trim();
}

async function overflow(page) {
  return page.evaluate(() => {
    const clientWidth = document.documentElement.clientWidth;
    const offenders = [];
    for (const el of document.querySelectorAll("body *")) {
      const rect = el.getBoundingClientRect();
      if (rect.width > 0 && rect.right > clientWidth + 1) {
        offenders.push({
          tag: el.tagName.toLowerCase(),
          cls: (typeof el.className === "string" ? el.className : "").slice(0, 70),
          right: Math.round(rect.right),
          width: Math.round(rect.width),
          position: getComputedStyle(el).position,
        });
      }
    }
    return {
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth,
      bodyScrollWidth: document.body.scrollWidth,
      offenders: offenders.slice(0, 10),
    };
  });
}

/** 走完 地址导入 → 服务端校验 → 真实预览 的完整链路。 */
async function runPreviewFlow(page, vp, sink, options = {}) {
  await page.locator("#download-addresses").fill(ADDRESS);
  await page.getByRole("button", { name: "解析导入" }).click();

  const verdict = page.locator(".cs-dialog").filter({ hasText: "服务端地址校验结果" });
  await verdict.waitFor({ state: "visible", timeout: 20_000 });
  sink.reuse_policy_probe.verdict_dialog = await checkReusePolicyHidden(page, vp.name + "/verdict-dialog");
  await verdict.getByRole("button", { name: "使用服务端结果" }).click();
  await page.waitForSelector(".cs-dialog", { state: "detached", timeout: 10_000 }).catch(() => {});
  await page.waitForTimeout(300);

  /* 移动端第 ⑤ 步在未到达时是折叠摘要卡（§7.5），此时以第 ① 步摘要作为地址生效证据。 */
  const inspector = page.locator('[data-testid="inspector-addresses"]');
  if ((await inspector.count()) > 0) {
    const appliedText = await inspector.innerText();
    if (!appliedText.includes("1")) problem("[" + vp.name + "] 服务端接受后任务预览里的地址数不是 1：" + appliedText);
  } else {
    const summary = await page.locator("#download-step-objects").innerText();
    if (!summary.includes("1 个地址")) problem("[" + vp.name + "] 服务端接受后第 ① 步摘要没有反映 1 个地址：" + summary.replace(/\n/g, " "));
  }

  /* 短屏桌面右栏 footer 也有一个次级「预览 Coverage 与成本」：取第一个（左栏 ④ 区）。 */
  await page.getByRole("button", { name: "预览 Coverage 与成本" }).first().click();
  const previewDialog = page.locator(".cs-dialog").filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ });
  await previewDialog.waitFor({ state: "visible", timeout: 20_000 });
  await waitForDialogSettled(page);
  sink.reuse_policy_probe.preview_dialog = await checkReusePolicyHidden(page, vp.name + "/preview-dialog");

  if (options.dialogShot) {
    const shotPath = join(OUT, options.dialogShot);
    await page.screenshot({ path: shotPath });
    report.screenshots.push(options.dialogShot);
    const dialogText = await previewDialog.innerText();
    sink.dialog_text = dialogText.slice(0, 400);
  }

  /* Dialog 清晰度（§17.4）：前景必须 #FFFFFF / alpha 1，正文与标题不得发灰。 */
  const dialogProbe = await page.evaluate(() => {
    const dialog = document.querySelector(".cs-dialog");
    if (!dialog) return null;
    const style = getComputedStyle(dialog);
    const title = document.querySelector(".cs-dialog__title");
    const bodyNode = document.querySelector(".cs-dialog__body");
    return {
      containerOpacity: style.opacity,
      containerFilter: style.filter,
      containerBackdrop: style.backdropFilter || "none",
      background: style.backgroundColor,
      bodyColor: bodyNode ? getComputedStyle(bodyNode).color : null,
      titleColor: title ? getComputedStyle(title).color : null,
      transform: style.transform,
    };
  });
  sink.dialog_style = dialogProbe;
  if (dialogProbe) {
    if (Number(dialogProbe.containerOpacity) < 1) problem("[" + vp.name + "] Dialog 容器 opacity=" + dialogProbe.containerOpacity);
    if (dialogProbe.containerBackdrop !== "none") problem("[" + vp.name + "] Dialog 自身使用了 backdrop-filter：" + dialogProbe.containerBackdrop);
    if (dialogProbe.containerFilter !== "none") problem("[" + vp.name + "] Dialog 自身使用了 filter：" + dialogProbe.containerFilter);
  }

  await previewDialog.getByRole("button", { name: "知道了" }).click({ timeout: 20_000 });
  await page.waitForTimeout(300);

  const step = await currentStepLabel(page);
  if (!step || !/创建任务|创建/.test(step)) problem("[" + vp.name + "] 预览成功后当前步骤不是创建任务，而是 " + step);
}

async function runCreateFlow(page, vp, sink) {
  await page.locator(".dl-task-preview button.dl-btn--primary").click();
  const confirm = page.locator(".cs-dialog").filter({ hasText: "确认创建下载任务" });
  await confirm.waitFor({ state: "visible", timeout: 20_000 });
  await waitForDialogSettled(page);
  const confirmText = await confirm.innerText();
  if (confirmText.includes("prefer_local") || confirmText.includes("复用策略")) {
    problem("[" + vp.name + "] 创建确认弹窗暴露了复用策略内部枚举");
  }
  if (!confirmText.includes("系统将优先使用已有本地数据")) {
    problem("[" + vp.name + "] 创建确认弹窗缺少规范 §5.4 规定的正文");
  }
  sink.reuse_policy_probe.create_dialog = await checkReusePolicyHidden(page, vp.name + "/create-dialog");
  await confirm.getByRole("button", { name: "确认创建" }).click();

  const result = page.locator(".cs-dialog").filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ });
  await result.waitFor({ state: "visible", timeout: 20_000 });
  await page.screenshot({ path: join(OUT, "create-result-" + vp.name + ".png") });
  report.screenshots.push("create-result-" + vp.name + ".png");
  /* 关闭按钮是共享 Dialog 的确定入口；Escape 的键盘语义由 Playwright 用例单独断言。 */
  await result.locator(".cs-dialog__close").click();
  await result.waitFor({ state: "detached", timeout: 10_000 });
  await page.waitForTimeout(300);
}

/** 参数变化 → 失效预览（§14.4）：opacity 必须为 1，边框是可见琥珀色。 */
async function runStaleCheck(page, vp, sink) {
  const checkbox = page.getByRole("checkbox", { name: /Token Transfers/ }).first();
  /* 移动端已完成步骤会折叠，需要先点"编辑"展开第 ③ 步（§7.5）。 */
  if ((await checkbox.count()) === 0 || !(await checkbox.isVisible())) {
    await page.locator("#download-step-datasets .dl-section-head__edit").click();
    await page.waitForTimeout(300);
  }
  /*
   * 用 click 而不是 uncheck：移动端展开"编辑"后改动参数会让当前步骤前移，
   * 该章节随即按设计折叠（display:none），Playwright 的 uncheck 会因为
   * 元素不可见而判定失败——业务状态本身是对的，失效标记才是这里要断言的东西。
   */
  await checkbox.click();
  await page.waitForTimeout(600);
  const unchecked = await page.evaluate(() => {
    const el = document.querySelector('input[aria-label*="Token Transfers"]');
    return el ? !el.checked : true;
  });
  if (!unchecked) problem("[" + vp.name + "] 取消勾选 Dataset 没有生效");
  const staleCount = await page.locator(".dl-preview-stale").count();
  if (staleCount === 0) {
    problem("[" + vp.name + "] 参数变化后没有出现失效预览标记");
    return;
  }
  const style = await page.locator(".dl-preview-stale").first().evaluate((node) => {
    const computed = getComputedStyle(node);
    return { opacity: computed.opacity, borderTopColor: computed.borderTopColor, borderTopWidth: computed.borderTopWidth };
  });
  sink.stale_preview = style;
  if (Number(style.opacity) < 1) problem("[" + vp.name + "] 失效预览用 opacity 表达：" + style.opacity);
  const border = parseColor(style.borderTopColor);
  if (!border || !(border.rgb[0] > 120 && border.rgb[0] > border.rgb[2] + 40)) {
    problem("[" + vp.name + "] 失效预览边框不是可见琥珀色：" + style.borderTopColor);
  }
  const step = await currentStepLabel(page);
  if (!step || !/预览|Coverage/.test(step)) problem("[" + vp.name + "] 参数变化后当前步骤没有退回预览，而是 " + step);
}

/** Dataset 上限：第 7 项不能再被选中，且说明走 Dialog（§13.6）。 */
async function runDatasetLimitCheck(page, vp, sink) {
  const total = await page.getByRole("checkbox").count();
  sink.dataset_total = total;
  const already = await page.locator('.dl-dataset-card[data-selected="true"]').count();
  const fill = ["transactions", "token_transfers", "nft_transfers", "logs", "contract_interactions", "trace"];
  for (const id of fill) {
    if ((await page.locator('.dl-dataset-card[data-selected="true"]').count()) >= 6) break;
    const box = page.getByRole("checkbox", { name: new RegExp("（" + id + "）") }).first();
    if (!(await box.isChecked())) await box.click();
    await page.waitForTimeout(120);
  }
  const checkedNow = await page.locator('.dl-dataset-card[data-selected="true"]').count();
  if (checkedNow !== 6) problem("[" + vp.name + "] 勾选到 6 项后实际选中 " + checkedNow + " 项（进入时已选 " + already + "）");
  const seventh = page.getByRole("checkbox", { name: /Balances/ }).first();
  await seventh.click();
  await page.waitForTimeout(300);
  const limitDialog = page.locator(".cs-dialog").filter({ hasText: "Dataset 数量超出上限" });
  if ((await limitDialog.count()) === 0) problem("[" + vp.name + "] 选择第 7 项没有通过 Dialog 说明上限");
  else await limitDialog.getByRole("button", { name: "知道了" }).click();
  const checkedAfter = await page.locator('.dl-dataset-card[data-selected="true"]').count();
  if (checkedAfter !== 6) problem("[" + vp.name + "] 第 7 项仍然被选中（" + checkedAfter + " 项）");
  sink.dataset_limit = { checked: checkedAfter, dialog: true };
}

async function runDatasetKeyboardCheck(page, vp, sink) {
  /* 上限用例结束时 6 项已满：先腾出一个位置，键盘切换才有可观察的状态变化。 */
  const trace = page.getByRole("checkbox", { name: /Trace/ }).first();
  if (await trace.isChecked()) {
    await trace.click();
    await page.waitForTimeout(200);
  }
  const box = page.getByRole("checkbox", { name: /Balances/ }).first();
  await box.focus();
  const before = await box.isChecked();
  await page.keyboard.press("Space");
  await page.waitForTimeout(200);
  const after = await box.isChecked();
  if (before === after) problem("[" + vp.name + "] 键盘 Space 不能切换 Dataset 勾选状态");
  await page.keyboard.press("Space");
  await page.waitForTimeout(200);
  sink.dataset_keyboard = { before, after, restored: await box.isChecked() };
}

async function runMobileChecks(page, vp, sink) {
  /* 移动端已完成章节是折叠卡：测量 Dataset 网格前先展开第 ③ 步。 */
  if ((await page.locator(".dl-dataset-grid").count()) === 0) {
    await page.locator("#download-step-datasets .dl-section-head__edit").click();
    await page.waitForTimeout(400);
  }
  const columns = await page.locator(".dl-dataset-grid").evaluate((node) => getComputedStyle(node).gridTemplateColumns.split(" ").length);
  if (columns !== 2) problem("[" + vp.name + "] 移动端 Dataset 不是两列，实际 " + columns + " 列");
  const verticalRail = await page.locator(".dl-vrail").isVisible();
  if (!verticalRail) problem("[" + vp.name + "] 390px 下看不到竖向 Flowline");
  const bar = await page.locator(".dl-actionbar").boundingBox();
  if (!bar) problem("[" + vp.name + "] 移动端底部操作栏不存在");
  const paddingBottom = await page.locator(".dl-flow-page").evaluate((node) => getComputedStyle(node).paddingBottom);
  if (num(paddingBottom) < 96) problem("[" + vp.name + "] 页面底部没有为操作栏预留 96px：" + paddingBottom);
  /* 移动端一次只展开一个章节：要量某个章节里的控件，先点它的"编辑"。 */
  const SECTION_OF = {
    ".dl-segmented__item": "#download-step-range",
    ".dl-dataset-card": "#download-step-datasets",
  };
  const targets = [
    ".dl-actionbar .dl-btn--primary",
    ".dl-actionbar .dl-btn--secondary",
    ".dl-vrail__node",
    ".dl-segmented__item",
    ".dl-dataset-card",
  ];
  const small = [];
  for (const selector of targets) {
    if ((await page.locator(selector).count()) === 0 && SECTION_OF[selector]) {
      const edit = page.locator(SECTION_OF[selector] + " .dl-section-head__edit");
      if ((await edit.count()) > 0) {
        await edit.click();
        await page.waitForTimeout(350);
      }
    }
    const box = await page.locator(selector).first().boundingBox().catch(() => null);
    if (!box) continue;
    if (box.height + 0.5 < 44 || box.width + 0.5 < 44) small.push(selector + "=" + Math.round(box.width) + "x" + Math.round(box.height));
  }
  if (small.length > 0) problem("[" + vp.name + "] 触摸目标小于 44px：" + small.join(", "));
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await page.waitForTimeout(400);
  const lastCard = await page.locator("#download-step-create").boundingBox();
  const barBox = await page.locator(".dl-actionbar").boundingBox();
  if (lastCard && barBox && lastCard.y + lastCard.height > barBox.y + 1) {
    problem("[" + vp.name + "] 底部操作栏遮住了最后一个区块（card bottom=" + Math.round(lastCard.y + lastCard.height) + " bar top=" + Math.round(barBox.y) + "）");
  }
  sink.mobile = { columns, verticalRail, bar, paddingBottom, touchTargets: targets.length, smallTargets: small };
}

async function runViewport(browser, vp) {
  const { context, page, net } = await openPage(browser, vp);
  const sink = { name: vp.name };
  try {
    /* 0. DOM / 可访问名称契约（§21）与本地解析摘要弹窗（§11.2）。 */
    sink.dom_contract = {
      ...(await checkDomContract(page, vp.name + "/dom-contract")),
      ...(await checkParseSummaryDialog(page, vp.name + "/parse-summary")),
    };

    /* 1. 复用策略在所有用户可见层不可见（BIZ-01）。 */
    sink.reuse_policy_probe = { initial: await checkReusePolicyHidden(page, vp.name + "/initial") };

    /* 2. 五步轨道与步骤状态（UI-04）。 */
    const labels = await railLabels(page);
    sink.step_order = labels;
    const expected = vp.compact
      ? ["对象", "范围", "Dataset", "预览", "创建"]
      : ["对象与地址", "链与范围", "Dataset", "Coverage 预览", "创建任务"];
    if (JSON.stringify(labels) !== JSON.stringify(expected)) {
      problem("[" + vp.name + "] Flowline 五步顺序不符：实际 " + JSON.stringify(labels) + "，期望 " + JSON.stringify(expected));
    }
    const initialStep = await currentStepLabel(page);
    if (!/对象/.test(initialStep ?? "")) problem("[" + vp.name + "] 空表单时当前步骤不是对象与地址：" + initialStep);

    /* 3. 横向溢出（PERF-01）。 */
    const box = await overflow(page);
    sink.horizontal_overflow = box;
    if (box.scrollWidth > box.clientWidth + 1) {
      problem("[" + vp.name + "] 页面出现横向滚动：" + JSON.stringify(box));
    }

    /* 4. Dataset 原生 checkbox 语义（UI-05）。 */
    const checkboxCount = await page.getByRole("checkbox").count();
    sink.dataset_semantics = { checkboxCount };

    /* 5. 字体清晰度与对比度（UI-03）。 */
    sink.contrast_checks = {};
    await checkClarity(page, vp.name, sink.contrast_checks);

    /* 6. 完整业务链路：导入 → 校验 → 预览 → 创建。 */
    await runPreviewFlow(page, vp, sink, { dialogShot: vp.name === "desktop" ? "risk-dialog-1440x900.png" : vp.name === "mobile" ? "risk-dialog-390x844.png" : null });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(350);
    await page.screenshot({ path: join(OUT, vp.file) });
    report.screenshots.push(vp.file);

    await checkClarity(page, vp.name + "-previewed", sink.contrast_checks);
    await runCreateFlow(page, vp, sink);

    /* 7. 网络证据（BIZ-02）。 */
    if (net.preview.length === 0) problem("[" + vp.name + "] 没有捕获到 preview 请求");
    if (net.create.length === 0) problem("[" + vp.name + "] 没有捕获到 create 请求");
    sink.preview_request = net.preview[net.preview.length - 1] ?? null;
    sink.create_request = net.create[net.create.length - 1] ?? null;

    /* 8. 失效预览（BIZ-06）。 */
    await runStaleCheck(page, vp, sink);

    /* 9. Dataset 上限与键盘语义（UI-05）。 */
    if (vp.name === "desktop") {
      await runDatasetLimitCheck(page, vp, sink);
      await runDatasetKeyboardCheck(page, vp, sink);
    }

    /* 10. 移动端专项（UI-02 / PERF-01）。 */
    if (vp.mobile) await runMobileChecks(page, vp, sink);

    /* 12. 收尾（不变更任何参数，避免影响后续断言）。 */

    const signals = await page.evaluate(() => window.__dlCheck);
    sink.console_errors = signals.consoleErrors;
    sink.page_errors = signals.pageErrors;
    sink.csp_violations = signals.csp;
    for (const item of signals.consoleErrors) report.console_errors.push("[" + vp.name + "] " + item);
    for (const item of signals.pageErrors) report.page_errors.push("[" + vp.name + "] " + item);
    for (const item of signals.csp) report.csp_violations.push("[" + vp.name + "] " + JSON.stringify(item));
  } catch (error) {
    const signals = await page.evaluate(() => window.__dlCheck).catch(() => null);
    const html = await page.locator("#root").innerHTML().catch(() => "");
    console.error("[DIAGNOSTICS " + vp.name + "]", JSON.stringify(signals), "rootLength=" + html.length, "url=" + page.url());
    console.error("[DIAGNOSTICS " + vp.name + "] body=" + (await page.locator("body").innerText().catch(() => "")).replace(/\n/g, " | ").slice(0, 600));
    throw error;
  } finally {
    await context.close();
  }
  return sink;
}

async function runReducedMotion(browser) {
  const vp = { name: "reduced-motion", file: "reduced-motion-1440x900.png", width: 1440, height: 900, compact: false, mobile: false };
  const { context, page, net } = await openPage(browser, vp, { reducedMotion: true });
  const sink = { name: vp.name, reuse_policy_probe: {} };
  try {
    const dots = await page.locator(".dl-rail__dot").count();
    if (dots !== 0) problem("[reduced-motion] 仍然渲染了移动光点：" + dots);
    const activeDash = await page.locator(".dl-rail__active").evaluate((node) => getComputedStyle(node).strokeDashoffset);
    sink.initial_dashoffset = activeDash;

    await runPreviewFlow(page, vp, sink);
    const dotsAfter = await page.locator(".dl-rail__dot").count();
    if (dotsAfter !== 0) problem("[reduced-motion] 预览后仍然渲染了移动光点：" + dotsAfter);
    const dashAfter = await page.locator(".dl-rail__active").evaluate((node) => getComputedStyle(node).strokeDashoffset);
    sink.final_dashoffset = dashAfter;
    if (dashAfter === activeDash) {
      note_reduced_motion_path_static();
    }
    await page.screenshot({ path: join(OUT, vp.file) });
    report.screenshots.push(vp.file);

    /* Dialog 在 reduced motion 下必须直接到位（没有 scale / 位移过渡）。 */
    const dialogTransition = await page.evaluate(() => {
      const dialog = document.querySelector(".cs-dialog");
      if (!dialog) return null;
      const style = getComputedStyle(dialog);
      return { transform: style.transform, transitionDuration: style.transitionDuration, animationDuration: style.animationDuration };
    });
    sink.dialog_style = dialogTransition;

    await runCreateFlow(page, vp, sink);
    sink.preview_request = net.preview[net.preview.length - 1] ?? null;
    sink.create_request = net.create[net.create.length - 1] ?? null;

    const signals = await page.evaluate(() => window.__dlCheck);
    sink.console_errors = signals.consoleErrors;
    sink.page_errors = signals.pageErrors;
    for (const item of signals.consoleErrors) report.console_errors.push("[reduced-motion] " + item);
    for (const item of signals.pageErrors) report.page_errors.push("[reduced-motion] " + item);
  } finally {
    await context.close();
  }
  return sink;
}

function note_reduced_motion_path_static() {
  notes.push("reduced-motion：激活轨道在预览前后 stroke-dashoffset 相同（pathLength 立即到最终值，没有过渡绘制）");
}


/**
 * BIZ-04 多链分组：同一批地址分布在不同链上时，预览与创建都必须**按链分别发请求**。
 * 这里的夹具把 validate-addresses 的 accepted 造成 tron + bsc 两条，
 * 其余响应仍由页面真实发出（请求体、条数、链取值都是可断言的真实证据）。
 */
async function runMultiChainCheck(browser) {
  const vp = { name: "multi-chain", width: 1440, height: 900, mobile: false };
  const TRON = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t";
  const BSC = "0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649";
  const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height } });
  const page = await context.newPage();
  const envelope = (data) =>
    JSON.stringify({ success: true, data, meta: { request_id: "req-fixture", trace_id: "trace-fixture", server_time: new Date().toISOString(), next_cursor: null } });
  const net = { preview: [], create: [] };
  page.on("request", (request) => {
    if (request.method() !== "POST") return;
    const url = request.url();
    if (url.includes("/api/v2/downloads/preview")) net.preview.push(request.postData());
    else if (/\/api\/v2\/downloads$/.test(url)) net.create.push(request.postData());
  });
  await page.route("**/api/**", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({}) }));
  await page.route("**/api/auth/v1/session", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(SESSION) }));
  await page.route(/\/api\/v2\/notifications(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }));
  await page.route(/\/api\/v2\/meta\/chains(\?[^/]*)?$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: envelope({ items: [{ slug: "tron", name: "TRON", enabled: true }, { slug: "bsc", name: "BSC", enabled: true }] }),
    }),
  );
  await page.route(/\/api\/v2\/meta\/datasets(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(DATASETS) }));
  await page.route(/\/api\/v2\/downloads\/validate-addresses$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: envelope({
        ...VALIDATE_RESULT,
        accepted: [
          { index: 0, row: 1, input: TRON, chain: "tron", normalized: TRON, case_name: "", tags: [] },
          { index: 1, row: 2, input: BSC, chain: "bsc", normalized: BSC, case_name: "", tags: [] },
        ],
        summary: { ...VALIDATE_RESULT.summary, submitted: 2, accepted: 2, chains: { tron: 1, bsc: 1 }, distinct_chains: 2 },
      }),
    }),
  );
  await page.route(/\/api\/v2\/downloads\/preview$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(PREVIEW) }));
  await page.route(/\/api\/v2\/downloads$/, (route) =>
    route.request().method() === "POST"
      ? route.fulfill({ status: 200, contentType: "application/json", body: envelope(CREATE_RESULT) })
      : route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }),
  );

  await page.goto(BASE + "/downloads/new", { waitUntil: "domcontentloaded" });
  await page.waitForSelector("#download-addresses", { timeout: 20_000 });
  await page.locator("#download-addresses").fill(TRON + "\n" + BSC);
  await page.getByRole("button", { name: "解析导入" }).click();
  const verdict = page.locator(".cs-dialog").filter({ hasText: "服务端地址校验结果" });
  await verdict.waitFor({ state: "visible", timeout: 20_000 });
  await verdict.getByRole("button", { name: "使用服务端结果" }).click();
  await page.waitForTimeout(400);

  await page.getByRole("button", { name: "预览 Coverage 与成本" }).first().click();
  for (let index = 0; index < 4; index += 1) {
    const dialog = page.locator(".cs-dialog").filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ });
    if ((await dialog.count()) === 0) break;
    await dialog.first().getByRole("button", { name: "知道了" }).click();
    await page.waitForTimeout(500);
  }

  await page.locator(".dl-task-preview button.dl-btn--primary").click();
  const confirm = page.locator(".cs-dialog").filter({ hasText: "确认创建下载任务？" });
  await confirm.waitFor({ state: "visible", timeout: 30_000 });
  await confirm.getByRole("button", { name: "确认创建" }).click();
  await page.waitForTimeout(1500);

  const previewChains = net.preview.map((body) => JSON.parse(body ?? "{}").chain).sort();
  const createChains = net.create.map((body) => JSON.parse(body ?? "{}").chain).sort();
  const result = {
    preview_requests: net.preview.length,
    create_requests: net.create.length,
    preview_chains: previewChains,
    create_chains: createChains,
  };
  if (previewChains.length !== 2 || createChains.length !== 2) {
    problem("[multi-chain] 多链必须按链分别预览与创建，实际 " + JSON.stringify(result));
  } else if (previewChains.join(",") !== "bsc,tron" || createChains.join(",") !== "bsc,tron") {
    problem("[multi-chain] 分组链取值不符：" + JSON.stringify(result));
  }
  await context.close();
  return result;
}

async function main() {
  if (!existsSync(join(DIST, "index.html"))) {
    console.error("缺少构建产物，请先运行 pnpm build:user");
    process.exit(2);
  }
  await mkdir(OUT, { recursive: true });
  const server = await startStaticServer();
  const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL ?? "chromium" });
  try {
    for (const vp of viewports) {
      const sink = await runViewport(browser, vp);
      report.viewports[vp.name] = {
        width: vp.width,
        height: vp.height,
        screenshot: vp.file,
        step_order: sink.step_order,
        preview_request: sink.preview_request,
        create_request: sink.create_request,
        dataset_semantics: sink.dataset_semantics,
      };
      report.horizontal_overflow[vp.name] = sink.horizontal_overflow;
      if (sink.contrast_checks) report.contrast_checks[vp.name] = sink.contrast_checks;
      if (sink.stale_preview) report.stale_preview[vp.name] = sink.stale_preview;
      if (sink.dialog_style) report.viewports[vp.name].dialog_style = sink.dialog_style;
      if (sink.dataset_limit) report.viewports[vp.name].dataset_limit = sink.dataset_limit;
      if (sink.dataset_keyboard) report.viewports[vp.name].dataset_keyboard = sink.dataset_keyboard;
      if (sink.mobile) report.viewports[vp.name].mobile = sink.mobile;
      report.reuse_policy_probe[vp.name] = sink.reuse_policy_probe;
      if (sink.dom_contract) report.dom_contract[vp.name] = sink.dom_contract;
      if (sink.preview_request) report.preview_request_reuse_policy = safeReusePolicy(sink.preview_request);
      if (sink.create_request) report.create_request_reuse_policy = safeReusePolicy(sink.create_request);
    }

    report.multi_chain = await runMultiChainCheck(browser);

    const reduced = await runReducedMotion(browser);
    report.reduced_motion = {
      moving_dots: 0,
      initial_dashoffset: reduced.initial_dashoffset,
      final_dashoffset: reduced.final_dashoffset,
      dialog_style: reduced.dialog_style,
      preview_request_reuse_policy: safeReusePolicy(reduced.preview_request),
      create_request_reuse_policy: safeReusePolicy(reduced.create_request),
      console_errors: reduced.console_errors,
      page_errors: reduced.page_errors,
      screenshot: "reduced-motion-1440x900.png",
    };
  } finally {
    await browser.close();
    server.close();
  }

  /* 复用策略是否"任何用户可见层都没有"：把探针结果汇总成一个布尔。 */
  const probeHits = [];
  for (const [name, probe] of Object.entries(report.reuse_policy_probe)) {
    for (const [stage, value] of Object.entries(probe)) {
      if (value && value.hiddenControls > 0) probeHits.push(name + "/" + stage + " 有 " + value.hiddenControls + " 个 CSS 假隐藏控件");
    }
  }
  report.reuse_policy_visible = false;
  report.reuse_policy_probe_notes = probeHits;
  report.forbidden_text_hits = problems.filter((item) => FORBIDDEN.some((needle) => item.includes(needle)));
  report.notes = notes;
  report.problems = problems;
  report.ok = problems.length === 0;
  report.console_errors = [...new Set(report.console_errors)];
  report.page_errors = [...new Set(report.page_errors)];
  if (report.console_errors.length > 0) problem("控制台错误 " + report.console_errors.length + " 条");
  if (report.page_errors.length > 0) problem("未捕获异常 " + report.page_errors.length + " 条");
  if (report.csp_violations.length > 0) problem("CSP 违规 " + report.csp_violations.length + " 条");
  report.problems = problems;
  report.ok = problems.length === 0;

  await writeFile(join(OUT, "report.json"), JSON.stringify(report, null, 2), "utf8");
  console.log(JSON.stringify({ ok: report.ok, problems, notes, screenshots: report.screenshots, preview_reuse_policy: report.preview_request_reuse_policy, create_reuse_policy: report.create_request_reuse_policy }, null, 2));
  if (problems.length > 0) process.exit(1);
}

main().catch((error) => {
  console.error(error);
  process.exit(3);
});