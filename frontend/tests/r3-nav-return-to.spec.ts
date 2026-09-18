import { expect, test } from "@playwright/test";
import { loginAdmin, screenshot, writeJson } from "./r3-helpers";

/**
 * R-3 导航闭环：旧 V1 后台页面（/api-control、/system）↔ 后台管理系统（/admin/）
 * 之间的"去后台登录 → 登录 → 回到原页面（含 query）"必须真实可用。
 *
 * 同时覆盖 return_to 的**开放重定向**防护：只有本地白名单
 * （`/api-control`、`/system` 及合法子路由 + query）才允许作为跳转目标。
 *
 * 全部断言基于真实浏览器的**最终 location**，不使用内部路由推断。
 */

const ADMIN_EMAIL = process.env.E2E_R3_ADMIN_EMAIL ?? "admin@local";
const ADMIN_PASSWORD = process.env.E2E_R3_ADMIN_PASSWORD;

test.skip(!ADMIN_PASSWORD, "需要 E2E_R3_ADMIN_PASSWORD（后台账号口令）");

const SITE = "https://127.0.0.1:8448";
const ALLOWLIST = ["/api-control", "/system"];

function summarize(url: string) {
  const parsed = new URL(url);
  return { href: parsed.href, origin: parsed.origin, pathname: parsed.pathname, search: parsed.search };
}

function pathAllowed(pathname: string): boolean {
  return ALLOWLIST.some((base) => pathname === base || pathname.startsWith(base + "/"));
}

test.describe("R-3 后台登录回跳与 return_to 白名单", () => {
  test("匿名访问后台页 → 点入口 → 后台登录 → 真实回到原页面（保留 query）", async ({ page }, testInfo) => {
    const target = "/api-control?tab=control";
    await page.goto(target, { waitUntil: "domcontentloaded" });

    const gate = page.getByTestId("backend-login-dialog");
    await expect(gate).toBeVisible();
    await expect(page.getByRole("heading", { name: "需要管理员后台会话" })).toBeVisible();
    await screenshot(page, `nav-${testInfo.project.name}-1-gate.png`);

    const entrance = gate.getByRole("link", { name: "去后台登录" });
    await expect(entrance).toBeVisible();
    const href = await entrance.getAttribute("href");
    expect(href, "入口必须携带 return_to").toContain("/admin/login?return_to=");
    expect(decodeURIComponent(href ?? ""), "return_to 必须是原页面（含 query）").toContain(target);
    await entrance.click();

    await expect(page).toHaveURL(/\/admin\/login\?return_to=/);
    await expect(page.locator("#identifier")).toBeVisible();
    const loginUrl = summarize(page.url());
    await screenshot(page, `nav-${testInfo.project.name}-2-login.png`);

    await page.locator("#identifier").fill(ADMIN_EMAIL);
    await page.locator("#password").fill(ADMIN_PASSWORD!);
    await page.getByRole("button", { name: /^登录(后台)?$/ }).click();

    // 真实浏览器跳转回旧 V1 SPA：最终 location 必须精确等于原页面（含 query）。
    await expect.poll(() => summarize(page.url()).href, { timeout: 30_000 }).toBe(SITE + target);
    await expect(page.getByRole("heading", { name: "API 控制中心" })).toBeVisible();
    await expect(page.getByTestId("backend-login-dialog")).toHaveCount(0);

    const finalUrl = summarize(page.url());
    writeJson(`nav-${testInfo.project.name}-happy-path.json`, {
      requested: target,
      entrance_href: href,
      login_page: loginUrl,
      final: finalUrl,
      query_preserved: finalUrl.search === "?tab=control",
      dialog_gone: true,
    });
    await screenshot(page, `nav-${testInfo.project.name}-3-returned.png`);
    expect(finalUrl.pathname + finalUrl.search).toBe(target);
  });

  test("已认证状态下入口直接回原页（/system?view=settings）", async ({ page }, testInfo) => {
    await loginAdmin(page, ADMIN_EMAIL, ADMIN_PASSWORD!);
    await page.goto("/admin/login?return_to=%2Fsystem%3Fview%3Dsettings", { waitUntil: "domcontentloaded" });
    await expect.poll(() => summarize(page.url()).href, { timeout: 30_000 }).toBe(SITE + "/system?view=settings");
    await expect(page.getByRole("heading", { name: "状态总览" })).toBeVisible();
    writeJson(`nav-${testInfo.project.name}-already-authenticated.json`, { final: summarize(page.url()) });
    await screenshot(page, `nav-${testInfo.project.name}-4-already-authenticated.png`);
  });

  test("未认证访问 /admin/* 时 return_to 不丢（search 保留）", async ({ page }, testInfo) => {
    await page.goto("/admin/sessions?return_to=%2Fapi-control%3Ftab%3Derrors", { waitUntil: "domcontentloaded" });
    // 未认证：admin console 的 * 路由必须把 search 一起带到 /login（原实现只带 pathname）。
    await expect.poll(() => summarize(page.url()).search, { timeout: 20_000 }).toContain("return_to=");
    await expect(page.locator("#identifier")).toBeVisible();
    await page.locator("#identifier").fill(ADMIN_EMAIL);
    await page.locator("#password").fill(ADMIN_PASSWORD!);
    await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
    await expect.poll(() => summarize(page.url()).href, { timeout: 30_000 }).toBe(SITE + "/api-control?tab=errors");
    writeJson(`nav-${testInfo.project.name}-search-preserved.json`, { final: summarize(page.url()), query_preserved: true });
    await screenshot(page, `nav-${testInfo.project.name}-5-search-preserved.png`);
  });

  test.describe("return_to 开放重定向防护", () => {
    const hostile: Array<[string, string]> = [
      ["protocol-relative", "//evil.example.com/api-control"],
      ["absolute-foreign", "https://evil.example.com/api-control"],
      ["javascript-scheme", "javascript:alert(document.domain)"],
      ["backslash", "/\\evil.example.com"],
      ["parent-escape", "/api-control/../../etc/passwd"],
      ["on-origin-not-allowlisted", "/admin/overview"],
      ["encoded-slash", "/api-control%2f%2fevil.example.com"],
      ["encoded-backslash", "/%5c%5cevil.example.com"],
      ["scheme-relative-host", "/api-control@evil.example.com"],
    ];

    test("恶意 return_to 一律落到白名单内（不会被外域接走）", async ({ page }, testInfo) => {
      await loginAdmin(page, ADMIN_EMAIL, ADMIN_PASSWORD!);
      const results: Array<Record<string, unknown>> = [];

      for (const [name, payload] of hostile) {
        await page.goto(`/admin/login?return_to=${encodeURIComponent(payload)}`, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(1200);
        const final = summarize(page.url());
        const rejected = final.origin === SITE && (final.pathname === "/admin/overview" || pathAllowed(final.pathname));
        results.push({ case: name, payload, final, rejected_foreign: rejected });
        expect(final.origin, `【${name}】不得跳到外域：${final.href}`).toBe(SITE);
        expect(
          final.pathname === "/admin/overview" || pathAllowed(final.pathname),
          `【${name}】最终路径既不是白名单后台页也不是默认落地页：${final.href}`,
        ).toBe(true);
        expect(final.href, `【${name}】不得包含攻击者路径`).not.toContain("evil.example.com");
      }

      // 正向对照：合法 return_to 必须真的生效（证明上面的拒绝不是因为功能整体失效）。
      await page.goto("/admin/login?return_to=%2Fapi-control%3Ftab%3Dbudgets", { waitUntil: "domcontentloaded" });
      await expect.poll(() => summarize(page.url()).href, { timeout: 30_000 }).toBe(SITE + "/api-control?tab=budgets");
      const positive = summarize(page.url());

      writeJson(`nav-${testInfo.project.name}-return-to-allowlist.json`, {
        rejected_cases: results,
        positive_control: positive,
        allowlist: ALLOWLIST,
      });
      expect(positive.pathname + positive.search).toBe("/api-control?tab=budgets");
    });
  });
});
