import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import path from "node:path";

const outputDir = process.env.E2E_SCREENSHOT_DIR;

function browserErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(`pageerror: ${error.message}`));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(`console: ${message.text()}`);
  });
  return errors;
}

async function openInvestigationByTitle(page: Page, title: string) {
  await expect(page.locator(".iw-history-list button").first()).toBeVisible({
    timeout: 20_000,
  });
  for (let pageIndex = 0; pageIndex < 8; pageIndex += 1) {
    const task = page.getByText(title, { exact: true }).first();
    if (await task.count()) {
      await task.click();
      return;
    }
    const next = page.getByLabel("下一页");
    if (!(await next.count()) || (await next.isDisabled())) break;
    await next.click();
    await expect(page.locator(".iw-history-list button").first()).toBeVisible();
  }
  throw new Error(`Investigation not found: ${title}`);
}

test("fund flow is an independent, interactive, evidence-backed workspace", async ({
  page,
}, testInfo) => {
  const errors = browserErrors(page);
  await page.goto("/fund-flow");
  await expect(page.getByText("2352", { exact: true }).first()).toBeVisible({
    timeout: 20_000,
  });
  await expect(page.locator(".react-flow__node")).toHaveCount(101);
  await page.getByRole("button", { name: "上游" }).click();
  await expect(page.locator(".flow-node-out")).toHaveCount(0);
  await page.getByRole("button", { name: "全局视图" }).click();
  const majorFlow = page.getByRole("button", { name: "大额流向" });
  await majorFlow.click();
  await expect(majorFlow).toHaveClass(/active/);
  await expect(page.locator(".react-flow__node")).toHaveCount(9);
  await page
    .locator(".react-flow__node")
    .nth(1)
    .evaluate((node) => (node as HTMLElement).click());
  await expect(page.getByRole("heading", { name: "关系详情" })).toBeVisible();
  await expect(page.locator(".flow-edge.is-focused")).toHaveCount(1);
  await expect(page.locator(".flow-edge.is-muted")).toHaveCount(7);
  const focusedPath = page.locator(".flow-edge.is-focused path").first();
  await expect.poll(() => focusedPath.evaluate((path) => getComputedStyle(path).animationName)).toBe("flow-money-travel");
  await expect(page.locator(".flow-evidence-list code").first()).toBeVisible();
  await page.getByLabel("收起详情").click();
  await expect(page.getByLabel("展开详情")).toBeVisible();
  if (testInfo.project.name === "desktop") {
    await page.getByLabel("展开详情").click();
  }
  if (outputDir) {
    mkdirSync(outputDir, { recursive: true });
    await page.screenshot({
      path: path.join(outputDir, `fund-flow-${testInfo.project.name}.png`),
      fullPage: true,
    });
  }
  expect(errors).toEqual([]);
});

test("smart investigation does not embed the fund graph", async ({ page }) => {
  await page.goto("/investigations");
  await expect(page.getByRole("heading", { name: "调查工作台" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "启动调查" })).toBeVisible();
  await expect(page.getByLabel("调查目的")).toBeVisible();
  await expect(page.getByText("期望结果", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "这是一个大额获利地址，寻找最终资金沉淀" }).click();
  await expect(page.getByLabel("调查目的")).toHaveValue("这是一个大额获利地址，寻找最终资金沉淀");
  await page.getByText("资金流图", { exact: true }).click();
  await page.getByRole("button", { name: "启动调查", exact: true }).click();
  await expect(page.getByRole("heading", { name: "缺少目标地址" })).toBeVisible();
  await page.getByRole("button", { name: "我知道了" }).click();
  await expect(page.locator(".react-flow")).toHaveCount(0);
  await expect(page.getByText("资金流画布")).toHaveCount(0);
});

test("smart investigation renders durable finding, evidence and graph patch", async ({ page }) => {
  await page.goto("/investigations");
  await openInvestigationByTitle(page, "验证持久化资金分析证据闭环");
  await expect(page.getByText("一跳资金关系已完成", { exact: true })).toBeVisible({ timeout: 15_000 });
  await expect(page.locator(".iw-metrics").getByText("100", { exact: true })).toBeVisible();
  await expect(page.locator(".iw-metrics").getByText("r1", { exact: true })).toBeVisible();
});

test("launch button creates and immediately starts a real investigation", async ({ page }, testInfo) => {
  const objective = `启动语义验收-${testInfo.project.name}-${Date.now()}`;
  await page.goto("/investigations");
  await page.getByLabel("目标地址").fill("TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m");
  await page.getByLabel("区块链").selectOption("tron");
  await page.getByLabel("调查目的").fill(objective);
  await page.getByText("资金流图", { exact: true }).click();
  await page.getByRole("button", { name: "启动调查", exact: true }).click();
  await expect(page.getByRole("heading", { name: "调查已启动" })).toBeVisible({ timeout: 25_000 });
  await page.getByRole("button", { name: "我知道了" }).click();
  await expect(page.getByText(objective, { exact: true }).first()).toBeVisible();
  await expect(page.locator(".iw-history-list").getByText(objective, { exact: true })).toBeVisible({ timeout: 20_000 });
});

test("phase 4 semantic projection is visible and rebuildable", async ({ page }) => {
  await page.goto("/investigations");
  await openInvestigationByTitle(page, "Phase 4 Semantic Engine 验收");
  await expect(page.getByRole("heading", { name: "协议语义" })).toBeVisible();
  await expect(page.getByText("protocol.semantic.decode", { exact: true })).toBeVisible();
  await expect(page.getByText("0 条语义事件", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "重建投影" }).click();
  await expect(page.getByRole("heading", { name: "协议语义已重建" })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(/投影 1.0，算法 protocol-semantic-v1.0/)).toBeVisible();
});

test("phase 5 advanced analysis keeps facts and inference boundaries visible", async ({ page }) => {
  const errors = browserErrors(page);
  await page.goto("/investigations");
  await openInvestigationByTitle(page, "Phase 4 Semantic Engine 验收");
  await expect(page.getByRole("heading", { name: "高级调查" })).toBeVisible();
  await page.getByRole("button", { name: "重新分析" }).click();
  await expect(page.getByRole("heading", { name: "高级分析已重建" })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(/净流量不等于已实现利润/)).toBeVisible();
  await page.getByRole("button", { name: "我知道了" }).click();
  await expect(page.getByText("USDT", { exact: true }).last()).toBeVisible();
  await expect(page.locator(".iw-related").getByText(/推断/).first()).toBeVisible();
  expect(errors).toEqual([]);
});

test("phase 6 generates and downloads an evidence-indexed report", async ({ page }) => {
  const errors = browserErrors(page);
  await page.goto("/investigations");
  await openInvestigationByTitle(page, "Phase 5 Advanced Investigation 验收");
  await page.getByRole("button", { name: "生成报告" }).click();
  await expect(page.getByRole("heading", { name: "调查报告已生成" })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(/SHA-256/)).toBeVisible();
  await page.getByRole("button", { name: "我知道了" }).click();
  const link = page.locator(".iw-reports a").first();
  await expect(link).toBeVisible();
  const href = await link.getAttribute("href");
  const response = await page.request.get(href!);
  expect(response.ok()).toBeTruthy();
  expect(response.headers()["content-type"]).toContain("text/markdown");
  const report = await response.text();
  expect(report).toContain("# 调查报告");
  expect(report).toContain("## Evidence Index");
  expect(report).toContain("净流量不等于已实现利润");
  expect(errors).toEqual([]);
});

test("phase 7 adds a typed user clue without presenting it as verified OSINT", async ({ page }, testInfo) => {
  const errors = browserErrors(page);
  const domain = `evidence-${testInfo.project.name}-${Date.now()}.example`;
  await page.goto("/investigations");
  await openInvestigationByTitle(page, "Phase 5 Advanced Investigation 验收");
  await page.getByLabel("线索类型").selectOption("DOMAIN");
  await page.getByLabel("线索内容").fill(domain);
  await page.getByRole("button", { name: "加入线索" }).click();
  await expect(page.getByRole("heading", { name: "调查线索已加入" })).toBeVisible();
  await expect(page.getByText(/USER 来源线索/)).toBeVisible();
  await page.getByRole("button", { name: "我知道了" }).click();
  const displayedDomain = `${domain.slice(0, 10)}…${domain.slice(-8)}`;
  await expect(page.locator(".iw-subject-list").getByText(displayedDomain, { exact: true }).last()).toBeVisible();
  await expect(page.locator(".iw-subject-list").getByText("USER · 100%", { exact: true }).last()).toBeVisible();
  expect(errors).toEqual([]);
});

test("case canvas can be created, branched and snapshotted", async ({ page }, testInfo) => {
  const errors = browserErrors(page);
  const suffix = `${testInfo.project.name}-${Date.now()}`;
  await page.goto("/fund-flow?investigation=ee66c8c6-7c8c-4ef0-9136-da50bc388025");
  await expect(page.getByText("2352", { exact: true }).first()).toBeVisible({ timeout: 20_000 });
  await page.getByRole("button", { name: "新建案件" }).click();
  await page.getByLabel("名称").fill(`画布验收-${suffix}`);
  await page.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByRole("heading", { name: "案件已创建" })).toBeVisible();
  await page.getByRole("button", { name: "我知道了" }).click();
  await page.getByRole("button", { name: "新建画布" }).click();
  await page.getByLabel("名称").fill(`主画布-${suffix}`);
  await page.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByRole("heading", { name: "画布已创建" })).toBeVisible();
  await page.getByRole("button", { name: "我知道了" }).click();
  await page.locator(".react-flow__node").nth(1).click({ force: true });
  await page.getByRole("button", { name: "从选择分支" }).click();
  await page.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByRole("heading", { name: "分支画布已创建" })).toBeVisible();
  await page.getByRole("button", { name: "我知道了" }).click();
  await page.getByRole("button", { name: "保存快照" }).click();
  await page.getByRole("button", { name: "保存快照", exact: true }).last().click();
  await expect(page.getByRole("heading", { name: "快照已固化" })).toBeVisible();
  expect(errors).toEqual([]);
});
