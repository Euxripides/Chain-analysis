import { defineConfig, devices } from "@playwright/test";

/**
 * 双系统（用户层 / 后台）浏览器验收配置。
 *
 * 目标环境由 E2E_BASE_URL 指定，默认指向已经部署了 Nginx 双系统路由的地址。
 * 不使用 webServer：被测对象是部署产物，而不是开发服务器。
 */
export default defineConfig({
  testDir: "./tests",
  testMatch: /dual-system.*\.spec\.ts/,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://127.0.0.1:18088",
    // VM 上只有系统 Chromium 时用 E2E_CHROMIUM_PATH 指定可执行文件；否则维持 msedge。
    channel: process.env.E2E_BROWSER_CHANNEL ?? (process.env.E2E_CHROMIUM_PATH ? undefined : "msedge"),
    launchOptions: process.env.E2E_CHROMIUM_PATH
      ? { executablePath: process.env.E2E_CHROMIUM_PATH }
      : undefined,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    ignoreHTTPSErrors: true,
  },
  projects: [
    { name: "user-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    {
      name: "admin-desktop",
      // 用户端重构验收规格只面向 /app-v2，后台 project 不复跑一遍（避免把用户路由当后台页面测）。
      testIgnore: /user-console-redesign/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } },
    },
    { name: "user-mobile", use: { ...devices["Desktop Chrome"], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
    // §14.5 最终轮要求的补充档位（各自只跑对应规格，不影响既有 project 的覆盖面）：
    //   平板档：后台管理操作的平板宽度（1024×768），触屏语义与桌面一致；
    //   cross-role：受众/角色越权的双向负向（服务端必须拒绝，不接受前端隐藏）；
    //   reduced-motion：减少动态效果下的可用性（先用 matchMedia 证明设置生效）。
    {
      name: "admin-tablet",
      testMatch: /dual-system-(sprint6|quality)\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1024, height: 768 }, hasTouch: true },
    },
    {
      name: "cross-role",
      // §7.5「每个角色只看到允许的导航和按钮」由 nav-permission 规格做双向验证（服务端为唯一判据）。
      testMatch: /dual-system-(cross-role|nav-permission)\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } },
    },
    {
      name: "reduced-motion",
      testMatch: /dual-system-reduced-motion\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 }, reducedMotion: "reduce" },
    },
  ],
});
