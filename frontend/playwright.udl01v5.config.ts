import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * U-DL-01 v5 专用配置（新文件；r9/r10 的配置原样保留）。
 * reporter 只在 config 里声明：给 CLI 传 --reporter 会覆盖 config，json 就写不进文件（R10 v1 的真实缺陷）。
 */
const OUT = process.env.UDL01_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /udl01-worker-isolation-v5\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-udl01v5-unspecified",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: OUT ? [["list"], ["json", { outputFile: path.join(OUT, "playwright-report.json") }]] : [["list"]],
  timeout: 600_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448",
    channel: process.env.E2E_BROWSER_CHANNEL ?? (process.env.E2E_CHROMIUM_PATH ? undefined : "msedge"),
    launchOptions: process.env.E2E_CHROMIUM_PATH ? { executablePath: process.env.E2E_CHROMIUM_PATH } : undefined,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    ignoreHTTPSErrors: true,
    acceptDownloads: true,
  },
  projects: [{ name: "uc-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } }],
});
