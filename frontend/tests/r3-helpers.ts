import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { expect, type Page, type Response } from "@playwright/test";

/**
 * R-3 证据输出目录（由 runner 通过 R3_OUT 注入）。
 * 缺省为空字符串时全部落盘动作静默跳过（只影响本地 explorative 运行，不影响断言）。
 */
export const OUT = process.env.R3_OUT ?? "";

export function outPath(name: string): string {
  if (!OUT) return "";
  mkdirSync(OUT, { recursive: true });
  return path.join(OUT, name);
}

export function writeJson(name: string, value: unknown): string {
  const target = outPath(name);
  if (!target) return "";
  writeFileSync(target, JSON.stringify(value, null, 2) + "\n", "utf8");
  return target;
}

export function writeText(name: string, value: string): string {
  const target = outPath(name);
  if (!target) return "";
  writeFileSync(target, value, "utf8");
  return target;
}

export async function screenshot(page: Page, name: string): Promise<void> {
  const target = outPath(name);
  if (target) await page.screenshot({ path: target, fullPage: true });
}

export type NetRecord = {
  at: string;
  method: string;
  url: string;
  status: number;
  requestId: string | null;
  traceId: string | null;
  audienceHeader: string | null;
  csrfHeader: string | null;
  responseBody: string | null;
};

/** 记录页面发出的全部 /api/ 请求（真实 URL / 状态码 / 追踪 ID / 响应体）。 */
export function startCapture(page: Page): NetRecord[] {
  const records: NetRecord[] = [];
  page.on("response", (response: Response) => {
    void (async () => {
      const url = response.url();
      if (!url.includes("/api/")) return;
      const request = response.request();
      const headers = request.headers();
      let body: string | null = null;
      const contentType = response.headers()["content-type"] ?? "";
      if (response.status() !== 204 && !contentType.includes("event-stream")) {
        try {
          body = (await response.text()).slice(0, 4000);
        } catch {
          body = null;
        }
      }
      records.push({
        at: new Date().toISOString(),
        method: request.method(),
        url,
        status: response.status(),
        requestId: response.headers()["x-request-id"] ?? null,
        traceId: response.headers()["x-trace-id"] ?? null,
        audienceHeader: headers["x-auth-audience"] ?? null,
        csrfHeader: headers["x-csrf-token"] ? "<present>" : null,
        responseBody: body,
      });
    })();
  });
  return records;
}

export function denied(records: NetRecord[]): NetRecord[] {
  return records.filter((r) => r.status === 401 || r.status === 403);
}

export function apiFailureSummary(records: NetRecord[]): Record<string, unknown> {
  return {
    total: records.length,
    statuses: records.reduce<Record<string, number>>((acc, r) => {
      acc[String(r.status)] = (acc[String(r.status)] ?? 0) + 1;
      return acc;
    }, {}),
    denied: denied(records).map((r) => ({
      method: r.method,
      url: r.url,
      status: r.status,
      request_id: r.requestId,
      trace_id: r.traceId,
      audience_header: r.audienceHeader,
      body: r.responseBody,
    })),
  };
}

/**
 * 从**后台入口**登录：/admin/login（admin 受众）。
 * 登录成功后浏览器持有 cs_admin_session / cs_admin_session_csrf。
 */
export async function loginAdmin(page: Page, email: string, password: string): Promise<void> {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 30_000 });
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
    await expect(notice).toBeHidden();
  }
}

/** 从**用户层入口**登录：/app-v2/login（user 受众）。 */
export async function loginUser(page: Page, email: string, password: string): Promise<void> {
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 30_000 });
}

export type CookieSnapshot = { name: string; domain: string; path: string; secure: boolean; httpOnly: boolean; session: boolean };

export async function cookieSnapshot(page: Page): Promise<CookieSnapshot[]> {
  const cookies = await page.context().cookies();
  return cookies
    .filter((c) => c.name.startsWith("cs_") || c.name.includes("session"))
    .map((c) => ({ name: c.name, domain: c.domain, path: c.path, secure: c.secure, httpOnly: c.httpOnly, session: c.session }));
}

/** 无横向溢出：文档滚动宽度不得超过视口宽度。 */
export async function horizontalOverflow(page: Page): Promise<{ scrollWidth: number; clientWidth: number; overflow: boolean }> {
  return page.evaluate(() => {
    const el = document.documentElement;
    return { scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, overflow: el.scrollWidth > el.clientWidth };
  });
}
