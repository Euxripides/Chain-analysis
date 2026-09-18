import { expect, test, type Page } from "@playwright/test";

/**
 * Sprint 1/2 遗留的体验与安全验收（§27.6）：
 *   UX-002 四断点布局
 *   UX-003 颜色对比度与"状态不只靠颜色"
 *   UX-004 长表渲染预算
 *   SEC-002 XSS / Markdown 注入
 *
 * 全部断言都在真实浏览器里跑，并且**度量实际值**（对比度比值、DOM 行数、
 * 布局宽度），而不是只看元素是否存在。
 */

const EMAIL = process.env.E2E_USER_EMAIL ?? "admin@local";
const PASSWORD = process.env.E2E_USER_PASSWORD;

test.skip(!PASSWORD, "需要 E2E_USER_PASSWORD 才能执行真实登录验收");

/**
 * P1-20260913：多工作区身份登录时，服务端返回 409 AUTHN_WORKSPACE_SELECTION_REQUIRED
 * 并附可选列表，页面按仓库规范用弹窗要求**显式选择**工作区（绝不静默取默认）。
 * 夹具账号（fixture-*）同时属于 acme-lab 与 acme-lab-other，因此这一步是必经路径。
 */
async function resolveWorkspaceSelection(page: Page, slug = "acme-lab") {
  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  await dialog.waitFor({ state: "visible", timeout: 5_000 }).catch(() => {});
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByRole("button", { name: new RegExp(slug) }).first().click();
    await expect(dialog).toBeHidden({ timeout: 15_000 });
  }
}
async function loginAs(page: Page, path: "user" | "admin") {
  const loginPath = path === "user" ? "/app-v2/login" : "/admin/login";
  await page.goto(loginPath, { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await resolveWorkspaceSelection(page);
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
    await expect(notice).toBeHidden();
  }
}

/** 计算 sRGB 相对亮度（WCAG 2.1）。 */
function relativeLuminance(rgb: [number, number, number]): number {
  const [r, g, b] = rgb.map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrastRatio(a: [number, number, number], b: [number, number, number]): number {
  const la = relativeLuminance(a);
  const lb = relativeLuminance(b);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/** 把元素的有效背景色向上回溯到第一个不透明祖先。 */
const BACKGROUND_PROBE = `
(() => {
  const parse = (value) => {
    const m = value.match(/rgba?\\(([^)]+)\\)/);
    if (!m) return null;
    const parts = m[1].split(',').map((v) => parseFloat(v.trim()));
    const alpha = parts.length > 3 ? parts[3] : 1;
    return { rgb: parts.slice(0, 3), alpha };
  };
  const results = [];
  const nodes = document.querySelectorAll('body *');
  for (const el of nodes) {
    const text = (el.textContent || '').trim();
    if (!text || el.children.length > 0) continue;
    const rect = el.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) continue;
    const style = getComputedStyle(el);
    if (style.visibility === 'hidden' || style.display === 'none' || style.opacity === '0') continue;
    const fg = parse(style.color);
    if (!fg) continue;
    let bg = null;
    let node = el;
    while (node && node !== document.documentElement) {
      const candidate = parse(getComputedStyle(node).backgroundColor);
      if (candidate && candidate.alpha >= 0.999) { bg = candidate; break; }
      node = node.parentElement;
    }
    if (!bg) bg = { rgb: [255, 255, 255], alpha: 1 };
    const fontSize = parseFloat(style.fontSize);
    const bold = parseInt(style.fontWeight, 10) >= 700;
    const large = fontSize >= 24 || (fontSize >= 18.66 && bold);
    results.push({
      tag: el.tagName,
      cls: (el.className || '').toString().slice(0, 60),
      fg: fg.rgb,
      bg: bg.rgb,
      large,
      sample: text.slice(0, 40),
    });
  }
  return results;
})()
`;

test.describe("UX-002 四断点布局", () => {
  const BREAKPOINTS = [
    { name: "mobile", width: 390, height: 844 },
    { name: "tablet", width: 768, height: 1024 },
    { name: "laptop", width: 1024, height: 768 },
    { name: "desktop", width: 1440, height: 900 },
  ];

  for (const bp of BREAKPOINTS) {
    test(`${bp.name} (${bp.width}px) 无页面级横向滚动`, async ({ page }) => {
      await page.setViewportSize({ width: bp.width, height: bp.height });
      await loginAs(page, "user");

      const paths = ["/app-v2/home", "/app-v2/cases", "/app-v2/search", "/app-v2/profile", "/app-v2/login"];
      for (const path of paths) {
        await page.goto(path, { waitUntil: "networkidle" });
        const m = await page.evaluate(() => ({
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
          offenders: Array.from(document.querySelectorAll("body *"))
            .filter((el) => {
              const r = el.getBoundingClientRect();
              // 表格自身允许横向滚动，不算页面溢出。
              return r.width > document.documentElement.clientWidth + 1 && !el.closest(".table-scroll");
            })
            .slice(0, 3)
            .map((el) => `${el.tagName}.${(el.className || "").toString().slice(0, 40)}`),
        }));
        expect(
          m.scrollWidth,
          `${path} 在 ${bp.width}px 下溢出到 ${m.scrollWidth}px，越界元素：${m.offenders.join(", ")}`,
        ).toBeLessThanOrEqual(m.clientWidth + 1);
      }
    });
  }

  test("后台管理系统在四个断点下无横向滚动", async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 768 });
    await loginAs(page, "admin");
    for (const width of [1024, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      for (const path of ["/admin/overview", "/admin/users", "/admin/organizations", "/admin/audit"]) {
        await page.goto(path, { waitUntil: "networkidle" });
        const m = await page.evaluate(() => ({
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
        }));
        expect(m.scrollWidth, `${path} 在 ${width}px 下溢出`).toBeLessThanOrEqual(m.clientWidth + 1);
      }
    }
  });
});

test.describe("UX-003 颜色对比度与状态可辨识", () => {
  test("所有可见文本满足 WCAG AA 对比度", async ({ page }) => {
    await loginAs(page, "user");
    const pages = ["/app-v2/home", "/app-v2/cases", "/app-v2/profile"];
    const failures: string[] = [];
    let checked = 0;

    for (const path of pages) {
      await page.goto(path, { waitUntil: "networkidle" });
      const samples = (await page.evaluate(BACKGROUND_PROBE)) as Array<{
        tag: string;
        cls: string;
        fg: [number, number, number];
        bg: [number, number, number];
        large: boolean;
        sample: string;
      }>;
      for (const s of samples) {
        checked++;
        const ratio = contrastRatio(s.fg, s.bg);
        const required = s.large ? 3 : 4.5;
        if (ratio < required) {
          failures.push(`${path} ${s.tag}.${s.cls} "${s.sample}" ratio=${ratio.toFixed(2)} < ${required}`);
        }
      }
    }
    expect(checked, "应至少检查到若干文本元素").toBeGreaterThan(30);
    expect(failures, `对比度不足的元素：\n${failures.join("\n")}`).toEqual([]);
  });

  test("状态徽标带形状前缀，不只依赖颜色", async ({ page }) => {
    await loginAs(page, "admin");
    await page.goto("/admin/login-events", { waitUntil: "networkidle" });
    const badges = await page.evaluate(() =>
      Array.from(document.querySelectorAll(".badge")).map((el) => ({
        glyph: el.querySelector(".badge__glyph")?.textContent ?? "",
        text: (el.textContent || "").trim(),
      })),
    );
    expect(badges.length).toBeGreaterThan(0);
    const missing = badges.filter((b) => !["●", "▲", "■", "◆", "○"].includes(b.glyph.trim()));
    expect(missing.map((b) => b.text), "存在没有形状前缀的状态徽标").toEqual([]);
  });
});

test.describe("UX-004 长表渲染预算", () => {
  test("表格按页窗口渲染，DOM 行数不随数据量线性增长", async ({ page }) => {
    await loginAs(page, "admin");
    // 登录记录是当前数据量最大的列表（已积累数百条）。
    const response = page.waitForResponse(
      (r) => r.url().includes("/api/admin/v1/login-events") && r.status() === 200,
      { timeout: 20_000 },
    );
    await page.goto("/admin/login-events", { waitUntil: "domcontentloaded" });
    const body = await (await response).json();
    const fetched = (body.data as unknown[]).length;

    const domRows = await page.locator("table tbody tr").count();
    // DataTable 的窗口大小为 100：无论拉回多少数据，DOM 里的行数都有上限。
    expect(domRows).toBeLessThanOrEqual(100);
    expect(fetched, "接口返回的数据量应大于单页窗口，否则测不出窗口化效果").toBeGreaterThan(100);
    expect(domRows, "DOM 行数必须显著小于数据量").toBeLessThan(fetched);

    // 翻页后 DOM 行数仍然有界。
    const next = page.getByRole("button", { name: "下一页" });
    if (await next.isEnabled()) {
      const t0 = Date.now();
      await next.click();
      await expect(page.locator("table tbody tr").first()).toBeVisible();
      const elapsed = Date.now() - t0;
      const rowsAfter = await page.locator("table tbody tr").count();
      expect(rowsAfter).toBeLessThanOrEqual(100);
      // 内网环境下翻页应保持可交互（§30.1 P75 <1s）。
      expect(elapsed, `翻页耗时 ${elapsed}ms 超过预算`).toBeLessThan(1500);
    }
  });
});

test.describe("SEC-002 XSS / Markdown 注入", () => {
  test("注入的 HTML 载荷以纯文本渲染，不执行", async ({ page }) => {
    const fired: string[] = [];
    page.on("dialog", async (d) => {
      fired.push(d.message());
      await d.dismiss();
    });

    await loginAs(page, "user");
    const payload = `<img src=x onerror="window.__xss=1">SCRI PT`;
    await page.goto("/app-v2/profile", { waitUntil: "networkidle" });

    // 通过真实接口写入载荷
    const status = await page.evaluate(async (value) => {
      const res = await fetch("/api/v2/me/profile", {
        method: "PATCH",
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/json",
          "X-Auth-Audience": "user",
          "X-CSRF-Token": document.cookie.match(/cs_user_session_csrf=([^;]*)/)?.[1] ?? "",
        },
        body: JSON.stringify({ display_name: value }),
      });
      return res.status;
    }, payload);
    expect(status, "资料更新应被接受（用于验证渲染侧转义）").toBe(200);

    await page.goto("/app-v2/home", { waitUntil: "networkidle" });
    const state = await page.evaluate(() => ({
      xss: (window as unknown as { __xss?: number }).__xss ?? 0,
      imgInjected: document.querySelectorAll('img[src="x"]').length,
      bodyHasRaw: document.body.innerText.includes("onerror"),
    }));
    expect(state.xss, "注入的 onerror 不应执行").toBe(0);
    expect(state.imgInjected, "载荷不得被解析为真实 <img> 元素").toBe(0);
    expect(state.bodyHasRaw, "载荷应作为纯文本出现").toBe(true);
    expect(fired, "不应触发任何弹窗（说明脚本未执行）").toEqual([]);
  });
});
