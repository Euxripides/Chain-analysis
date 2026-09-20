import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * DL-FLOW 整改专项（REM-015 / REM-E2E-013：Dataset 上限三层验证）配置。
 *
 * 为什么这个 config 放在 tests/ 下：本轮整改的改动范围被限定在
 * frontend/tests/、frontend/scripts/、scripts/s7/，而 frontend/ 根目录下已有的
 * playwright.dlflow.config.ts（Flowline 重构轮）与 playwright.r9v4/udl01*.config.ts
 * 属于其他轮次的产物，不覆盖、不修改。testDir 相对本文件解析为 frontend/tests。
 *
 * 运行（与 r9v4 / dlflow 相同的约定：真实浏览器 + 真实服务端 + 真实登录）：
 *   cd frontend
 *   E2E_BASE_URL=https://127.0.0.1:8448 E2E_CHROMIUM_PATH=/usr/bin/chromium-browser \
 *   DLF_OUT=<evidence dir> DLF_EMAIL=... DLF_PASSWORD=... \
 *   node node_modules/@playwright/test/cli.js test tests/download-flowline-remediation.spec.ts \
 *     --config tests/playwright.dlflow-remediation.config.ts --project=uc-desktop
 *
 * 也可以用仓库默认 config 收集本文件（默认 testMatch 覆盖全部 *.spec.ts），
 * 但默认 config 的 webServer 是 dev server，证据轮次请用本 config。
 *
 * 缺少凭据时用例会 skip（并在 run 输出里如实标注），不会把 skip 当成 PASS。
 */
const OUT = process.env.DLF_OUT ?? "";

export default defineConfig({
  testDir: ".",
  testMatch: /download-flowline-remediation\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-dlflow-remediation-unspecified",
  fullyParallel: false,
  workers: 1,
  retries: 0,
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
  projects: [{ name: "uc-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } }],
});
