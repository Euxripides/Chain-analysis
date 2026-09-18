import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * R10 — 服务端 excelize XLSX 导出 + legacy 客户端 xlsx 导出/导入 专用配置。
 *
 * outputDir 固定到当轮唯一证据目录：Playwright 每次运行都会清空 outputDir
 * （v1 轮就是因此丢了截图/trace，见 EVIDENCE-LOSS-RECORD.txt）。R10 必须显式设置，
 * 不允许再出现共享 test-results 被覆盖的情况。
 */
const OUT = process.env.R10_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /r10-.*\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-r10-unspecified",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: OUT
    ? [["list"], ["json", { outputFile: path.join(OUT, "playwright-report.json") }]]
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
