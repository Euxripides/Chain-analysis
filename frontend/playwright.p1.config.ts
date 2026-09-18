import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * P1 工作区作用域闸门专用配置（P1-20260913）。
 *
 * 与 r10v2 配置同样的 reporter 纪律：只在这里声明 reporter，**不要**给 CLI 传 --reporter
 * （CLI 的 --reporter 会覆盖 config，json 会写到 stdout 而不是 outputFile）。
 * outputDir 固定到当轮唯一证据目录，避免 Playwright 每次运行清空目录。
 */
const OUT = process.env.R10_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /p1-workspace-scope\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-p1-unspecified",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: OUT
    ? [["list"], ["json", { outputFile: path.join(OUT, "playwright-p1-report.json") }]]
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
