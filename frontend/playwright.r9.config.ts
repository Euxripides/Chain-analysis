import { defineConfig, devices } from "@playwright/test";

/**
 * R9 — U-DL-01 /downloads/new（app-v2）CSV/XLSX 导入真实浏览器验收专用配置。
 * 入口必须由 E2E_BASE_URL 指定（runner 强制 HTTPS 回环），断点固定桌面 1440 与手机 390。
 */
export default defineConfig({
  testDir: "./tests",
  testMatch: /r9-.*\.spec\.ts/,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
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
