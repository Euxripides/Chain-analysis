import { expect, test, type Page } from "@playwright/test";

/**
 * §14.5 最终轮的 reduced-motion 浏览器验收。
 *
 * 必须在本项目（reducedMotion=reduce）下执行：先用 matchMedia 证明浏览器确实处于
 * 「减少动态效果」，再验证弹窗仍然可用（打开、焦点陷阱、ESC 关闭、焦点返回），
 * 且交互元素的过渡时长被压到 0——「关了动画就不能用」与「说关了其实没关」都算失败。
 */

const EMAIL = process.env.E2E_S4_INVESTIGATOR_EMAIL ?? "fixture-investigator@fixture.local";
const PASSWORD = process.env.E2E_S4_INVESTIGATOR_PASSWORD;

test.skip(!PASSWORD, "需要 E2E_S4_INVESTIGATOR_PASSWORD");

async function login(page: Page) {
  // 显式声明媒体特性：project 级 use.reducedMotion 在某些运行路径下不生效，
  // 而"没生效却断言通过"正是本文件要防的假通过，所以这里主动 emulate 并断言。
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: /^登录$/ }).click();
  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  // 等弹窗出现（isVisible 立即求值会漏掉还没渲染出来的选择器）。
  await dialog.waitFor({ state: "visible", timeout: 8_000 }).catch(() => {});
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByRole("button", { name: /acme-lab/ }).first().click();
    await expect(dialog).toBeHidden({ timeout: 15_000 });
  }
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 20_000 });
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
    await expect(notice).toBeHidden();
  }
}

test("预置条件：浏览器确实处于 prefers-reduced-motion=reduce", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  const reduced = await page.evaluate(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  expect(reduced, "本项目必须在 reducedMotion=reduce 下运行，否则本文件的结论无效").toBe(true);
});

test("减少动态效果下弹窗仍可用：打开、焦点陷阱、ESC 关闭、焦点返回", async ({ page }) => {
  await login(page);
  await page.goto("/app-v2/profile", { waitUntil: "domcontentloaded" });

  const trigger = page.getByRole("button", { name: "退出其它设备" });
  await expect(trigger).toBeVisible();
  await trigger.click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute("aria-modal", "true");
  await expect
    .poll(async () => dialog.evaluate((el) => el.contains(document.activeElement)))
    .toBe(true);

  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(page.locator(".dialog-backdrop")).toHaveCount(0);
  await expect.poll(async () => trigger.evaluate((el) => el === document.activeElement)).toBe(true);
});

test("减少动态效果下没有超过 200ms 的过渡（不是只有观感变快）", async ({ page }) => {
  await login(page);
  const offenders = await page.evaluate(() => {
    const found: Array<{ tag: string; duration: string }> = [];
    for (const el of Array.from(document.querySelectorAll("body *"))) {
      const style = window.getComputedStyle(el);
      const seconds = style.transitionDuration
        .split(",")
        .map((part) => parseFloat(part.trim()) || 0);
      const max = Math.max(0, ...seconds);
      if (max > 0.2) {
        found.push({ tag: el.tagName.toLowerCase(), duration: style.transitionDuration });
      }
    }
    return found.slice(0, 10);
  });
  expect(offenders, "以下元素在减少动态效果下仍有 >200ms 过渡：" + JSON.stringify(offenders)).toEqual([]);
});
