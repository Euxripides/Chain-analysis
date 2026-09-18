import { defineConfig, devices } from "@playwright/test";

/**
 * R-3（旧 V1 前端 api-control / system 的真实浏览器验收）专用配置。
 *
 * 与 playwright.dual.config.ts 同源，但：
 *   - testDir 相同，只匹配 r3-*.spec.ts，避免动到既有双系统套件；
 *   - 断点固定为桌面 1440x900 与移动 390x844（任务要求的两端）；
 *   - 入口必须由 E2E_BASE_URL 指定（runner 会强制 HTTPS 回环入口）。
 */
export default defineConfig({
  testDir: "./tests",
  testMatch: /r3-.*\.spec\.ts/,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  timeout: 120_000,
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
    { name: "r3-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    { name: "r3-mobile", use: { ...devices["Desktop Chrome"], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
});
