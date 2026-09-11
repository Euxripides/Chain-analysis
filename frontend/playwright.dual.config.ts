import { defineConfig, devices } from "@playwright/test";

/**
 * 双系统（用户层 / 后台）浏览器验收配置。
 *
 * 目标环境由 E2E_BASE_URL 指定，默认指向已经部署了 Nginx 双系统路由的地址。
 * 不使用 webServer：被测对象是部署产物，而不是开发服务器。
 */
export default defineConfig({
  testDir: "./tests",
  testMatch: /dual-system\.spec\.ts/,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://127.0.0.1:18088",
    channel: process.env.E2E_BROWSER_CHANNEL ?? "msedge",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    ignoreHTTPSErrors: true,
  },
  projects: [
    { name: "user-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    { name: "admin-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    { name: "user-mobile", use: { ...devices["Desktop Chrome"], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
});
