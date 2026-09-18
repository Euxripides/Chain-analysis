import { expect, test } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";

// R7：真实用户界面 /jobs 行数 必须同时等于 API 返回与数据库基线。
//
// 本文件是 v2（修正 v1 的两处证据真实性缺陷，旧文件与旧 run 原样保留）：
//   v1 缺陷 A：登录走 page.context().request.post("/api/auth/v1/login")，**没有**走真实登录 UI；
//              根监控只读核实实际登录页是 /app-v2/login（用户控制台 BrowserRouter basename=/app-v2），
//              根 /login 只是旧 V1 SPA fallback，匿名也能渲染旧 shell，不能当已登录证据。
//   v1 缺陷 B：两个 project 共用 r7-jobs-desktop.png / r7-jobs-mobile.png / r7-ui-evidence.json，
//              且在每个 project 内又 setViewportSize(390x844)，后跑的 user-mobile 覆盖了 user-desktop，
//              文件名无法证明桌面证据。
// v2 修正：
//   * 每个 project 使用独立子目录（testInfo.project.name）与独立文件名；
//   * **不改变** viewport，只断言并记录实际 width/height/DPR/isMobile；
//   * 真实浏览器 goto /app-v2/login → fill #identifier/#password → 提交 → 断言真实离开登录页；
//   * 每个 project 独立产出 API JobIDs / DB JobIDs / DOM 行数 / 截图 / JSON。

const OUT = process.env.R7_OUT ?? "";
const EMAIL = process.env.R7_EMAIL ?? "";
const PASSWORD = process.env.R7_PASSWORD ?? "";
const EXPECTED = Number(process.env.R7_EXPECTED_ROWS ?? "3");
const DB_IDS = (process.env.R7_DB_IDS ?? "").split(",").map((s) => s.trim()).filter(Boolean);
const LOGIN_PATH = process.env.R7_LOGIN_PATH ?? "/app-v2/login";

test.use({ ignoreHTTPSErrors: true });

test.describe("R7 /jobs UI scope regression (v2)", () => {
  test("R7-01 真实登录 UI 后旧 V1 /jobs 行数 == API == DB，且无其它工作区任务", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD, "R7_EMAIL / R7_PASSWORD not provided");

    const project = testInfo.project.name;
    const dir = OUT ? path.join(OUT, project) : "";
    if (dir) mkdirSync(dir, { recursive: true });
    const dump = (name: string, value: unknown) => {
      if (!dir) return;
      writeFileSync(path.join(dir, name), JSON.stringify(value, null, 2) + "\n", "utf8");
    };

    // 实际视口（不修改，只记录）——用于证明"桌面/移动"证据与 project 配置一致
    const vp = page.viewportSize();
    const dpr = await page.evaluate(() => window.devicePixelRatio);
    const isMobile = testInfo.project.use.isMobile === true;

    const apiCalls: Array<{ url: string; status: number; count: number; ids: string[]; requestId: string | null; traceId: string | null }> = [];
    const consoleErrors: string[] = [];
    page.on("response", (response) => {
      void (async () => {
        const url = response.url();
        if (!url.includes("/api/v1/download-jobs") || url.includes("/events")) return;
        let ids: string[] = [];
        try {
          const body = await response.json();
          const jobs = (body && body.jobs) || [];
          ids = jobs.map((j: { id: string }) => j.id);
        } catch {
          ids = [];
        }
        apiCalls.push({
          url,
          status: response.status(),
          count: ids.length,
          ids,
          requestId: response.headers()["x-request-id"] ?? null,
          traceId: response.headers()["x-trace-id"] ?? null
        });
      })();
    });
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });

    // ---- 真实登录 UI ----
    await page.goto(LOGIN_PATH, { waitUntil: "domcontentloaded" });
    await expect(page.locator("#identifier"), "login identifier input").toBeVisible({ timeout: 30_000 });
    const loginScreenshotBefore = dir;
    if (dir) await page.screenshot({ path: path.join(dir, "01-login-page.png"), fullPage: false });
    await page.locator("#identifier").fill(EMAIL);
    await page.locator("#password").fill(PASSWORD);
    await page.getByRole("button", { name: "登录", exact: true }).click();

    const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
    if (await notice.isVisible().catch(() => false)) {
      await page.keyboard.press("Escape");
      await expect(notice).toBeHidden();
    }
    await expect.poll(() => page.url(), { timeout: 30_000 }).not.toContain(LOGIN_PATH);
    const afterLoginUrl = page.url();
    if (dir) await page.screenshot({ path: path.join(dir, "02-after-login.png"), fullPage: false });

    // 会话必须真实可用：用户控制台会话接口返回 200 且为 user 受众
    const sessResp = await page.context().request.get("/api/auth/v1/session", {
      headers: { "X-Auth-Audience": "user" }
    });
    const sessionStatus = sessResp.status();

    // ---- 旧 V1 /jobs ----
    await page.goto("/jobs", { waitUntil: "domcontentloaded" });
    await expect(page.locator("table.task-table")).toBeVisible({ timeout: 30_000 });
    await expect(page.locator("table.task-table tbody tr.skeleton-row")).toHaveCount(0, { timeout: 30_000 });

    const jobRows = page.locator("table.task-table tbody tr").filter({ hasNot: page.locator(".table-empty") });
    await expect(jobRows).toHaveCount(EXPECTED, { timeout: 30_000 });
    const domRows = await jobRows.count();

    const bodyText = (await page.locator("body").innerText()) || "";
    const forbidden = (process.env.R7_FORBIDDEN_JOB_NOS ?? "").split(",").map((s) => s.trim()).filter(Boolean);
    const leaked = forbidden.filter((n) => bodyText.includes(n));

    if (dir) {
      await page.screenshot({ path: path.join(dir, "03-jobs.png"), fullPage: true });
    }

    const last = apiCalls[apiCalls.length - 1];
    const apiIds = last ? last.ids.slice().sort() : [];
    const dbIds = DB_IDS.slice().sort();
    dump("r7-ui-evidence.json", {
      project,
      login_path: LOGIN_PATH,
      login_page_screenshot: loginScreenshotBefore ? "01-login-page.png" : null,
      url_after_login: afterLoginUrl,
      session_status: sessionStatus,
      viewport: { width: vp ? vp.width : null, height: vp ? vp.height : null, dpr, isMobile },
      dom_rows: domRows,
      expected_rows: EXPECTED,
      api_calls: apiCalls,
      db_ids: dbIds,
      api_matches_db: dbIds.length > 0 && apiIds.join(",") === dbIds.join(","),
      forbidden_job_nos: forbidden,
      leaked_job_nos: leaked,
      final_url: page.url(),
      console_errors: consoleErrors.slice(0, 20)
    });

    expect(sessionStatus, "user session after real login UI").toBe(200);
    expect(apiCalls.length, "no /api/v1/download-jobs call observed").toBeGreaterThan(0);
    expect(last.status, "download-jobs HTTP status").toBe(200);
    expect(last.count, "API job count").toBe(EXPECTED);
    expect(domRows, "DOM row count").toBe(EXPECTED);
    expect(dbIds.length, "DB baseline ids must be provided").toBeGreaterThan(0);
    expect(apiIds, "API ids == DB ids").toEqual(dbIds);
    expect(leaked, "other-workspace job_no leaked into DOM").toEqual([]);
  });
});
