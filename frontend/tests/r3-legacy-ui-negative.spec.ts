import { expect, test } from "@playwright/test";
import { denied, loginUser, screenshot, startCapture, writeJson, type NetRecord } from "./r3-helpers";

/**
 * R-3 负向对照：受众/导航修复**不得**放宽任何授权。
 *
 * 覆盖四条边界（每条都记录真实状态码、真实响应体、X-Request-Id，供审计逐条核对）：
 *   1. 用户层会话（audience=user）访问后台页 → 页面发起的后台接口必须被拒；
 *   2. 用户层会话显式声明 `X-Auth-Audience: user` 调后台接口 → 必须 403 audience_mismatch
 *      （这是 P0 建立的"用户会话不得调用后台接口"硬不变量）；
 *   3. 匿名访问后台页 → 必须 401；
 *   4. 受众头不能提权：把 user 会话令牌塞进 admin Cookie 名 + 声明 admin 受众 → 仍必须被拒。
 */

const USER_EMAIL = process.env.E2E_R3_USER_EMAIL ?? "fixture-investigator@fixture.local";
const USER_PASSWORD = process.env.E2E_R3_USER_PASSWORD;
const ADMIN_EMAIL = process.env.E2E_R3_ADMIN_EMAIL ?? "admin@local";
const ADMIN_PASSWORD = process.env.E2E_R3_ADMIN_PASSWORD;
const SITE = process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448";

test.skip(!USER_PASSWORD || !ADMIN_PASSWORD, "需要 E2E_R3_USER_PASSWORD 与 E2E_R3_ADMIN_PASSWORD");

function denials(records: NetRecord[]) {
  return denied(records).map((r) => ({
    method: r.method,
    url: r.url,
    status: r.status,
    request_id: r.requestId,
    trace_id: r.traceId,
    audience_header: r.audienceHeader,
    csrf_header: r.csrfHeader,
    body: r.responseBody,
  }));
}

test("1. 用户层会话访问 /api-control：页面发起的后台接口全部被拒", async ({ page }, testInfo) => {
  await loginUser(page, USER_EMAIL, USER_PASSWORD!);
  const records = startCapture(page);
  await page.goto("/api-control", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(3000);

  // 导航修复的可见证据：用户层会话看到的是"需要后台会话"的阻断弹窗。
  await expect(page.getByTestId("backend-login-dialog")).toBeVisible();
  await screenshot(page, `negative-${testInfo.project.name}-user-session.png`);

  const adminCalls = records.filter((r) => r.url.includes("/api/v1/api-control/") || r.url.includes("/api/v1/providers/"));
  const blocked = records.filter((r) => adminCalls.includes(r) && (r.status === 401 || r.status === 403));
  writeJson(`negative-${testInfo.project.name}-user-session.json`, {
    case: "user-audience session -> V1 backend page",
    service_denials: denials(blocked),
    all_admin_calls: adminCalls.map((r) => ({ status: r.status, url: r.url, request_id: r.requestId })),
    denied_request_ids: blocked.map((r) => r.requestId).filter(Boolean),
  });

  expect(adminCalls.length, "页面确实发起了后台接口请求").toBeGreaterThan(0);
  expect(blocked.length, "用户层会话对后台接口必须全部被拒").toBe(adminCalls.length);
  for (const record of blocked) {
    expect(record.status, `${record.url} 必须被拒`).toBeGreaterThanOrEqual(401);
    expect(record.responseBody ?? "").toMatch(/unauthenticated|permission_denied/);
  }
});

test("2a. 普通用户层会话声明 user 受众调后台接口：403", async ({ page }, testInfo) => {
  await loginUser(page, USER_EMAIL, USER_PASSWORD!);
  await page.goto("/api-control", { waitUntil: "domcontentloaded" });

  // 在真实浏览器里用真实 Cookie 发起：只声明 user 受众（不伪造任何身份头）。
  const result = await page.evaluate(async () => {
    const res = await fetch("/api/v1/api-control/overview?range=24h", {
      headers: { "X-Auth-Audience": "user" },
      credentials: "same-origin",
    });
    return { status: res.status, requestId: res.headers.get("x-request-id"), body: await res.text() };
  });

  writeJson(`negative-${testInfo.project.name}-audience-mismatch.json`, {
    case: "plain user-audience session + X-Auth-Audience: user -> admin route",
    status: result.status,
    request_id: result.requestId,
    body: result.body.slice(0, 800),
    note: "该身份同时不具备 admin:dashboard:read，拒绝原因可能是 missing_permission；关键不变量是 403 拒绝。",
  });

  expect(result.status, "用户受众会话调用 admin:* 路由必须是 403").toBe(403);
  expect(result.body).toContain("permission_denied");
  expect(result.body).toMatch(/missing_permission|audience_mismatch/);
});

test("2b. 已持有 admin:* 权限但会话受众为 user：403 audience_mismatch", async ({ browser }, testInfo) => {
  // 用同一个 owner 身份（持有 admin:* 权限）在 **user 受众** 下登录，
  // 这样"权限"这一层必然通过，唯一可能拒绝它的就是**受众闸门**本身。
  const context = await browser.newContext({ ignoreHTTPSErrors: true, baseURL: SITE });
  const login = await context.request.post("/api/auth/v1/login", {
    headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" },
    data: { identifier: ADMIN_EMAIL, password: ADMIN_PASSWORD, audience: "user" },
  });
  const loginBody = await login.text();
  expect(login.ok(), `user 受众登录应成功：${login.status()} ${loginBody.slice(0, 200)}`).toBe(true);

  const response = await context.request.get("/api/v1/api-control/overview?range=24h", {
    headers: { "X-Auth-Audience": "user" },
  });
  const body = await response.text();
  writeJson(`negative-${testInfo.project.name}-audience-mismatch-privileged.json`, {
    case: "owner-role identity, user-audience session, X-Auth-Audience: user -> admin route",
    login_status: login.status(),
    login_audience: "user",
    status: response.status(),
    request_id: response.headers()["x-request-id"] ?? null,
    body: body.slice(0, 800),
    invariant: "会话受众本身是独立闸门：权限再高，user 受众也不得调用 admin:* 路由",
  });

  expect(response.status(), "user 受众会话（即使持有 admin 权限）必须是 403").toBe(403);
  expect(body).toContain("permission_denied");
  expect(body, "拒绝原因必须是 audience_mismatch（而不是缺权限）").toContain("audience_mismatch");
  await context.close();
});

test("3. 匿名访问后台页：401", async ({ browser }, testInfo) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  const records = startCapture(page);
  await page.goto("/api-control", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(3000);
  await expect(page.getByTestId("backend-login-dialog")).toBeVisible();
  await screenshot(page, `negative-${testInfo.project.name}-anonymous.png`);

  const adminCalls = records.filter((r) => r.url.includes("/api/v1/api-control/"));
  const blocked = adminCalls.filter((r) => r.status === 401 || r.status === 403);
  writeJson(`negative-${testInfo.project.name}-anonymous.json`, {
    case: "anonymous -> V1 backend page",
    service_denials: denials(blocked),
    denied_request_ids: blocked.map((r) => r.requestId).filter(Boolean),
  });
  expect(adminCalls.length).toBeGreaterThan(0);
  expect(blocked.length, "匿名会话对后台接口必须全部被拒").toBe(adminCalls.length);
  for (const record of blocked) expect(record.status).toBe(401);
  await context.close();
});

test("4. 受众头不能提权：user 令牌放进 admin Cookie 名仍被拒绝", async ({ page, browser }, testInfo) => {
  await loginUser(page, USER_EMAIL, USER_PASSWORD!);
  const cookies = await page.context().cookies();
  const userSession = cookies.find((c) => c.name.endsWith("cs_user_session"));
  const userCsrf = cookies.find((c) => c.name.endsWith("cs_user_session_csrf"));
  expect(userSession, "用户层登录必须产生 cs_user_session").toBeTruthy();

  const forged = await browser.newContext({ ignoreHTTPSErrors: true });
  await forged.addCookies([
    { name: "__Host-cs_admin_session", value: userSession!.value, domain: "127.0.0.1", path: "/", secure: true, httpOnly: true, sameSite: "Lax" },
    { name: "cs_admin_session", value: userSession!.value, domain: "127.0.0.1", path: "/", secure: true, httpOnly: true, sameSite: "Lax" },
    ...(userCsrf
      ? [{ name: "cs_admin_session_csrf", value: userCsrf.value, domain: "127.0.0.1", path: "/", secure: true, httpOnly: false, sameSite: "Lax" as const }]
      : []),
  ]);
  const response = await forged.request.get("/api/v1/api-control/overview?range=24h", {
    headers: { "X-Auth-Audience": "admin" },
  });
  const body = await response.text();
  writeJson(`negative-${testInfo.project.name}-audience-header-cannot-escalate.json`, {
    case: "user token placed in admin cookie name + X-Auth-Audience: admin",
    status: response.status(),
    request_id: response.headers()["x-request-id"] ?? null,
    body: body.slice(0, 800),
  });
  expect([401, 403], `必须被拒绝，实际 ${response.status()}`).toContain(response.status());
  expect(body).toMatch(/unauthenticated|permission_denied/);
  await forged.close();
});
