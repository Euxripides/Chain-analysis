import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * 新建数据下载 Flowline 专项验收配置（《新建数据下载 Flowline 界面重构实施规范》§25.2 / §29）。
 *
 * 与 r9v4 一致：**真实浏览器 + 真实服务端 + 真实登录**，
 * 默认打在部署好的用户端（https://127.0.0.1:8448 → nginx → :3001）。
 *
 * 运行：
 *   pnpm exec playwright test tests/download-flowline-redesign.spec.ts --config playwright.dlflow.config.ts --project=uc-desktop
 *   pnpm exec playwright test tests/download-flowline-redesign.spec.ts --config playwright.dlflow.config.ts --project=uc-mobile
 *
 * 缺少凭据时用例会 skip（并在 run 输出里如实标注），不会把 skip 当成 PASS。
 */
const OUT = process.env.DLF_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /download-flowline-redesign\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-dlflow-unspecified",
  fullyParallel: false,
  workers: 1,
  reporter: OUT ? [["list"], ["json", { outputFile: path.join(OUT, "playwright-report.json") }]] : [["list"]],
  timeout: 180_000,
  expect: { timeout: 25_000 },
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "https://127.0.0.1:8448",
    channel: process.env.E2E_BROWSER_CHANNEL ?? (process.env.E2E_CHROMIUM_PATH ? undefined : "msedge"),
    launchOptions: process.env.E2E_CHROMIUM_PATH ? { executablePath: process.env.E2E_CHROMIUM_PATH } : undefined,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    ignoreHTTPSErrors: true,
  },
  projects: [
    { name: "uc-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    { name: "uc-mobile", use: { ...devices["Desktop Chrome"], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
});
