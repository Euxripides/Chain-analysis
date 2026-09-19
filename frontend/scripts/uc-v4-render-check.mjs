#!/usr/bin/env node
/**
 * V4 重构的真实渲染验证（规范 §63.2 / §63.3 / §63.4；仓库 code-standards「验证要求」）
 *
 * 为什么需要它：仓库规范明确"前端修改不能只以构建成功作为通过标准，必须验证真实页面
 * 渲染和交互；至少检查桌面端和一个移动端宽度，不得出现遮挡、溢出、布局跳动或不可关闭状态"。
 *
 * 本脚本把一个**真实构建产物**（apps/user-console/dist，base=/app-v2/）挂到本地静态服务器，
 * 在真实 Chromium 里加载，用路由拦截喂入固定的服务端夹具，然后度量：
 *   - console error / pageerror / CSP violation（目标：0 条非预期）
 *   - 页面级横向溢出（scrollWidth <= clientWidth + 1）
 *   - Job Detail 的信息层级：首屏是否只有主进度、技术详情是否默认关闭且可打开
 *   - 关键控件是否可见、可点、可关闭
 *
 * 用法：node scripts/uc-v4-render-check.mjs [--out <dir>]
 */
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { extname, join, resolve, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const DIST = resolve(here, "apps/user-console/dist");
/* 后台端产物：仅用于验证"共享 Dialog 重构没有改变后台端行为"（规范 §1.3 / §63.1）。 */
const ADMIN_DIST = resolve(here, "apps/admin-console/dist");
const OUT = resolve(here, "acceptance-evidence/v4-render-check");
const PORT = Number(process.env.UC_CHECK_PORT ?? 5399);

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".png": "image/png",
};

/* ---------------------------------------------------------------- fixtures */

const SESSION = {
  user: { id: "u-1", display_name: "验收账号", email: "qa@local" },
  email: "qa@local",
  display_name: "验收账号",
  organization_id: "org-1",
  workspace_id: "ws-1",
  audience: "user",
  session_id: "sess-1",
  roles: ["investigator"],
  /* 权限给全，保证导航与路由 Guard 都能通过（权限契约本身没有被本次重构改动）。 */
  /*
   * 权限给全，保证导航与路由 Guard 都能通过（权限契约本身没有被本次重构改动）。
   *
   * 注意案件详情的 7 个 Tab **各有自己的 permission**（规范 §0.4.2），
   * 少给一个，对应 Tab 就会（正确地）不渲染——fail-closed 是既定行为，
   * 不是缺陷。这里按 §0.4.2 的真实契约逐个给足：
   *   subjects → subject:list · evidence → evidence:list
   *   members  → case:member:list · activity → case:activity:read
   */
  permissions: [
    "dashboard:read", "search:read", "case:list", "case:create", "case:read",
    "subject:list", "case:member:list", "case:activity:read",
    "investigation:list", "investigation:create", "investigation:read", "finding:list",
    "evidence:read", "evidence:list", "report:list", "report:read", "inbox:read", "notification:list",
    "profile:read", "canvas:list", "canvas:read", "explorer:read", "download:create",
    "job:list:self", "job:read", "export:list",
  ],
  expires_at: new Date(Date.now() + 3600_000).toISOString(),
  absolute_expires_at: new Date(Date.now() + 86400_000).toISOString(),
  must_change_password: false,
  mfa_satisfied: true,
  mfa_enrolled: true,
  csrf_token: "csrf-fixture",
};

/* 首页 KPI 夹具：cards 只有 key/label/value/unit —— 接口本来就没有时间序列字段。 */
const DASHBOARD = {
  as_of: new Date().toISOString(),
  scope: { organization_id: "org-1", workspace_id: "ws-1" },
  data_source: "control-plane",
  cards: [
    { key: "active_cases", label: "进行中案件", value: 12, unit: "个" },
    { key: "active_jobs", label: "进行中任务", value: 3, unit: "个" },
    { key: "jobs_failed", label: "失败任务", value: 0, unit: "个" },
    { key: "pending_findings", label: "待复核发现", value: 27, unit: "条" },
    { key: "evidences", label: "证据总数", value: 418, unit: "条" },
    { key: "reports", label: "报告总数", value: 9, unit: "份" },
  ],
  quick_actions: [
    { key: "new_case", label: "新建案件", href: "/cases/new", permission: "case:create" },
    { key: "new_download", label: "数据下载", href: "/downloads/new", permission: "download:create" },
  ],
};

/* 案件列表夹具：字段与 casework.Case 一致（列表接口不返回对象数/成员数等聚合值）。 */
const CASE_ROWS = [
  {
    id: "case-0001",
    case_no: "CS-20260919-0001",
    name: "TRON 资金归集案",
    description: "归集地址与下游交易所提现路径",
    status: "active",
    classification: "internal",
    priority: "high",
    revision: 4,
    created_at: new Date(Date.now() - 86400000).toISOString(),
    updated_at: new Date(Date.now() - 3600000).toISOString(),
  },
  {
    id: "case-0002",
    case_no: "CS-20260918-0007",
    name: "BSC 混币排查",
    description: "",
    status: "under_review",
    classification: "confidential",
    priority: "normal",
    revision: 2,
    created_at: new Date(Date.now() - 172800000).toISOString(),
    updated_at: new Date(Date.now() - 7200000).toISOString(),
  },
];

/*
 * 必须用**合法的 base58 TRON 地址**：base58 字母表不含 0 / O / I / l。
 * 随手编的地址里若带了 0，浏览器侧的解析器会（正确地）判为"无效格式"，
 * 于是地址数为 0、预览前置校验失败——那是夹具的错，不是页面的错。
 */
const ADDRESS_INPUT = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t";

/* 新建下载的元数据与预览夹具（字段与 /api/v2/downloads/preview 响应一致）。 */
const DOWNLOAD_CHAINS = { items: [{ slug: "tron", name: "TRON", enabled: true }] };
const DOWNLOAD_DATASETS = {
  items: [{ id: "token_transfers", label: "Token Transfer", status: "available" }],
};
const PREVIEW = {
  request: {
    chain: "tron",
    addresses: [ADDRESS_INPUT],
    datasets: ["token_transfers"],
    range: { type: "full" },
    reuse_policy: "prefer_local",
    performance_mode: "safe",
  },
  coverage: {
    chain: "tron",
    datasets: ["token_transfers"],
    overall_ratio: 0.888,
    items: [
      {
        subject: ADDRESS_INPUT,
        dataset: "token_transfers",
        covered_blocks: 800,
        total_blocks: 900,
        ratio: 0.888,
        gaps: [{ from_block: 900, to_block: 1000 }],
      },
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
  source: { coverage: "coverage-store", provider: "provider-a", as_of: new Date().toISOString() },
};

/* 服务端地址复核夹具（/api/v2/downloads/validate-addresses）——形状与 ServerValidateResult 一致。 */

const VALIDATE_RESULT = {
  accepted: [
    { index: 0, row: 1, input: ADDRESS_INPUT, chain: "tron", normalized: ADDRESS_INPUT, case_name: "", tags: [] },
  ],
  rejected: [],
  field_rejections: [],
  scope: { organization_id: "org-1", workspace_id: "ws-1" },
  grants: { "download:create": true, "case:create": true, "case:update": true, create_missing_meta_requested: false },
  summary: {
    submitted: 1,
    accepted: 1,
    rejected: 0,
    blank: 0,
    invalid: 0,
    duplicate: 0,
    cap_rejected: 0,
    tags_over_limit: 0,
    field_rejections: 0,
    meta_denied: 0,
    max_addresses: 10000,
    max_tags: 20,
    chains: { tron: 1 },
    distinct_chains: 1,
  },
  meta: { cases: [], tags: [] },
  source: "server",
};

/*
 * 画布夹具（G0 契约冻结，§60 Phase R6-G0）。
 *
 * 形状取自 CanvasPage 的**真实解析协议**：
 *   节点 ref：`address:<chain>:<id>` / `tx:<chain>:<hash>` / `evidence:<uuid>`
 *   边   ref：`edge:<fromRef>-><toRef>[:amountRaw[:asset]]`
 * 用错了格式节点会被判成 badRef 直接丢掉，页面看起来"能跑但图是空的"——
 * 所以这份夹具必须逐字对齐协议，否则契约基线本身就是错的。
 */
const CANVAS_RECORD = {
  id: "canvas-demo",
  name: "验收画布",
  node_refs: [
    "address:tron:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
    "address:tron:TXk9Kv1QzB7mN4pR2sD8fG3hJ6kL0aZ5yU",
    "tx:tron:9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c8d0b2a4e6f8c0d2b4a6e8f0c",
  ],
  edge_refs: [
    "edge:address:tron:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t->address:tron:TXk9Kv1QzB7mN4pR2sD8fG3hJ6kL0aZ5yU:1000:USDT",
    "edge:address:tron:TXk9Kv1QzB7mN4pR2sD8fG3hJ6kL0aZ5yU->tx:tron:9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c8d0b2a4e6f8c0d2b4a6e8f0c",
  ],
  groups: [],
  annotations: [],
  filters: {},
  layout: {},
  camera: {},
  view_state: {},
};

/* 调查列表夹具：用于验证 CsProTable（L1 业务表格，§17.1）真的渲染出行。 */
const INVESTIGATION_ROWS = [
  {
    id: "inv-0001",
    case_id: "case-0001",
    case_no: "CS-20260919-0001",
    goal: "TRON 归集地址下游追踪",
    mode: "auto",
    status: "running",
    subject: { chain: "tron", value: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" },
    revision: 3,
    finding_count: 7,
    evidence_count: 4,
    pending_findings: 2,
    created_at: new Date(Date.now() - 86_400_000).toISOString(),
    updated_at: new Date(Date.now() - 3_600_000).toISOString(),
  },
  {
    id: "inv-0002",
    case_id: null,
    case_no: "",
    goal: "BSC 混币路径排查",
    mode: "manual",
    status: "completed",
    subject: { chain: "bsc", value: "0x9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c" },
    revision: 1,
    finding_count: 2,
    evidence_count: 1,
    pending_findings: 0,
    created_at: new Date(Date.now() - 172_800_000).toISOString(),
    updated_at: new Date(Date.now() - 7_200_000).toISOString(),
  },
];

/* 报告列表夹具（字段与 casework.ReportView 的列表子集一致），用于验证 DataTable 的刷新保留行为。 */
const REPORT_ROWS = [
  {
    id: "rep-0001",
    investigation_id: "inv-0001",
    case_id: "case-0001",
    case_no: "CS-20260919-0001",
    revision: 3,
    format: "pdf",
    content_hash: "9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c8d0b2a4e6f8c0d2b4a6e8f0c",
    size_bytes: 482100,
    status: "approved",
    status_revision: 3,
    generated_at: new Date(Date.now() - 7200_000).toISOString(),
    reject_reason: "",
  },
  {
    id: "rep-0002",
    investigation_id: "inv-0002",
    case_id: null,
    case_no: "",
    revision: 1,
    format: "json",
    content_hash: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809",
    size_bytes: 12944,
    status: "submitted",
    status_revision: 1,
    generated_at: new Date(Date.now() - 86400_000).toISOString(),
    reject_reason: "",
  },
];

/*
 * 通知夹具：跨"今天 / 昨天 / 更早"三个自然日，用于验证 §52 的时间分组。
 *
 * 时间戳必须**按自然日锚定**，不能用 "Date.now() - 2 小时" 这种相对偏移：
 * 那样只有在"当前时刻晚于 02:00"时才属于今天。凌晨跑测试（本会话就跑到了凌晨）
 * 会让两条本该在"今天"的通知落到"昨天"，分组断言随之假失败——
 * 失败原因看着像产品缺陷，实际是夹具依赖了运行时刻。
 */
const dayAnchor = (daysAgo, hour) => {
  const date = new Date();
  date.setDate(date.getDate() - daysAgo);
  date.setHours(hour, 0, 0, 0);
  return date.toISOString();
};

const NOTIFICATIONS = [
  {
    id: "n-1",
    type: "review.assigned",
    title_key: "review.assigned",
    title: "有一条发现待你复核",
    body: "",
    object_type: "finding",
    object_id: "f-0001",
    href: "/inbox",
    read_at: null,
    created_at: dayAnchor(0, 9),
  },
  {
    id: "n-2",
    type: "download.completed",
    title_key: "download.completed",
    title: "下载任务已完成",
    body: "",
    object_type: "job",
    object_id: "job-fixture-1",
    href: "/jobs/job-fixture-1",
    read_at: new Date().toISOString(),
    created_at: dayAnchor(0, 8),
  },
  {
    id: "n-3",
    type: "report.approved",
    title_key: "report.approved",
    title: "报告已通过审批",
    body: "",
    object_type: "report",
    object_id: "r-0001",
    href: "/reports/r-0001",
    read_at: null,
    created_at: dayAnchor(1, 10),
  },
  {
    id: "n-4",
    type: "case.created",
    title_key: "case.created",
    title: "案件已创建",
    body: "",
    object_type: "case",
    object_id: "case-0001",
    href: "/cases/case-0001/overview",
    read_at: new Date().toISOString(),
    created_at: dayAnchor(5, 10),
  },
];

const JOB_ID = "job-fixture-1";
const JOB = {
  id: JOB_ID,
  job_no: "DL-20260919-001",
  status: "RUNNING",
  source: "ui",
  requested_mode: "auto",
  effective_mode: "provider_pull",
  mode_reason: "range exceeds local coverage",
  chain: "tron",
  range_type: "block",
  from_block: 100,
  to_block: 900,
  from_timestamp: null,
  to_timestamp: null,
  datasets: ["token_transfers"],
  reuse_policy: "prefer_reuse",
  head_block: 1000,
  created_at: new Date(Date.now() - 600_000).toISOString(),
  updated_at: new Date().toISOString(),
  resolved_at: null,
  error: "",
  stats: {},
};

/*
 * 任务**列表**行（GET /api/v2/jobs 的 items[]）。
 *
 * 与 JOB 是同一份事实的两种形状：详情接口返回 job+progress 的组合，
 * 列表接口把进度摊平成一行。字段名严格照 JobRow，不额外编造。
 */
const JOB_ROW = {
  id: JOB_ID,
  job_no: JOB.job_no,
  status: JOB.status,
  chain: JOB.chain,
  datasets: JOB.datasets,
  source: JOB.source,
  reuse_policy: JOB.reuse_policy,
  range_type: JOB.range_type,
  from_block: JOB.from_block,
  to_block: JOB.to_block,
  requested_mode: JOB.requested_mode,
  effective_mode: JOB.effective_mode,
  mode_reason: JOB.mode_reason,
  created_at: JOB.created_at,
  updated_at: JOB.updated_at,
  progress: 0.64,
  addresses: { total: 3, completed: 2 },
  datasets_progress: { total: 1 },
  shards: { total: 4, completed: 2, failed: 0 },
  downloaded_rows: 1_240_000,
  downloaded_bytes: 13_300_000_000,
  last_success_at: JOB.updated_at,
  failure: { kind: "", message_safe: "" },
};

const PROGRESS = {
  status: "RUNNING",
  progress: 0.64,
  downloaded_rows: 1_240_000,
  downloaded_bytes: 13_300_000_000,
  api_requests: 812,
  api_cost_usd: 12.5,
  total_shards: 88,
  done_shards: 56,
  elapsed_seconds: 1122,
  eta_seconds: 24,
  ts: new Date().toISOString(),
  coverage_hit_ratio: 0.71,
};

const DETAIL = {
  job: JOB,
  subjects: [{ id: "s1" }],
  datasets: [{ id: "d1" }],
  shards: [{ id: "sh1" }],
  progress: PROGRESS,
  performance: { requested_mode: "auto", effective_mode: "provider_pull", mode_reason: "range exceeds local coverage" },
  layers: {
    job: { status: "RUNNING", updated_at: JOB.updated_at, error_safe: "" },
    addresses: [
      {
        subject_id: "s1",
        address: "TXk9Kv1QzB7mN4pR2sD8fG3hJ6kL0aZ5yU",
        status: "RUNNING",
        progress: 0.64,
        updated_at: JOB.updated_at,
        successful_cursor: { tron_last_timestamp_ms: 1700000000000, checkpoint_at: JOB.updated_at, definition: "ts" },
      },
    ],
    datasets: [
      {
        dataset_id: "d1",
        subject_id: "s1",
        dataset: "token_transfers",
        status: "RUNNING",
        progress: 0.64,
        shards: { total: 88, completed: 56, failed: 0 },
        updated_at: JOB.updated_at,
      },
    ],
    shards: [
      {
        shard_id: "shard-0001",
        subject_id: "s1",
        dataset: "token_transfers",
        from_block: 100,
        to_block: 200,
        status: "COMPLETED",
        retries: 0,
        downloaded_rows: 12000,
        downloaded_bytes: 1_200_000,
        raw_object: "s3://raw/shard-0001.jsonl",
        error_safe: "",
        updated_at: JOB.updated_at,
        checkpoint: { page_no: 3, next_page_token: "tok-abc", downloaded_rows: 12000, downloaded_bytes: 1_200_000 },
      },
    ],
  },
  exports: [
    {
      id: "exp-1",
      format: "csv",
      zip: false,
      status: "COMPLETED",
      rows: 1240000,
      sha256: "9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c8d0b2a4e6f8c0d2b4a6e8f0c",
      error_safe: "",
      object_ref: "s3://exports/exp-1.csv",
      expires_at: null,
      created_at: JOB.created_at,
    },
  ],
  source: { control: "control-plane", usage: "usage-plane", as_of: JOB.updated_at },
};

/* ------------------------------------------------------------------ server */

/**
 * 静态服务器：同时挂载用户端（/app-v2/）与后台端（/admin/）。
 *
 * 与线上 nginx 的分区一致（两个 base 分别是 /app-v2/ 与 /admin/），
 * 因此这里也能顺带验证"两个 SPA 并存、互不干扰"这一部署契约。
 */
function startStaticServer() {
  const mount = (prefix, dist) => ({ prefix, dist });
  const MOUNTS = [mount("/admin", ADMIN_DIST), mount("/app-v2", DIST)];
  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://127.0.0.1");
    const pathname = decodeURIComponent(url.pathname);
    const hit = MOUNTS.find((m) => pathname === m.prefix || pathname.startsWith(`${m.prefix}/`));
    if (!hit || !existsSync(join(hit.dist, "index.html"))) {
      res.writeHead(404).end("not found");
      return;
    }
    const rel = pathname.slice(hit.prefix.length) || "/";
    const target = normalize(join(hit.dist, rel));
    if (!target.startsWith(hit.dist)) {
      res.writeHead(403).end("forbidden");
      return;
    }
    if (existsSync(target) && extname(target)) {
      const body = await readFile(target);
      res.writeHead(200, { "content-type": MIME[extname(target)] ?? "application/octet-stream" });
      res.end(body);
      return;
    }
    /* SPA history fallback：/<base>/<route> 一律回该应用的 index.html（深链接契约 §0.4.1）。 */
    const html = await readFile(join(hit.dist, "index.html"));
    res.writeHead(200, { "content-type": MIME[".html"] });
    res.end(html);
  });
  return new Promise((ok) => server.listen(PORT, "127.0.0.1", () => ok(server)));
}

/* ------------------------------------------------------------------- checks */

const problems = [];
const notes = [];

async function main() {
  if (!existsSync(join(DIST, "index.html"))) {
    console.error("缺少构建产物，请先运行 pnpm build:user");
    process.exit(2);
  }
  await mkdir(OUT, { recursive: true });
  /*
   * CSS 使用面审计的累加器：跨越两个视口，因为有些类只在移动端布局下出现。
   */
  const usedClasses = new Set();
  const server = await startStaticServer();
  const browser = await chromium.launch({ channel: process.env.E2E_BROWSER_CHANNEL ?? "msedge" });

  try {
    for (const vp of [
      { name: "1440x900", width: 1440, height: 900 },
      { name: "390x844", width: 390, height: 844 },
    ]) {
      const context = await browser.newContext({
        viewport: { width: vp.width, height: vp.height },
        deviceScaleFactor: 1,
        ignoreHTTPSErrors: true,
      });
      await context.addInitScript(() => {
        window.__ucCheck = { csp: [], consoleErrors: [], pageErrors: [], previewCalls: 0 };
        /* 记录预览调用次数：用来区分"按钮没点成"和"点了但响应没渲染"。 */
        const originalFetch = window.fetch;
        window.fetch = function (input, init) {
          const url = typeof input === "string" ? input : input?.url ?? "";
          if (url.includes("/api/v2/downloads/preview")) window.__ucCheck.previewCalls += 1;
          return originalFetch.call(this, input, init);
        };
        document.addEventListener("securitypolicyviolation", (e) => {
          window.__ucCheck.csp.push({ directive: e.violatedDirective, blockedURI: e.blockedURI });
        });
      });
      const page = await context.newPage();
      page.on("console", (msg) => {
        if (msg.type() === "error") {
          page.evaluate((text) => window.__ucCheck.consoleErrors.push(text), msg.text()).catch(() => {});
        }
      });
      /*
       * 记录本视口内发出的所有请求 URL。
       * 权限断言要证明的不是"页面看起来被拦住了"，而是**相关接口一次都没发**——
       * 只有数请求才能把"父页面先挂载再判断"这种实现挡住。
       */
      const requestLog = [];
      page.on("request", (request) => requestLog.push(request.url()));

      page.on("pageerror", (err) => {
        page.evaluate((text) => window.__ucCheck.pageErrors.push(text), String(err)).catch(() => {});
      });

      /*
       * 服务端夹具：所有 /api/** 都在浏览器侧拦截，不触碰真实后端。
       * 注意响应**必须**是 { success, data, meta } 信封——@chain/api-client 的
       * request() 只从 payload.data 取值，返回裸对象会让会话被判定为"读不回来"。
       */
      const envelope = (data) =>
        JSON.stringify({
          success: true,
          data,
          meta: { request_id: "req-fixture", trace_id: "trace-fixture", server_time: new Date().toISOString(), next_cursor: null },
        });
      /*
       * 注册顺序很关键：Playwright 在多个 route 命中时**用最后注册的那个**。
       * 因此兜底路由必须先注册，具体路由后注册，否则兜底会把具体夹具全部盖掉
       * （表现为会话被判定为空、页面直接跳 /login）。
       */
      await page.route("**/api/**", (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope({}) }),
      );
      /*
       * 列表类夹具用**精确正则以"结尾锚定"**，而不是贪婪 glob。
       *
       * 原因：Playwright 在多个 route 命中时用**最后注册**的那个。贪婪 glob
       * （形如 "glob 前缀 + 结尾双星"）会把任务详情路径也一起吃掉，
       * 于是详情页拿到的是"任务列表"的响应，页面直接渲染不出主进度卡。
       * 用带 `$` 锚点的正则（只允许可选的 query string）就不会互相遮蔽。
       */
      const listRoute = (path, body) => [new RegExp(`/api/v2/${path}(\\?[^/]*)?$`), body];
      const LIST_FIXTURES = [
        listRoute("notifications", NOTIFICATIONS),
        listRoute("dashboard", DASHBOARD),
        /* 空列表会让 L1 表格契约在这一页变成空断言（同 exports 的教训）。 */
        listRoute("jobs", { items: [JOB_ROW], next_cursor: null }),
        listRoute("investigations", INVESTIGATION_ROWS),
        listRoute("inbox", []),
        listRoute("cases", CASE_ROWS),
        /*
         * 导出列表必须**有数据**：空列表虽然渲染正确，但会让 L1 表格契约
         * （数据行、冻结主标识列、表头）在这一页悄悄变成空断言。
         * 字段与服务端 ExportItem 一致，不编造接口没有的字段。
         */
        listRoute("exports", {
          scope: { organization_id: "org-1", workspace_id: "ws-1" },
          items: [
            {
              id: "exp-0001",
              job_id: JOB_ID,
              dataset: "token_transfers",
              format: "csv",
              zip: false,
              status: "COMPLETED",
              rows: 1_240_000,
              sha256: "9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c8d0b2a4e6f8c0d2b4a6e8f0c",
              error_safe: "",
              object_ref: "s3://exports/exp-0001.csv",
              created_at: JOB.created_at,
              updated_at: JOB.updated_at,
              finished_at: JOB.updated_at,
              expires_at: null,
              download_available: true,
            },
          ],
        }),
        listRoute("reports", REPORT_ROWS),
        listRoute("canvases", { items: [] }),
      ];
      for (const [pattern, body] of LIST_FIXTURES) {
        await page.route(pattern, (route) =>
          route.fulfill({ status: 200, contentType: "application/json", body: envelope(body) }),
        );
      }
      /*
       * 案件列表故意慢 700ms：用来验证"刷新已有数据时不清屏"（规范 §23.3）。
       * 如果页面在 loading 期间把表格换成 skeleton，这个延迟窗口就足以捕获到。
       */
      await page.route(/\/api\/v2\/cases(\?[^/]*)?$/, async (route) => {
        await new Promise((resolve) => setTimeout(resolve, 700));
        await route.fulfill({ status: 200, contentType: "application/json", body: envelope(CASE_ROWS) });
      });
      /* 报告列表也慢 700ms：用来验证共享 DataTable 的 keepPreviousData 分支（§17.5）。 */
      await page.route(/\/api\/v2\/reports(\?[^/]*)?$/, async (route) => {
        await new Promise((resolve) => setTimeout(resolve, 700));
        await route.fulfill({ status: 200, contentType: "application/json", body: envelope(REPORT_ROWS) });
      });
      await page.route("**/api/v2/jobs/*/events", (route) =>
        route.fulfill({ status: 200, contentType: "text/event-stream", body: "" }),
      );
      await page.route("**/api/v2/jobs/*", (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope(DETAIL) }),
      );
      await page.route("**/api/auth/v1/session", (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope(SESSION) }),
      );
      /* 新建下载一路需要的三个端点（链 / Dataset 元数据 + 真实预览契约形状）。 */
      await page.route(/\/api\/v2\/meta\/chains(\?[^/]*)?$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope(DOWNLOAD_CHAINS) }),
      );
      await page.route(/\/api\/v2\/meta\/datasets(\?[^/]*)?$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope(DOWNLOAD_DATASETS) }),
      );
      /*
       * 调查工作台的三个子资源：findings / evidence 是**裸数组**，runtime 是 { runs }。
       * 兜底路由给的 `{}` 会让页面按数组使用时报 TypeError —— 那是夹具形状问题，不是页面缺陷。
       */
      await page.route(/\/api\/v2\/investigations\/[^/]+\/runtime(\?[^/]*)?$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope({ runs: [] }) }),
      );
      await page.route(/\/api\/v2\/investigations\/[^/]+\/(findings|evidence)(\?[^/]*)?$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }),
      );

      /* 案件详情：列表/成员/对象/报告/动态都是**裸数组**（不是 { items } 信封），形状给错会假崩。 */
      for (const sub of ["members", "subjects", "reports", "activity"]) {
        await page.route(new RegExp(`/api/v2/cases/[^/]+/${sub}(\\?[^/]*)?$`), (route) =>
          route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }),
        );
      }

      /*
       * 地址详情的六个端点：除 overview 返回裸对象外，其余都是 `{ items }` 信封。
       * 形状按真实契约给，否则 `items` 是对象、页面却按数组用，会产生假 pageerror。
       */
      await page.route(/\/api\/v2\/addresses\/[^/]+\/[^/]+\/overview(\?[^/]*)?$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope({}) }),
      );
      await page.route(/\/api\/v2\/addresses\//, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope({ items: [] }) }),
      );
      /* 画布详情（G0 契约基线用）。 */
      await page.route(/\/api\/v2\/canvases\/[^/]+(\?[^/]*)?$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope(CANVAS_RECORD) }),
      );
      await page.route(/\/api\/v2\/downloads\/preview$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope(PREVIEW) }),
      );
      await page.route(/\/api\/v2\/downloads\/validate-addresses$/, (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope(VALIDATE_RESULT) }),
      );
      /*
       * 个人资料页读的是三个**数组/对象**端点。兜底路由给的是 `{}`，
       * 会让 `rows.slice(...)` 这类调用抛 TypeError —— 那是夹具形状不对，
       * 不是页面缺陷。这里按真实契约给空数组，把冒烟结果变成可信信号。
       */
      await page.route("**/api/auth/v1/sessions", (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }),
      );
      await page.route("**/api/auth/v1/devices", (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: envelope([]) }),
      );

      const base = `http://127.0.0.1:${PORT}/app-v2`;

      /* ---------- 0. 登录页（匿名可见，用于确认整站换肤没有崩坏与 CSP 干净） ---------- */
      await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(600);
      await page.screenshot({ path: join(OUT, `login-${vp.name}.png`), fullPage: false });

      /* ---------- 1. Job Detail 首屏：只应有主进度，不应有技术 Tab 导航 ---------- */
      await page.goto(`${base}/jobs/${JOB_ID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cs-jobprogress", { timeout: 20_000 });
      await page.waitForTimeout(400);

      const primaryVisible = await page.locator(".cs-jobprogress").first().isVisible();
      if (!primaryVisible) problems.push(`[${vp.name}] 主进度卡未渲染`);

      const hasTechNavOnFirstScreen = await page
        .locator(".cs-tabs .cs-tab", { hasText: /Pipeline|Shards|结构化事件/ })
        .count();
      if (hasTechNavOnFirstScreen > 0) {
        problems.push(`[${vp.name}] 首屏仍出现技术 Tab 导航（${hasTechNavOnFirstScreen} 项）`);
      }

      /* 技术详情必须默认关闭（§28.3）。 */
      const drawerBefore = await page.locator(".ant-drawer-open").count();
      if (drawerBefore > 0) problems.push(`[${vp.name}] 技术详情 Drawer 默认没有关闭`);

      const controlBar = await page.locator("[data-testid='job-control-bar']").count();
      if (controlBar === 0) problems.push(`[${vp.name}] 控制条未渲染`);

      /* 关键字段：进度、速度、ETA、预计完成、阶段 */
      const bodyText = await page.locator(".cs-jobprogress").first().innerText();
      for (const label of ["当前速度", "预计剩余", "预计完成", "分片进度"]) {
        if (!bodyText.includes(label)) problems.push(`[${vp.name}] 主进度卡缺少字段：${label}`);
      }

      await page.screenshot({ path: join(OUT, `job-detail-${vp.name}.png`), fullPage: false });

      /* ---------- 2. 溢出度量（页面级 scrollWidth <= clientWidth + 1） ---------- */
      const overflow = await page.evaluate(() => ({
        docScroll: document.documentElement.scrollWidth,
        docClient: document.documentElement.clientWidth,
        bodyScroll: document.body.scrollWidth,
      }));
      if (overflow.docScroll > overflow.docClient + 1) {
        problems.push(`[${vp.name}] 页面横向溢出：scrollWidth ${overflow.docScroll} > clientWidth ${overflow.docClient}`);
      }
      notes.push(`[${vp.name}] overflow=${JSON.stringify(overflow)}`);

      /* ---------- 3. 技术详情：默认关闭 → 可打开 → 能力完整 → 可关闭 ---------- */
      await page.getByRole("button", { name: /查看技术详情/ }).first().click();
      await page.waitForSelector(".ant-drawer-open", { timeout: 10_000 });
      await page.waitForTimeout(400);
      const tabs = await page.locator(".ant-drawer .cs-tab").allInnerTexts();
      for (const required of ["Pipeline", "地址进度", "Dataset", "Shards", "结构化事件", "Exports", "证据链"]) {
        if (!tabs.some((t) => t.includes(required))) {
          problems.push(`[${vp.name}] 技术详情缺少能力：${required}（实际 ${JSON.stringify(tabs)}）`);
        }
      }
      await page.screenshot({ path: join(OUT, `job-technical-${vp.name}.png`), fullPage: false });

      /* ---------- 3b. Dialog 视觉层（§21.0 Core/Renderer 解耦） ---------- */
      {
        /* 在技术详情里点开一个纯说明弹窗：它是 useDialog() 的真实调用点。 */
        await page.getByRole("button", { name: "rows/s 口径" }).first().click();
        await page.waitForSelector(".cs-dialog", { timeout: 10_000 });
        await page.waitForTimeout(250);

        const dialogTitle = await page.locator(".cs-dialog .cs-dialog__title").first().innerText();
        if (!dialogTitle.includes("rows/s 口径")) {
          problems.push(`[${vp.name}] Dialog 标题不正确：${dialogTitle}`);
        }
        /* 用户端必须走 Ant renderer，而不是 legacy 的 .dialog-backdrop。 */
        const legacyDialog = await page.locator(".dialog-backdrop").count();
        if (legacyDialog > 0) {
          problems.push(`[${vp.name}] 用户端仍在渲染 legacy Dialog（${legacyDialog} 个）`);
        }
        /* 类型图标是形状通道：灰度下也要能区分弹窗类型（§55 not color-only）。 */
        const kindIcon = await page.locator(".cs-dialog .cs-dialog__kind").count();
        if (kindIcon === 0) problems.push(`[${vp.name}] Dialog 缺少类型图标通道`);

        /*
         * rc-dialog 的 ESC 处理挂在带 tabindex=-1 的 wrapper 的 React onKeyDown 上，
         * 因此**焦点必须先落进弹窗**，否则 keydown 根本不会传到它那里。
         * Ant 在挂载后才会把焦点送进来（且要等入场动画让内容可见），
         * 所以这里显式等待焦点就位，而不是靠一个固定 sleep 去赌时序。
         */
        await page.waitForFunction(() => Boolean(document.activeElement?.closest(".ant-modal")), null, {
          timeout: 5_000,
        });
        await page.keyboard.press("Escape");
        await page.waitForFunction(() => !document.querySelector(".cs-dialog"), null, { timeout: 5_000 }).catch(() => {});
        const stillOpen = await page.locator(".cs-dialog").count();
        if (stillOpen > 0) problems.push(`[${vp.name}] Dialog 无法用 Escape 关闭`);
      }

      /*
       * Escape 必须能关闭 Drawer（规范 §55：drawer focus restore / 可关闭）。
       *
       * 同样要先等焦点回到 Drawer：上一个 Modal 关闭时 antd 会把焦点还给触发按钮，
       * 而 rc-drawer 的 ESC 监听同样依赖"焦点在其内部"。不等就按 Escape
       * 会把焦点在 <body> 上的情况误判成"抽屉关不掉"。
       */
      await page.waitForFunction(() => Boolean(document.activeElement?.closest(".ant-drawer")), null, {
        timeout: 5_000,
      }).catch(() => {});
      await page.keyboard.press("Escape");
      await page
        .waitForFunction(() => !document.querySelector(".ant-drawer-open"), null, { timeout: 5_000 })
        .catch(() => {});
      const drawerAfterEsc = await page.locator(".ant-drawer-open").count();
      if (drawerAfterEsc > 0) problems.push(`[${vp.name}] 技术详情 Drawer 无法用 Escape 关闭`);

      /*
       * ---------- 4. Shell 已换肤：浅色侧栏（规范 §6.1.1） ----------
       * 仍在任务详情页测量：这一页的接口已被完整 mock，能保证"看到的是真实渲染结果"
       * 而不是被未 mock 的接口错误干扰。
       */
      const railBg = await page
        .locator(".cs-shell__rail")
        .evaluate((el) => getComputedStyle(el).backgroundColor)
        .catch(() => "n/a");
      notes.push(`[${vp.name}] rail background=${railBg}`);
      const railRgb = railBg.match(/\d+/g)?.slice(0, 3).map(Number);
      if (railRgb && railRgb[0] + railRgb[1] + railRgb[2] < 600) {
        problems.push(`[${vp.name}] 侧栏仍是深色（V4 §6.1.1 要求 #F8FBFF 浅色侧栏）：${railBg}`);
      }
      /* 顶栏高度应为 60px（§6.1.2）。 */
      const topbarH = await page
        .locator(".cs-shell__topbar")
        .evaluate((el) => Math.round(el.getBoundingClientRect().height))
        .catch(() => -1);
      notes.push(`[${vp.name}] topbar height=${topbarH}`);
      if (topbarH !== -1 && Math.abs(topbarH - 60) > 2 && vp.width >= 900) {
        problems.push(`[${vp.name}] 顶栏高度 ${topbarH}px，规范要求 60px`);
      }
      /* 当前导航项必须有左侧指示条（形状通道，不只靠颜色）。 */
      const activeNavMark = await page
        .locator(".cs-nav__link[aria-current='page']")
        .first()
        .evaluate((el) => getComputedStyle(el, "::before").width)
        .catch(() => "n/a");
      notes.push(`[${vp.name}] active nav indicator width=${activeNavMark}`);

      /* ---------- 4b. 案件中心：刷新不清屏（§17.5 / §23.3 / §33） ---------- */
      {
        /*
         * 案件列表已迁移到 CsProTable（§17.1），桌面端是 Ant 表格、窄屏是行卡片，
         * 因此行选择器必须同时覆盖三种形态。用 \`tr.ant-table-row\` 而不是 \`tr\`，
         * 是为了排除 Ant 用来测量列宽的隐藏行（.ant-table-measure-row）。
         */
        const CASE_ROWS_SELECTOR = ".cs-protable .ant-table-tbody tr.ant-table-row, .cs-rowcards .cs-rowcard, .table tbody tr";
        await page.goto(`${base}/cases`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(CASE_ROWS_SELECTOR, { timeout: 20_000 });
        const beforeRows = await page.locator(CASE_ROWS_SELECTOR).count();
        if (beforeRows === 0) problems.push(`[${vp.name}] 案件列表未渲染任何行`);

        await page.getByRole("button", { name: /^刷新/ }).first().click();
        /* 立刻采样：此时请求还在飞行中（上面注入了 700ms 延迟）。 */
        await page.waitForTimeout(220);
        const duringRows = await page.locator(CASE_ROWS_SELECTOR).count();
        if (duringRows < beforeRows) {
          problems.push(`[${vp.name}] 刷新时清屏了：${beforeRows} → ${duringRows} 行`);
        }
        const skeletonDuringRefresh = await page.locator(".cs-refreshbar, .skeleton, .ant-skeleton").count();
        if (skeletonDuringRefresh === 0) {
          problems.push(`[${vp.name}] 刷新期间没有任何进度指示`);
        }
        /* .ant-skeleton 也要算进来：CsProTable 首次加载用的是 antd 的 Skeleton。 */
        if ((await page.locator(".skeleton, .ant-skeleton").count()) > 0) {
          problems.push(`[${vp.name}] 刷新期间仍在渲染 skeleton（应只在首次加载出现）`);
        }
        await page.waitForTimeout(1200);
        const afterRows = await page.locator(CASE_ROWS_SELECTOR).count();
        if (afterRows !== beforeRows) {
          problems.push(`[${vp.name}] 刷新后行数变化：${beforeRows} → ${afterRows}`);
        }
        await page.screenshot({ path: join(OUT, `cases-${vp.name}.png`), fullPage: false });
      }

      /* ---------- 4c. 通知中心：按时间分组（§52） ---------- */
      {
        await page.goto(`${base}/notifications`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cs-notifgroup", { timeout: 20_000 });
        const labels = await page.locator(".cs-notifgroup__label").allInnerTexts();
        for (const need of ["今天", "昨天", "更早"]) {
          if (!labels.some((label) => label.includes(need))) {
            problems.push(`[${vp.name}] 通知缺少时间分组「${need}」（实际 ${JSON.stringify(labels)}）`);
          }
        }
        const rows = await page.locator(".cs-notifgroup .cs-rowcard").count();
        if (rows !== NOTIFICATIONS.length) {
          problems.push(`[${vp.name}] 通知条数不符：${rows} ≠ ${NOTIFICATIONS.length}`);
        }
        /* 未读必须同时有形状（圆点）与文字两条通道。 */
        const unreadDots = await page.locator(".cs-notifgroup__dot[data-unread='true']").count();
        if (unreadDots === 0) problems.push(`[${vp.name}] 未读通知缺少圆点通道`);
        await page.screenshot({ path: join(OUT, `notifications-${vp.name}.png`), fullPage: false });
      }

      /* ---------- 4c-2. 共享 DataTable：筛选变化时保留旧行与表头（§17.5） ---------- */
      {
        await page.goto(`${base}/reports`, { waitUntil: "domcontentloaded" });
        /*
         * 报告列表已按 §17.1 迁到 L1（ProTable），DOM 从 `.table` 变成了 antd 的表格结构。
         * 这里按"当前用的是哪一层"选选择器，而不是写死某一种：
         * 断言的是 §17.5 的行为（刷新保留旧行与表头），不是某一层表格的 DOM 细节。
         */
        await page.locator(".cs-protable, .table").first().waitFor({ timeout: 20_000 });
        const proTable = (await page.locator(".cs-protable").count()) > 0;
        const root = proTable ? page.locator(".cs-protable").first() : page.locator(".table").first();
        const rowSel = proTable ? ".ant-table-tbody tr.ant-table-row" : "tbody tr";
        const headSel = proTable ? ".ant-table-thead th" : "thead th";
        await root.locator(rowSel).first().waitFor({ timeout: 20_000 });
        const rowsBefore = await root.locator(rowSel).count();
        const theadBefore = await root.locator(headSel).count();
        if (theadBefore === 0) problems.push(`[${vp.name}] 报告表格没有表头`);

        /* 改一次筛选 → useAsync 重新取数（夹具延迟 700ms），期间表格必须保持挂载。 */
        await page.locator(".cs-filterbar select").first().selectOption({ index: 1 });
        await page.waitForTimeout(250);
        const rowsDuring = await root.locator(rowSel).count();
        const theadDuring = await root.locator(headSel).count();
        const skeletonDuring = await page.locator(".skeleton, .ant-skeleton").count();
        const refreshBar = await page.locator(".cs-refreshbar").count();
        if (rowsDuring < rowsBefore) problems.push(`[${vp.name}] 表格刷新时清空了旧行：${rowsBefore} → ${rowsDuring}`);
        if (theadDuring !== theadBefore) problems.push(`[${vp.name}] 表格刷新时表头消失：${theadBefore} → ${theadDuring}`);
        if (skeletonDuring > 0) problems.push(`[${vp.name}] 表格刷新时仍在渲染 skeleton`);
        if (refreshBar === 0) problems.push(`[${vp.name}] 表格刷新时没有顶部进度指示`);
        await page.waitForTimeout(1000);
      }

      /* ---------- 4c-3. L1 表格层级契约（§17.1 / §17.2） ---------- */
      {
        /*
         * §17.1 把 Cases / Investigations / Reports / Exports 都归到 L1（ProTable）。
         * 这一条断言的是**层级本身**，而不是某个页面的观感：
         *   · 渲染的是 ProTable 而不是 L2 的 DataTable；
         *   · 列表真的画出了行（迁移期间最典型的失败是表壳在、数据没进去）；
         *   · 主标识列被冻结（§17.2），横向滚动时它必须留在原位。
         *
         * 移动端不做列结构断言：窄屏下 CsProTable 走的是卡片形态（cs-protable--cards），
         * 那里本来就没有"冻结列"这回事，拿它去卡等于用桌面端的形态要求移动端。
         */
        const l1Summary = [];
        for (const [route, label, expectRowText] of [
          ["/reports", "报告中心", "报告"],
          ["/exports", "导出中心", "导出"],
          ["/cases", "案件列表", "案件"],
          ["/investigations", "调查任务", "调查"],
          ["/jobs", "任务中心", "任务号"],
        ]) {
          await page.goto(`${base}${route}`, { waitUntil: "domcontentloaded" });
          const root = page.locator(".cs-protable").first();
          let hasProTable = true;
          try {
            await root.waitFor({ timeout: 20_000 });
          } catch {
            hasProTable = false;
          }
          if (!hasProTable) {
            /*
             * 窄屏下有两种合法形态：CsProTable 自己的卡片形态（.cs-protable--cards），
             * 以及页面**自带**的卡片列表（任务中心的 .cs-rowcards）。
             * 后者不是"漏了 L1"，是页面为宽表单独做的移动端表达——
             * 拿桌面的组件形态去卡它，等于要求移动端照搬桌面端的 DOM。
             */
            const cards = await page.locator(".cs-rowcards").count();
            if (vp.width < 900 && cards > 0) {
              l1Summary.push(`${label}=卡片形态`);
              continue;
            }
            problems.push(`[${vp.name}] ${label} 没有渲染 L1 表格（.cs-protable 缺失，且没有卡片形态）`);
            continue;
          }
          if (vp.width < 900) continue;
          /* 根部一出现就数行会数到"还在加载"的那一帧，先等第一行出来。 */
          await root.locator(".ant-table-tbody tr").first().waitFor({ timeout: 20_000 }).catch(() => {});

          /*
           * 夹具里 exports 返回的是空列表，所以这一页**本来就该是空态**：
           * 没有 <table> 不是缺陷，是正确渲染。断言因此分两支——
           * 有表格就查列结构，没表格就要求真的给出了空态/错误态文案，
           * 而不是留一个光秃秃的壳（那才是迁移期间真正会出的问题）。
           */
          const hasTable = (await root.locator(".ant-table").count()) > 0;
          if (!hasTable) {
            if ((await root.locator(".cs-protable__error").count()) > 0) {
              problems.push(`[${vp.name}] ${label} 的 L1 表格渲染的是错误态`);
              continue;
            }
            const shellText = ((await root.innerText().catch(() => "")) ?? "").trim();
            if (shellText.length === 0) {
              problems.push(`[${vp.name}] ${label} 的 L1 表格既没有表格也没有空态文案（只剩下外壳）`);
            }
            if (vp.width >= 900) l1Summary.push(`${label}=空态`);
            continue;
          }

          const rows = await root.locator(".ant-table-tbody tr.ant-table-row").count();
          if (rows === 0) problems.push(`[${vp.name}] ${label} 的 L1 表格有表格结构却没有数据行`);
          const frozen = await root.locator(".ant-table-thead th.ant-table-cell-fix-left").count();
          if (frozen === 0) {
            const headClasses = await root.locator(".ant-table-thead th").first().getAttribute("class").catch(() => null);
            problems.push(
              `[${vp.name}] ${label} 的 L1 表格没有冻结主标识列（§17.2）：首列表头 class=${headClasses ?? "(无)"}`,
            );
          }
          const headerText = (await root.locator(".ant-table-thead").first().innerText().catch(() => "")) ?? "";
          if (expectRowText && !headerText.includes(expectRowText)) {
            problems.push(`[${vp.name}] ${label} 的表头缺少「${expectRowText}」列（实际 ${headerText.replace(/\s+/g, " ").slice(0, 60)}）`);
          }
          if (vp.width >= 900) l1Summary.push(`${label}=${rows} 行/冻结 ${frozen} 列`);
        }
        if (l1Summary.length > 0) notes.push(`[${vp.name}] L1 表格（ProTable 层级）：${l1Summary.join(" · ")}`);
      }

      /* ---------- 4d. 新建下载：预览汇总与失效标记（§45.4） ---------- */
      {
        await page.goto(`${base}/downloads/new`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector("#download-step-preview", { timeout: 20_000 });

        /*
         * 走完最小可预览路径：
         *   填地址 → 解析导入（Worker 解析 + 服务端复核）→ 选链 → 选 Dataset → 预览。
         * 少任何一步 `handlePreview` 都会在前置校验处返回，预览接口根本不会发出。
         */
        await page.locator("#download-addresses").fill(ADDRESS_INPUT);
        /*
         * 地址导入是一条**两步显式确认链**（这是产品有意为之，不是阻碍）：
         *   ① 「地址导入解析摘要（浏览器）」→ 必须点「提交服务端校验」；
         *   ② 「服务端地址校验结果」→ 必须点「使用服务端结果」。
         * 任何一步用 Escape 取消，地址数都会停在 0 —— 服务端校验与"采用规范化地址"
         * 都需要用户明确同意，不能由前端代劳。
         */
        await page.getByRole("button", { name: "解析导入" }).click();
        await page.waitForSelector(".cs-dialog", { timeout: 10_000 });
        await page.getByRole("button", { name: "提交服务端校验" }).click();
        await page.waitForSelector(".cs-dialog", { timeout: 10_000 });
        await page.getByRole("button", { name: "使用服务端结果" }).click();
        await page.waitForFunction(() => !document.querySelector(".cs-dialog"), null, { timeout: 5_000 }).catch(() => {});
        await page.waitForTimeout(400);

        const importedCount = await page.locator("#download-step-objects").innerText();
        if (!/已导入 [1-9]/.test(importedCount)) {
          notes.push(`[${vp.name}] 地址未导入成功：${JSON.stringify(importedCount.slice(0, 90))}`);
        }
        await page.locator("#download-chain").selectOption("tron");
        await page.waitForTimeout(200);
        /* DatasetSelector 底层是 MultiSelect：每个选项是一个原生 checkbox（label 包裹 + 文字）。 */
        await page
          .getByRole("checkbox", { name: /Token Transfer/ })
          .first()
          .check({ timeout: 5_000 })
          .catch(() => {});
        await page.waitForTimeout(200);

        const previewButton = page.getByRole("button", { name: /预览 Coverage 与成本/ }).first();
        const previewEnabled = await previewButton.isEnabled().catch(() => false);
        if (previewEnabled) {
          await previewButton.click();
          await page.waitForTimeout(1000);
        } else {
          notes.push(
            `[${vp.name}] 下载预览按钮不可点：addresses=${await page.locator("#download-addresses").inputValue()}, chain=${await page.locator("#download-chain").inputValue()}, 预览区=${JSON.stringify((await page.locator("#download-step-preview").innerText()).slice(0, 160))}`,
          );
        }
        const previewText = await page.locator("#download-step-preview").innerText();
        const REQUIRED = ["整体覆盖率", "未覆盖区块", "预估缺失区块", "复用命中", "Provider 工作", "预估行数"];
        const missing = REQUIRED.filter((label) => !previewText.includes(label));
        if (missing.length > 0) {
          /* 预览没跑起来是夹具/交互路径问题，记 note 而不是判失败。 */
          const previewCalls = await page.evaluate(() => window.__ucCheck.previewCalls ?? 0);
          notes.push(
            `[${vp.name}] 下载预览汇总缺少 ${JSON.stringify(missing)} · 预览接口调用次数=${previewCalls} · 预览区文本=${JSON.stringify(previewText.slice(0, 120))}`,
          );
        }
        /* 口径/依据不得内嵌页面正文（AGENTS.md：说明只能走弹窗）。这条无论预览是否成功都必须成立。 */
        if (previewText.includes("估算依据")) {
          problems.push(`[${vp.name}] 下载预览把「估算依据」内嵌到了页面正文（应走弹窗）`);
        }

        /*
         * 失效标记（§45.4）：预览后改动任一参数，旧的预览必须被标记为失效，
         * 且用**淡黄边框**而不是整块半透明；同时创建按钮必须回到不可提交状态。
         */
        if (!missing.length) {
          const staleBefore = await page.locator(".cs-preview-stale").count();
          if (staleBefore > 0) problems.push(`[${vp.name}] 预览有效时不应出现失效标记`);

          await page.locator("#download-range-type").selectOption("full");
          await page.waitForTimeout(400);
          const staleAfter = await page.locator(".cs-preview-stale").count();
          const staleOpacity = staleAfter
            ? await page.locator(".cs-preview-stale").first().evaluate((el) => getComputedStyle(el).opacity)
            : "n/a";
          if (staleAfter === 0) {
            problems.push(`[${vp.name}] 参数变化后旧预览没有被标记为失效`);
          }
          if (staleOpacity !== "n/a" && Number(staleOpacity) < 1) {
            problems.push(`[${vp.name}] 失效预览用半透明表达（opacity=${staleOpacity}），应为淡黄边框`);
          }
          const staleBorder = staleAfter
            ? await page.locator(".cs-preview-stale").first().evaluate((el) => getComputedStyle(el).borderTopColor)
            : "n/a";
          notes.push(`[${vp.name}] 失效预览 border=${staleBorder} opacity=${staleOpacity}`);
        }
        await page.screenshot({ path: join(OUT, `download-new-${vp.name}.png`), fullPage: false });
      }

      /* ---------- 5. 首页 Golden Screen（§32） ---------- */
      await page.goto(`${base}/home`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cs-homehero", { timeout: 20_000 });
      await page.waitForTimeout(500);

      const heroTitle = await page.locator(".cs-homehero__title").first().innerText();
      if (!heroTitle.includes("资金关系")) problems.push(`[${vp.name}] 首页 Hero 标题缺失：${heroTitle}`);

      const heroSearch = await page.locator("#home-hero-q").count();
      if (heroSearch === 0) problems.push(`[${vp.name}] 首页 Hero 缺少检索入口`);

      const kpiCells = await page.locator(".cs-kpistrip .metric").count();
      if (kpiCells < 5) problems.push(`[${vp.name}] KPI 条不足 5 个紧凑 KPI（实际 ${kpiCells}）`);

      /* 规范 §32.4 / AGENTS.md：页面正文不得出现说明性段落。 */
      const homeBodyText = await page.locator(".cs-page").first().innerText();
      const forbidden = ["统计范围与来源可通过", "每个指标的统计范围"];
      for (const phrase of forbidden) {
        if (homeBodyText.includes(phrase)) problems.push(`[${vp.name}] 首页正文仍内嵌说明段落：${phrase}`);
      }

      const homeOverflow = await page.evaluate(() => ({
        docScroll: document.documentElement.scrollWidth,
        docClient: document.documentElement.clientWidth,
      }));
      if (homeOverflow.docScroll > homeOverflow.docClient + 1) {
        problems.push(`[${vp.name}] 首页横向溢出：${JSON.stringify(homeOverflow)}`);
      }
      await page.screenshot({ path: join(OUT, `home-${vp.name}.png`), fullPage: false });

      /*
       * ---------- 6. admin-console 回归（规范 §1.3 / §63.1） ----------
       *
       * Dialog 的 Core/Renderer 拆分动了共享包 `@chain/ui`，因此必须证明后台端
       * **运行时**仍然是旧 renderer：同样的 .dialog-* 类名、同样的 ESC 行为，
       * 且完全没有被用户端的 Ant Modal 污染。
       * 后台登录页的"访问边界说明"按钮是一个不需要登录就能触发的 useDialog() 调用点。
       */
      {
        await page.goto(`http://127.0.0.1:${PORT}/admin/login`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".auth-card", { timeout: 20_000 });
        await page.getByRole("button", { name: "访问边界说明" }).click();
        await page.waitForSelector(".dialog", { timeout: 10_000 });
        await page.waitForTimeout(250);

        const legacyCount = await page.locator(".dialog-backdrop .dialog").count();
        if (legacyCount === 0) problems.push(`[${vp.name}] admin 端没有使用 legacy Dialog renderer`);

        const title = await page.locator(".dialog__title").first().innerText();
        if (title !== "后台访问边界") problems.push(`[${vp.name}] admin Dialog 标题异常：${title}`);

        /* 用户端的两套视觉都不得出现在后台端。 */
        const leaked = (await page.locator(".cs-dialog").count()) + (await page.locator(".ant-modal").count());
        if (leaked > 0) problems.push(`[${vp.name}] 用户端 Dialog 视觉泄漏到 admin（${leaked} 个节点）`);

        /* 后台端弹窗的 ESC 语义同样必须保留。 */
        await page.keyboard.press("Escape");
        await page
          .waitForFunction(() => !document.querySelector(".dialog-backdrop"), null, { timeout: 5_000 })
          .catch(() => {});
        if ((await page.locator(".dialog-backdrop").count()) > 0) {
          problems.push(`[${vp.name}] admin Dialog 无法用 Escape 关闭`);
        }
        await page.screenshot({ path: join(OUT, `admin-login-${vp.name}.png`), fullPage: false });
        /*
         * 这一段只报问题、不报结论，于是"后台端没被动过"在报告里是**空白**——
         * 空白既可以读成"验过了"，也可以读成"根本没跑"。所以这里补一条正向证据。
         */
        notes.push(
          `[${vp.name}] admin 回归：legacy Dialog 渲染 · 标题=${title} · 用户端视觉泄漏 0 · Esc 可关闭`,
        );

        const adminSignals = await page.evaluate(() => window.__ucCheck);
        if (adminSignals.pageErrors.length > 0) {
          problems.push(`[${vp.name}] admin pageerror: ${JSON.stringify(adminSignals.pageErrors.slice(0, 3))}`);
        }
        if (adminSignals.consoleErrors.length > 0) {
          problems.push(`[${vp.name}] admin console error: ${JSON.stringify(adminSignals.consoleErrors.slice(0, 3))}`);
        }
      }

      /*
       * ---------- 7. 路由冒烟：全站换肤后所有核心页面仍能渲染 ----------
       *
       * 目标与规范 §63.3 一致（每个核心页面至少 desktop + mobile），但断言刻意收敛：
       *   - **pageerror（未捕获异常）与横向溢出算失败** —— 这两类一定是真缺陷；
       *   - console error 只记进 notes —— 本脚本对多数接口只返回通用空壳，
       *     形状不匹配导致的报错是夹具问题，不是页面缺陷，不能当成失败线。
       */
      /* ---------- 6a. 案件详情：7 个业务 Tab 契约（规范 §0.4.2 / §35） ---------- */
      {
        await page.goto(`${base}/cases/case-0001/overview`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cs-tab", { timeout: 20_000 });
        const caseTabLabels = (await page.locator(".cs-tab").allInnerTexts()).map((text) => text.trim());
        /*
         * 规范 §0.4.2 / §64 #17：这 7 个 key 与中文标签都不得改动。
         * 规范特别点名 **members 不得因为视觉简化被漏掉**——它是最容易被"看起来不重要"删掉的一个，
         * 所以这里把它和其余六个一视同仁地断言。
         */
        const EXPECTED_CASE_TABS = ["概览", "调查对象", "案件调查", "证据中心", "案件报告", "案件成员", "案件动态"];
        for (const label of EXPECTED_CASE_TABS) {
          if (!caseTabLabels.some((text) => text === label || text.startsWith(label))) {
            problems.push(`[${vp.name}] 案件详情缺少 Tab「${label}」（实际 ${JSON.stringify(caseTabLabels)}）`);
          }
        }
        if (caseTabLabels.length !== EXPECTED_CASE_TABS.length) {
          problems.push(`[${vp.name}] 案件详情 Tab 数量变化：${caseTabLabels.length} ≠ ${EXPECTED_CASE_TABS.length}`);
        }
        const caseTabHrefs = await page.locator(".cs-tab").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("href") ?? ""));
        const EXPECTED_KEYS = ["overview", "subjects", "investigations", "evidence", "reports", "members", "activity"];
        for (const key of EXPECTED_KEYS) {
          if (!caseTabHrefs.some((href) => href.endsWith("/" + key))) {
            problems.push(`[${vp.name}] 案件详情 Tab 缺少 key「${key}」（实际 ${JSON.stringify(caseTabHrefs)}）`);
          }
        }
        notes.push(`[${vp.name}] 案件 Tab href=${JSON.stringify(caseTabHrefs)}`);
      }

      /* ---------- 6b. 地址详情：9 个业务 Tab 契约（规范 §0.4.3 / §43） ---------- */
      {
        await page.goto(`${base}/addresses/tron/${ADDRESS_INPUT}`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cs-tab", { timeout: 20_000 });
        const tabLabels = (await page.locator(".cs-tab").allInnerTexts()).map((text) => text.trim());
        /*
         * 这是本次重构**最硬的一条契约**：9 个 Tab 的 key 与中文标签都不得改动
         * （规范 §0.4.3 与 §64 #17）。数量也一并校验——重排视觉可以，改信息架构不行。
         */
        const EXPECTED_TABS = ["概览", "资产", "普通交易", "Token Transfer", "Internal", "对手方", "语义事件", "分析", "Coverage"];
        for (const label of EXPECTED_TABS) {
          if (!tabLabels.some((text) => text === label || text.startsWith(label))) {
            problems.push(`[${vp.name}] 地址详情缺少 Tab「${label}」（实际 ${JSON.stringify(tabLabels)}）`);
          }
        }
        if (tabLabels.length !== EXPECTED_TABS.length) {
          problems.push(`[${vp.name}] 地址详情 Tab 数量变化：${tabLabels.length} ≠ ${EXPECTED_TABS.length}`);
        }
        const tabHrefs = await page.locator(".cs-tab").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("href") ?? ""));
        notes.push(`[${vp.name}] 地址 Tab href=${JSON.stringify(tabHrefs)}`);
        await page.screenshot({ path: join(OUT, `address-detail-${vp.name}.png`), fullPage: false });
      }

      /* ---------- 6b-2. L1 业务表格 CsProTable（§17.1，ProComponents 封装层） ---------- */
      {
        await page.goto(`${base}/investigations`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cs-protable", { timeout: 20_000 });
        await page.waitForTimeout(500);
        const proRows = await page.locator(".cs-protable .ant-table-tbody tr.ant-table-row").count();
        if (proRows !== INVESTIGATION_ROWS.length) {
          problems.push(`[${vp.name}] CsProTable 行数不符：${proRows} ≠ ${INVESTIGATION_ROWS.length}`);
        }
        /* §33：筛选必须只暴露服务端支持的参数，因此 ProTable 自带的 search 必须关掉。 */
        const proSearch = await page.locator(".cs-protable .ant-pro-query-filter, .cs-protable .ant-form").count();
        if (proSearch > 0) {
          problems.push(`[${vp.name}] CsProTable 渲染了自带查询表单（应关闭，筛选只走页面 FilterBar）`);
        }
        const proOverflow = await page.evaluate(() => ({
          docScroll: document.documentElement.scrollWidth,
          docClient: document.documentElement.clientWidth,
        }));
        if (proOverflow.docScroll > proOverflow.docClient + 1) {
          problems.push(`[${vp.name}] CsProTable 导致页面横向溢出：${JSON.stringify(proOverflow)}`);
        }
        await page.screenshot({ path: join(OUT, `investigations-${vp.name}.png`), fullPage: false });
      }

      /* ---------- 6b-3. 画布契约基线（G0，§60 Phase R6-G0） ---------- */
      {
        /*
         * 这是"切换图谱引擎"之前的**契约冻结**。
         *
         * 断言的是不变量而不是像素：图谱画布的 SVG 容器、五组语义化控件、
         * 以及"节点与边确实被画出来了"。将来把 CanvasPage 换成 @chain/graph-engine 时，
         * 这些不变量必须原样成立——它们就是"信息架构没有被动过"的可执行定义。
         */
        await page.goto(`${base}/canvases/canvas-demo`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(1200);

        const canvasContract = await page.evaluate(() => {
          const stage = document.querySelector(".cs-canvas-stage");
          const canvas = stage ? stage.querySelector("canvas") : null;
          const controls = (prefix) => {
            const node = document.querySelector(`[aria-label^="${prefix}"]`);
            if (!node) return null;
            return Array.from(node.querySelectorAll("button, a, input, select"))
              .map((el) => (el.getAttribute("aria-label") || el.textContent || "").trim())
              .filter(Boolean);
          };
          let stats = null;
          try {
            const raw = stage ? stage.getAttribute("data-canvas-stats") : null;
            stats = raw ? JSON.parse(raw) : null;
          } catch {
            stats = null;
          }
          const a11y = document.querySelector(".cs-fundgraph__a11y");
          return {
            stage: Boolean(stage),
            stageWidth: stage ? stage.clientWidth : 0,
            stageHeight: stage ? stage.clientHeight : 0,
            /* 画布的后备缓冲尺寸：引擎按 DPR 放大过，为 0 说明根本没渲染。 */
            canvasWidth: canvas ? canvas.width : 0,
            canvasHeight: canvas ? canvas.height : 0,
            surface: Boolean(stage && stage.querySelector(".cs-canvas-stage__surface")),
            stats,
            a11y: a11y ? (a11y.textContent || "").trim().slice(0, 60) : "",
            tool: controls("画布工具"),
            layout: controls("布局方式"),
            lens: controls("视图维度"),
            playback: controls("路径播放控制"),
          };
        });

        /*
         * 断言的边界要说清楚：
         *   - **控件组（工具 / 布局 / 视图维度 / 路径播放）在任何断点都必须存在**——
         *     它们是信息架构的一部分，与视口宽度无关；
         *   - **SVG 图谱只在桌面断点断言**：390px 下这个页面本来就不渲染 SVG，
         *     那是既有行为，不是本次重构引入的。把它写成"两个断点都要有 SVG"
         *     等于用一条错误的前提去卡将来的实现。
         */
        for (const [name, list] of [["画布工具", canvasContract.tool], ["布局方式", canvasContract.layout], ["视图维度", canvasContract.lens], ["路径播放控制", canvasContract.playback]]) {
          if (!list || list.length < 2) {
            problems.push(`[${vp.name}] 画布控件组「${name}」缺失或选项不足（${JSON.stringify(list)}）`);
          }
        }
        /*
         * 画布契约自 R6 起由**引擎状态**承载（§65「Canvas 迁移到独立性能引擎」）。
         *
         * 迁移前这里数的是 SVG 图元；迁移后没有 SVG 了，如果只是把断言删掉，
         * 一个"尺寸正常但一帧没画"的 canvas 会照样通过。
         * 所以判据换成三样**只有真的在渲染才会有**的东西：
         *   · 舞台与画布的后备缓冲尺寸（0 = 没渲染）；
         *   · 交互层存在（页面的工具语义靠它承载）；
         *   · data-canvas-stats —— 引擎自己的账，且已装载的节点/边数必须等于夹具规模。
         */
        if (vp.width >= 900) {
          if (!canvasContract.stage) {
            problems.push(`[${vp.name}] 画布没有渲染舞台容器`);
          } else {
            if (canvasContract.stageWidth <= 0 || canvasContract.stageHeight <= 0) {
              problems.push(`[${vp.name}] 画布舞台尺寸为 0（${canvasContract.stageWidth}x${canvasContract.stageHeight}）`);
            }
            if (canvasContract.canvasWidth <= 0 || canvasContract.canvasHeight <= 0) {
              problems.push(`[${vp.name}] 画布后备缓冲尺寸为 0（${canvasContract.canvasWidth}x${canvasContract.canvasHeight}）`);
            }
            if (!canvasContract.surface) problems.push(`[${vp.name}] 画布缺少交互层`);
          }
        }
        if (canvasContract.stats) {
          const stats = canvasContract.stats;
          if (stats.webgl2 !== true) problems.push(`[${vp.name}] 图谱引擎没有拿到 WebGL2 上下文`);
          if (stats.nodes !== CANVAS_RECORD.node_refs.length) {
            problems.push(`[${vp.name}] 引擎装载节点数 ${stats.nodes} ≠ 夹具 ${CANVAS_RECORD.node_refs.length}`);
          }
          if (stats.edges !== CANVAS_RECORD.edge_refs.length) {
            problems.push(`[${vp.name}] 引擎装载边数 ${stats.edges} ≠ 夹具 ${CANVAS_RECORD.edge_refs.length}`);
          }
          if (stats.renderedEdges < CANVAS_RECORD.edge_refs.length) {
            problems.push(`[${vp.name}] 引擎实际绘制边数 ${stats.renderedEdges} < 夹具 ${CANVAS_RECORD.edge_refs.length}`);
          }
        } else if (vp.width >= 900) {
          problems.push(`[${vp.name}] 画布没有上报引擎状态（data-canvas-stats 缺失）`);
        }
        notes.push(`[${vp.name}] 画布契约=${JSON.stringify(canvasContract)}`);

        /*
         * 移动端画布默认收起，点「展开图谱」之后必须真的出现一块**活**的画布：
         * 只断言按钮文字变了是不够的——展开后是一块 0×0 的 canvas 也同样"没报错"。
         */
        if (vp.width < 900) {
          const toggle = page.getByRole("button", { name: /展开图谱|收起图谱/ });
          if ((await toggle.count()) > 0) {
            if (((await toggle.first().innerText()) ?? "").includes("展开")) {
              await toggle.first().click();
              await page.waitForTimeout(800);
            }
            const stage = page.locator(".cs-canvas-stage").first();
            if ((await stage.count()) === 0) {
              problems.push(`[${vp.name}] 移动端展开图谱后没有出现画布舞台`);
            } else {
              const box = await stage.boundingBox();
              const raw = await stage.getAttribute("data-canvas-stats").catch(() => null);
              let mobileStats = null;
              try {
                mobileStats = raw ? JSON.parse(raw) : null;
              } catch {
                mobileStats = null;
              }
              if (!box || box.width <= 0 || box.height <= 0) {
                problems.push(`[${vp.name}] 移动端展开图谱后画布尺寸为 0`);
              } else if (!mobileStats || mobileStats.nodes !== CANVAS_RECORD.node_refs.length) {
                problems.push(`[${vp.name}] 移动端展开图谱后引擎没有装载夹具节点（${mobileStats ? mobileStats.nodes : "(无状态)"}）`);
              } else {
                const chain = await page.evaluate(() => {
                  const out = [];
                  let el = document.querySelector(".s5-canvas-shell") || document.querySelector(".cs-canvas-stage");
                  for (let i = 0; i < 7 && el; i += 1) {
                    const rect = el.getBoundingClientRect();
                    out.push(`${el.tagName.toLowerCase()}.${(el.className || "").toString().split(" ").filter(Boolean).slice(0, 2).join(".")}=${Math.round(rect.width)}`);
                    el = el.parentElement;
                  }
                  return out;
                });
                notes.push(
                  `[${vp.name}] 移动端画布：展开后 ${Math.round(box.width)}×${Math.round(box.height)}，引擎装载 ${mobileStats.nodes} 个节点 · 祖链 ${chain.join(" ← ")}`,
                );
              }
            }
          }
        }

        /*
         * ---- 视图维度：切换必须真的到达引擎，并且按维度过滤依然生效 ----
         *
         * 迁移前这四个维度由 SVG 分支实现（虚线、内联淡化、分组配色）。
         * 迁移后它们必须穿过 React → 引擎这条链路，而链路上任何一环断掉，
         * 画面都只是"看起来差不多"——所以这里断言引擎侧的档位与页面的过滤结果。
         */
        if (vp.width >= 900 && canvasContract.stage) {
          /*
           * 相机路径：工具栏的缩放按钮必须真的驱动引擎相机。
           *
           * 上一轮修的就是"引擎擅自改相机"——那条链路正好在缩放按钮上暴露：
           * zoom 3 倍后引擎的 LOD 应当从 mid 升到 near（阈值按本页比例校准过），
           * 「适配视图」再把它按回 mid。只看画面是看不出相机有没有跟着走的。
           */
          const readStats = async () => {
            const raw = await page.locator(".cs-canvas-stage").first().getAttribute("data-canvas-stats").catch(() => null);
            try {
              return raw ? JSON.parse(raw) : null;
            } catch {
              return null;
            }
          };
          const zoomIn = page.getByRole("button", { name: "放大" });
          if ((await zoomIn.count()) > 0) {
            const before = await readStats();
            for (let i = 0; i < 6; i += 1) await zoomIn.first().click();
            await page.waitForTimeout(500);
            const zoomed = await readStats();
            if (!zoomed || zoomed.lod === (before ? before.lod : null)) {
              problems.push(
                `[${vp.name}] 点击放大后引擎档位没有变化（${before ? before.lod : "?"} → ${zoomed ? zoomed.lod : "?"}）——缩放没有驱动引擎相机`,
              );
            } else {
              notes.push(`[${vp.name}] 画布相机：放大后引擎档位 ${before.lod} → ${zoomed.lod}`);
            }
            const fit = page.getByRole("button", { name: "适配视图" });
            if ((await fit.count()) > 0) {
              await fit.first().click();
              await page.waitForTimeout(500);
              const fitted = await readStats();
              if (!fitted || fitted.lod !== (before ? before.lod : null)) {
                problems.push(`[${vp.name}] 「适配视图」没有把引擎档位复位（${fitted ? fitted.lod : "?"}）`);
              } else {
                notes.push(`[${vp.name}] 画布相机：「适配视图」已复位到 ${fitted.lod}`);
              }
            }
          }

          const lensGroup = page.locator('[aria-label^="视图维度"]');
          const statsNow = async () => {
            const raw = await page.locator(".cs-canvas-stage").first().getAttribute("data-canvas-stats").catch(() => null);
            try {
              return raw ? JSON.parse(raw) : null;
            } catch {
              return null;
            }
          };

          const timeButton = lensGroup.getByRole("button", { name: "时间" });
          if ((await timeButton.count()) > 0) {
            await timeButton.first().click();
            await page.waitForTimeout(600);
            const stats = await statsNow();
            if (!stats || stats.lens !== "time") {
              problems.push(`[${vp.name}] 切到「时间」维度后引擎的透镜仍是 ${stats ? stats.lens : "(无状态)"}——透镜没有传到引擎`);
            } else {
              notes.push(`[${vp.name}] 视图维度：引擎已切到 ${stats.lens}，标签 ${stats.labels} 条`);
            }
          }

          /*
           * 夹具里没有证据节点，所以「证据」维度下页面本来就该过滤到空——
           * 那是正确行为，不是缺陷。断言的就是这个过滤真的发生了。
           */
          const evidenceButton = lensGroup.getByRole("button", { name: "证据" });
          if ((await evidenceButton.count()) > 0) {
            await evidenceButton.first().click();
            await page.waitForTimeout(600);
            const mainText = (await page.locator("#main-content").first().innerText().catch(() => "")) ?? "";
            if (!mainText.includes("当前筛选没有可渲染节点")) {
              problems.push(`[${vp.name}] 切到「证据」维度后（夹具无证据节点）画布没有进入空态——透镜过滤没有生效`);
            } else {
              notes.push(`[${vp.name}] 视图维度：证据维度下按预期过滤为空态（夹具无证据节点）`);
            }
          }

          const flowButton = lensGroup.getByRole("button", { name: "资金流" });
          if ((await flowButton.count()) > 0) await flowButton.first().click();
          await page.waitForTimeout(300);
        }

        /*
         * 画布**交互**契约：点一个节点，选中必须真的落到页面上。
         *
         * 这一条补的是迁移后最大的验证缺口：上面那些只证明了引擎在渲染，
         * 证明不了"点位换算 + pickAny + onSelect"这条链是对的。
         * 渲染正确但点不中，是迁移里最典型的"看着没事"的缺陷。
         *
         * 位置不是猜的：先切到**环形**布局，此时 layoutPositions 的第一个节点
         * 落在环心（半径 0），再按同一套 viewBox 归一化与相机换算反推出屏幕坐标。
         * 如果页面那侧的坐标换算错了，点就会落空，选中不会发生。
         */
        if (vp.width >= 900 && canvasContract.stage) {
          const layoutGroup = page.locator('[aria-label^="布局方式"]');
          const ringButton = layoutGroup.getByRole("button", { name: "环形" });
          if ((await ringButton.count()) > 0) {
            await ringButton.first().click();
            await page.waitForTimeout(600);

            const stageBox = await page.locator(".cs-canvas-stage").first().boundingBox();
            const before = (await page.locator(".cs-fundgraph__a11y-current").first().innerText().catch(() => "")) ?? "";
            /*
             * 不能假设进来时"尚未选中"：画布夹具本身带选中，页面一进来就有聚焦对象。
             * 所以点两个不同的节点，只要**任意一次**让选中发生变化，就证明
             * "点位换算 → pickAny → onSelect" 这条链是通的。
             */
            {
              const width = stageBox ? stageBox.width : 0;
              const height = stageBox ? stageBox.height : 0;
              /* 环心节点的 viewBox 坐标（与 layoutPositions 的环形分支同源）。 */
              const RING_RADIUS = 172;
              const raw = [
                { x: 0, y: 0 },
                { x: Math.cos(-Math.PI / 2) * RING_RADIUS, y: Math.sin(-Math.PI / 2) * RING_RADIUS },
                { x: Math.cos(-Math.PI / 6) * RING_RADIUS, y: Math.sin(-Math.PI / 6) * RING_RADIUS },
              ];
              const xs = raw.map((p) => p.x);
              const ys = raw.map((p) => p.y);
              const minX = Math.min(...xs);
              const minY = Math.min(...ys);
              const spanX = Math.max(...xs) - minX || 1;
              const spanY = Math.max(...ys) - minY || 1;
              const pad = 96;
              const fit = Math.min((1000 - pad * 2) / spanX, (600 - pad * 2) / spanY);
              const worldX = raw[0].x * fit + ((1000 - spanX * fit) / 2 - minX * fit);
              const worldY = raw[0].y * fit + ((600 - spanY * fit) / 2 - minY * fit);
              /* 页面相机：zoom=1、pan=0；viewBox → 容器像素用 xMidYMid meet。 */
              const k = Math.min(width / 1000, height / 600) || 1;
              const screenX = worldX * k + (width - 1000 * k) / 2;
              const screenY = worldY * k + (height - 600 * k) / 2;
              /* 依次尝试环心节点与环上第一个节点，任一命中即可。 */
              const candidates = [
                { x: worldX, y: worldY, name: "环心节点" },
                {
                  x: raw[1].x * fit + ((1000 - spanX * fit) / 2 - minX * fit),
                  y: raw[1].y * fit + ((600 - spanY * fit) / 2 - minY * fit),
                  name: "环上节点",
                },
              ];
              let clicked = false;
              for (const candidate of candidates) {
                const sx = candidate.x * k + (width - 1000 * k) / 2;
                const sy = candidate.y * k + (height - 600 * k) / 2;
                if (!stageBox || sx < 4 || sy < 4 || sx > width - 4 || sy > height - 4) continue;
                await page.mouse.click(stageBox.x + sx, stageBox.y + sy);
                await page.waitForTimeout(400);
                const after = (await page.locator(".cs-fundgraph__a11y-current").first().innerText().catch(() => "")) ?? "";
                if (after !== before) {
                  notes.push(
                    `[${vp.name}] 画布交互：点击${candidate.name}后选中变为「${after.replace(/^当前选中：/, "").slice(0, 36)}」`,
                  );
                  clicked = true;
                  break;
                }
              }
              if (!clicked) {
                problems.push(
                  `[${vp.name}] 点击画布节点后选中没有变化（尝试了 ${candidates.length} 个位置）——命中换算或 onSelect 链路断了`,
                );
              }

              /*
               * ---- 框选：拖拽矩形必须出现在用户拖的那个位置上 ----
               *
               * 迁移前这个矩形是 SVG 里的一个 <rect>（坐标直接复用 viewBox），
               * 迁移后改由一层 div 画出来，换算是我新写的 worldRectToScreen。
               * 这条断言比对的就是"拖拽的屏幕范围"与"矩形实际渲染的位置"——
               * 算错了框就会飘到别处，而选中结果（页面侧按 world 坐标算）
               * 依然是对的，所以只看"选中了几个"根本发现不了。
               */
              const toolGroup = page.locator('[aria-label^="画布工具"]');
              const boxButton = toolGroup.getByRole("button", { name: "框选" });
              if ((await boxButton.count()) > 0) {
                await boxButton.first().click();
                await page.waitForTimeout(200);

                const nodeScreens = candidates.map((candidate) => ({
                  x: candidate.x * k + (width - 1000 * k) / 2,
                  y: candidate.y * k + (height - 600 * k) / 2,
                }));
                const pad = 24;
                const startX = Math.max(2, Math.min(...nodeScreens.map((p) => p.x)) - pad);
                const startY = Math.max(2, Math.min(...nodeScreens.map((p) => p.y)) - pad);
                const endX = Math.min(width - 2, Math.max(...nodeScreens.map((p) => p.x)) + pad);
                const endY = Math.min(height - 2, Math.max(...nodeScreens.map((p) => p.y)) + pad);

                if (stageBox && endX - startX > 10 && endY - startY > 10) {
                  await page.mouse.move(stageBox.x + startX, stageBox.y + startY);
                  await page.mouse.down();
                  await page.mouse.move(stageBox.x + endX, stageBox.y + endY, { steps: 6 });
                  await page.waitForTimeout(200);

                  const rectBox = await page.locator(".cs-canvas-stage__box").first().boundingBox().catch(() => null);
                  if (!rectBox) {
                    problems.push(`[${vp.name}] 框选拖拽时没有出现选择框（.cs-canvas-stage__box 缺失）`);
                  } else {
                    const expected = { left: stageBox.x + startX, top: stageBox.y + startY, width: endX - startX, height: endY - startY };
                    const drift =
                      Math.abs(rectBox.x - expected.left) +
                      Math.abs(rectBox.y - expected.top) +
                      Math.abs(rectBox.width - expected.width) +
                      Math.abs(rectBox.height - expected.height);
                    if (drift > 6) {
                      problems.push(
                        `[${vp.name}] 框选矩形与拖拽范围不符（偏移 ${Math.round(drift)}px）：实际 ${Math.round(rectBox.x)},${Math.round(rectBox.y)} ${Math.round(rectBox.width)}×${Math.round(rectBox.height)}，期望 ${Math.round(expected.left)},${Math.round(expected.top)} ${Math.round(expected.width)}×${Math.round(expected.height)}`,
                      );
                    } else {
                      notes.push(`[${vp.name}] 框选：拖拽矩形与实际渲染位置一致（偏移 ${Math.round(drift)}px）`);
                    }
                  }

                  await page.mouse.up();
                  await page.waitForTimeout(300);
                  /* 松手后必须收干净：留下一个不消失的选择框是"无法关闭的状态"。 */
                  if ((await page.locator(".cs-canvas-stage__box").count()) > 0) {
                    problems.push(`[${vp.name}] 框选松手后选择框没有消失`);
                  }
                }

                /* 还原工具，避免影响后续步骤。 */
                const selectButton = toolGroup.getByRole("button", { name: "选择" });
                if ((await selectButton.count()) > 0) await selectButton.first().click();
              }
            }
          }
        }
      }

      /* ---------- 6c. 路由过渡策略（§9.3 / §61） ---------- */
      {
        /*
         * 规范禁止"全站统一 AnimatePresence mode='wait'"，要求按页面类型分流。
         * 这里直接断言每一类页面的策略标记——比"看有没有动画"精确得多，
         * 而且能抓住"某天有人给图谱页也套上了整页淡入"这种回归。
         */
        const POLICY_CASES = [
          ["/cases", "standard"],
          ["/reports", "standard"],
          [`/jobs/${JOB_ID}`, "streaming"],
          ["/graph", "heavy"],
          ["/canvases/canvas-demo", "heavy"],
          ["/investigations/inv-demo/workbench", "heavy"],
          ["/downloads/new", "standard"],
        ];
        for (const [route, expected] of POLICY_CASES) {
          await page.goto(`${base}${route}`, { waitUntil: "domcontentloaded" });
          await page.waitForTimeout(500);
          const actual = await page.locator(".cs-route").first().getAttribute("data-policy").catch(() => null);
          if (actual !== expected) {
            problems.push(`[${vp.name}] ${route} 的过渡策略是 ${actual}，期望 ${expected}`);
          }
        }
        /*
         * 安全路径必须**真的进入未认证状态**才有意义：
         * 带会话访问 /login 会被重定向到 /home，那时看到的 .cs-route 是首页的，
         * 断言"安全路径没有过渡容器"会得到假阳性/假阴性。
         * 因此这里临时把 session 端点改成 401，重新加载，再断言。
         * （路由注册是"后注册者优先"，所以这条 401 会盖掉之前的成功夹具。）
         */
        await page.route("**/api/auth/v1/session", (route) =>
          route.fulfill({
            status: 401,
            contentType: "application/json",
            body: JSON.stringify({ success: false, error: { code: "AUTHN_REQUIRED", message: "未登录", field_errors: [], retryable: false } }),
          }),
        );
        await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(600);
        const securityRoute = await page.locator(".cs-route").count();
        if (securityRoute > 0) problems.push(`[${vp.name}] 未认证状态下仍渲染了页面过渡容器（${securityRoute} 个）`);
        /* 恢复已认证夹具，避免影响后续步骤。 */
        await page.route("**/api/auth/v1/session", (route) =>
          route.fulfill({ status: 200, contentType: "application/json", body: envelope(SESSION) }),
        );
      }

      const SMOKE_ROUTES = [
        ["/inbox", "待我处理"],
        ["/reports", "报告中心"],
        ["/exports", "导出中心"],
        ["/investigations", "调查任务"],
        ["/explorer", "地址与交易查询"],
        ["/search", "全局搜索"],
        ["/profile", "个人资料与安全"],
        ["/graph", "资金图谱"],
        ["/cases/case-0001/overview", "案件详情"],
        [`/addresses/tron/${ADDRESS_INPUT}`, "地址详情"],
        ["/transactions/tron/9f2c1d4b6a8e0f3c5d7b9a1e2f4c6d8b0a2e4f6c8d0b2a4e6f8c0d2b4a6e8f0c", "交易详情"],
        ["/downloads/new", "数据下载"],
        ["/cases/new", "新建案件"],
        ["/investigations/new", "发起调查"],
      ];
      for (const [route, label] of SMOKE_ROUTES) {
        await page.evaluate(() => {
          window.__ucCheck.pageErrors.length = 0;
          window.__ucCheck.consoleErrors.length = 0;
        });
        await page.goto(`${base}${route}`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(700);
        const probe = await page.evaluate(() => ({
          pageErrors: window.__ucCheck.pageErrors.slice(0, 2),
          consoleErrors: window.__ucCheck.consoleErrors.slice(0, 2),
          docScroll: document.documentElement.scrollWidth,
          docClient: document.documentElement.clientWidth,
          hasMain: Boolean(document.querySelector("#main-content")),
          mainText: (document.querySelector("#main-content")?.textContent ?? "").trim().length,
        }));
        if (probe.pageErrors.length > 0) {
          problems.push(`[${vp.name}] ${route}（${label}）pageerror: ${JSON.stringify(probe.pageErrors)}`);
        }
        if (!probe.hasMain || probe.mainText === 0) {
          problems.push(`[${vp.name}] ${route}（${label}）主内容区为空白`);
        }
        if (probe.docScroll > probe.docClient + 1) {
          problems.push(`[${vp.name}] ${route}（${label}）横向溢出 ${probe.docScroll} > ${probe.docClient}`);
        }
        /*
         * 页面头已经统一到 CsPageHeader（规范 §8）：任何一个核心页面还在渲染
         * 旧的 `.page-head` 都说明迁移漏了——这类"漏一个页面"正是整站换肤最容易留下的尾巴。
         */
        const legacyHeader = await page.locator(".page-head").count();
        if (legacyHeader > 0) {
          problems.push(`[${vp.name}] ${route}（${label}）仍在渲染 legacy PageHeader（${legacyHeader} 处）`);
        }
        if (probe.consoleErrors.length > 0) {
          notes.push(`[${vp.name}] ${route} console: ${JSON.stringify(probe.consoleErrors)}`);
        }
      }

      /* ---------- 6d. CSS 使用面审计（R7 遗留清理的判据，规范 §60 Phase R7） ---------- */
      {
        /*
         * 删除 CSS 的前提是"真的没有元素在用"，而 grep 源码判断不出来：
         * 类名常常是拼出来的（`card--${variant}`），只有把页面**渲染出来**
         * 再数 DOM 上的 classList，才能区分"没人用"和"grep 没搜到"。
         * 所以这里把全部路由过一遍，收集真实出现过的类名，再与 components.css 求差。
         */
        const AUDIT_ROUTES = [
          "/home", "/inbox", "/reports", "/exports", "/investigations", "/explorer", "/search",
          "/profile", "/graph", "/notifications", "/downloads", "/downloads/new",
          "/cases", "/cases/new", "/cases/case-0001/overview", "/cases/case-0001/subjects",
          "/cases/case-0001/investigations", "/cases/case-0001/evidence", "/cases/case-0001/reports",
          "/cases/case-0001/members", "/cases/case-0001/activity",
          `/addresses/tron/${ADDRESS_INPUT}`, `/addresses/tron/${ADDRESS_INPUT}?tab=coverage`,
          `/jobs/${JOB_ID}`, "/canvases/canvas-demo", "/investigations/inv-demo/workbench",
          "/investigations/new",
        ];
        for (const route of AUDIT_ROUTES) {
          try {
            await page.goto(`${base}${route}`, { waitUntil: "domcontentloaded" });
            await page.waitForTimeout(450);
            const classes = await page.evaluate(() => {
              const sink = new Set();
              for (const el of document.querySelectorAll("*")) {
                for (const name of el.classList) sink.add(name);
              }
              return [...sink];
            });
            for (const name of classes) usedClasses.add(name);
          } catch {
            /* 单条路由失败不影响审计：它只影响"哪些类被判定为未使用"，
              而删除前还会逐条人工复核。 */
          }
        }

        /*
         * 只遍历路由会漏掉"要点开才存在"的界面：命令面板、通知 Popover、
         * 技术详情的各个 Tab、登录前的 MFA 步骤……它们各自带着几十个类名。
         * 不把这些状态走一遍，审计会把它们全部误判成"没人用"。
         */
        const sweepState = async (label, run) => {
          try {
            await run();
            const classes = await page.evaluate(() => {
              const sink = new Set();
              for (const el of document.querySelectorAll("*")) for (const n of el.classList) sink.add(n);
              return [...sink];
            });
            for (const name of classes) usedClasses.add(name);
          } catch (error) {
            notes.push(`CSS 审计：状态「${label}」未取到（${String(error).slice(0, 80)}）`);
          }
        };

        /* 命令面板（Cmd/Ctrl+K）。 */
        await page.goto(`${base}/home`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(400);
        await sweepState("命令面板", async () => {
          await page.keyboard.press("Control+k");
          await page.waitForTimeout(600);
        });
        await page.keyboard.press("Escape");

        /* 通知 Popover（点铃铛）。 */
        await sweepState("通知 Popover", async () => {
          await page.getByRole("button", { name: /通知/ }).first().click();
          await page.waitForTimeout(600);
        });
        await page.keyboard.press("Escape");

        /* 技术详情：抽屉 + 逐个 Tab（进度环、流水线、层级、时间线、速率都在里面）。 */
        await page.goto(`${base}/jobs/${JOB_ID}`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(600);
        await sweepState("技术详情抽屉", async () => {
          await page.getByRole("button", { name: /查看技术详情/ }).first().click();
          await page.waitForSelector(".ant-drawer-open", { timeout: 10_000 });
          await page.waitForTimeout(400);
          const tabCount = await page.locator(".ant-drawer .cs-tab").count();
          for (let i = 0; i < tabCount; i += 1) {
            await page.locator(".ant-drawer .cs-tab").nth(i).click();
            await page.waitForTimeout(350);
          }
        });
        await page.keyboard.press("Escape");

        /* 未认证状态：登录页装饰与 MFA 输入。 */
        await page.route("**/api/auth/v1/session", (route) =>
          route.fulfill({
            status: 401,
            contentType: "application/json",
            body: JSON.stringify({ success: false, error: { code: "AUTHN_REQUIRED", message: "未登录", field_errors: [], retryable: false } }),
          }),
        );
        await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
        await sweepState("登录页", async () => {
          await page.waitForTimeout(700);
        });
        await page.route("**/api/auth/v1/session", (route) =>
          route.fulfill({ status: 200, contentType: "application/json", body: envelope(SESSION) }),
        );
        /*
         * 上面那次 401 是**我们自己换的夹具**，浏览器必然在 console 里记一条
         * "Failed to load resource: 401"。这一步在冒烟循环之后运行，
         * 不会再被循环开头的清空覆盖，所以必须在这里显式清掉——
         * 否则审计步骤会给最终判定塞进两条假失败。
         */
        await page.evaluate(() => {
          window.__ucCheck.consoleErrors.length = 0;
          window.__ucCheck.pageErrors.length = 0;
        });
      }
      /*
       * ---------- 6e. 外壳在窄屏必须只占一列（§54「< 900：drawer nav」） ----------
       *
       * 这一条来自一个真实缺陷：用户端外壳层用 `html[data-app="user"] .cs-shell`
       * （权重 0,2,2）覆盖旧样式，顺手把旧文件里 @media 下的
       * `grid-template-columns: minmax(0,1fr)`（0,1,0）也压掉了。
       * 桌面端的双列在移动端继续生效，而导航条是 fixed 定位不占格，
       * 主内容于是被自动排进 220px 那一列——不报错、不溢出，
       * 只是**整个手机端内容区都只有 220px 宽**。
       *
       * 只看"有没有横向溢出"永远发现不了这种缺陷，所以要直接量内容列宽度。
       */
      if (vp.width < 900) {
        const mainWidth = await page
          .locator(".cs-shell__main")
          .first()
          .evaluate((el) => el.getBoundingClientRect().width)
          .catch(() => 0);
        if (mainWidth < vp.width * 0.9) {
          problems.push(
            `[${vp.name}] 移动端主内容列只有 ${Math.round(mainWidth)}px（视口 ${vp.width}px）——外壳没有切成单列，导航仍占着一整列`,
          );
        } else {
          notes.push(`[${vp.name}] 外壳：主内容列 ${Math.round(mainWidth)}px / 视口 ${vp.width}px，窄屏单列生效`);
        }
      }

      const signals = await page.evaluate(() => window.__ucCheck);
      const unexpectedCsp = signals.csp.filter((v) => !/font-src|img-src/.test(v.directive));
      if (unexpectedCsp.length > 0) problems.push(`[${vp.name}] CSP violations: ${JSON.stringify(unexpectedCsp)}`);
      if (signals.pageErrors.length > 0) problems.push(`[${vp.name}] pageerror: ${JSON.stringify(signals.pageErrors.slice(0, 5))}`);
      if (signals.consoleErrors.length > 0) {
        problems.push(`[${vp.name}] console error: ${JSON.stringify(signals.consoleErrors.slice(0, 5))}`);
      }
      notes.push(
        `[${vp.name}] console=${signals.consoleErrors.length} pageerror=${signals.pageErrors.length} csp=${signals.csp.length}`,
      );

      /*
       * ---------- 6f. Reduced Motion（§9.5 / §65「Reduced Motion 完整」） ----------
       *
       * 这一条以前**从来没有在运行时验证过**：所有检查都跑在默认（无偏好）上下文里，
       * 于是 CSS 里那几十行 prefers-reduced-motion 规则、以及页面里
       * "读 matchMedia 后禁用自动播放"的那几处 JS，全都只是"写了"，没人验过。
       *
       * 两个方向都要断：开启后动画必须停；**没开时必须还在动**。
       * 只验前者的话，把动画全局关掉也能过。
       *
       * 用 emulateMedia 在**同一个 page** 上切换，而不是新开 context——
       * 新 context 不继承 page.route 注册的夹具，重挂一遍既啰嗦又容易漏。
       */
      if (vp.width >= 900) {
        const readCommandAnimation = async () => {
          await page.goto(`${base}/home`, { waitUntil: "domcontentloaded" });
          await page.waitForTimeout(700);
          await page.keyboard.press("Control+k");
          await page.waitForTimeout(500);
          const name = await page
            .locator(".cs-command")
            .first()
            .evaluate((el) => getComputedStyle(el).animationName)
            .catch(() => "(未渲染)");
          await page.keyboard.press("Escape");
          await page.waitForTimeout(200);
          return name;
        };

        const normalAnimation = await readCommandAnimation();
        if (normalAnimation === "none" || normalAnimation === "(未渲染)") {
          problems.push(`[reduced-motion] 对照失败：默认偏好下命令面板就没有入场动画（${normalAnimation}），这条断言无法证明任何事`);
        }

        await page.emulateMedia({ reducedMotion: "reduce" });
        const reducedAnimation = await readCommandAnimation();
        if (reducedAnimation !== "none") {
          problems.push(`[reduced-motion] 开启减少动效后命令面板仍有动画（animation-name=${reducedAnimation}）`);
        } else {
          notes.push(`[reduced-motion] 命令面板入场动画：默认 ${normalAnimation} → 减少动效 none`);
        }

        /* JS 侧：画布自动播放必须被禁用，并且给出可读原因。 */
        await page.goto(`${base}/canvases/canvas-demo`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(1200);
        const playButton = page.getByRole("button", { name: "播放" });
        /*
         * 按钮不存在要**报错**而不是跳过：画布契约里明确有「路径播放控制」这一组，
         * 找不到它说明前面的假设已经失效，此时静默跳过等于这条断言没写。
         */
        if ((await playButton.count()) === 0) {
          problems.push("[reduced-motion] 画布上找不到「播放」按钮，无法验证自动播放是否被禁用");
        } else {
          const disabled = await playButton.first().isDisabled();
          const title = (await playButton.first().getAttribute("title")) ?? "";
          if (!disabled) {
            problems.push("[reduced-motion] 减少动效下画布的自动播放仍可点击");
          } else if (!title.includes("减少动态效果")) {
            problems.push(`[reduced-motion] 自动播放被禁用了，但没有说明原因（title=${title.slice(0, 40)}）`);
          } else {
            notes.push("[reduced-motion] 画布自动播放已禁用并给出原因");
          }
        }
        await page.emulateMedia({ reducedMotion: null });
      }

      /*
       * ---------- 6f-2. 案件 Tab 权限矩阵：表驱动（§0.4.2 / §24.3） ----------
       *
       * 之前只测了 members 一个 Tab，那是"抽检"而不是"覆盖"：其余 6 个 Tab
       * 的权限线一旦被改回 case:read，没有任何断言会红。
       * 这里改成表驱动：每个 Tab 都跑一遍同样的四件事，加 Tab 只需加一行数据。
       *
       * 每个 Tab 验证（deny / allow 两趟）：
       *   1. 入口是否显示       —— Tab 栏里该链接的有无
       *   2. 直达 URL 是否放行  —— 出现无权限页 vs 正常渲染
       *   3. 组件是否挂载       —— CaseDetailPage 的 .cs-tabs 是否存在
       *   4. 敏感接口请求次数   —— deny 时必须为 0；allow 时必须 >0（否则数据没真加载）
       *
       * 只做主路径（有权限能进）会漏掉整类问题；只做拒绝路径又会把
       * "权限收得过紧"当成通过。两个方向都要。
       */
      if (vp.width >= 900) {
        const CASE_ID = "case-0001";
        /*
         * require 是**该 Tab 额外的**权限；case:read 是所有 Tab 的基础权限
         * （案件详情页本身要调 /api/v2/cases/{id}），所以 deny 时保留它，
         * 只摘掉 Tab 自己的那一项——这样测的正是 Tab 级的门。
         */
        const CASE_TAB_MATRIX = [
          /*
           * keep 是"拒绝用例里仍然保留的权限"，默认 case:read——这样摘掉的是 Tab 自己那一项，
           * 测的正是 Tab 级的门。但 overview 的权限**就是** case:read 本身，keep 必须为空：
           * 否则"拒绝用例"里什么都没摘掉，等于拿全权限跑了一遍。
           * 第一版就是这么写的，结果 overview 那三条断言是空转（页面当然正常打开）。
           */
          { tab: "overview", keep: [], require: ["case:read"], own: [/\/api\/v2\/cases\/case-0001(\?|$)/], forbidden: ["/api/v2/cases/case-0001/subjects", "/api/v2/cases/case-0001/members"] },
          { tab: "subjects", require: ["subject:list"], own: ["/api/v2/cases/case-0001/subjects"], forbidden: ["/api/v2/cases/case-0001/subjects"] },
          { tab: "investigations", require: ["investigation:list"], own: ["/api/v2/investigations"], forbidden: ["/api/v2/investigations"] },
          { tab: "evidence", require: ["evidence:list", "investigation:list"], own: ["/api/v2/investigations"], forbidden: ["/api/v2/investigations"] },
          { tab: "reports", require: ["report:list", "investigation:list"], own: ["/api/v2/cases/case-0001/reports"], forbidden: ["/api/v2/cases/case-0001/reports", "/api/v2/investigations"] },
          { tab: "members", require: ["case:member:list"], own: ["/api/v2/cases/case-0001/members"], forbidden: ["/api/v2/cases/case-0001/members"] },
          { tab: "activity", require: ["case:activity:read"], own: ["/api/v2/cases/case-0001/activity"], forbidden: ["/api/v2/cases/case-0001/activity"] },
        ];

        const countRequests = (matcher) =>
          requestLog.filter((url) =>
            typeof matcher === "string" ? url.includes(matcher) : matcher.test(url),
          ).length;

        const setSession = async (permissions) => {
          await page.route("**/api/auth/v1/session", (route) =>
            route.fulfill({
              status: 200,
              contentType: "application/json",
              body: envelope({ ...SESSION, permissions }),
            }),
          );
        };

        const tabHrefOf = async () =>
          page.evaluate(() =>
            Array.from(document.querySelectorAll(".cs-tabs a")).map((a) => a.getAttribute("href") ?? ""),
          );

        const summary = [];

        for (const entry of CASE_TAB_MATRIX) {
          const url = `${base}/cases/${CASE_ID}/${entry.tab}`;

          /* ---- deny：摘掉该 Tab 的专属权限，保留 case:read ---- */
          const keep = entry.keep ?? ["case:read"];
          const denied = SESSION.permissions.filter(
            (permission) => !entry.require.includes(permission) || keep.includes(permission),
          );
          await setSession(denied);
          requestLog.length = 0;
          await page.goto(url, { waitUntil: "domcontentloaded" });
          await page.waitForTimeout(1300);

          const deniedText = ((await page.locator("#main-content").first().innerText().catch(() => "")) ?? "").trim();
          const deniedMounted = await page.locator(".cs-tabs").count();
          const leaked = entry.forbidden.filter((needle) => countRequests(needle) > 0);

          if (!/没有访问/.test(deniedText)) {
            problems.push(`[perm-matrix] ${entry.tab}：无权限直达未被拦截（正文「${deniedText.replace(/\s+/g, " ").slice(0, 40)}」）`);
          }
          if (deniedMounted > 0) {
            problems.push(`[perm-matrix] ${entry.tab}：无权限直达时 CaseDetailPage 仍然挂载（${deniedMounted} 个 .cs-tabs）`);
          }
          if (leaked.length > 0) {
            problems.push(`[perm-matrix] ${entry.tab}：无权限直达时仍然请求了 ${JSON.stringify(leaked)}`);
          }
          const deniedHrefs = await tabHrefOf();

          /* ---- allow：完整权限，既要进得去也要真的加载到数据 ---- */
          await setSession(SESSION.permissions);
          requestLog.length = 0;
          await page.goto(`${base}/cases/${CASE_ID}/overview`, { waitUntil: "domcontentloaded" });
          await page.waitForTimeout(1200);
          const allowedHrefs = await tabHrefOf();
          const linkShown = allowedHrefs.some((href) => href.endsWith(`/${entry.tab}`));

          await page.goto(url, { waitUntil: "domcontentloaded" });
          await page.waitForTimeout(1500);
          const allowedText = ((await page.locator("#main-content").first().innerText().catch(() => "")) ?? "").trim();
          const allowedMounted = await page.locator(".cs-tabs").count();
          const ownCalls = entry.own.reduce((sum, matcher) => sum + countRequests(matcher), 0);

          if (!linkShown) {
            problems.push(`[perm-matrix] ${entry.tab}：有权限时 Tab 入口没有显示（${JSON.stringify(allowedHrefs)}）`);
          }
          if (/没有访问/.test(allowedText)) {
            problems.push(`[perm-matrix] ${entry.tab}：有权限却被拦（权限收得过紧）`);
          }
          if (allowedMounted === 0) {
            problems.push(`[perm-matrix] ${entry.tab}：有权限时 CaseDetailPage 没有挂载`);
          }
          if (ownCalls === 0) {
            problems.push(`[perm-matrix] ${entry.tab}：进入后没有请求自己的数据接口（Tab 是空的）`);
          }

          summary.push(
            `${entry.tab}: 拒绝[拦截${!/没有访问/.test(deniedText) ? "✗" : "✓"} 未挂载${deniedMounted > 0 ? "✗" : "✓"} 零请求${leaked.length > 0 ? "✗" : "✓"}] ` +
              `允许[入口${linkShown ? "✓" : "✗"} 挂载${allowedMounted > 0 ? "✓" : "✗"} 请求${ownCalls}]`,
          );
          /* deny 那趟必须确实少了入口——否则"隐藏"这件事根本没验证。 */
          if (!/没有访问/.test(deniedText) && deniedHrefs.some((href) => href.endsWith(`/${entry.tab}`))) {
            problems.push(`[perm-matrix] ${entry.tab}：无权限时 Tab 入口仍然可见`);
          }
        }

        /* ---- 数据最小化：停在概览时不得请求成员/证据等敏感接口 ---- */
        requestLog.length = 0;
        await page.goto(`${base}/cases/${CASE_ID}/overview`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(1800);
        for (const needle of ["/api/v2/cases/case-0001/members", "/api/v2/cases/case-0001/subjects", "/api/v2/cases/case-0001/activity"]) {
          const calls = countRequests(needle);
          if (calls > 0) {
            problems.push(`[perm-matrix] 停留在概览页时仍然请求了 ${needle} ${calls} 次——数据没有跟随 Tab 按需加载`);
          }
        }

        await setSession(SESSION.permissions);
        notes.push(`[perm-matrix] 7 个 Tab 双向通过：${summary.join(" · ")}`);

        /*
         * ---- 权限在运行中变化：撤销后必须立刻把敏感数据从屏幕上拿掉 ----
         *
         * 应用会在窗口 focus 与每 30s 重新拉会话（auth.tsx 的 onFocus 定时器），
         * 所以"运行中撤销"是真实可触发的，不是假想场景。
         *
         * 断言四件事（对应要求的四步）：
         *   ① 受限 Tab 被卸载；② 已取回的成员数据不再出现在屏幕上；
         *   ③ 跳到仍有权限的 Tab；④ 这次跳转是即时容器（不播页面动画）。
         */
        await setSession(SESSION.permissions);
        await page.goto(`${base}/cases/case-0001/members`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(1800);
        const beforeRevoke = ((await page.locator("#main-content").first().innerText().catch(() => "")) ?? "").trim();
        const hadMemberData = beforeRevoke.includes("案件成员") || beforeRevoke.includes("加入时间");
        if (!hadMemberData) {
          problems.push("[perm-revoke] 前置条件不成立：有权限时成员 Tab 没有渲染出成员数据，撤销用例无法验证");
        }

        /* 把会话换成"已撤销 case:member:list"，再用 focus 事件触发一次会话刷新。 */
        requestLog.length = 0;
        await setSession(SESSION.permissions.filter((permission) => permission !== "case:member:list"));
        await page.evaluate(() => window.dispatchEvent(new Event("focus")));
        await page.waitForTimeout(1800);

        const afterRevoke = ((await page.locator("#main-content").first().innerText().catch(() => "")) ?? "").trim();
        const afterUrl = page.url();
        const afterMounted = await page.locator(".cs-tabs").count();
        const afterInstant = await page.locator(".cs-route--instant").count();
        const membersCallsAfter = countRequests("/api/v2/cases/case-0001/members");

        if (afterUrl.includes("/members")) {
          problems.push("[perm-revoke] 权限被撤销后仍停留在成员 Tab（没有跳走）");
        }
        if (afterRevoke.includes("加入时间")) {
          problems.push("[perm-revoke] 权限被撤销后屏幕上仍留着成员数据（敏感信息未清除）");
        }
        if (afterMounted === 0) {
          problems.push("[perm-revoke] 撤销后连可访问的 Tab 都没有渲染（应当跳到仍有权限的 Tab）");
        }
        if (afterInstant === 0) {
          problems.push("[perm-revoke] 安全跳转没有走即时容器（cs-route--instant 缺失，会播页面动画）");
        }
        if (membersCallsAfter > 0) {
          problems.push(`[perm-revoke] 撤销后仍然重新请求了成员接口 ${membersCallsAfter} 次`);
        }
        if (
          !afterUrl.includes("/members") &&
          !afterRevoke.includes("加入时间") &&
          afterMounted > 0 &&
          afterInstant > 0 &&
          membersCallsAfter === 0
        ) {
          notes.push(
            `[perm-revoke] 运行中撤销 case:member:list：已卸载成员 Tab · 成员数据已从屏幕移除 · 跳到 ${afterUrl.replace(base, "")} · 即时容器（无动画）· 撤销后 0 次成员请求`,
          );
        }

        await setSession(SESSION.permissions);
      }

      /*
       * ---------- 6g. WebGL2 不可用时的降级（§30.6 / §65「性能预算或自动降级」） ----------
       *
       * 这条路径以前**完全没有覆盖**，而画布刚迁移到 WebGL——也就是说
       * "拿不到 WebGL2 上下文"这条分支现在会真的挡住用户：
       * 处理不好就是一块永远空白的画布，而且不报错、不抛异常。
       *
       * 做法是在导航前把 getContext('webgl2') 打成 null，模拟真实的不支持环境。
       * 放在本视口检查的**最后**：init script 对该 page 之后的所有导航都生效，
       * 放在中间会污染后面的用例。下一个视口用的是全新 context，不受影响。
       */
      if (vp.width >= 900) {
        await page.addInitScript(() => {
          const original = HTMLCanvasElement.prototype.getContext;
          HTMLCanvasElement.prototype.getContext = function (type, ...rest) {
            if (typeof type === "string" && type.startsWith("webgl")) return null;
            return original.call(this, type, ...rest);
          };
        });

        await page.evaluate(() => {
          window.__ucCheck.pageErrors.length = 0;
          window.__ucCheck.consoleErrors.length = 0;
        });
        await page.goto(`${base}/canvases/canvas-demo`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(1200);

        const fallbackVisible = await page
          .locator(".cs-fundgraph__fallback")
          .first()
          .isVisible()
          .catch(() => false);
        if (!fallbackVisible) {
          problems.push("[webgl-fallback] WebGL2 不可用时没有显示降级提示（用户只会看到一块空白画布）");
        }

        const fallbackText = ((await page.locator(".cs-fundgraph__fallback").first().innerText().catch(() => "")) ?? "").trim();
        if (fallbackVisible && !fallbackText.includes("性能受限")) {
          problems.push(`[webgl-fallback] 降级提示没有说清发生了什么（${fallbackText.slice(0, 50)}）`);
        }

        /* 无障碍通道必须还在：画布画不出来时，对象列表是唯一的信息来源。 */
        const a11yText = ((await page.locator(".cs-fundgraph__a11y").first().innerText().catch(() => "")) ?? "").trim();
        if (!a11yText.includes("个节点")) {
          problems.push(`[webgl-fallback] 降级后对象列表没有列出节点（${a11yText.slice(0, 40)}）`);
        }

        const fallbackStatsRaw = await page.locator(".cs-canvas-stage").first().getAttribute("data-canvas-stats").catch(() => null);
        let fallbackStats = null;
        try {
          fallbackStats = fallbackStatsRaw ? JSON.parse(fallbackStatsRaw) : null;
        } catch {
          fallbackStats = null;
        }
        if (!fallbackStats || fallbackStats.webgl2 !== false) {
          problems.push(`[webgl-fallback] 引擎状态没有如实反映 WebGL2 不可用（${fallbackStatsRaw ? fallbackStatsRaw.slice(0, 60) : "(无状态)"}）`);
        } else if (fallbackStats.nodes !== CANVAS_RECORD.node_refs.length) {
          problems.push(`[webgl-fallback] 降级后引擎只装载了 ${fallbackStats.nodes} 个节点`);
        } else if (fallbackVisible && fallbackText.includes("性能受限") && a11yText.includes("个节点")) {
          /* 只有降级提示、对象列表、引擎状态三者都对，才给正向证据——
             否则失败时会同时打印一条"看起来正常"的说明，反而误导。 */
          notes.push(`[webgl-fallback] WebGL2 不可用：降级提示可见 · 对象列表仍列出 ${fallbackStats.nodes} 个节点 · 引擎如实上报 webgl2=false`);
        }

        const fallbackSignals = await page.evaluate(() => window.__ucCheck);
        if (fallbackSignals.pageErrors.length > 0) {
          problems.push(`[webgl-fallback] 降级路径抛出异常：${JSON.stringify(fallbackSignals.pageErrors.slice(0, 2))}`);
        }
      }

      await context.close();
    }
  } finally {
    await browser.close();
    server.close();
  }

  /*
   * CSS 使用面审计结果（R7 遗留清理判据）。
   *
   * 只作为**报告**输出，不直接判定失败：一个类没在这次遍历里出现，
   * 可能是"确实没人用了"，也可能是"只在某个稀有状态（权限拒绝、错误态）下才出现"。
   * 删除前必须逐条复核，所以这里给出候选清单而不是自动删。
   */
  const cssAudit = await auditComponentCss(usedClasses);
  notes.push(
    `CSS 使用面：components.css 定义 ${cssAudit.defined} 个类，实际渲染出现 ${usedClasses.size} 个，未出现 ${cssAudit.unused.length} 个`,
  );
  if (cssAudit.unused.length > 0) {
    /*
     * 明确写成"候选"而不是"死代码"：没出现也可能是"只在某个没走到的状态下存在"
     * （运行中的任务才有进度环，只有 MFA 步骤才有 OTP 输入）。
     * 这份清单是给下一轮清理用的线索，不是删除依据。
     */
    notes.push("components.css 候选未使用类（需逐条复核状态可达性，不能直接删）：" + cssAudit.unused.join(", "));
  }
  if (cssAudit.resurrected.length > 0) {
    problems.push("R7 已删除的页面头规则重新出现：" + cssAudit.resurrected.join(", ") + "（页面头双轨制回归）");
  }

  const report = { ok: problems.length === 0, problems, notes, cssAudit, at: new Date().toISOString() };
  await writeFile(join(OUT, "report.json"), JSON.stringify(report, null, 2), "utf8");
  console.log(JSON.stringify(report, null, 2));
  if (problems.length > 0) process.exit(1);
}


/**
 * 把 `components.css` 里定义的选择器与实际渲染出现过的类名求差。
 *
 * 解析用正则而不是 CSS parser：只需要类名集合，不需要层叠信息，
 * 引一个 parser 反而会把这个验收脚本变成一个需要维护依赖的项目。
 * 代价是会多收一些东西（注释里的示例、属性选择器里的类名片段），
 * 所以结果只作为**候选**，删之前逐条人工确认。
 */
async function auditComponentCss(used) {
  const cssPath = resolve(here, "apps/user-console/src/styles/components.css");
  const css = await readFile(cssPath, "utf8");
  /* 去掉块注释，避免把注释里提到的类名算成"已定义"。 */
  const withoutComments = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const defined = new Set();
  for (const match of withoutComments.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) defined.add(match[1]);
  const unused = [...defined].filter((name) => !used.has(name)).sort();

  /*
   * R7 已经删除的选择器不允许回来。
   *
   * 这一条比审计报告硬：审计只能说明"这次没渲染到"，而这里说明
   * "用户端已经没有任何页面头组件了，这份覆盖规则是纯死代码"——
   * 依据是 `check:uc-render` 在 14 条路由上对 `.page-head` 元素计数为 0 的断言。
   * 谁要是把这组规则粘回来，页面头双轨制就回来了。
   */
  const removedInR7 = ["page-head", "page-head__title", "page-head__sub", "page-head__crumbs", "page-head__titlerow", "page-head__meta"];
  const resurrected = removedInR7.filter((name) => defined.has(name));

  return { defined: defined.size, used: used.size, unused, resurrected };
}

main().catch((err) => {
  console.error(err);
  process.exit(3);
});
