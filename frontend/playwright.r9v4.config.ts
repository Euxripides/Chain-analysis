import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * R9 v3 — U-DL-01 /downloads/new（app-v2）导入验收专用配置。
 * outputDir 固定到当轮唯一证据目录：Playwright 会在每次运行开始时清空 outputDir，
 * v2 就是因为没有设置它而清掉了 v1 的 test-results（见 EVIDENCE-LOSS-RECORD.txt）。
 */
const OUT = process.env.R9_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /r9-uc-download-import-v4\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-r9v3-unspecified",
  fullyParallel: false,
  workers: 1,
  reporter: OUT
    ? [["list"], ["json", { outputFile: path.join(OUT, "playwright-report.json") }]]
    : [["list"]],
  timeout: 180_000,
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
