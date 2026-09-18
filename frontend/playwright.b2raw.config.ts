import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

/**
 * B2 raw-only UI sample config (new file; r9/r10/p1/udl01 configs are untouched).
 *
 * Purpose: create a BRAND-NEW export through the REAL user console (real Chromium against the
 * deployed frontend), then download it through the real one-time-ticket button. API-only
 * creation is explicitly not accepted as evidence for this round.
 *
 * testMatch is pinned to the single b2raw spec; reporter writes the JSON report into the unique
 * evidence directory so a later run can never overwrite an earlier one.
 */
const OUT = process.env.B2RAW_OUT ?? "";

export default defineConfig({
  testDir: "./tests",
  testMatch: /b2-raw-ui-export\.spec\.ts/,
  outputDir: OUT ? path.join(OUT, "test-results") : "test-results-b2raw-unspecified",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: OUT ? [["list"], ["json", { outputFile: path.join(OUT, "playwright-report.json") }]] : [["list"]],
  timeout: 900_000,
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
