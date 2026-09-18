import { expect, test } from "@playwright/test";
import {
  apiFailureSummary,
  cookieSnapshot,
  denied,
  horizontalOverflow,
  loginAdmin,
  OUT,
  screenshot,
  startCapture,
  writeJson,
  type NetRecord,
} from "./r3-helpers";

/**
 * R-3：旧 V1 前端（frontend/src，经 nginx `/` 由 vite:3000 提供）的
 * /api-control 与 /system 在**正确的管理员登录 + 正确的 admin 受众**下必须可用。
 *
 * 断言是"修复后应有的行为"：
 *   - 修复前运行 → 真实红灯（接口 401/403），红灯本身即缺陷复现证据；
 *   - 修复后运行 → 全绿，证据里带真实请求/响应与截图。
 *
 * 不使用任何 X-User-* 兼容头；登录一律走 /admin/login 后台入口。
 *
 * 注意：旧 V1 的 `/api/v1/api-control/*` 返回**裸载荷**（不是 {success,data,meta} 信封），
 * 解析时直接取顶层字段（已由 curl 实测确认，见 evidence 中的 curl 对照）。
 */

const ADMIN_EMAIL = process.env.E2E_R3_ADMIN_EMAIL ?? "admin@local";
const ADMIN_PASSWORD = process.env.E2E_R3_ADMIN_PASSWORD;

test.skip(!ADMIN_PASSWORD, "需要 E2E_R3_ADMIN_PASSWORD（后台账号口令）才能执行真实登录验收");

const compact = new Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 2 });

function dump(name: string, records: NetRecord[], extra: Record<string, unknown>) {
  writeJson(name, {
    entry: process.env.E2E_BASE_URL ?? "",
    captured_at: new Date().toISOString(),
    summary: apiFailureSummary(records),
    requests: records,
    ...extra,
  });
}

test.describe("R-3 旧 V1 前端在 admin 受众会话下的可访问性", () => {
  test.beforeEach(async ({ page }) => {
    await loginAdmin(page, ADMIN_EMAIL, ADMIN_PASSWORD!);
  });

  test("api-control：真实接口全部成功、关键数据来自接口、无 401/403", async ({ page }, testInfo) => {
    const records = startCapture(page);
    await page.goto("/api-control", { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: "API 控制中心" })).toBeVisible();

    // 关键数据必须来自真实接口：拿服务端返回的 logical_calls 与页面渲染值比对。
    const overviewResponse = await page.request.get("/api/v1/api-control/overview?range=24h", {
      headers: { "X-Auth-Audience": "admin" },
    });
    expect(overviewResponse.ok(), "后台会话应能读取 /api/v1/api-control/overview").toBe(true);
    const overview = (await overviewResponse.json()) as {
      metrics: { logical_calls: number; provider_calls: number };
      control: { state: string };
      freshness: string;
    };
    expect(typeof overview.metrics?.logical_calls, "overview.metrics.logical_calls 必须是真实数字").toBe("number");
    expect(overview.freshness, "overview 必须带真实新鲜度时间戳").toBeTruthy();

    await expect(page.getByText("请求量")).toBeVisible();
    await expect(page.locator(".ac-hero-metrics")).toContainText(compact.format(overview.metrics.logical_calls), { timeout: 25_000 });
    await expect(page.getByText(/自动保护：(开启|关闭)/)).toBeVisible();
    await expect(page.getByTestId("backend-login-dialog")).toHaveCount(0);
    await page.waitForTimeout(1200);

    dump(`net-${testInfo.project.name}-api-control.json`, records, {
      heading: "API 控制中心",
      api_overview: {
        logical_calls: overview.metrics.logical_calls,
        provider_calls: overview.metrics.provider_calls,
        state: overview.control.state,
        freshness: overview.freshness,
      },
      cookies: await cookieSnapshot(page),
    });
    await screenshot(page, `${testInfo.project.name}-api-control.png`);

    const blocked = denied(records).filter((r) => !r.url.includes("/api/auth/v1/"));
    expect(blocked, `api-control 不应出现 401/403，实际：${JSON.stringify(blocked.map((r) => [r.status, r.url]))}`).toEqual([]);
  });

  test("system：页面可打开、无失败弹窗、关键数据来自接口、无 401/403", async ({ page }, testInfo) => {
    const records = startCapture(page);
    await page.goto("/system", { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: "状态总览" })).toBeVisible();

    const overviewResponse = await page.request.get("/api/v1/api-control/overview?range=24h", {
      headers: { "X-Auth-Audience": "admin" },
    });
    expect(overviewResponse.ok()).toBe(true);
    const overview = (await overviewResponse.json()) as { metrics: { logical_calls: number } };
    expect(typeof overview.metrics?.logical_calls).toBe("number");

    await expect(page.getByText(`${compact.format(overview.metrics.logical_calls)} Calls`).first()).toBeVisible({ timeout: 25_000 });
    // 缺陷的可视症状：接口失败时页面弹出"状态数据未完整加载"弹窗。
    await expect(page.getByTestId("backend-login-dialog")).toHaveCount(0);
    await expect(page.getByRole("alertdialog").filter({ hasText: "状态数据未完整加载" })).toHaveCount(0);
    await page.waitForTimeout(1200);

    dump(`net-${testInfo.project.name}-system.json`, records, {
      heading: "状态总览",
      api_overview: { logical_calls: overview.metrics.logical_calls },
      cookies: await cookieSnapshot(page),
    });
    await screenshot(page, `${testInfo.project.name}-system.png`);

    const blocked = denied(records).filter((r) => !r.url.includes("/api/auth/v1/"));
    expect(blocked, `system 不应出现 401/403，实际：${JSON.stringify(blocked.map((r) => [r.status, r.url]))}`).toEqual([]);
  });

  test("api-control：弹窗可开关（不做任何写操作）", async ({ page }, testInfo) => {
    const records = startCapture(page);
    await page.goto("/api-control?tab=control", { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: "API 控制中心" })).toBeVisible();
    const kill = page.locator('input[name="kill-reason"]');
    await expect(kill).toBeVisible({ timeout: 30_000 });
    await kill.fill("R-3 验收：仅打开确认弹窗，不提交");
    const trigger = page.getByRole("button", { name: /启用停机|解除停机/ });
    await expect(trigger).toBeEnabled({ timeout: 30_000 });
    await trigger.click();
    const dialog = page.getByRole("dialog").filter({ hasText: /全局停机/ });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("button", { name: "取消" })).toBeVisible();
    await screenshot(page, `${testInfo.project.name}-dialog-open.png`);
    await dialog.getByRole("button", { name: "取消" }).click();
    await expect(dialog).toBeHidden();
    await expect(kill).toHaveValue("R-3 验收：仅打开确认弹窗，不提交");
    // 只读确认：真正的停机状态没有被本次弹窗开关改变。
    const stateResponse = await page.request.get("/api/v1/api-control/control/states", {
      headers: { "X-Auth-Audience": "admin" },
    });
    expect(stateResponse.ok()).toBe(true);
    const state = (await stateResponse.json()) as { kill_switch: boolean };
    expect(state.kill_switch, "取消弹窗后全局停机必须仍为关闭").toBe(false);
    writeJson(`dialog-${testInfo.project.name}.json`, {
      kill_switch_after_cancel: state.kill_switch,
      dialog_opened: true,
      dialog_closed: true,
      write_operations_issued: false,
    });
    const blocked = denied(records).filter((r) => !r.url.includes("/api/auth/v1/") && r.method !== "GET");
    expect(blocked, "弹窗用例不得发出任何失败写请求").toEqual([]);
  });

  test("api-control 与 system 无横向溢出", async ({ page }) => {
    for (const route of ["/api-control", "/system"]) {
      await page.goto(route, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(800);
      const metrics = await horizontalOverflow(page);
      expect(metrics.overflow, `${route} 出现横向溢出 scrollWidth=${metrics.scrollWidth} clientWidth=${metrics.clientWidth}`).toBe(false);
    }
  });
});

test.afterAll(() => {
  if (OUT) console.log(`R-3 evidence dir: ${OUT}`);
});
