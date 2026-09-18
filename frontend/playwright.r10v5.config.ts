import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * R10 v5 — 服务端 excelize XLSX 导出 + legacy 客户端导出/导入 + 列表契约 + 跨工作区作用域 专用配置。
 *
 * v4（playwright.r10v4.config.ts）原样保留，不 hot-edit。v5 相对 v4 只有两处机械变更：
 *   testMatch r10v3-* -> r10v4-*；outputDir 未指定时的占位目录名同名替换。其余逐字节相同。
 *
 * outputDir 固定到当轮唯一证据目录：Playwright 每次运行都会清空 outputDir
 * （v1 轮就是因此丢了截图/trace，见 EVIDENCE-LOSS-RECORD.txt）。
 *
 * reporter 只在这里声明（list + json outputFile）。**不要**再给 CLI 传 --reporter：
 * CLI 的 --reporter 会覆盖 config，json 会写到 stdout 而不是文件 —— R10 v1 的真实缺陷。
 */
const OUT = process.env.R10_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /r10v5-.*\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-r10v5-unspecified",
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
