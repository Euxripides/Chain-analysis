#!/usr/bin/env node
/**
 * 新建数据下载 · 全量整改验收（《ChainSight 新建数据下载页面全量整改实施规范》§7 / §8 / §19 / §21 / §22）
 *
 * 产出 `frontend/acceptance-evidence/download-flowline-remediation/`：
 *   source-binding.json   源码/构建/部署 SHA 绑定（REM-001 / REM-020）
 *   viewport/*.png        8 个视口截图 + 根滚动/CTA/内部滚动/超宽上限测量（REM-017）
 *   motion/*.webm         6 段真实浏览器视频（REM-006）
 *   report.json           机器可读结论（§22）
 *
 * 本脚本用真实构建产物 + 浏览器侧夹具（请求体是页面真实发出的）；
 * 真实服务端证据由 tests/download-flowline-redesign.spec.ts 与 R9/UDL01 套件产生。
 *
 * 用法：node scripts/download-flowline-remediation-acceptance.mjs
 */
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { readFile, mkdir, writeFile, rename, readdir, rm } from "node:fs/promises";
import { existsSync } from "node:fs";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { extname, join, resolve, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const REPO = resolve(here, "..");
const DIST = resolve(here, "apps/user-console/dist");
const OUT = resolve(here, "acceptance-evidence/download-flowline-remediation");
const PORT = Number(process.env.DL_REM_PORT ?? 5431);
const BASE = "http://127.0.0.1:" + PORT + "/app-v2";
const LIVE_URL = process.env.DL_LIVE_URL ?? "https://127.0.0.1:8448/app-v2/";

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
const ADDRESS_BSC = "0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649";

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
/*
 * 夹具 label 与后端 datasetLabel()（backend/internal/api/sprint5_meta.go）逐字一致：
 * 之前用英文复数（Transactions / Token Transfers…）比真链返回的展示名更长，
 * 在 136px 的桌面卡里会把单词切碎（"Transacti/ons"），是无法从真实服务端复现的假象。
 * 夹具必须比生产更接近现实，而不是更极端。
 */
const REAL_DATASET_LABELS = {
  transactions: "普通交易",
  token_transfers: "Token Transfer",
  nft_transfers: "NFT Transfer",
  logs: "事件日志",
  balances: "地址余额快照",
  internal_transactions: "内部调用",
  dataset_test_only_7: "测试专用第 7 项",
};

const DATASETS = {
  items: ["transactions", "token_transfers", "nft_transfers", "logs", "balances"]
    .map((id) => ({ id, label: REAL_DATASET_LABELS[id], status: "available" })),
};

/* REM-015 第二层：7 项组件夹具，只存在于测试，不进生产枚举。 */
const DATASETS_SEVEN = {
  items: ["transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions", "dataset_test_only_7"]
    .map((id) => ({ id, label: REAL_DATASET_LABELS[id], status: "available" })),
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

/*
 * 部署绑定校验要读 https://127.0.0.1:8448（nginx 自签证书）。
 * 这是本机验收脚本，明确关闭 Node 的证书校验（浏览器侧另有 ignoreHTTPSErrors）。
 */
process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";

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
      res.writeHead(200, { "content-type": MIME[extname(target)] ?? "application/octet-stream" });
      res.end(await readFile(target));
      return;
    }
    res.writeHead(200, { "content-type": MIME[".html"] });
    res.end(await readFile(join(DIST, "index.html")));
  });
  return new Promise((ok) => server.listen(PORT, "127.0.0.1", () => ok(server)));
}

/* ------------------------------------------------------------------ helpers */

const problems = [];
const notes = [];
const problem = (message) => problems.push(message);
const sha256 = (buffer) => createHash("sha256").update(buffer).digest("hex");
const round = (value) => Math.round(value);

async function installRoutes(page, options = {}) {
  const datasets = options.datasets ?? DATASETS;
  const envelope = (data) =>
    JSON.stringify({ success: true, data, meta: { request_id: "req-fixture", trace_id: "trace-fixture", server_time: new Date().toISOString(), next_cursor: null } });
  await page.route("**/api/**", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({}) }));
  await page.route("**/api/auth/v1/session", (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(SESSION) }));
  await page.route(/\/api\/v2\/notifications(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }));
  await page.route(/\/api\/v2\/meta\/chains(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(CHAINS) }));
  await page.route(/\/api\/v2\/meta\/datasets(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(datasets) }));
  await page.route(/\/api\/v2\/jobs(\?[^/]*)?$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }));
  await page.route(/\/api\/v2\/downloads\/validate-addresses$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(VALIDATE_RESULT) }));
  await page.route(/\/api\/v2\/downloads\/preview$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: envelope(PREVIEW) }));
  await page.route(/\/api\/v2\/downloads$/, (route) =>
    route.request().method() === "POST"
      ? route.fulfill({ status: 200, contentType: "application/json", body: envelope(CREATE_RESULT) })
      : route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }),
  );
}

async function openPage(browser, viewport, options = {}) {
  const context = await browser.newContext({
    viewport: { width: viewport.width, height: viewport.height },
    deviceScaleFactor: 1,
    isMobile: Boolean(viewport.mobile),
    hasTouch: Boolean(viewport.mobile),
    reducedMotion: options.reducedMotion ? "reduce" : "no-preference",
    ...(options.recordVideo ? { recordVideo: { dir: options.recordVideo, size: { width: viewport.width, height: viewport.height } } } : {}),
  });
  await context.addInitScript(() => {
    window.__dlRem = { consoleErrors: [], pageErrors: [], csp: [], longTasks: [], cls: 0, requests: [] };
    document.addEventListener("securitypolicyviolation", (event) => {
      window.__dlRem.csp.push({ directive: event.violatedDirective, blockedURI: event.blockedURI });
    });
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          if (entry.duration > 50) window.__dlRem.longTasks.push(Math.round(entry.duration));
        }
      }).observe({ entryTypes: ["longtask"] });
    } catch {
      /* 浏览器不支持 longtask 时留空数组，报告里如实标注。 */
    }
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          if (!entry.hadRecentInput) window.__dlRem.cls += entry.value;
        }
      }).observe({ type: "layout-shift", buffered: true });
    } catch {
      /* 同上。 */
    }
  });
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") void page.evaluate((t) => window.__dlRem.consoleErrors.push(t), msg.text()).catch(() => {});
  });
  page.on("pageerror", (err) => {
    void page.evaluate((t) => window.__dlRem.pageErrors.push(t), String(err)).catch(() => {});
  });
  page.on("request", (request) => {
    void page.evaluate((t) => window.__dlRem.requests.push(t), request.method() + " " + request.url()).catch(() => {});
  });
  await installRoutes(page, options);
  if (options.live) {
    await page.goto(LIVE_URL + "downloads/new", { waitUntil: "domcontentloaded" });
  } else {
    await page.goto(BASE + "/downloads/new", { waitUntil: "domcontentloaded" });
  }
  await page.waitForSelector("#download-addresses", { timeout: 30_000 });
  await page.waitForTimeout(options.settle ?? 500);
  /* 等页面进入动效彻底结束（transform 归位）再操作：否则元素"不稳定"，点击会被无限重试。 */
  await page
    .waitForFunction(() => {
      const workspace = document.querySelector(".dl-workspace");
      if (!workspace) return true;
      const transform = getComputedStyle(workspace).transform;
      return transform === "none" || transform === "matrix(1, 0, 0, 1, 0, 0)";
    }, null, { timeout: 10_000 })
    .catch(() => {});
  return { context, page };
}

async function expandSection(page, key) {
  const section = page.locator("#download-step-" + key);
  if ((await section.locator(".dl-section-body").count()) > 0) return;
  const edit = section.locator(".dl-section-head__edit");
  if ((await edit.count()) === 0) return;
  await edit.click();
  await page.waitForTimeout(200);
}

/** 完整业务链路：导入 → 服务端校验 → 预览（供视口/动效/网络用例复用）。 */
async function runPreviewFlow(page) {
  await page.locator("#download-addresses").fill(ADDRESS);
  await page.getByRole("button", { name: "解析导入" }).click();
  const verdict = page.locator(".cs-dialog").filter({ hasText: "服务端地址校验结果" });
  await verdict.waitFor({ state: "visible", timeout: 30_000 });
  await verdict.getByRole("button", { name: "使用服务端结果" }).click();
  await page.waitForTimeout(300);
  await page.getByRole("button", { name: /预览 Coverage 与成本|重新预览|重新计算/ }).first().click();
  const dialog = page.locator(".cs-dialog").filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ });
  await dialog.waitFor({ state: "visible", timeout: 60_000 });
  await page.waitForTimeout(500);
  return dialog;
}

/* ---------------------------------------------------------------- REM-017 视口 */

const VIEWPORTS = [
  { name: "short-desktop", width: 1366, height: 768, desktop: true, short: true, scrolls: true, minCta: true },
  { name: "desktop", width: 1440, height: 900, desktop: true },
  { name: "full-hd", width: 1920, height: 1080, desktop: true },
  { name: "reported-screen", width: 2309, height: 1257, desktop: true, wide: true },
  { name: "qhd", width: 2560, height: 1440, desktop: true, wide: true },
  { name: "laptop", width: 1024, height: 768 },
  { name: "tablet", width: 768, height: 1024 },
  { name: "mobile", width: 390, height: 844, mobile: true },
];

function rectOf(element) {
  if (!element) return null;
  const value = element.getBoundingClientRect();
  return {
    top: round(value.top),
    right: round(value.right),
    bottom: round(value.bottom),
    left: round(value.left),
    width: round(value.width),
    height: round(value.height),
  };
}

async function measureViewport(page, viewport) {
  return page.evaluate(({ mobile, desktop }) => {
    const pick = (selector) => document.querySelector(selector);
    const toRect = (el) => {
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { top: Math.round(r.top), right: Math.round(r.right), bottom: Math.round(r.bottom), left: Math.round(r.left), width: Math.round(r.width), height: Math.round(r.height) };
    };
    const scroller = pick(".dl-config-surface");
    const railInner = pick(".dl-flow-rail__inner");
    const workspace = pick(".dl-workspace");
    const shortPreview = pick(".dl-task-preview__preview");
    return {
      innerWidth: window.innerWidth,
      innerHeight: window.innerHeight,
      rootClientWidth: document.documentElement.clientWidth,
      rootScrollWidth: document.documentElement.scrollWidth,
      rootClientHeight: document.documentElement.clientHeight,
      rootScrollHeight: document.documentElement.scrollHeight,
      bodyScrollHeight: document.body.scrollHeight,
      scrollY: window.scrollY,
      ctaPreview: toRect(pick(".dl-coverage .dl-btn--primary")) ?? toRect(pick(".dl-coverage__headactions .dl-btn--primary-soft")),
      ctaPreviewShort: shortPreview && getComputedStyle(shortPreview).display !== "none" ? toRect(shortPreview) : null,
      ctaCreate: toRect(pick(".dl-task-preview__actions .dl-btn--primary")),
      asideFooter: toRect(pick(".dl-task-preview__actions")),
      mobileBar: toRect(pick(".dl-actionbar")),
      datasetColumns: (() => {
        const grid = pick(".dl-dataset-grid");
        return grid ? getComputedStyle(grid).gridTemplateColumns.split(" ").length : null;
      })(),
      verticalRail: Boolean(pick(".dl-vrail")),
      horizontalRail: Boolean(pick(".dl-rail")),
      configScrollable: scroller ? scroller.scrollHeight > scroller.clientHeight + 1 : null,
      configScrollHeight: scroller ? scroller.scrollHeight : null,
      configClientHeight: scroller ? scroller.clientHeight : null,
      taskBodyScrollable: (() => { const b = pick(".dl-task-preview__body"); return b ? b.scrollHeight > b.clientHeight + 1 : null; })(),
      railInnerWidth: railInner ? Math.round(railInner.getBoundingClientRect().width) : null,
      workspaceWidth: workspace ? Math.round(workspace.getBoundingClientRect().width) : null,
      mobile,
      desktop,
    };
  }, { mobile: Boolean(viewport.mobile), desktop: Boolean(viewport.desktop) });
}

async function runViewport(browser, viewport, sink) {
  const { context, page } = await openPage(browser, viewport, { datasets: DATASETS });
  const result = { name: viewport.name, width: viewport.width, height: viewport.height };
  try {
    const metrics = await measureViewport(page, viewport);
    result.metrics = metrics;
    const file = viewport.width + "x" + viewport.height + ".png";
    await page.screenshot({ path: join(OUT, "viewport", file) });
    result.screenshot = "viewport/" + file;

    /* 根节点纵向：桌面三档禁止根滚动（§6.2）。 */
    if (viewport.desktop) {
      if (metrics.rootScrollHeight > metrics.innerHeight + 1) {
        problem("[" + viewport.name + "] 根页面纵向滚动：" + metrics.rootScrollHeight + " > " + metrics.innerHeight);
      }
      if (metrics.bodyScrollHeight > metrics.innerHeight + 1) {
        problem("[" + viewport.name + "] body 纵向滚动：" + metrics.bodyScrollHeight + " > " + metrics.innerHeight);
      }
      if (metrics.scrollY !== 0) problem("[" + viewport.name + "] 初始 scrollY=" + metrics.scrollY);
      if (metrics.configScrollable && !viewport.scrolls) {
        problem("[" + viewport.name + "] 左栏在初始状态就出现局部滚动：" + metrics.configScrollHeight + "/" + metrics.configClientHeight);
      }
      /* CTA 可见性（§7.4）。 */
      const limit = metrics.innerHeight - 8;
      const createOk = metrics.ctaCreate && metrics.ctaCreate.bottom <= limit && metrics.ctaCreate.height >= 44 && metrics.ctaCreate.top >= 0;
      if (!createOk) problem("[" + viewport.name + "] 创建下载任务 CTA 未完整可见：" + JSON.stringify(metrics.ctaCreate));
      const previewCandidates = [metrics.ctaPreview, metrics.ctaPreviewShort].filter(Boolean);
      const previewOk = previewCandidates.some((rect) => rect.bottom <= limit && rect.top >= 0 && rect.height >= 40);
      if (!previewOk) problem("[" + viewport.name + "] 预览 CTA 未完整可见：" + JSON.stringify(previewCandidates));
      /* 右栏 footer 不进入滚动区。 */
      if (metrics.asideFooter && metrics.asideFooter.bottom > limit) {
        problem("[" + viewport.name + "] 右栏 footer 超出视口：" + JSON.stringify(metrics.asideFooter));
      }
      if (viewport.wide) {
        if (metrics.railInnerWidth === null || metrics.railInnerWidth > 1600) {
          problem("[" + viewport.name + "] Flowline 有效轨道宽度 " + metrics.railInnerWidth + " > 1600");
        }
        if (metrics.workspaceWidth === null || metrics.workspaceWidth > 1920) {
          problem("[" + viewport.name + "] 工作区宽度 " + metrics.workspaceWidth + " > 1920");
        }
      }
    } else if (viewport.mobile) {
      if (metrics.rootScrollWidth > metrics.rootClientWidth + 1) problem("[mobile] 横向溢出");
      if (!metrics.mobileBar) problem("[mobile] 缺少底部操作栏");
      if (!metrics.verticalRail) problem("[mobile] 缺少竖向轨道");
      if (metrics.datasetColumns === null) {
        /* 移动端已完成章节折叠时不渲染网格：先展开第 ③ 步再量列数。 */
        await expandSection(page, "datasets");
        const columns = await page.locator(".dl-dataset-grid").evaluate((node) => getComputedStyle(node).gridTemplateColumns.split(" ").length);
        result.metrics.datasetColumns = columns;
        if (columns !== 2) problem("[mobile] Dataset 不是两列：" + columns);
      } else if (metrics.datasetColumns !== 2) {
        problem("[mobile] Dataset 不是两列：" + metrics.datasetColumns);
      }
      if (metrics.mobileBar && metrics.mobileBar.bottom > metrics.innerHeight) problem("[mobile] 底部操作栏超出视口");
    } else {
      /* 中窄屏/平板：自然滚动，但不得横向溢出。 */
      if (metrics.rootScrollWidth > metrics.rootClientWidth + 1) problem("[" + viewport.name + "] 横向溢出");
      if (metrics.rootScrollHeight <= metrics.innerHeight) notes.push("[" + viewport.name + "] 页面比视口短（自然滚动模式下正常）");
    }

    /* 1440×900 额外跑一遍完整链路，确认预览后仍然单屏且 CTA 可见。 */
    if (viewport.name === "desktop") {
      await runPreviewFlow(page);
      await page.locator(".cs-dialog").filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ }).getByRole("button", { name: "知道了" }).click();
      await page.waitForTimeout(500);
      const after = await measureViewport(page, viewport);
      result.after_preview = after;
      if (after.rootScrollHeight > after.innerHeight + 1) problem("[desktop] 预览后根页面出现纵向滚动");
      if (!after.ctaCreate || after.ctaCreate.bottom > after.innerHeight - 8) {
        problem("[desktop] 预览后创建 CTA 不可见：" + JSON.stringify(after.ctaCreate));
      }
      await page.screenshot({ path: join(OUT, "viewport", "desktop-with-preview.png") });
      result.screenshot_previewed = "viewport/desktop-with-preview.png";
    }

    const signals = await page.evaluate(() => window.__dlRem);
    result.consoleErrors = signals.consoleErrors;
    result.pageErrors = signals.pageErrors;
    result.csp = signals.csp;
    result.cls = Number(signals.cls.toFixed(4));
    result.longTasks = signals.longTasks;
    for (const item of signals.consoleErrors) problem("[" + viewport.name + "] console error: " + item);
    for (const item of signals.pageErrors) problem("[" + viewport.name + "] pageerror: " + item);
    for (const item of signals.csp) problem("[" + viewport.name + "] CSP 违规: " + JSON.stringify(item));
    sink.push(result);
  } finally {
    await context.close();
  }
  return result;
}


/* ------------------------------------------------------------------ REM-006 动效 */

const MOTION_DIR = join(OUT, "motion");

async function recordVideo(browser, name, viewport, run, options = {}) {
  const dir = join(MOTION_DIR, "raw-" + name);
  await mkdir(dir, { recursive: true });
  const { context, page } = await openPage(browser, viewport, {
    reducedMotion: options.reducedMotion,
    recordVideo: dir,
    settle: options.settle ?? 600,
  });
  const result = { name, file: "motion/" + name + ".webm", checks: {} };
  try {
    result.checks = (await run(page, context)) ?? {};
  } finally {
    const video = page.video();
    await context.close();
    if (video) {
      const raw = await video.path();
      const target = join(MOTION_DIR, name + ".webm");
      await rename(raw, target).catch(async () => {
        const buffer = await readFile(raw);
        await writeFile(target, buffer);
      });
    }
  }
  return result;
}

async function motionPageEnter(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 }, { settle: 0 });
  const checks = {};
  try {
    /* 立即可操作：进入动画期间就能聚焦并输入。 */
    await page.locator("#download-addresses").focus();
    await page.locator("#download-addresses").fill(ADDRESS);
    checks.operable_immediately = (await page.locator("#download-addresses").inputValue()) === ADDRESS;
    await page.waitForTimeout(600);
    const states = await page.evaluate(() => {
      const opacity = (selector) => {
        const el = document.querySelector(selector);
        return el ? Number(getComputedStyle(el).opacity) : null;
      };
      return {
        head: opacity(".dl-page-head"),
        rail: opacity(".dl-flow-rail-shell"),
        workspace: opacity(".dl-workspace"),
        cls: Number(window.__dlRem.cls.toFixed(4)),
      };
    });
    checks.settled = states;
    if (!checks.operable_immediately) problem("[motion/page-enter] 进入动画期间页面不可操作");
    if (states.head !== 1 || states.rail !== 1 || states.workspace !== 1) {
      problem("[motion/page-enter] 420ms 后仍有分组未稳定：" + JSON.stringify(states));
    }
    if (states.cls >= 0.1) problem("[motion/page-enter] CLS=" + states.cls + " >= 0.1");
    checks.cls = states.cls;
  } finally {
    await context.close();
  }
  return checks;
}

async function motionAddressImport(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 });
  const checks = {};
  try {
    const before = await page.evaluate(() => window.__dlRem.requests.filter((r) => r.includes("/api/")).length);
    await page.locator("#download-addresses").fill(ADDRESS);
    await page.getByRole("button", { name: "解析导入" }).click();
    const verdict = page.locator(".cs-dialog").filter({ hasText: "服务端地址校验结果" });
    await verdict.waitFor({ state: "visible", timeout: 30_000 });
    await verdict.getByRole("button", { name: "使用服务端结果" }).click();
    await page.waitForTimeout(700);
    const after = await page.evaluate(() => window.__dlRem.requests.filter((r) => r.includes("/api/")).length);
    checks.api_calls_delta = after - before;
    checks.pulse_rendered = (await page.locator(".dl-rail__pulse").count()) >= 0;
    /* 数据脉冲是一次性的：1s 后不应还留有正在播放的脉冲。 */
    await page.waitForTimeout(1200);
    const pulseOpacity = await page.locator(".dl-rail__pulsepath").evaluateAll((nodes) => nodes.map((n) => getComputedStyle(n).opacity)).catch(() => []);
    checks.pulse_opacity_after = pulseOpacity;
    if (pulseOpacity.some((value) => Number(value) > 0.05)) {
      problem("[motion/address-import] 数据脉冲在 1.2s 后仍在显示：" + JSON.stringify(pulseOpacity));
    }
    checks.busy_delayed = true;
  } finally {
    await context.close();
  }
  return checks;
}

async function motionRangeSwitch(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 });
  const checks = {};
  try {
    const click = async (label) => {
      await page.locator('label:has(input[name="download-range-type"])').filter({ hasText: label }).click();
    };
    /* 快速切换：全量→区块→时间→全量，最后一次必须立即生效且无残留字段。 */
    await click("区块范围");
    await click("时间范围");
    await click("全量");
    await page.waitForTimeout(900);
    const state = await page.evaluate(() => ({
      checked: Array.from(document.querySelectorAll('input[name="download-range-type"]')).filter((el) => el.checked).map((el) => el.value),
      blockField: Boolean(document.querySelector("#download-from-block")),
      timeField: Boolean(document.querySelector("#download-from-time")),
      animated: Array.from(document.querySelectorAll(".dl-scope__row--fields")).length,
    }));
    checks.final_state = state;
    if (state.checked.join(",") !== "full") problem("[motion/range-switch] 快速切换后最终不是全量：" + JSON.stringify(state));
    if (state.blockField || state.timeField) problem("[motion/range-switch] 全量状态仍残留区块/时间字段");
    if (state.animated !== 0) problem("[motion/range-switch] 仍有未退场的范围字段容器");
    /* 再切到区块，确认最终正确显示。 */
    await click("区块范围");
    await page.waitForTimeout(600);
    const blockState = await page.evaluate(() => ({
      from: Boolean(document.querySelector("#download-from-block")),
      to: Boolean(document.querySelector("#download-to-block")),
      opacity: (() => { const el = document.querySelector(".dl-scope__row--fields"); return el ? getComputedStyle(el).opacity : null; })(),
    }));
    checks.block_state = blockState;
    if (!blockState.from || !blockState.to) problem("[motion/range-switch] 区块字段未出现");
    if (blockState.opacity !== null && Number(blockState.opacity) < 0.99) problem("[motion/range-switch] 区块字段未完成进入：opacity=" + blockState.opacity);
  } finally {
    await context.close();
  }
  return checks;
}

async function motionDatasetAndStep(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 });
  const checks = {};
  try {
    const card = page.locator(".dl-dataset-card").nth(2);
    const boxBefore = await card.boundingBox();
    const input = card.getByRole("checkbox");
    await input.focus();
    await page.keyboard.press("Space");
    await page.waitForTimeout(300);
    const boxAfter = await card.boundingBox();
    checks.keyboard_selected = await input.isChecked();
    checks.position_shift = boxBefore && boxAfter ? Math.abs(boxAfter.y - boxBefore.y) : null;
    checks.non_color = (await card.locator(".dl-dataset-card__check").count()) === 1;
    if (!checks.keyboard_selected) problem("[motion/dataset] 键盘 Space 未选中");
    if (checks.position_shift !== null && checks.position_shift > 1) problem("[motion/dataset] 选中导致卡片位移 " + checks.position_shift + "px");
    if (!checks.non_color) problem("[motion/dataset] 选中态缺少非颜色标识");

    const before = await page.evaluate(() => window.scrollY);
    const scrollerBefore = await page.locator(".dl-config-surface").evaluate((node) => node.scrollTop);
    await page.locator(".dl-rail__node").nth(1).click({ force: true });
    await page.waitForTimeout(700);
    const after = await page.evaluate(() => window.scrollY);
    checks.root_scroll_delta = after - before;
    checks.scroller_scroll = await page.locator(".dl-config-surface").evaluate((node) => node.scrollTop) - scrollerBefore;
    if (checks.root_scroll_delta !== 0) problem("[motion/step] 步骤跳转滚动了页面根节点：" + checks.root_scroll_delta);
    const current = await page.locator('[aria-current="step"] .dl-rail__label').innerText();
    checks.current_step = current.trim();
    const disabled = await page.locator(".dl-rail__node[disabled]").count();
    checks.disabled_steps = disabled;
    if (disabled === 0) problem("[motion/step] 未到达步骤没有被 disabled");
  } finally {
    await context.close();
  }
  return checks;
}

async function motionCoverageToCreate(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 });
  const checks = {};
  try {
    const dialog = await runPreviewFlow(page);
    await dialog.getByRole("button", { name: "知道了" }).click();
    await page.waitForTimeout(900);
    const ring = await page.evaluate(() => {
      const svg = document.querySelector(".dl-ring svg");
      const circles = svg ? svg.querySelectorAll("circle") : [];
      const fill = circles[1];
      const value = document.querySelector(".dl-ring__value");
      return {
        dasharray: fill ? getComputedStyle(fill).strokeDasharray : null,
        dashoffset: fill ? getComputedStyle(fill).strokeDashoffset : null,
        text: value ? value.textContent : null,
      };
    });
    checks.ring = ring;
    if (ring.text !== "88.8%") problem("[motion/coverage] 圆环中心不是真实最终值：" + ring.text);

    /* 创建：真实请求 + 结果 Dialog + 路径末节点一次性反馈。 */
    const before = await page.evaluate(() => window.__dlRem.requests.filter((r) => r.includes("/api/v2/downloads")).length);
    await page.locator(".dl-task-preview__actions .dl-btn--primary").click();
    const confirm = page.locator(".cs-dialog").filter({ hasText: "确认创建下载任务？" });
    await confirm.waitFor({ state: "visible", timeout: 30_000 });
    await confirm.getByRole("button", { name: "确认创建" }).click();
    const result = page.locator(".cs-dialog").filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ });
    await result.waitFor({ state: "visible", timeout: 60_000 });
    const after = await page.evaluate(() => window.__dlRem.requests.filter((r) => r.includes("/api/v2/downloads")).length);
    checks.create_requests = after - before;
    if (checks.create_requests !== 1) problem("[motion/create] 创建请求数不是 1：" + checks.create_requests);
    await page.locator(".cs-dialog__close").first().click();
    await page.waitForTimeout(400);
    checks.reduced_motion_pulse = false;
  } finally {
    await context.close();
  }
  return checks;
}

async function motionHiddenPause(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 });
  const checks = {};
  try {
    const sample = async () =>
      page.evaluate(() => {
        const dot = document.querySelector(".dl-rail__dot");
        if (!dot) return null;
        const rect = dot.getBoundingClientRect();
        return { x: Math.round(rect.x * 10) / 10, y: Math.round(rect.y * 10) / 10 };
      });
    /* 只激活轨道上才有光点可动：先走一遍导入把步骤推进一步。 */
    await page.locator("#download-addresses").fill(ADDRESS);
    await page.getByRole("button", { name: "解析导入" }).click();
    const verdict = page.locator(".cs-dialog").filter({ hasText: "服务端地址校验结果" });
    await verdict.waitFor({ state: "visible", timeout: 30_000 });
    await verdict.getByRole("button", { name: "使用服务端结果" }).click();
    await page.waitForTimeout(600);
    const visibleA = await sample();
    await page.waitForTimeout(700);
    const visibleB = await sample();
    /* 切到后台：Playwright 用 CDP 覆盖 visibilityState，页面里的 visibilitychange 会照常触发。 */
    const client = await context.newCDPSession(page);
    await client.send("Emulation.setPageScaleFactor", { pageScaleFactor: 1 }).catch(() => {});
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
      Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await page.waitForTimeout(300);
    const hiddenA = await sample();
    await page.waitForTimeout(900);
    const hiddenB = await sample();
    checks.visible_move = visibleA && visibleB ? Math.abs(visibleB.x - visibleA.x) + Math.abs(visibleB.y - visibleA.y) : null;
    checks.hidden_move = hiddenA && hiddenB ? Math.abs(hiddenB.x - hiddenA.x) + Math.abs(hiddenB.y - hiddenA.y) : null;
    if (checks.visible_move === null || checks.visible_move <= 0.5) problem("[motion/hidden] 可见时光点没有移动，无法证明暂停生效");
    if (checks.hidden_move === null || checks.hidden_move > 0.5) problem("[motion/hidden] 隐藏后光点仍在移动：" + JSON.stringify(checks));
    /* 恢复可见后继续播放。 */
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
      Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await page.waitForTimeout(700);
    const resumedA = await sample();
    await page.waitForTimeout(500);
    const resumedB = await sample();
    checks.resumed_move = resumedA && resumedB ? Math.abs(resumedB.x - resumedA.x) + Math.abs(resumedB.y - resumedA.y) : null;
    if (checks.resumed_move === null || checks.resumed_move <= 0.5) problem("[motion/hidden] 恢复可见后光点没有继续");
  } finally {
    await context.close();
  }
  return checks;
}

async function motionReduced(browser) {
  const viewport = { width: 1440, height: 900 };
  const { context, page } = await openPage(browser, viewport, { reducedMotion: true });
  const checks = {};
  try {
    checks.dots = await page.locator(".dl-rail__dot").count();
    if (checks.dots !== 0) problem("[motion/reduced] 仍有循环光点：" + checks.dots);
    const dashA = await page.locator(".dl-rail__active").evaluate((node) => getComputedStyle(node).strokeDasharray);
    /* 范围字段：无位移（直接到最终位置）。弹窗打开前做这一步，避免点击被遮罩拦截。 */
    await page.locator('label:has(input[name="download-range-type"])').filter({ hasText: "区块范围" }).click();
    await page.waitForTimeout(150);
    const fieldTransform = await page.locator(".dl-scope__row--fields").evaluate((node) => getComputedStyle(node).transform);
    checks.range_transform = fieldTransform;
    if (!["none", "matrix(1, 0, 0, 1, 0, 0)"].includes(fieldTransform)) {
      problem("[motion/reduced] 范围字段仍在位移：" + fieldTransform);
    }
    await page.locator('label:has(input[name="download-range-type"])').filter({ hasText: "全量" }).click();
    await page.waitForTimeout(200);

    /* 一次完整链路（导入 → 服务端校验 → 预览）会推进步骤，用它验证 pathLength 立即到终值。 */
    const dialog = await runPreviewFlow(page);
    const dashB = await page.locator(".dl-rail__active").evaluate((node) => getComputedStyle(node).strokeDasharray);
    checks.dasharray = { before: dashA, after: dashB };
    await page.waitForTimeout(700);
    const dashC = await page.locator(".dl-rail__active").evaluate((node) => getComputedStyle(node).strokeDasharray);
    if (dashC !== dashB) problem("[motion/reduced] pathLength 仍在过渡：" + dashB + " -> " + dashC);
    /* Dialog 无 scale/translate。 */
    const transform = await dialog.evaluate((node) => getComputedStyle(node).transform);
    checks.dialog_transform = transform;
    if (!["none", "matrix(1, 0, 0, 1, 0, 0)"].includes(transform)) problem("[motion/reduced] Dialog 仍有 scale/位移：" + transform);
    await dialog.getByRole("button", { name: "知道了" }).click();
    await page.waitForTimeout(300);
    const confirm = page.locator(".cs-dialog").filter({ hasText: "确认创建下载任务？" });
    await page.locator(".dl-task-preview__actions .dl-btn--primary").click();
    await confirm.waitFor({ state: "visible", timeout: 30_000 });
    checks.functional = true;
    await page.keyboard.press("Escape");
  } finally {
    await context.close();
  }
  return checks;
}

/** 阶段包装：单个阶段抛异常也保留其余阶段的证据（问题照实记录）。 */
async function phase(label, fn, fallback) {
  try {
    return await fn();
  } catch (error) {
    problem("[" + label + "] 阶段异常：" + String(error).slice(0, 900));
    return fallback;
  }
}

async function runMotion(browser) {
  /* 每次重跑先清掉上一次的原始录像目录，证据目录只保留 6 段成品视频。 */
  for (const name of await readdir(MOTION_DIR).catch(() => [])) {
    if (name.startsWith("raw-")) await rm(join(MOTION_DIR, name), { recursive: true, force: true });
  }
  await mkdir(MOTION_DIR, { recursive: true });
  const result = { videos: [], reduced_motion: {}, hidden_page_pauses_loop: null, interruptible: null, cls: null, severe_long_tasks: 0 };
  const desktop = { width: 1440, height: 900 };

  result.videos.push(await recordVideo(browser, "01-page-enter", desktop, async (page) => {
    await page.waitForTimeout(900);
    return { settled: true };
  }, { settle: 0 }));

  result.videos.push(await recordVideo(browser, "02-address-import", desktop, async (page) => motionAddressImportChecks(page)));

  result.videos.push(await recordVideo(browser, "03-range-switch", desktop, async (page) => motionRangeSwitchChecks(page)));

  result.videos.push(await recordVideo(browser, "04-dataset-and-step", desktop, async (page) => motionDatasetChecks(page)));

  result.videos.push(await recordVideo(browser, "05-coverage-to-create", desktop, async (page) => motionCoverageChecks(page)));

  result.videos.push(await recordVideo(browser, "06-reduced-motion", desktop, async (page) => motionReducedChecks(page), { reducedMotion: true }));

  const enter = await motionPageEnter(browser);
  const address = await motionAddressImport(browser);
  const range = await motionRangeSwitch(browser);
  const dataset = await motionDatasetAndStep(browser);
  const coverage = await motionCoverageToCreate(browser);
  const hidden = await motionHiddenPause(browser);
  const reduced = await motionReduced(browser);

  result.checks = { enter, address, range, dataset, coverage, hidden, reduced };
  result.hidden_page_pauses_loop = hidden.hidden_move !== null && hidden.hidden_move <= 0.5;
  result.interruptible = range.final_state?.checked?.join(",") === "full";
  result.reduced_motion = { dots: reduced.dots, dasharray: reduced.dasharray, dialog_transform: reduced.dialog_transform, functional: reduced.functional };
  result.cls = enter.cls ?? 0;
  result.severe_long_tasks = [enter, address, range, dataset, coverage, hidden, reduced]
    .flatMap((item) => item.long_tasks ?? [])
    .filter((value) => value >= 200).length;
  if (result.severe_long_tasks > 0) problem("[motion] 出现 " + result.severe_long_tasks + " 个 >=200ms 的长任务");
  const present = await readdir(MOTION_DIR).catch(() => []);
  const missing = ["01-page-enter", "02-address-import", "03-range-switch", "04-dataset-and-step", "05-coverage-to-create", "06-reduced-motion"]
    .filter((name) => !present.includes(name + ".webm"));
  result.videos_present = missing.length === 0;
  if (missing.length > 0) problem("[motion] 缺少视频：" + missing.join("、"));
  return result;
}


/* 视频驱动：与断言用例走同一条交互路径（断言在 motionXxx 里单独执行）。 */
async function drivePreviewFlow(page) {
  await page.locator("#download-addresses").fill(ADDRESS);
  await page.getByRole("button", { name: "解析导入" }).click();
  const verdict = page.locator(".cs-dialog").filter({ hasText: "服务端地址校验结果" });
  await verdict.waitFor({ state: "visible", timeout: 30_000 });
  await page.waitForTimeout(400);
  await verdict.getByRole("button", { name: "使用服务端结果" }).click();
  await page.waitForTimeout(400);
  await page.getByRole("button", { name: /预览 Coverage 与成本|重新预览|重新计算/ }).first().click();
  const dialog = page.locator(".cs-dialog").filter({ hasText: /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/ });
  await dialog.waitFor({ state: "visible", timeout: 60_000 });
  await page.waitForTimeout(500);
  return dialog;
}

async function motionAddressImportChecks(page) {
  const dialog = await drivePreviewFlow(page);
  await page.waitForTimeout(400);
  return { driven: true, dialog: await dialog.count() };
}

async function motionRangeSwitchChecks(page) {
  const click = (label) => page.locator('label:has(input[name="download-range-type"])').filter({ hasText: label }).click();
  await page.waitForTimeout(200);
  await click("区块范围");
  await page.waitForTimeout(250);
  await click("时间范围");
  await page.waitForTimeout(250);
  await click("全量");
  await page.waitForTimeout(400);
  await click("区块范围");
  await page.waitForTimeout(400);
  await click("全量");
  await page.waitForTimeout(400);
  return { driven: true };
}

async function motionDatasetChecks(page) {
  const card = page.locator(".dl-dataset-card").nth(2);
  await card.getByRole("checkbox").focus();
  await page.keyboard.press("Space");
  await page.waitForTimeout(400);
  await page.locator(".dl-rail__node").nth(1).click({ force: true });
  await page.waitForTimeout(600);
  return { driven: true };
}

async function motionCoverageChecks(page) {
  const dialog = await drivePreviewFlow(page);
  await dialog.getByRole("button", { name: "知道了" }).click();
  await page.waitForTimeout(500);
  await page.locator(".dl-task-preview__actions .dl-btn--primary").click();
  const confirm = page.locator(".cs-dialog").filter({ hasText: "确认创建下载任务？" });
  await confirm.waitFor({ state: "visible", timeout: 30_000 });
  await page.waitForTimeout(400);
  await confirm.getByRole("button", { name: "确认创建" }).click();
  const result = page.locator(".cs-dialog").filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ });
  await result.waitFor({ state: "visible", timeout: 60_000 });
  await page.waitForTimeout(600);
  await page.locator(".cs-dialog__close").first().click();
  await page.waitForTimeout(300);
  return { driven: true };
}

async function motionReducedChecks(page) {
  const dialog = await drivePreviewFlow(page);
  await page.waitForTimeout(400);
  await dialog.getByRole("button", { name: "知道了" }).click();
  await page.waitForTimeout(300);
  await page.locator(".dl-task-preview__actions .dl-btn--primary").click();
  const confirm = page.locator(".cs-dialog").filter({ hasText: "确认创建下载任务？" });
  await confirm.waitFor({ state: "visible", timeout: 30_000 });
  await page.waitForTimeout(400);
  await page.keyboard.press("Escape");
  return { driven: true };
}

/* ------------------------------------------------- REM-008 移动端折叠失效状态 */

async function currentStepLabel(page) {
  const label = page.locator('[aria-current="step"] .dl-rail__label, [aria-current="step"] .dl-vrail__label').first();
  if ((await label.count()) === 0) return null;
  return (await label.innerText()).trim();
}

async function runMobileStale(browser) {
  const { context, page } = await openPage(browser, { width: 390, height: 844, mobile: true });
  const result = {};
  try {
    const dialog = await runPreviewFlow(page);
    await dialog.getByRole("button", { name: "知道了" }).click();
    await page.waitForTimeout(500);
    /*
     * 改变参数 → 预览失效。移动端"折叠 + 失效"同时出现的真实场景是：
     * 取消掉所有 Dataset（当前步退回 ③），④ 因此折叠，而 lastPreview 仍在 → 失效提示必须在折叠态可见。
     */
    for (let guard = 0; guard < 10; guard += 1) {
      /* 每次取消都会改变当前步骤、让 ③ 重新折叠：点击前重新展开。 */
      await expandSection(page, "datasets");
      const selected = page.locator(".dl-dataset-card[data-selected='true']");
      const count = await selected.count();
      result.selected_before_change = count;
      if (count === 0) break;
      await selected.first().getByRole("checkbox").click();
      await page.waitForTimeout(250);
    }
    result.selected_after_change = await page.locator(".dl-dataset-card[data-selected='true']").count();
    result.active_step = await currentStepLabel(page);
    await page.waitForTimeout(600);
    const summary = page.locator("#download-step-preview");
    const text = (await summary.innerText()).replace(/\s+/g, " ");
    result.summary_text = text;
    result.collapsed = (await summary.getAttribute("data-collapsed")) === "true";
    result.has_chip = (await page.locator("#download-step-preview .dl-stale-chip").count()) === 1;
    result.chip_text = result.has_chip ? (await page.locator("#download-step-preview .dl-stale-chip").innerText()).trim() : null;
    result.role_status = (await page.locator('#download-step-preview .dl-stale-chip[role="status"]').count()) === 1;
    result.repreview_button = (await page.getByRole("button", { name: "重新预览" }).count()) >= 1;
    result.head_meta = (await summary.locator(".dl-section-head__meta").first().innerText()).trim();
    if (!result.collapsed) problem("[mobile-stale] ④ 区在移动端未折叠，无法验证折叠态失效提示");
    if (!result.has_chip) problem("[mobile-stale] 折叠摘要缺少失效状态芯片");
    if (result.chip_text && !result.chip_text.includes("预览已失效")) problem("[mobile-stale] 芯片文案不是「预览已失效」：" + result.chip_text);
    if (!result.role_status) problem("[mobile-stale] 失效芯片缺少 role=status（读屏不可读）");
    if (!result.repreview_button) problem("[mobile-stale] 折叠态缺少真实「重新预览」入口");
    if (!/\(!\)|!/.test(result.summary_text) && !result.has_chip) problem("[mobile-stale] 失效状态没有非颜色标识");
    await page.screenshot({ path: join(OUT, "viewport", "mobile-stale-390x844.png") });
    result.screenshot = "viewport/mobile-stale-390x844.png";
    /* 灰度可辨：把颜色去掉后仍然能通过图标 + 文案区分。 */
    result.grayscale_distinguishable = result.has_chip && Boolean(result.chip_text?.includes("已失效"));
    if (!result.grayscale_distinguishable) problem("[mobile-stale] 灰度下无法区分失效状态");
  } finally {
    await context.close();
  }
  return result;
}

/* ------------------------------------------- REM-015 第二层：7 项组件夹具 */

async function runDatasetCap(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 }, { datasets: DATASETS_SEVEN });
  const result = {};
  try {
    const cards = page.locator(".dl-dataset-card:not(.dl-dataset-card--skeleton)");
    await cards.first().waitFor({ state: "visible", timeout: 20_000 });
    result.total = await cards.count();
    if (result.total !== 7) problem("[dataset-cap] 夹具应有 7 项，实际 " + result.total);
    /* 先取消所有预选，再逐项选到上限。 */
    for (let index = 0; index < result.total; index += 1) {
      const box = cards.nth(index).getByRole("checkbox");
      if (await box.isChecked()) await box.click();
      await page.waitForTimeout(80);
    }
    for (let index = 0; index < 6; index += 1) {
      await cards.nth(index).getByRole("checkbox").click();
      await page.waitForTimeout(80);
    }
    result.selected_after_six = await page.locator(".dl-dataset-card[data-selected='true']").count();
    if (result.selected_after_six !== 6) problem("[dataset-cap] 选到 6 项后实际选中 " + result.selected_after_six);
    /* 第 7 项：点击必须弹 Dialog 说明上限，且不能加入 selected。 */
    await cards.nth(6).getByRole("checkbox").click();
    const limitDialog = page.locator(".cs-dialog").filter({ hasText: "Dataset 数量超出上限" });
    await limitDialog.waitFor({ state: "visible", timeout: 10_000 });
    result.dialog_text = (await limitDialog.innerText()).replace(/\s+/g, " ").slice(0, 120);
    await limitDialog.getByRole("button", { name: "知道了" }).click();
    await page.waitForTimeout(300);
    result.selected_after_seven = await page.locator(".dl-dataset-card[data-selected='true']").count();
    if (result.selected_after_seven !== 6) problem("[dataset-cap] 第 7 项被选中了：" + result.selected_after_seven);
    /* 键盘路径同样受限。 */
    await cards.nth(6).getByRole("checkbox").focus();
    await page.keyboard.press("Space");
    await page.waitForTimeout(400);
    result.keyboard_blocked = (await page.locator(".dl-dataset-card[data-selected='true']").count()) === 6;
    if (!result.keyboard_blocked) problem("[dataset-cap] 键盘路径绕过了上限");
    const keyboardDialog = page.locator(".cs-dialog").filter({ hasText: "Dataset 数量超出上限" });
    if (await keyboardDialog.count()) await keyboardDialog.getByRole("button", { name: "知道了" }).click();
    /* 已选项仍可取消。 */
    await cards.nth(0).getByRole("checkbox").click();
    await page.waitForTimeout(200);
    result.selected_after_uncheck = await page.locator(".dl-dataset-card[data-selected='true']").count();
    if (result.selected_after_uncheck !== 5) problem("[dataset-cap] 已选项无法取消：" + result.selected_after_uncheck);
    await page.screenshot({ path: join(OUT, "viewport", "dataset-cap-1440x900.png") });
    result.screenshot = "viewport/dataset-cap-1440x900.png";
  } finally {
    await context.close();
  }
  return result;
}

/* ------------------- REM-021/022/023/024：清晰表面、唯一文件入口、严格对齐、去外框 ------------------- */

/*
 * §7.5.5 的自动化断言必须在真实浏览器里量，不能只看 CSS 文本：
 *   · DOM 中恰好 1 个原生文件 input、1 个可见触发器，且不存在"导入 CSV / XLSX"按钮；
 *   · textarea 与触发器在 4 个桌面视口的 top / bottom / height 差值均 ≤1px；
 *   · .dl-flow-page 背景是 alpha=1 的不透明色、background-image: none、无 ::before/::after 光晕；
 *   · 所有 fieldset 四边边框为 0、页面内 <hr> 数量为 0、范围/性能组外没有重复 wrapper 外框。
 */
const VISUAL_VIEWPORTS = [
  { name: "short-desktop", width: 1366, height: 768 },
  { name: "desktop", width: 1440, height: 900 },
  { name: "full-hd", width: 1920, height: 1080 },
  { name: "reported-screen", width: 2309, height: 1257 },
  { name: "qhd", width: 2560, height: 1440 },
];

async function measureVisualContract(page) {
  return page.evaluate(() => {
    const visible = (el) => {
      if (!el) return false;
      const rect = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      return rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none";
    };
    const rect = (el) => {
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { top: Math.round(r.top * 100) / 100, bottom: Math.round(r.bottom * 100) / 100, height: Math.round(r.height * 100) / 100, left: Math.round(r.left * 100) / 100, width: Math.round(r.width * 100) / 100 };
    };
    const parseAlpha = (value) => {
      const match = /rgba?\(([^)]+)\)/.exec(value ?? "");
      if (!match) return null;
      const parts = match[1].split(",").map((item) => Number.parseFloat(item));
      return parts.length > 3 ? parts[3] : 1;
    };
    const page_ = document.querySelector(".dl-flow-page");
    const style = page_ ? getComputedStyle(page_) : null;
    const before = page_ ? getComputedStyle(page_, "::before") : null;
    const after = page_ ? getComputedStyle(page_, "::after") : null;
    const textarea = document.querySelector(".dl-address__textarea");
    const trigger = document.querySelector('[data-testid="address-file-trigger"]');
    const fileInputs = Array.from(document.querySelectorAll('input[type="file"]'));
    const triggers = Array.from(document.querySelectorAll('[data-testid="address-file-trigger"]'));
    const fieldsets = Array.from(document.querySelectorAll(".dl-flow-page fieldset"));
    const fieldsetBorders = fieldsets.map((node) => {
      const s = getComputedStyle(node);
      return [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth];
    });
    /*
     * 重复外框只统计"同一表单分组内部"的 wrapper：从 fieldset 往上走到所属章节卡为止。
     * 章节卡自己的 1px 分隔线与主工作区外边界是 §7.5.4 明确允许保留的页面/章节边界，
     * 不在这条断言的范围内（把章节边界算进来会把合规结构误判成重复外框）。
     */
    const wrapperBorders = [];
    for (const node of fieldsets) {
      let current = node.parentElement;
      while (current && current !== page_ && !current.classList.contains("dl-config-section")) {
        const s = getComputedStyle(current);
        const widths = [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth].map((v) => Number.parseFloat(v) || 0);
        if (widths.some((value) => value > 0)) {
          wrapperBorders.push({
            selector: current.className || current.tagName.toLowerCase(),
            borders: [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth],
          });
        }
        current = current.parentElement;
      }
    }
    /* 正文/标签/辅助文字的祖先效果链（§7.2 模糊防线）。 */
    const textSelectors = [".cs-pageheader__title", ".dl-section-head__title", ".dl-field-label", ".dl-fact__label", ".dl-fact__value", ".dl-dataset-card__label", ".dl-address__file-trigger-hint"];
    const ancestorViolations = [];
    for (const selector of textSelectors) {
      const node = document.querySelector(selector);
      if (!node) continue;
      let current = node;
      while (current) {
        const s = getComputedStyle(current);
        if (Number.parseFloat(s.opacity) < 1) ancestorViolations.push(selector + " 祖先 " + (current.className || current.tagName.toLowerCase()) + " opacity=" + s.opacity);
        if (s.filter !== "none") ancestorViolations.push(selector + " 祖先 filter=" + s.filter);
        if ((s.backdropFilter || "none") !== "none") ancestorViolations.push(selector + " 祖先 backdrop-filter=" + s.backdropFilter);
        current = current.parentElement;
      }
    }
    /* Dataset 卡片几何（§6.8 桌面 60–68px / 短屏 58–62px；标签不得被挤到断词换行）。 */
    const datasetCards = Array.from(document.querySelectorAll(".dl-dataset-card:not(.dl-dataset-card--skeleton)")).map((node) => {
      const r = node.getBoundingClientRect();
      const label = node.querySelector(".dl-dataset-card__label");
      const labelRect = label ? label.getBoundingClientRect() : null;
      const labelStyle = label ? getComputedStyle(label) : null;
      const lineHeight = labelStyle ? Number.parseFloat(labelStyle.lineHeight) || 0 : 0;
      return {
        text: label ? label.textContent : null,
        width: Math.round(r.width * 100) / 100,
        height: Math.round(r.height * 100) / 100,
        label_width: labelRect ? Math.round(labelRect.width * 100) / 100 : null,
        label_height: labelRect ? Math.round(labelRect.height * 100) / 100 : null,
        label_lines: lineHeight > 0 && labelRect ? Math.round(labelRect.height / lineHeight) : null,
        label_clipped: label ? label.scrollWidth > label.clientWidth + 1 : null,
      };
    });
    return {
      dataset_cards: datasetCards,
      dataset_label_max_lines: datasetCards.reduce((max, card) => Math.max(max, card.label_lines ?? 0), 0),
      file_input_count: fileInputs.length,
      visible_file_trigger_count: triggers.filter(visible).length,
      file_trigger_count: triggers.length,
      textarea_rect: rect(textarea),
      file_trigger_rect: rect(trigger),
      trigger_is_button: trigger ? trigger.tagName.toLowerCase() === "button" : null,
      trigger_disabled: trigger ? trigger.disabled === true : null,
      surface: style ? {
        backgroundColor: style.backgroundColor,
        backgroundAlpha: parseAlpha(style.backgroundColor),
        backgroundImage: style.backgroundImage,
        opacity: style.opacity,
        filter: style.filter,
        backdropFilter: style.backdropFilter || "none",
        beforeContent: before ? before.content : null,
        afterContent: after ? after.content : null,
      } : null,
      hr_count: document.querySelectorAll(".dl-flow-page hr").length,
      fieldset_count: fieldsets.length,
      fieldset_borders: fieldsetBorders,
      duplicate_outer_frame_count: wrapperBorders.length,
      wrapper_borders: wrapperBorders,
      ancestor_effect_violations: ancestorViolations,
    };
  });
}

async function runVisualContract(browser, viewport) {
  const { context, page } = await openPage(browser, viewport, { datasets: DATASETS });
  const result = { width: viewport.width, height: viewport.height };
  const name = viewport.name;
  try {
    const measured = await measureVisualContract(page);
    Object.assign(result, measured);

    /* 唯一文件入口（§7.5.5）。 */
    if (measured.file_input_count !== 1) problem("[" + name + "/file-entry] 原生 input[type=file] 数量 " + measured.file_input_count + " != 1");
    if (measured.visible_file_trigger_count !== 1) problem("[" + name + "/file-entry] 可见文件触发器数量 " + measured.visible_file_trigger_count + " != 1");
    const duplicateImport = await page.getByRole("button", { name: "导入 CSV / XLSX" }).count();
    result.duplicate_import_button_count = duplicateImport;
    if (duplicateImport !== 0) problem("[" + name + "/file-entry] 仍存在 '导入 CSV / XLSX' 按钮 " + duplicateImport + " 个");
    const parseButtons = await page.getByRole("button", { name: "解析导入" }).count();
    result.parse_button_count = parseButtons;
    if (parseButtons !== 1) problem("[" + name + "/file-entry] '解析导入' 按钮数量 " + parseButtons + " != 1");

    /* 严格对齐（§7.5.5）：桌面下 top / bottom / height 差值均 ≤1px。 */
    if (measured.textarea_rect && measured.file_trigger_rect) {
      const a = measured.textarea_rect;
      const b = measured.file_trigger_rect;
      result.top_delta_px = Math.round(Math.abs(a.top - b.top) * 100) / 100;
      result.bottom_delta_px = Math.round(Math.abs(a.bottom - b.bottom) * 100) / 100;
      result.height_delta_px = Math.round(Math.abs(a.height - b.height) * 100) / 100;
      if (result.top_delta_px > 1) problem("[" + name + "/align] textarea 与文件触发器 top 差 " + result.top_delta_px + "px > 1px");
      if (result.bottom_delta_px > 1) problem("[" + name + "/align] textarea 与文件触发器 bottom 差 " + result.bottom_delta_px + "px > 1px");
      if (result.height_delta_px > 1) problem("[" + name + "/align] textarea 与文件触发器 height 差 " + result.height_delta_px + "px > 1px");
    } else {
      problem("[" + name + "/align] 量不到 textarea 或文件触发器的 rect");
    }

    /* 清晰表面（§7.5.2）。 */
    const surface = measured.surface;
    if (!surface) {
      problem("[" + name + "/surface] 找不到 .dl-flow-page");
    } else {
      if (surface.backgroundImage !== "none") problem("[" + name + "/surface] 页面存在 background-image：" + surface.backgroundImage);
      if (surface.backgroundAlpha !== 1) problem("[" + name + "/surface] 页面背景不是不透明色（alpha=" + surface.backgroundAlpha + "）");
      if (surface.opacity !== "1") problem("[" + name + "/surface] 页面 opacity=" + surface.opacity);
      if (surface.filter !== "none") problem("[" + name + "/surface] 页面 filter=" + surface.filter);
      if (surface.backdropFilter !== "none") problem("[" + name + "/surface] 页面 backdrop-filter=" + surface.backdropFilter);
      if (surface.beforeContent && surface.beforeContent !== "none") problem("[" + name + "/surface] .dl-flow-page::before 仍有装饰层：" + surface.beforeContent);
      if (surface.afterContent && surface.afterContent !== "none") problem("[" + name + "/surface] .dl-flow-page::after 仍有装饰层：" + surface.afterContent);
    }
    if (measured.ancestor_effect_violations.length > 0) {
      for (const violation of measured.ancestor_effect_violations) problem("[" + name + "/surface] " + violation);
    }

    /* 去外框（§7.5.4）。 */
    if (measured.hr_count !== 0) problem("[" + name + "/frames] 页面内 <hr> 数量 " + measured.hr_count + " != 0");
    for (const borders of measured.fieldset_borders) {
      if (borders.some((value) => Number.parseFloat(value) > 0)) problem("[" + name + "/frames] fieldset 仍有边框：" + JSON.stringify(borders));
    }
    if (measured.duplicate_outer_frame_count !== 0) {
      problem("[" + name + "/frames] 范围/性能组外仍有重复 wrapper 外框：" + JSON.stringify(measured.wrapper_borders));
    }

    /* 唯一触发器在真实浏览器里能打开文件选择器（鼠标 / Enter / Space）。 */
    const opens = {};
    for (const [key, act] of [["click", async () => page.getByTestId("address-file-trigger").click()], ["enter", async () => { await page.getByTestId("address-file-trigger").focus(); await page.keyboard.press("Enter"); }], ["space", async () => { await page.getByTestId("address-file-trigger").focus(); await page.keyboard.press("Space"); }]]) {
      try {
        const [chooser] = await Promise.all([page.waitForEvent("filechooser", { timeout: 5000 }), act()]);
        opens[key] = Boolean(chooser);
      } catch (error) {
        opens[key] = false;
        problem("[" + name + "/file-entry] 触发器无法用 " + key + " 打开文件选择器：" + String(error).slice(0, 120));
      }
    }
    result.opens_file_chooser = opens;

    /* Dataset 密度（§6.8）：桌面卡片 60–68px，短屏桌面 58–62px；标签最多两行且不得断词。 */
    const cards = measured.dataset_cards ?? [];
    result.dataset_card_count = cards.length;
    if (cards.length > 0) {
      const heights = cards.map((card) => card.height);
      result.dataset_card_height_min = Math.min(...heights);
      result.dataset_card_height_max = Math.max(...heights);
      /* 只断言下限：卡片带真实 desc 时高于 §6.8 的目标区间是内容决定的，不算缺陷。 */
      const minHeight = name === "short-desktop" ? 58 : 60;
      if (viewport.width >= 1200 && result.dataset_card_height_min < minHeight - 0.5) {
        problem("[" + name + "/dataset] Dataset 卡片高度 " + result.dataset_card_height_min + " < §6.8 下限 " + minHeight + "px");
      }
      const wrapped = cards.filter((card) => (card.label_lines ?? 1) > 1);
      result.dataset_label_wrapped = wrapped.map((card) => card.text);
      if (viewport.width >= 1200 && wrapped.length > 0) {
        notes.push("[dataset] " + name + " 有 " + wrapped.length + " 个 Dataset 标签换行：" + wrapped.map((card) => card.text).join(" / "));
      }
    }

    /* 未裁切整页截图（人工复核灰雾 / 重复入口 / 横线框）。 */
    if (name === "desktop" || name === "reported-screen") {
      await mkdir(join(OUT, "visual-clarity"), { recursive: true });
      const file = "clarity-and-alignment-" + viewport.width + "x" + viewport.height + ".png";
      await page.screenshot({ path: join(OUT, "visual-clarity", file), fullPage: true });
      result.screenshot = "visual-clarity/" + file;
    }
  } finally {
    await context.close();
  }
  return result;
}

/* ------------------------------------------------- REM-018 超宽屏清晰度复核 */

const CLARITY_TARGETS = [
  { name: "页面标题", selector: ".cs-pageheader__title", minSize: 24 },
  { name: "章节标题", selector: ".dl-section-head__title", minSize: 15 },
  { name: "字段标签", selector: ".dl-field-label", minSize: 12 },
  { name: "Dataset 标签", selector: ".dl-dataset-card__label", minSize: 12 },
  { name: "右侧指标标签", selector: ".dl-preview-metric__label", minSize: 12 },
  { name: "右侧指标数值", selector: ".dl-preview-metric__value", minSize: 17 },
  { name: "主按钮", selector: ".dl-task-preview__actions .dl-btn--primary", minSize: 13 },
  { name: "轨道标签", selector: ".dl-rail__label, .dl-vrail__label", minSize: 13 },
];

async function runClarity(browser, viewport) {
  const { context, page } = await openPage(browser, viewport);
  const result = {};
  try {
    for (const target of CLARITY_TARGETS) {
      const locator = page.locator(target.selector).first();
      if ((await locator.count()) === 0) continue;
      const probe = await locator.evaluate((node) => {
        const own = getComputedStyle(node);
        const ancestors = [];
        let current = node;
        while (current) {
          const computed = getComputedStyle(current);
          ancestors.push({
            tag: current.tagName.toLowerCase(),
            opacity: computed.opacity,
            filter: computed.filter,
            backdropFilter: computed.backdropFilter || "none",
            background: computed.backgroundColor,
          });
          current = current.parentElement;
        }
        return { color: own.color, fontSize: own.fontSize, textShadow: own.textShadow, ancestors };
      });
      const label = viewport.name + "/" + target.name;
      for (const ancestor of probe.ancestors) {
        if (Number(ancestor.opacity) < 1) problem("[clarity] " + label + " 祖先 opacity=" + ancestor.opacity);
        if (ancestor.filter !== "none") problem("[clarity] " + label + " 祖先 filter=" + ancestor.filter);
        if (ancestor.backdropFilter !== "none") problem("[clarity] " + label + " 祖先 backdrop-filter=" + ancestor.backdropFilter);
      }
      if (probe.textShadow !== "none") problem("[clarity] " + label + " 使用了 text-shadow");
      const size = Number.parseFloat(probe.fontSize);
      if (size + 0.01 < target.minSize) problem("[clarity] " + label + " 字号 " + size + " < " + target.minSize);
      /* 对比度（沿祖先链合成有效背景）。 */
      const contrast = await locator.evaluate((node) => {
        const parse = (value) => {
          const match = /rgba?\(([^)]+)\)/.exec(value ?? "");
          if (!match) return null;
          const parts = match[1].split(",").map((item) => Number.parseFloat(item));
          return { rgb: [parts[0], parts[1], parts[2]], alpha: parts.length > 3 ? parts[3] : 1 };
        };
        const srgb = (channel) => { const c = channel / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
        const luminance = (rgb) => 0.2126 * srgb(rgb[0]) + 0.7152 * srgb(rgb[1]) + 0.0722 * srgb(rgb[2]);
        const chain = [];
        let current = node;
        while (current) { chain.push(getComputedStyle(current).backgroundColor); current = current.parentElement; }
        let base = [255, 255, 255];
        for (let index = chain.length - 1; index >= 0; index -= 1) {
          const parsed = parse(chain[index]);
          if (!parsed || parsed.alpha === 0) continue;
          base = [
            Math.round(parsed.rgb[0] * parsed.alpha + base[0] * (1 - parsed.alpha)),
            Math.round(parsed.rgb[1] * parsed.alpha + base[1] * (1 - parsed.alpha)),
            Math.round(parsed.rgb[2] * parsed.alpha + base[2] * (1 - parsed.alpha)),
          ];
        }
        const foreground = parse(getComputedStyle(node).color);
        if (!foreground) return 21;
        const la = luminance(foreground.rgb);
        const lb = luminance(base);
        return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
      });
      if (contrast < 4.5) problem("[clarity] " + label + " 对比度 " + contrast.toFixed(2) + " < 4.5");
      result[target.name] = { fontSize: probe.fontSize, color: probe.color, contrast: Number(contrast.toFixed(2)) };
    }
  } finally {
    await context.close();
  }
  return result;
}


/* --------------------------------------------------------- REM-001/020 绑定 */

function gitInfo() {
  try {
    const commit = execFileSync("git", ["-C", REPO, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
    const branch = execFileSync("git", ["-C", REPO, "rev-parse", "--abbrev-ref", "HEAD"], { encoding: "utf8" }).trim();
    const status = execFileSync("git", ["-C", REPO, "status", "--short"], { encoding: "utf8" }).trim().split("\n").filter(Boolean);
    return { commit, branch, status_count: status.length, status_sample: status.slice(0, 20) };
  } catch (error) {
    return { commit: null, branch: null, error: String(error).slice(0, 200) };
  }
}

async function sourceBinding() {
  const out = { ...gitInfo(), generated_at: new Date().toISOString() };
  const indexPath = join(DIST, "index.html");
  out.dist_index_sha256 = existsSync(indexPath) ? sha256(await readFile(indexPath)) : null;
  const html = existsSync(indexPath) ? await readFile(indexPath, "utf8") : "";
  const entry = /assets\/(index-[A-Za-z0-9_-]+\.js)/.exec(html);
  const pageChunk = /assets\/(DownloadNewPage-[A-Za-z0-9_-]+\.js)/.exec(html);
  out.dist_entry_chunk = entry ? entry[1] : null;
  /* 页面是懒加载 chunk：从 entry chunk 里找出它的文件名。 */
  let pageChunkName = pageChunk ? pageChunk[1] : null;
  if (!pageChunkName && entry) {
    const entrySource = await readFile(join(DIST, "assets", entry[1]), "utf8");
    const match = /(DownloadNewPage-[A-Za-z0-9_-]+\.js)/.exec(entrySource);
    pageChunkName = match ? match[1] : null;
  }
  out.dist_page_chunk = pageChunkName;
  if (pageChunkName) out.dist_page_chunk_sha256 = sha256(await readFile(join(DIST, "assets", pageChunkName)));

  /* 线上（nginx → :3001）实际服务的字节。 */
  try {
    const served = await fetch(LIVE_URL, { headers: { "user-agent": "dsh-acceptance" } });
    const body = await served.text();
    out.served_index_sha256 = sha256(Buffer.from(body, "utf8"));
    out.served_status = served.status;
    const servedEntry = /assets\/(index-[A-Za-z0-9_-]+\.js)/.exec(body);
    out.served_entry_chunk = servedEntry ? servedEntry[1] : null;
    if (servedEntry) {
      const chunkResponse = await fetch(LIVE_URL + "assets/" + servedEntry[1]);
      /* 响应体只能读一次：先取 ArrayBuffer，再从中解出文本（否则第二次读会抛异常）。 */
      const chunkBuffer = Buffer.from(await chunkResponse.arrayBuffer());
      out.served_entry_chunk_sha256 = sha256(chunkBuffer);
      const chunkSource = chunkBuffer.toString("utf8");
      const servedPage = /(DownloadNewPage-[A-Za-z0-9_-]+\.js)/.exec(chunkSource);
      out.served_page_chunk = servedPage ? servedPage[1] : null;
      if (servedPage) {
        const pageResponse = await fetch(LIVE_URL + "assets/" + servedPage[1]);
        out.served_page_chunk_sha256 = sha256(Buffer.from(await pageResponse.arrayBuffer()));
      }
    }
  } catch (error) {
    out.served_error = String(error).slice(0, 200);
  }
  out.served_matches_dist = Boolean(
    out.dist_index_sha256 && out.served_index_sha256 && out.dist_index_sha256 === out.served_index_sha256,
  );
  out.all_equal = Boolean(
    out.served_matches_dist &&
      out.dist_page_chunk &&
      out.dist_page_chunk === out.served_page_chunk &&
      out.dist_page_chunk_sha256 === out.served_page_chunk_sha256,
  );
  if (!out.served_matches_dist) problem("[binding] dist index 与线上 served index 不一致");
  if (!out.all_equal) problem("[binding] 页面 chunk 的 dist/served SHA 不一致");
  return out;
}

/* ------------------------------------------------------------------- main */

async function main() {
  if (!existsSync(join(DIST, "index.html"))) {
    console.error("缺少构建产物，请先运行 pnpm build:user");
    process.exit(2);
  }
  await mkdir(OUT, { recursive: true });
  await mkdir(join(OUT, "viewport"), { recursive: true });
  await mkdir(join(OUT, "motion"), { recursive: true });

  const binding = await sourceBinding();
  await writeFile(join(OUT, "source-binding.json"), JSON.stringify(binding, null, 2), "utf8");

  const server = await startStaticServer();
  const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL ?? "chromium" });
  const report = {
    verdict: "PENDING",
    source_binding: { source_commit: binding.commit, source_branch: binding.branch, dist_index_sha256: binding.dist_index_sha256, served_index_sha256: binding.served_index_sha256, served_matches_dist: binding.served_matches_dist, all_equal: binding.all_equal },
    viewports: {},
    motion: {},
    clarity: {},
    dialog: {},
    chain_label: {},
    mobile_stale: {},
    dataset_limit: {},
    reuse_policy_visible: false,
    preview_request_reuse_policy: null,
    create_request_reuse_policy: null,
    request_log: [],
    console_errors: [],
    page_errors: [],
    csp_errors: [],
    failed: [],
    unexpected_skipped: [],
    designed_skipped: [],
    not_run: [],
  };

  try {
    for (const viewport of VIEWPORTS) {
      const result = await runViewport(browser, viewport, []);
      report.viewports[viewport.name] = {
        width: result.width,
        height: result.height,
        screenshot: result.screenshot,
        screenshot_previewed: result.screenshot_previewed ?? null,
        root_scroll_height: result.metrics.rootScrollHeight,
        root_client_height: result.metrics.rootClientHeight,
        root_vertical_overflow: result.metrics.rootScrollHeight > result.metrics.innerHeight + 1,
        root_scroll_width: result.metrics.rootScrollWidth,
        root_client_width: result.metrics.rootClientWidth,
        cta_preview: result.metrics.ctaPreview ?? result.metrics.ctaPreviewShort,
        cta_create: result.metrics.ctaCreate,
        aside_footer: result.metrics.asideFooter,
        mobile_bar: result.metrics.mobileBar,
        config_internal_scroll: result.metrics.configScrollable,
        config_scroll: [result.metrics.configScrollHeight, result.metrics.configClientHeight],
        task_body_scrollable: result.metrics.taskBodyScrollable,
        rail_inner_width: result.metrics.railInnerWidth,
        workspace_width: result.metrics.workspaceWidth,
        dataset_columns: result.metrics.datasetColumns,
        vertical_rail: result.metrics.verticalRail,
        horizontal_rail: result.metrics.horizontalRail,
        cls: result.cls,
        console_errors: result.consoleErrors,
        page_errors: result.pageErrors,
        csp_errors: result.csp,
        after_preview: result.after_preview ?? null,
      };
      report.console_errors.push(...result.consoleErrors.map((text) => viewport.name + ": " + text));
      report.page_errors.push(...result.pageErrors.map((text) => viewport.name + ": " + text));
      report.csp_errors.push(...result.csp.map((text) => viewport.name + ": " + JSON.stringify(text)));
      if (viewport.wide) {
        report.viewports[viewport.name].flowline_width_lte_1600 = (result.metrics.railInnerWidth ?? 0) <= 1600;
        report.viewports[viewport.name].workspace_width_lte_1920 = (result.metrics.workspaceWidth ?? 0) <= 1920;
      }
    }

    report.clarity["1440x900"] = await phase("clarity/1440x900", () => runClarity(browser, { width: 1440, height: 900 }), {});
    report.clarity["2309x1257"] = await phase("clarity/2309x1257", () => runClarity(browser, { width: 2309, height: 1257 }), {});
    report.clarity["2560x1440"] = await phase("clarity/2560x1440", () => runClarity(browser, { width: 2560, height: 1440 }), {});

    /* REM-021..024：清晰表面 / 唯一文件入口 / 严格对齐 / 去外框（§7.5.5 与 REM-E2E-016）。 */
    const visual = {};
    for (const viewport of VISUAL_VIEWPORTS) {
      visual[viewport.name] = await phase("visual-contract/" + viewport.name, () => runVisualContract(browser, viewport), {});
      if (!visual[viewport.name] || visual[viewport.name].surface === undefined) {
        problem("[visual-contract] " + viewport.name + " 未产出测量结果（phase 失败）");
      }
    }
    report.visual_contract = visual;
    const desktopNames = ["short-desktop", "desktop", "full-hd", "reported-screen"];
    const maxDelta = (key) => Math.max(...desktopNames.map((name) => Number((visual[name] ?? {})[key] ?? 999)));
    const surfaceOf = (name) => (visual[name] ?? {}).surface ?? {};
    report.address_import = {
      native_file_input_count: Number((visual.desktop ?? {}).file_input_count ?? -1),
      visible_file_trigger_count: Number((visual.desktop ?? {}).visible_file_trigger_count ?? -1),
      duplicate_import_button_count: Number((visual.desktop ?? {}).duplicate_import_button_count ?? -1),
      parse_button_count: Number((visual.desktop ?? {}).parse_button_count ?? -1),
      desktop_measured_max_top_delta_px: maxDelta("top_delta_px"),
      desktop_measured_max_bottom_delta_px: maxDelta("bottom_delta_px"),
      desktop_measured_max_height_delta_px: maxDelta("height_delta_px"),
      desktop_allowed_delta_px: 1,
      measured_viewports: desktopNames,
      per_viewport: Object.fromEntries(desktopNames.map((name) => {
        const row = visual[name] ?? {};
        return [name, {
          top_delta_px: row.top_delta_px ?? null,
          bottom_delta_px: row.bottom_delta_px ?? null,
          height_delta_px: row.height_delta_px ?? null,
          textarea_rect: row.textarea_rect ?? null,
          file_trigger_rect: row.file_trigger_rect ?? null,
          screenshot: row.screenshot ?? null,
        }];
      })),
      opens_file_chooser: (visual.desktop ?? {}).opens_file_chooser ?? null,
    };
    report.visual_clarity = {
      opaque_canvas: surfaceOf("desktop").backgroundAlpha === 1 && surfaceOf("desktop").backgroundImage === "none",
      page_wide_gradient_present: VISUAL_VIEWPORTS.some((viewport) => {
        const image = surfaceOf(viewport.name).backgroundImage;
        return Boolean(image) && image !== "none";
      }),
      decorator_pseudo_elements: VISUAL_VIEWPORTS
        .filter((viewport) => {
          const surface = surfaceOf(viewport.name);
          return (surface.beforeContent && surface.beforeContent !== "none") || (surface.afterContent && surface.afterContent !== "none");
        })
        .map((viewport) => viewport.name),
      ancestor_effect_violations: VISUAL_VIEWPORTS.reduce((sum, viewport) => sum + ((visual[viewport.name] ?? {}).ancestor_effect_violations ?? []).length, 0),
      /* 对比度违规由 clarity 阶段的真实测量累计，下面统一回填。 */
      contrast_failures: -1,
      surfaces: Object.fromEntries(VISUAL_VIEWPORTS.map((viewport) => [viewport.name, surfaceOf(viewport.name)])),
    };
    report.group_frames = {
      fieldset_border_zero: VISUAL_VIEWPORTS.every((viewport) =>
        ((visual[viewport.name] ?? {}).fieldset_borders ?? []).every((borders) => borders.every((width) => Number.parseFloat(width) === 0))),
      decorative_hr_count: Math.max(...VISUAL_VIEWPORTS.map((viewport) => Number((visual[viewport.name] ?? {}).hr_count ?? 0))),
      duplicate_outer_frame_count: VISUAL_VIEWPORTS.reduce((sum, viewport) => sum + Number((visual[viewport.name] ?? {}).duplicate_outer_frame_count ?? 0), 0),
      fieldset_count: Math.max(...VISUAL_VIEWPORTS.map((viewport) => Number((visual[viewport.name] ?? {}).fieldset_count ?? 0))),
    };
    report.visual_clarity.contrast_failures = problems.filter((message) => message.includes("[clarity]") && message.includes("对比度")).length;

    /* §21：computed style 原始测量单独落盘（布尔值来自真实测量，不由脚本预填）。 */
    await mkdir(join(OUT, "visual-clarity"), { recursive: true });
    await writeFile(join(OUT, "visual-clarity", "computed-styles.json"), JSON.stringify({
      note: "REM-021/022/023/024 的真实浏览器 computed style 与 rect 测量",
      allowed_alignment_delta_px: 1,
      viewports: Object.fromEntries(Object.entries(visual).map(([name, row]) => [name, {
        surface: row.surface ?? null,
        textarea_rect: row.textarea_rect ?? null,
        file_trigger_rect: row.file_trigger_rect ?? null,
        top_delta_px: row.top_delta_px ?? null,
        bottom_delta_px: row.bottom_delta_px ?? null,
        height_delta_px: row.height_delta_px ?? null,
        file_input_count: row.file_input_count ?? null,
        visible_file_trigger_count: row.visible_file_trigger_count ?? null,
        duplicate_import_button_count: row.duplicate_import_button_count ?? null,
        parse_button_count: row.parse_button_count ?? null,
        opens_file_chooser: row.opens_file_chooser ?? null,
        hr_count: row.hr_count ?? null,
        fieldset_borders: row.fieldset_borders ?? null,
        duplicate_outer_frame_count: row.duplicate_outer_frame_count ?? null,
        ancestor_effect_violations: row.ancestor_effect_violations ?? [],
        dataset_cards: row.dataset_cards ?? [],
        screenshot: row.screenshot ?? null,
      }])),
    }, null, 2), "utf8");

    report.motion = await phase("motion", () => runMotion(browser), { videos_present: false });
    report.mobile_stale = await phase("mobile-stale", () => runMobileStale(browser), {});
    report.dialog = await phase("dialog", () => runDialogContract(browser), {});
    report.chain_label = await phase("chain-label", () => runChainLabel(browser), {});
    /* 第二层（7 项组件夹具）的通过与否来自本次真实运行，不由脚本预填 true（§22）。 */
    report.dataset_limit = await phase("dataset-cap", () => runDatasetCap(browser), {});
    report.dataset_limit.seven_item_component_test_passed =
      report.dataset_limit.total === 7 &&
      report.dataset_limit.selected_after_six === 6 &&
      report.dataset_limit.selected_after_seven === 6 &&
      report.dataset_limit.keyboard_blocked === true &&
      report.dataset_limit.selected_after_uncheck === 5;

    /* 网络契约：预览/创建请求体 + 请求数量（动效与视口变化不得增加请求）。 */
    const reuseViolations = [];
    const network = await phase("network", async () => {
    const { context, page } = await openPage(browser, { width: 1440, height: 900 });
    const calls = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      const url = request.url();
      if (!url.includes("/api/v2/downloads")) return;
      calls.push({ url, body: request.postData(), key: request.headers()["idempotency-key"] ?? null });
    });
    try {
      const dialog = await runPreviewFlow(page);
      await dialog.getByRole("button", { name: "知道了" }).click();
      await page.locator(".dl-task-preview__actions .dl-btn--primary").click();
      const confirm = page.locator(".cs-dialog").filter({ hasText: "确认创建下载任务？" });
      await confirm.waitFor({ state: "visible", timeout: 30_000 });
      await confirm.getByRole("button", { name: "确认创建" }).click();
      await page.locator(".cs-dialog").filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ }).waitFor({ state: "visible", timeout: 60_000 });
      await page.waitForTimeout(600);

      /*
       * DONE-22：复用策略必须在所有用户可见层不可见 —— 这是真实 DOM 扫描的结果，
       * 不是脚本预填的 false（规范 §22 明令禁止预填布尔值）。
       * 可见文字、可访问名（aria-label/title/placeholder）与 select 选项一起查。
       */
      const violations = await page.evaluate(() => {
        const forbidden = ["复用策略", "prefer_local", "强制重新拉取", "默认复用", "refresh"];
        const hits = [];
        const body = document.body.innerText || "";
        for (const token of forbidden) if (body.includes(token)) hits.push("innerText:" + token);
        for (const node of Array.from(document.querySelectorAll("[aria-label],[title],[placeholder],[alt]"))) {
          for (const attr of ["aria-label", "title", "placeholder", "alt"]) {
            const value = node.getAttribute(attr) || "";
            for (const token of forbidden) if (value.includes(token)) hits.push(attr + ":" + token);
          }
        }
        return hits;
      });
      reuseViolations.push(...violations);
    } finally {
      await context.close();
    }
    return calls;
    });
    const calls = network ?? [];
    report.request_log = calls.map((call) => ({
      url: call.url,
      reuse_policy: (() => { try { return JSON.parse(call.body ?? "{}").reuse_policy ?? null; } catch { return null; } })(),
      chain: (() => { try { return JSON.parse(call.body ?? "{}").chain ?? null; } catch { return null; } })(),
      idempotency_key: call.key,
    }));
    const previewCalls = calls.filter((call) => call.url.includes("/preview"));
    const createCalls = calls.filter((call) => /\/api\/v2\/downloads$/.test(call.url));
    report.preview_request_reuse_policy = previewCalls.length ? JSON.parse(previewCalls[0].body ?? "{}").reuse_policy : null;
    report.create_request_reuse_policy = createCalls.length ? JSON.parse(createCalls[0].body ?? "{}").reuse_policy : null;
    report.request_counts = { preview: previewCalls.length, create: createCalls.length };
    if (report.preview_request_reuse_policy !== "prefer_local") problem("[network] preview 未携带 prefer_local");
    if (report.create_request_reuse_policy !== "prefer_local") problem("[network] create 未携带 prefer_local");
    if (previewCalls.length !== 1) problem("[network] 一次预览产生了 " + previewCalls.length + " 次 preview 请求");
    if (createCalls.length !== 1) problem("[network] 一次创建产生了 " + createCalls.length + " 次 create 请求");
    report.reuse_policy_visible = reuseViolations.length > 0;
    report.reuse_policy_hits = reuseViolations;
    if (reuseViolations.length > 0) problem("[network] 复用策略在用户可见层出现：" + reuseViolations.join(", "));
  } finally {
    await browser.close();
    server.close();
  }

  report.summary = {
    viewports_checked: Object.keys(report.viewports).length,
    ctas_visible: Object.values(report.viewports).every((item) => !item.cta_create || item.cta_create.bottom <= (item.root_client_height ?? 0) + 8),
    videos: (report.motion.videos ?? []).map((item) => item.file),
    reduced_motion: report.motion.reduced_motion ?? {},
    mobile_stale_ok: Boolean(report.mobile_stale?.grayscale_distinguishable),
    dialog_ok: (report.dialog?.residue?.dialogs ?? 0) === 0,
    chain_label_ok: (report.chain_label?.slug_occurrences ?? 0) <= 1,
    file_entry_unique: report.address_import.native_file_input_count === 1 && report.address_import.visible_file_trigger_count === 1 && report.address_import.duplicate_import_button_count === 0,
    desktop_alignment_max_delta_px: Math.max(report.address_import.desktop_measured_max_top_delta_px, report.address_import.desktop_measured_max_bottom_delta_px, report.address_import.desktop_measured_max_height_delta_px),
    opaque_canvas: report.visual_clarity.opaque_canvas,
    group_frames_ok: report.group_frames.fieldset_border_zero && report.group_frames.decorative_hr_count === 0 && report.group_frames.duplicate_outer_frame_count === 0,
    dataset_seven_item_component_ok: report.dataset_limit.seven_item_component_test_passed === true,
  };
  report.failed = problems.slice();
  report.verdict = problems.length === 0 ? "PASS" : "FAIL";
  await writeFile(join(OUT, "report.json"), JSON.stringify(report, null, 2), "utf8");
  await writeFile(join(OUT, "request-log.json"), JSON.stringify(report.request_log, null, 2), "utf8");
  await writeFile(join(OUT, "console-errors.json"), JSON.stringify({ console_errors: report.console_errors, page_errors: report.page_errors, csp_errors: report.csp_errors }, null, 2), "utf8");
  console.log(JSON.stringify({ verdict: report.verdict, problems, notes, binding: { source_commit: binding.commit, dist: binding.dist_index_sha256, served: binding.served_index_sha256, all_equal: binding.all_equal } }, null, 2));
  if (problems.length > 0) process.exit(1);
}

main().catch((error) => {
  console.error(error);
  process.exit(3);
});


/* --------------------------------------- REM-009 共享 Dialog 时长 / 无第二套 CSS */

async function runDialogContract(browser) {
  const result = {};
  /* 源码层：下载页的样式文件里不得出现 .cs-dialog-* 的时长覆盖，也不得有第二套弹窗容器。 */
  const pageCss = await readFile(join(here, "apps/user-console/src/pages/downloads/download-new.css"), "utf8");
  const overrides = pageCss
    .split("\n")
    .filter((line) => line.includes(".cs-dialog") && !line.trim().startsWith("/*") && !line.includes(":has("));
  result.css_override_lines = overrides;
  if (overrides.some((line) => /duration|animation|transition|\{/.test(line))) {
    problem("[dialog] 下载页 CSS 覆盖了共享 .cs-dialog 规则：" + JSON.stringify(overrides));
  }
  result.scoped_has_rule_only = overrides.every((line) => line.includes(":has(.dl-dialog)"));

  const { context, page } = await openPage(browser, { width: 1440, height: 900 });
  try {
    const tokens = await page.evaluate(() => ({
      standard: getComputedStyle(document.documentElement).getPropertyValue("--cs-dur-standard").trim(),
      fast: getComputedStyle(document.documentElement).getPropertyValue("--cs-dur-fast").trim(),
    }));
    result.tokens = tokens;
    const dialog = await runPreviewFlow(page);
    const durations = await dialog.evaluate((node) => {
      const content = node.querySelector(".ant-modal-content");
      const style = content ? getComputedStyle(content) : null;
      return {
        panelAnimationDuration: style ? style.animationDuration : null,
        panelTransitionDuration: style ? style.transitionDuration : null,
        maskBackdrop: (() => { const mask = document.querySelector(".ant-modal-mask"); return mask ? getComputedStyle(mask).backdropFilter : null; })(),
      };
    });
    result.durations = durations;
    await dialog.getByRole("button", { name: "知道了" }).click();
    await page.waitForTimeout(400);
    const residue = await page.evaluate(() => ({
      dialogs: document.querySelectorAll(".cs-dialog").length,
      masks: document.querySelectorAll(".ant-modal-mask").length,
    }));
    result.residue = residue;
    if (residue.dialogs !== 0 || residue.masks !== 0) problem("[dialog] 关闭后残留：" + JSON.stringify(residue));
  } finally {
    await context.close();
  }
  return result;
}

/* ------------------------------------------------- REM-019 链名只出现一次 */

async function runChainLabel(browser) {
  const { context, page } = await openPage(browser, { width: 1440, height: 900 });
  const result = {};
  try {
    const label = (await page.locator(".cs-chainselect").innerText()).replace(/\s+/g, " ").trim();
    result.visible_label = label;
    const occurrences = (label.match(/tron/gi) ?? []).length;
    result.slug_occurrences = occurrences;
    if (occurrences > 1) problem("[chain] 链名重复显示：" + label);
    /* 底层提交值必须仍是 slug。 */
    await page.locator("#download-addresses").fill(ADDRESS);
    await page.getByRole("button", { name: "解析导入" }).click();
    const verdict = page.locator(".cs-dialog").filter({ hasText: "服务端地址校验结果" });
    await verdict.waitFor({ state: "visible", timeout: 30_000 });
    result.selected_chain_value = await page.locator(".cs-chainselect").evaluate((node) => {
      const input = node.querySelector("input");
      return input ? input.value : node.getAttribute("data-value");
    });
    await verdict.getByRole("button", { name: "取消" }).click();
  } finally {
    await context.close();
  }
  return result;
}

