import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * P1 工作区作用域闸门 v2 专用配置（P1-20260913）。
 * v1 轮（playwright.p1.config.ts + p1-workspace-scope.spec.ts）真实运行并保留：
 * 6 passed / 1 failed（P1-B3 因测试侧漏传 Idempotency-Key 得 400），v2 只修正该测试侧缺陷。
 *
 * reporter 只在这里声明，不传 CLI --reporter（CLI 会覆盖 config，json 会落到 stdout）。
 */
const OUT = process.env.R10_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /p1-workspace-scope-v2\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-p1v2-unspecified",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: OUT
    ? [["list"], ["json", { outputFile: path.join(OUT, "playwright-p1v2-report.json") }]]
    : [["list"]],
  timeout: 600_000,
  expect: { timeout: 25_000 },
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448",
    channel: process.env.E2E_BROWSER_CHANNEL ?? (process.env.E2E_CHROMIUM_PATH ? undefined : "msedge"),
    launchOptions: process.env.E2E_CHROMIUM_PATH ? { executablePath: process.env.E2E_CHROMIUM_PATH } : undefined,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    ignoreHTTPSErrors: true,
    acceptDownloads: true,
  },
  projects: [
    { name: "uc-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    { name: "uc-mobile", use: { ...devices["Desktop Chrome"], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
});
