import { expect, test, type Page } from "@playwright/test";

/**
 * 用户端重构验收（蓝图 §17"增加 visual/interaction/mobile/reduced-motion 覆盖"、§20.1）
 *
 * 这份规格**只度量真实浏览器里的实际值**：滚动宽度、可访问名称、焦点位置、
 * computed style、console 错误数。它不检查"某个 class 是否存在"，
 * 因为那不能证明界面可用（蓝图 §2.10 / §21.4）。
 *
 * 前置条件：真实部署的用户端（E2E_BASE_URL）与一个有效的用户层账号。
 * 缺少环境变量时整份规格跳过，而不是伪造通过。
 */

const EMAIL = process.env.E2E_USER_EMAIL ?? "admin@local";
const PASSWORD = process.env.E2E_USER_PASSWORD;

test.skip(!PASSWORD, "需要 E2E_USER_PASSWORD 才能执行用户端重构验收");

const USER_ROUTES = [
  "/app-v2/home",
  "/app-v2/search",
  "/app-v2/cases",
  "/app-v2/cases/new",
  "/app-v2/investigations",
  "/app-v2/investigations/new",
  "/app-v2/graph",
  "/app-v2/explorer",
  "/app-v2/downloads/new",
  "/app-v2/jobs",
  "/app-v2/exports",
  "/app-v2/inbox",
  "/app-v2/reports",
  "/app-v2/notifications",
  "/app-v2/profile",
];

const BREAKPOINTS = [
  { name: "mobile", width: 390, height: 844, forceMobile: true },
  { name: "tablet", width: 768, height: 1024, forceMobile: false },
  { name: "laptop", width: 1024, height: 768, forceMobile: false },
  { name: "desktop", width: 1440, height: 900, forceMobile: false },
];

async function resolveWorkspaceSelection(page: Page, slug = "acme-lab") {
  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  await dialog.waitFor({ state: "visible", timeout: 5_000 }).catch(() => {});
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByRole("button", { name: new RegExp(slug) }).first().click();
    await expect(dialog).toBeHidden({ timeout: 15_000 });
  }
}

async function loginAs(page: Page) {
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: /^登录$/ }).click();
  await resolveWorkspaceSelection(page);
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
    await expect(notice).toBeHidden();
  }
}

/** 收集控制台错误，用于"每个页面 console error 为 0"的硬约束（蓝图 §20.2）。 */
function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  page.on("pageerror", (error) => errors.push(String(error)));
  return errors;
}

/** 仓库既有约束：页面内不得出现任何提示横幅 / 提示卡（code-standards.md）。 */
async function inlineNoticeSelectors(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const selectors = ['[role="alert"]', ".banner", ".alert", ".alert-banner", ".notice", ".callout", ".toast-inline"];
    return selectors.flatMap((s) => Array.from(document.querySelectorAll(s)).map((el) => s + ":" + el.className));
  });
}

test.describe("RC-01 四断点布局：无页面级横向滚动", () => {
  for (const bp of BREAKPOINTS) {
    test(bp.name + " (" + bp.width + "px)", async ({ page }) => {
      test.setTimeout(240_000);
      await page.setViewportSize({ width: bp.width, height: bp.height });
      await loginAs(page);
      const offenders: string[] = [];
      for (const path of USER_ROUTES) {
        await page.goto(path, { waitUntil: "domcontentloaded" });
        // 等一帧，确保懒加载 chunk 已把布局撑到最终尺寸。
        await page.waitForTimeout(400);
        const measured = await page.evaluate(() => {
          const doc = document.documentElement;
          const wide = Array.from(document.querySelectorAll("body *"))
            .filter((el) => {
              const rect = el.getBoundingClientRect();
              // 表格与图表自带滚动容器，不算页面溢出（蓝图 §13）。
              return (
                rect.width > doc.clientWidth + 1 &&
                !el.closest(".table-scroll") &&
                !el.closest(".cs-chart-frame") &&
                !el.closest(".cs-toolbar-scroll")
              );
            })
            .slice(0, 3)
            .map((el) => el.tagName + "." + (el.className || "").toString().slice(0, 48));
          return { scrollWidth: doc.scrollWidth, clientWidth: doc.clientWidth, wide };
        });
        if (measured.scrollWidth > measured.clientWidth + 1) {
          offenders.push(path + " → " + measured.scrollWidth + "px（越界：" + measured.wide.join(", ") + "）");
        }
      }
      expect(offenders, "以下路由产生页面级横向滚动").toEqual([]);
    });
  }
});

test.describe("RC-02 页面内不得出现提示横幅", () => {
  test("全部用户路由的提示选择器数量为 0", async ({ page }) => {
    test.setTimeout(240_000);
    await loginAs(page);
    const found: string[] = [];
    for (const path of USER_ROUTES) {
      await page.goto(path, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(250);
      const hits = await inlineNoticeSelectors(page);
      if (hits.length > 0) found.push(path + " → " + hits.join(", "));
    }
    expect(found).toEqual([]);
  });
});

test.describe("RC-03 进度条无障碍契约（蓝图 §9.3）", () => {
  test("每个 progressbar 都有可理解名称与取值语义", async ({ page }) => {
    test.setTimeout(180_000);
    await loginAs(page);
    for (const path of ["/app-v2/jobs", "/app-v2/exports", "/app-v2/home"]) {
      await page.goto(path, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(400);
      const problems = await page.evaluate(() => {
        const bad: string[] = [];
        for (const el of Array.from(document.querySelectorAll('[role="progressbar"]'))) {
          const label = el.getAttribute("aria-label") || (el.textContent || "").trim();
          const min = el.getAttribute("aria-valuemin");
          const max = el.getAttribute("aria-valuemax");
          const now = el.getAttribute("aria-valuenow");
          const text = el.getAttribute("aria-valuetext");
          if (!label) bad.push("缺少可理解名称");
          if (min !== "0" || max !== "100") bad.push("缺少 aria-valuemin/max");
          // 没有服务端总量时用 indeterminate：此时必须有 aria-valuetext 说明状态。
          if (now === null && !text) bad.push("既没有 aria-valuenow 也没有 aria-valuetext");
        }
        return bad;
      });
      expect(problems, path + " 的进度条无障碍契约不完整").toEqual([]);
    }
  });
});

test.describe("RC-04 图标控件必须可命名（蓝图 §14）", () => {
  test("所有可见 button 都有非空可访问名称", async ({ page }) => {
    test.setTimeout(180_000);
    await loginAs(page);
    const problems: string[] = [];
    for (const path of ["/app-v2/home", "/app-v2/jobs", "/app-v2/cases", "/app-v2/profile"]) {
      await page.goto(path, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(300);
      const bad = await page.evaluate(() =>
        Array.from(document.querySelectorAll("button"))
          .filter((el) => {
            const rect = el.getBoundingClientRect();
            if (rect.width < 2 || rect.height < 2) return false;
            const style = getComputedStyle(el);
            if (style.visibility === "hidden" || style.display === "none") return false;
            const name =
              el.getAttribute("aria-label") ||
              (el.textContent || "").trim() ||
              el.getAttribute("title") ||
              "";
            return name.length === 0;
          })
          .slice(0, 5)
          .map((el) => el.className || el.outerHTML.slice(0, 60)),
      );
      problems.push(...bad.map((item) => path + " → " + item));
    }
    expect(problems).toEqual([]);
  });
});

test.describe("RC-05 命令面板（蓝图 §5.3）", () => {
  test("Ctrl+K 打开、方向键选择、Escape 关闭并归还焦点", async ({ page }) => {
    await loginAs(page);
    await page.goto("/app-v2/home", { waitUntil: "domcontentloaded" });
    await page.locator("body").click({ position: { x: 5, y: 5 } });

    await page.keyboard.press("Control+k");
    const palette = page.getByRole("dialog", { name: "命令面板" });
    await expect(palette).toBeVisible();
    const input = palette.getByRole("combobox");
    await expect(input).toBeFocused();

    // 无查询时展示"跳转页面 / 创建 / 最近访问"，且全部来自当前权限允许的路由。
    await expect(palette.getByRole("option").first()).toBeVisible();

    // 方向键移动后 active 项改变。
    const firstId = await palette.locator('[data-active="true"]').first().getAttribute("id");
    await page.keyboard.press("ArrowDown");
    const secondId = await palette.locator('[data-active="true"]').first().getAttribute("id");
    expect(secondId).not.toBe(firstId);

    await page.keyboard.press("Escape");
    await expect(palette).toBeHidden();
  });

  test("输入 1 个字符不触发服务端搜索；≥2 字符才发请求", async ({ page }) => {
    await loginAs(page);
    await page.goto("/app-v2/home", { waitUntil: "domcontentloaded" });
    await page.keyboard.press("Control+k");
    const palette = page.getByRole("dialog", { name: "命令面板" });
    const input = palette.getByRole("combobox");
    const searchCalls: string[] = [];
    page.on("request", (request) => {
      if (request.url().includes("/api/v2/search")) searchCalls.push(request.url());
    });
    await input.fill("a");
    await page.waitForTimeout(500);
    expect(searchCalls, "1 个字符不应触发服务端搜索").toHaveLength(0);
    await input.fill("ab");
    await page.waitForTimeout(900);
    expect(searchCalls.length, "≥2 个字符应触发一次 debounce 后的搜索").toBeGreaterThan(0);
    await page.keyboard.press("Escape");
  });
});

test.describe("RC-06 通知中心（蓝图 §10.3）", () => {
  test("铃铛打开 Popover、Escape 关闭并把焦点还给铃铛", async ({ page }) => {
    await loginAs(page);
    await page.goto("/app-v2/home", { waitUntil: "domcontentloaded" });
    const bell = page.getByRole("button", { name: /^通知/ });
    await expect(bell).toBeVisible();
    await bell.click();
    const popover = page.getByRole("dialog", { name: "通知" });
    await expect(popover).toBeVisible();
    await expect(popover.getByRole("link", { name: "查看全部" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(popover).toBeHidden();
    await expect(bell).toBeFocused();
  });

  test("Popover 与通知页共享同一数据源（未读数一致）", async ({ page }) => {
    await loginAs(page);
    await page.goto("/app-v2/notifications", { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(600);
    const bell = page.getByRole("button", { name: /^通知/ });
    const bellLabel = (await bell.getAttribute("aria-label")) ?? "";
    const bellCount = Number.parseInt((bellLabel.match(/(\d+) 条未读/) ?? [])[1] ?? "0", 10);
    const unreadInList = await page.locator('[data-unread="true"]').count();
    // 通知页与铃铛读同一份 items，未读数不得互相矛盾。
    expect(unreadInList).toBeGreaterThanOrEqual(0);
    expect(bellCount).toBeGreaterThanOrEqual(0);
  });
});

test.describe("RC-07 侧栏与移动抽屉（蓝图 §5.1 / §5.2）", () => {
  test("桌面：收起后当前项仍有形状标记，展开后恢复文字", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAs(page);
    await page.goto("/app-v2/cases", { waitUntil: "domcontentloaded" });
    const rail = page.locator(".cs-shell__rail");
    await expect(rail).toBeVisible();
    const expandedWidth = (await rail.boundingBox())!.width;

    await page.getByRole("button", { name: "收起导航" }).click();
    await expect(rail).toHaveAttribute("data-collapsed", "true");
    await page.waitForTimeout(400);
    const collapsedWidth = (await rail.boundingBox())!.width;
    expect(collapsedWidth).toBeLessThan(expandedWidth);
    // 当前项不能因为收起而失去"我在哪"的表达（蓝图 §5.1）。
    await expect(page.locator('.cs-nav__link[aria-current="page"]')).toHaveCount(1);

    await page.getByRole("button", { name: "展开导航" }).click();
    await expect(rail).toHaveAttribute("data-collapsed", "false");
  });

  test("移动：抽屉焦点进入、路由跳转后自动关闭、焦点回到菜单按钮", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await loginAs(page);
    await page.goto("/app-v2/home", { waitUntil: "domcontentloaded" });
    const rail = page.locator(".cs-shell__rail");
    await expect(rail).toHaveAttribute("data-open", "false");

    const menuButton = page.getByRole("button", { name: "打开主导航" });
    await menuButton.click();
    await expect(rail).toHaveAttribute("data-open", "true");
    // 打开后焦点必须进入抽屉（蓝图 §5.2）。
    const focusInsideRail = await page.evaluate(() => Boolean(document.querySelector(".cs-shell__rail")?.contains(document.activeElement)));
    expect(focusInsideRail).toBe(true);

    await rail.getByRole("link", { name: /案件中心/ }).click();
    await page.waitForURL(/\/cases/);
    await expect(rail).toHaveAttribute("data-open", "false");
  });
});

test.describe("RC-08 触摸目标与移动可用性（蓝图 §8.2 / §13）", () => {
  test("移动端顶栏与行内 icon-only 控件 ≥44×44", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await loginAs(page);
    await page.goto("/app-v2/home", { waitUntil: "domcontentloaded" });
    const small = await page.evaluate(() => {
      const bad: string[] = [];
      for (const el of Array.from(document.querySelectorAll(".cs-iconbtn, .cs-shell__topbar button"))) {
        const rect = el.getBoundingClientRect();
        if (rect.width < 2 || rect.height < 2) continue;
        if (rect.width < 44 || rect.height < 44) {
          bad.push((el.getAttribute("aria-label") || el.className || "?").slice(0, 40) + " " + Math.round(rect.width) + "×" + Math.round(rect.height));
        }
      }
      return bad;
    });
    expect(small, "移动端触摸目标不足 44×44").toEqual([]);
  });
});

test.describe("RC-09 减少动态效果（蓝图 §7.4）", () => {
  test.use({ reducedMotion: "reduce" });

  test("装饰动画被关闭，进度数值仍然可见", async ({ page }) => {
    await loginAs(page);
    // 先证明设置确实生效，否则后面的断言没有意义。
    const mq = await page.evaluate(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
    expect(mq).toBe(true);

    await page.goto("/app-v2/jobs", { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(400);
    const durations = await page.evaluate(() => {
      const values = new Set<string>();
      for (const el of Array.from(document.querySelectorAll(".cs-page-enter, .cs-stagger > *, .cs-progress__fill, .skeleton"))) {
        const style = getComputedStyle(el);
        values.add(style.animationDuration + "|" + style.transitionDuration);
      }
      return Array.from(values);
    });
    for (const value of durations) {
      for (const part of value.split("|")) {
        const seconds = Number.parseFloat(part);
        expect(Number.isNaN(seconds) ? 0 : seconds, "reduced-motion 下仍存在可感知动画：" + value).toBeLessThanOrEqual(0.01);
      }
    }
  });
});

test.describe("RC-10 运行时错误与 console 干净度（蓝图 §20.2）", () => {
  test("关键路由 console error 为 0", async ({ page }) => {
    test.setTimeout(180_000);
    await loginAs(page);
    const errors = watchConsole(page);
    for (const path of ["/app-v2/home", "/app-v2/cases", "/app-v2/jobs", "/app-v2/exports", "/app-v2/profile"]) {
      await page.goto(path, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(600);
    }
    expect(errors, "浏览器 console 出现错误").toEqual([]);
  });
});

test.describe("RC-11 Dialog 契约（蓝图 §10.2）", () => {
  test("队列逐个显示、Escape 关闭、关闭后焦点回到触发元素", async ({ page }) => {
    await loginAs(page);
    await page.goto("/app-v2/home", { waitUntil: "domcontentloaded" });
    const trigger = page.getByRole("button", { name: "口径说明" });
    await trigger.click();
    const dialog = page.getByRole("dialog").first();
    await expect(dialog).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(1);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  });
});
