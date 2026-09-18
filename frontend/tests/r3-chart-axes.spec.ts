import { expect, test, type Page } from "@playwright/test";
import { loginAdmin, screenshot, writeJson } from "./r3-helpers";

/**
 * R-3 图表可读性：共享趋势图（APIControl 与 System 两页共用 TrendChart）
 * 的坐标轴刻度**不得被容器裁剪**。
 *
 * 为什么需要单独的用例：`document.scrollWidth <= clientWidth`（无横向滚动）
 * **不能**证明坐标轴数值可读——刻度是在图表内部被裁掉的，页面不会出现横向溢出。
 * 真实缺陷（由截图证据发现）：`grid.left` 固定 48px，而"请求量"刻度是
 * "15,000" 这类四位数文本，宽度超出后被裁成 "15," "12," "9,"（纵轴单位丢失）。
 *
 * 判定口径升级为**几何断言**：趋势图已改用 SVG 渲染器，每个刻度都是真实 <text>；
 * 取每个刻度的 getBoundingClientRect() 与图表容器/SVG 的 rect 比较，
 * 要求 left ≥ 容器 left - 1px 且 right ≤ 容器 right + 1px，
 * 并把每个刻度的**文本与 rect 数值**落盘。
 */

const ADMIN_EMAIL = process.env.E2E_R3_ADMIN_EMAIL ?? "admin@local";
const ADMIN_PASSWORD = process.env.E2E_R3_ADMIN_PASSWORD;
const TOLERANCE = 1;

test.skip(!ADMIN_PASSWORD, "需要 E2E_R3_ADMIN_PASSWORD（后台账号口令）");

type TickBox = {
  text: string;
  role: "y-left" | "y-right" | "x" | "legend" | "other";
  left: number;
  right: number;
  top: number;
  bottom: number;
  width: number;
  height: number;
  clippedLeft: number;
  clippedRight: number;
};

async function measureChart(page: Page) {
  return page.evaluate(() => {
    const host = document.querySelector<HTMLElement>(".ac-chart");
    if (!host) return null;
    const svg = host.querySelector("svg");
    if (!svg) return { renderer: "none", ticks: [] as unknown[], host: null, svg: null };
    const hostRect = host.getBoundingClientRect();
    const svgRect = svg.getBoundingClientRect();
    const ticks = Array.from(svg.querySelectorAll("text")).map((node) => {
      const el = node as SVGTextElement;
      const rect = el.getBoundingClientRect();
      const anchor = el.getAttribute("text-anchor") ?? "";
      const fill = el.getAttribute("fill") ?? "";
      // ECharts SVG 渲染器的锚点约定：
      //   左轴刻度 text-anchor=end（右对齐到轴线）、右轴刻度 start（左对齐）、
      //   X 轴分类标签 middle、图例 start 且贴在顶部。
      let role: string = "other";
      if (anchor === "end") role = "y-left";
      else if (anchor === "middle") role = "x";
      else if (anchor === "start") role = rect.top > hostRect.top + 44 ? "y-right" : "legend";
      return {
        text: el.textContent ?? "",
        role,
        anchor,
        fill,
        left: Number(rect.left.toFixed(2)),
        right: Number(rect.right.toFixed(2)),
        top: Number(rect.top.toFixed(2)),
        bottom: Number(rect.bottom.toFixed(2)),
        width: Number(rect.width.toFixed(2)),
        height: Number(rect.height.toFixed(2)),
        clippedLeft: Number((hostRect.left - rect.left).toFixed(2)),
        clippedRight: Number((rect.right - hostRect.right).toFixed(2)),
      };
    });
    return {
      renderer: "svg",
      host: { left: hostRect.left, right: hostRect.right, top: hostRect.top, bottom: hostRect.bottom, width: hostRect.width, height: hostRect.height },
      svg: { left: svgRect.left, right: svgRect.right, top: svgRect.top, bottom: svgRect.bottom, width: svgRect.width, height: svgRect.height },
      ticks,
    };
  });
}

const ROUTES: Array<{ path: string; name: string }> = [
  { path: "/api-control", name: "api-control" },
  { path: "/system", name: "system" },
];

test.describe("R-3 共享趋势图坐标轴不被裁剪", () => {
  test.beforeEach(async ({ page }) => {
    await loginAdmin(page, ADMIN_EMAIL, ADMIN_PASSWORD!);
  });

  for (const route of ROUTES) {
    test(`${route.name}：趋势图刻度完整落在容器内（几何断言）`, async ({ page }, testInfo) => {
      await page.goto(route.path, { waitUntil: "domcontentloaded" });
      await expect(page.locator(".ac-chart svg")).toBeVisible({ timeout: 30_000 });
      // 等图表完成首次布局（数据 + resize）。
      await expect.poll(async () => (await measureChart(page))?.ticks.length ?? 0, { timeout: 30_000 }).toBeGreaterThan(4);
      await page.waitForTimeout(600);

      const measured = await measureChart(page);
      expect(measured, "必须能取到 .ac-chart 的 SVG 渲染结果").not.toBeNull();
      const ticks = (measured!.ticks as TickBox[]).filter((t) => t.role !== "other");
      const axisTicks = ticks.filter((t) => t.role === "y-left" || t.role === "y-right");
      const xTicks = ticks.filter((t) => t.role === "x");

      const outOfBounds = ticks.filter(
        (t) => t.left < measured!.host!.left - TOLERANCE || t.right > measured!.host!.right + TOLERANCE,
      );

      // "修复前"状态的**可复算推导**：旧实现 grid.left=48px / grid.right=24px 都是固定值，
      // 刻度文本宽度是渲染后实测的 —— 只要 (最宽刻度 + 8px 间隙) 超过对应侧的固定预算，
      // 该侧刻度必然被容器裁掉（截图里右轴只剩 "15," "12," "9,"）。
      const maxLeftLabelWidth = axisTicks.filter((t) => t.role === "y-left").reduce((max, t) => Math.max(max, t.width), 0);
      const maxRightLabelWidth = axisTicks.filter((t) => t.role === "y-right").reduce((max, t) => Math.max(max, t.width), 0);
      const legacyGridLeft = 48;
      const legacyGridRight = 24;
      const legacyGap = 8;
      const maxYLabelWidth = Math.max(maxLeftLabelWidth, maxRightLabelWidth);

      writeJson(`chart-axes-${testInfo.project.name}-${route.name}.json`, {
        route: route.path,
        renderer: measured!.renderer,
        tolerance_px: TOLERANCE,
        container: measured!.host,
        svg: measured!.svg,
        y_axis_tick_texts: axisTicks.map((t) => t.text),
        y_axis_tick_count: axisTicks.length,
        x_axis_tick_texts: xTicks.map((t) => t.text),
        x_axis_tick_count: xTicks.length,
        ticks,
        out_of_bounds: outOfBounds,
        legacy_budget_check: {
          note: "修复前 grid.left=48px / grid.right=24px 固定值；下面是同一份渲染文本的实测宽度，用于复算旧预算在哪一侧必然裁剪。",
          legacy_tick_gap_px: legacyGap,
          left_axis: {
            legacy_budget_px: legacyGridLeft,
            max_label_text: axisTicks.filter((t) => t.role === "y-left").reduce((a, t) => (t.width > (a?.width ?? -1) ? t : a), null as TickBox | null)?.text ?? null,
            max_label_width_px: Number(maxLeftLabelWidth.toFixed(2)),
            required_px: Number((maxLeftLabelWidth + legacyGap).toFixed(2)),
            would_have_clipped: maxLeftLabelWidth + legacyGap > legacyGridLeft,
          },
          right_axis: {
            legacy_budget_px: legacyGridRight,
            max_label_text: axisTicks.filter((t) => t.role === "y-right").reduce((a, t) => (t.width > (a?.width ?? -1) ? t : a), null as TickBox | null)?.text ?? null,
            max_label_width_px: Number(maxRightLabelWidth.toFixed(2)),
            required_px: Number((maxRightLabelWidth + legacyGap).toFixed(2)),
            would_have_clipped: maxRightLabelWidth + legacyGap > legacyGridRight,
            symptom_matches_screenshot: "右轴 formatter 为 {value}ms，旧预算 24px 不足以容纳 \"12000ms\"，被容器右缘裁成 \"12,\" 一类残段（与截图一致）。",
          },
          max_y_label_width_px: Number(maxYLabelWidth.toFixed(2)),
          would_have_clipped: maxLeftLabelWidth + legacyGap > legacyGridLeft || maxRightLabelWidth + legacyGap > legacyGridRight,
        },
        verdict: outOfBounds.length === 0 ? "axes fully inside container" : "axis label clipped",
      });

      await screenshot(page, `chart-axes-${testInfo.project.name}-${route.name}.png`);

      expect(axisTicks.length, "必须存在可度量的 Y 轴刻度文本（否则刻度无法被验收）").toBeGreaterThan(2);
      expect(xTicks.length, "必须存在可度量的 X 轴刻度文本").toBeGreaterThan(0);
      expect(
        outOfBounds,
        `以下刻度超出图表容器（被裁剪）：${JSON.stringify(outOfBounds.map((t) => ({ text: t.text, role: t.role, clippedLeft: t.clippedLeft, clippedRight: t.clippedRight })))}`,
      ).toEqual([]);
    });
  }
});
