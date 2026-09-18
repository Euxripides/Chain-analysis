import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { authHeaders, dump, login, startCapture, unwrap, type NetRecord } from "./r10-helpers";

/**
 * P1 工作区作用域闸门 —— 真实浏览器、真实独立上下文（P1-20260913）。
 *
 * 背景（真实事故，非假设）：IAM Login 用 org 默认工作区（ORDER BY slug LIMIT 1）签发会话，
 * 完全不读 workspace_memberships。仅属 wsB 的 viewer 因此拿到 wsA 会话，
 * GET /api/v2/exports 返回 wsA 的 5 条导出，读 wsA 导出明细得 200 —— 真实跨工作区泄露。
 *
 * 本文件用**两个互不共享 cookie 的 browser context** 验证修复后的行为：
 *   wsB 身份：列表 0 项、看不到 wsA 目标（列表/明细/票据/UI DOM 四处）；
 *   wsA 身份：正向可见（对照组）；
 *   权限闸（403）与作用域闸（404）在**同一身份/同一 job** 上分别成立；
 *   零成员资格拒绝登录且不落 cookie；多成员资格未选择被拒绝、显式选择生效。
 *
 * 不做任何 mock：全部经真实浏览器会话 + 真实 API（X-Request-Id / X-Trace-Id 原样记录）。
 *
 * v2 修正（v1 轮真实失败，测试侧缺陷）：POST /api/v2/exports 必须携带 Idempotency-Key，
 * v1 漏传导致 400 validation_error 先于作用域判定返回，把 wsB investigator 的 404 断言打成了 400。
 * 产品侧行为本身正确（shell 闸门 B4 带幂等键时真实得到 404）。
 */

const BASE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";
const WS_A = process.env.P1_WS_A ?? "";
const WS_B = process.env.P1_WS_B ?? "";
const WS_NOMEM = process.env.P1_WS_NOMEM ?? "";
const EXPORT_A = process.env.P1_EXPORT_A ?? "";
const JOB_A = process.env.P1_JOB_A ?? "";
const E_WSB = process.env.P1_WSB_EMAIL ?? "";
const P_WSB = process.env.P1_WSB_PW ?? "";
const E_WSA = process.env.P1_WSA_EMAIL ?? "";
const P_WSA = process.env.P1_WSA_PW ?? "";
const E_WSBINV = process.env.P1_WSBINV_EMAIL ?? "";
const P_WSBINV = process.env.P1_WSBINV_PW ?? "";
const E_MULTI = process.env.P1_MULTI_EMAIL ?? "";
const P_MULTI = process.env.P1_MULTI_PW ?? "";
const E_NOMEM = process.env.P1_NOMEM_EMAIL ?? "";
const P_NOMEM = process.env.P1_NOMEM_PW ?? "";

const EXPORTS_PATH = "/app-v2/exports";
const EMPTY_TEXT = "没有符合筛选条件的导出";

function requireEnv() {
  const missing = Object.entries({
    P1_WS_A: WS_A, P1_WS_B: WS_B, P1_EXPORT_A: EXPORT_A, P1_JOB_A: JOB_A,
    P1_WSB_EMAIL: E_WSB, P1_WSB_PW: P_WSB, P1_WSA_EMAIL: E_WSA, P1_WSA_PW: P_WSA,
    P1_WSBINV_EMAIL: E_WSBINV, P1_WSBINV_PW: P_WSBINV,
    P1_MULTI_EMAIL: E_MULTI, P1_MULTI_PW: P_MULTI,
    P1_NOMEM_EMAIL: E_NOMEM, P1_NOMEM_PW: P_NOMEM,
    P1_WS_NOMEM: WS_NOMEM,
  }).filter(([, v]) => !v).map(([k]) => k);
  expect(missing, "缺少 P1 夹具环境变量（runner 必须从 0600 凭据文件注入）").toEqual([]);
}

async function newContext(browser: { newContext: (o?: unknown) => Promise<BrowserContext> }, viewport: { width: number; height: number }) {
  return await browser.newContext({ ignoreHTTPSErrors: true, viewport });
}

async function sessionScope(page: Page): Promise<{ ws: string; sessionId: string; permissions: string[] }> {
  const r = await page.request.get(BASE + "/api/auth/v1/session", { headers: { "X-Auth-Audience": "user" } });
  expect(r.status(), "/api/auth/v1/session 必须 200").toBe(200);
  const d = unwrap(await r.text());
  return {
    ws: String(d.workspace_id ?? ""),
    sessionId: String(d.session_id ?? ""),
    permissions: (d.permissions as string[]) ?? [],
  };
}

function countWsaTargetInDom(page: Page) {
  return page.locator('[title="' + EXPORT_A + '"]').count();
}

test.describe("P1 工作区作用域（真实浏览器双上下文）", () => {
  test("P1-B1 wsB 身份：导出中心 0 条、wsA 目标在 API 与 DOM 均不可见", async ({ browser }, testInfo) => {
    requireEnv();
    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    const net: NetRecord[] = [];
    const pageErrors: string[] = [];
    page.on("pageerror", (e) => pageErrors.push(String(e).slice(0, 300)));
    startCapture(page, net);
    try {
      await login(page, E_WSB, P_WSB);
      const scope = await sessionScope(page);
      expect(scope.ws, "wsB 身份的会话工作区必须是 wsB（修复前是 wsA）").toBe(WS_B);
      expect(scope.permissions, "该 fixture 必须具备 export:list").toContain("export:list");
      expect(scope.permissions, "该 fixture 不得具备 export:create（用于权限闸验证）").not.toContain("export:create");

      const list = await page.request.get(BASE + "/api/v2/exports?limit=50", { headers: { "X-Auth-Audience": "user" } });
      const listData = unwrap(await list.text());
      const items = (listData.items as Record<string, unknown>[]) ?? [];
      expect(list.status(), "导出列表 200").toBe(200);
      expect(items.length, "wsB 身份列表必须 0 项").toBe(0);
      expect(items.filter((i) => String(i.job_id ?? "") === JOB_A).length, "不得含 wsA 任务的导出").toBe(0);

      const detail = await page.request.get(BASE + "/api/v2/exports/" + EXPORT_A, { headers: { "X-Auth-Audience": "user" } });
      expect(detail.status(), "跨 scope 读 wsA 导出明细必须 404").toBe(404);

      const ticketHeaders = await authHeaders(page);
      const ticket = await page.request.post(BASE + "/api/v2/exports/" + EXPORT_A + "/download-ticket", { headers: ticketHeaders });
      expect(ticket.status(), "对 wsA 导出取票据必须 403").toBe(403);

      await page.goto(EXPORTS_PATH, { waitUntil: "domcontentloaded" });
      await expect(page.getByText(EMPTY_TEXT).first(), "wsB 身份导出中心必须显示空态").toBeVisible({ timeout: 30_000 });
      expect(await countWsaTargetInDom(page), "wsA 导出 id 不得出现在 wsB 身份的 DOM 中").toBe(0);
      const bodyText = await page.locator("body").innerText();
      expect(bodyText.includes(EXPORT_A), "wsA 导出 id 不得出现在 wsB 身份的页面文本中").toBe(false);

      dump(testInfo, "p1-b1-wsb.json", {
        session: scope, listStatus: list.status(), itemCount: items.length,
        detailStatus: detail.status(), ticketStatus: ticket.status(),
        domWsaTargetCount: await countWsaTargetInDom(page), pageErrors,
        network: net.filter((n) => n.url.includes("/api/v2/exports")),
      });
      expect(pageErrors, "wsB 身份页面不得有 pageerror").toEqual([]);
    } finally {
      await ctx.close();
    }
  });

  test("P1-B2 wsA 身份：正向可见（对照组）", async ({ browser }, testInfo) => {
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    requireEnv();
    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    const net: NetRecord[] = [];
    startCapture(page, net);
    try {
      await login(page, E_WSA, P_WSA);
      const scope = await sessionScope(page);
      expect(scope.ws, "wsA 身份的会话工作区必须是 wsA").toBe(WS_A);

      const list = await page.request.get(BASE + "/api/v2/exports?limit=50", { headers: { "X-Auth-Audience": "user" } });
      const listData = unwrap(await list.text());
      const items = (listData.items as Record<string, unknown>[]) ?? [];
      expect(items.length, "wsA 身份列表必须 > 0").toBeGreaterThan(0);
      expect(items.filter((i) => String(i.id ?? "") === EXPORT_A).length, "wsA 目标对 wsA 身份必须可见").toBeGreaterThan(0);

      const detail = await page.request.get(BASE + "/api/v2/exports/" + EXPORT_A, { headers: { "X-Auth-Audience": "user" } });
      expect(detail.status(), "wsA 身份读自己的导出明细 200").toBe(200);

      await page.goto(EXPORTS_PATH, { waitUntil: "domcontentloaded" });
      await expect.poll(async () => await countWsaTargetInDom(page), { timeout: 30_000 }).toBeGreaterThan(0);

      dump(testInfo, "p1-b2-wsa.json", {
        session: scope, listStatus: list.status(), itemCount: items.length,
        detailStatus: detail.status(), domWsaTargetCount: await countWsaTargetInDom(page),
        network: net.filter((n) => n.url.includes("/api/v2/exports")),
      });
    } finally {
      await ctx.close();
    }
  });

  test("P1-B3 双上下文隔离 + 权限闸与作用域闸分别成立", async ({ browser }, testInfo) => {
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    requireEnv();
    const ctxB = await newContext(browser, { width: 1440, height: 900 });
    const ctxA = await newContext(browser, { width: 1440, height: 900 });
    const ctxBI = await newContext(browser, { width: 1440, height: 900 });
    const pageB = await ctxB.newPage();
    const pageA = await ctxA.newPage();
    const pageBI = await ctxBI.newPage();
    try {
      await login(pageB, E_WSB, P_WSB);
      await login(pageA, E_WSA, P_WSA);
      await login(pageBI, E_WSBINV, P_WSBINV);
      const sB = await sessionScope(pageB);
      const sA = await sessionScope(pageA);
      const sBI = await sessionScope(pageBI);
      expect(sB.sessionId, "两个上下文必须是不同会话（真实隔离）").not.toBe(sA.sessionId);
      expect(sB.ws).toBe(WS_B);
      expect(sA.ws).toBe(WS_A);
      expect(sBI.ws).toBe(WS_B);
      expect(sBI.permissions, "wsB investigator fixture 必须具备 export:create").toContain("export:create");

      const listB = unwrap(await (await pageB.request.get(BASE + "/api/v2/exports?limit=50", { headers: { "X-Auth-Audience": "user" } })).text());
      const listA = unwrap(await (await pageA.request.get(BASE + "/api/v2/exports?limit=50", { headers: { "X-Auth-Audience": "user" } })).text());
      const itemsB = (listB.items as Record<string, unknown>[]) ?? [];
      const itemsA = (listA.items as Record<string, unknown>[]) ?? [];
      expect(itemsB.length, "wsB 在同一时刻仍为 0 项").toBe(0);
      expect(itemsA.length, "wsA 在同一时刻 > 0 项").toBeGreaterThan(0);

      const createB = await pageB.request.post(BASE + "/api/v2/exports",
        { headers: { ...(await authHeaders(pageB)), "Idempotency-Key": "p1b3-viewer-" + Date.now() }, data: { job_id: JOB_A, format: "xlsx" } });
      const bodyB = JSON.parse(await createB.text());
      expect(createB.status(), "wsB viewer（无 export:create）必须 403 —— 权限闸").toBe(403);
      expect(bodyB.error.code, "403 必须带 permission_denied").toBe("permission_denied");

      const createBI = await pageBI.request.post(BASE + "/api/v2/exports",
        { headers: { ...(await authHeaders(pageBI)), "Idempotency-Key": "p1b3-invb-" + Date.now() }, data: { job_id: JOB_A, format: "xlsx" } });
      const bodyBI = JSON.parse(await createBI.text());
      expect(createBI.status(), "wsB investigator（有 export:create）跨 wsA job 必须 404 —— 作用域闸").toBe(404);
      expect(bodyBI.error.code, "404 必须带 RESOURCE_NOT_FOUND").toBe("RESOURCE_NOT_FOUND");

      dump(testInfo, "p1-b3-dual-context.json", {
        sessionB: sB, sessionA: sA, sessionBI: sBI,
        itemsB: itemsB.length, itemsA: itemsA.length,
        createB: { status: createB.status(), code: bodyB.error.code },
        createBI: { status: createBI.status(), code: bodyBI.error.code },
      });
    } finally {
      await ctxB.close(); await ctxA.close(); await ctxBI.close();
    }
  });

  test("P1-B4 移动视口 wsB：同样 0 条且无 wsA 目标", async ({ browser }, testInfo) => {
    test.skip(testInfo.project.name !== "uc-mobile", "mobile-only assertion set");
    requireEnv();
    const ctx = await newContext(browser, { width: 390, height: 844 });
    const page = await ctx.newPage();
    const pageErrors: string[] = [];
    page.on("pageerror", (e) => pageErrors.push(String(e).slice(0, 300)));
    try {
      await login(page, E_WSB, P_WSB);
      const scope = await sessionScope(page);
      expect(scope.ws).toBe(WS_B);
      await page.goto(EXPORTS_PATH, { waitUntil: "domcontentloaded" });
      await expect(page.getByText(EMPTY_TEXT).first()).toBeVisible({ timeout: 30_000 });
      expect(await countWsaTargetInDom(page)).toBe(0);
      dump(testInfo, "p1-b4-mobile.json", { session: scope, domWsaTargetCount: 0, pageErrors });
      expect(pageErrors).toEqual([]);
    } finally {
      await ctx.close();
    }
  });

  test("P1-B5 零成员资格：拒绝登录且不落任何会话 cookie", async ({ browser }, testInfo) => {
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    requireEnv();
    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    try {
      const r = await page.request.post(BASE + "/api/auth/v1/login", {
        headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" },
        data: { identifier: E_NOMEM, password: P_NOMEM, audience: "user" },
      });
      const body = JSON.parse(await r.text());
      expect(r.status(), "零成员资格必须 403（fail closed）").toBe(403);
      expect(body.error.code).toBe("AUTHZ_NO_WORKSPACE_MEMBERSHIP");
      const cookies = await ctx.cookies();
      const sessionCookies = cookies.filter((c) => c.name.indexOf("cs_user_session") === 0);
      expect(sessionCookies.map((c) => c.name), "拒绝登录不得下发会话 cookie").toEqual([]);
      dump(testInfo, "p1-b5-nomem.json", { status: r.status(), code: body.error.code, cookieNames: cookies.map((c) => c.name) });
    } finally {
      await ctx.close();
    }
  });

  test("P1-B6 多成员资格：未选择被拒绝（带可选列表），显式选择 wsB 生效", async ({ browser }, testInfo) => {
    test.skip(testInfo.project.name !== "uc-desktop", "desktop-only assertion set");
    requireEnv();
    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    try {
      const bare = await page.request.post(BASE + "/api/auth/v1/login", {
        headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" },
        data: { identifier: E_MULTI, password: P_MULTI, audience: "user" },
      });
      const bareBody = JSON.parse(await bare.text());
      expect(bare.status(), "多成员资格未选择必须被拒绝（不得任取首行）").toBe(409);
      expect(bareBody.error.code).toBe("AUTHN_WORKSPACE_SELECTION_REQUIRED");
      const options = (bareBody.error.details?.workspaces ?? []) as Array<{ id: string; slug: string }>;
      expect(options.length, "必须返回可选工作区列表（选择器契约）").toBe(2);
      expect(options.map((o) => o.id).sort()).toEqual([WS_A, WS_B].sort());

      const chosen = await page.request.post(BASE + "/api/auth/v1/login", {
        headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" },
        data: { identifier: E_MULTI, password: P_MULTI, audience: "user", workspace_id: WS_B },
      });
      expect(chosen.status(), "显式选择 wsB 后登录成功").toBe(200);
      const scope = await sessionScope(page);
      expect(scope.ws, "显式选择必须真实绑定到会话").toBe(WS_B);

      const ctx2 = await newContext(browser, { width: 1440, height: 900 });
      const page2 = await ctx2.newPage();
      try {
        const bad = await page2.request.post(BASE + "/api/auth/v1/login", {
          headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" },
          data: { identifier: E_MULTI, password: P_MULTI, audience: "user", workspace_id: WS_NOMEM },
        });
        const badBody = JSON.parse(await bad.text());
        expect(bad.status(), "选择非成员工作区必须被拒绝").toBe(403);
        expect(badBody.error.code).toBe("AUTHZ_WORKSPACE_NOT_MEMBER");
      } finally {
        await ctx2.close();
      }

      dump(testInfo, "p1-b6-multi.json", {
        bareStatus: bare.status(), bareCode: bareBody.error.code, options: options.map((o) => o.slug),
        chosenStatus: chosen.status(), sessionWs: scope.ws,
      });
    } finally {
      await ctx.close();
    }
  });
});
