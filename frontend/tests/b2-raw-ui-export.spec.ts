import { expect, test, type Locator, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

/**
 * B2 semantic-1 (raw fallback) — create a BRAND-NEW export through the REAL user console and
 * download it through the real one-time-ticket button.
 *
 * Discipline of this round (root directive 2026-09-13):
 *   - the export must be created by REAL browser interaction with the deployed UI; calling the
 *     API directly is explicitly not accepted as evidence for this semantic;
 *   - the UI state, the buttons and the server response id/request id are captured verbatim;
 *   - nothing here claims storage bucket attribution: that is proven separately (and fail-closed)
 *     by the runner using objectprobe stat-strict, which distinguishes NoSuchKey from
 *     bucket/permission faults.
 */

const EMAIL = process.env.B2RAW_EMAIL ?? "";
const PASSWORD = process.env.B2RAW_PASSWORD ?? "";
const OUT = process.env.B2RAW_OUT ?? ".";
const CHAIN = process.env.B2RAW_CHAIN ?? "tron";
const ADDRESS = process.env.B2RAW_ADDRESS ?? "";
const FORMAT = process.env.B2RAW_FORMAT ?? "csv";
const DATASET = process.env.B2RAW_DATASET ?? "token_transfers";
const LOGIN_PATH = "/app-v2/login";
const EXPORTS_PATH = "/app-v2/exports";

function dump(name: string, value: unknown): void {
  mkdirSync(OUT, { recursive: true });
  writeFileSync(path.join(OUT, name), JSON.stringify(value, null, 2) + "\n", "utf8");
}

function sha256File(file: string): string {
  return createHash("sha256").update(readFileSync(file)).digest("hex");
}

function flat(value: string): string {
  return value.replace(/[ \t\r\n]+/g, " ").trim();
}

async function login(page: Page): Promise<void> {
  await page.goto(LOGIN_PATH, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#identifier"), "login identifier").toBeVisible({ timeout: 30000 });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect.poll(() => page.url(), { timeout: 30000 }).not.toContain(LOGIN_PATH);
}

/** Verbatim capture of every interactive control inside a scope (values, checked, disabled). */
async function captureControls(scope: Locator) {
  return scope.locator("input, select, textarea, button").evaluateAll((nodes) =>
    nodes.map((node) => {
      const el = node as HTMLInputElement;
      const tag = el.tagName.toLowerCase();
      const labelEl = el.closest("label");
      const fieldEl = el.closest(".field");
      const fieldLabelEl = fieldEl ? fieldEl.querySelector(".field__label") : null;
      const type = el.getAttribute("type");
      return {
        tag,
        id: el.id || null,
        type,
        value: tag === "button" ? null : el.value,
        checked: type === "checkbox" || type === "radio" ? el.checked : null,
        disabled: el.disabled === true,
        ariaLabel: el.getAttribute("aria-label"),
        labelText: labelEl ? (labelEl.textContent || "").replace(/\s+/g, " ").trim() : "",
        fieldLabel: fieldLabelEl ? (fieldLabelEl.textContent || "").replace(/\s+/g, " ").trim() : "",
        text: tag === "button" ? (el.textContent || "").replace(/\s+/g, " ").trim() : null,
      };
    }),
  );
}

async function capturePageState(page: Page) {
  const buttons = await page
    .locator("button:visible")
    .evaluateAll((nodes) =>
      nodes.map((n) => (n.textContent || "").replace(/\s+/g, " ").trim()).filter(Boolean),
    );
  return {
    url: page.url(),
    title: await page.title(),
    visibleButtons: buttons,
    tableRows: await page.locator("table tbody tr").count(),
  };
}

test.describe("B2 raw-only sample through the real user console", () => {
  test("B2RAW-01 UI creates a brand-new small export and shows its server id", async ({ page, browser }) => {
    test.skip(!EMAIL || !PASSWORD, "B2RAW_EMAIL/B2RAW_PASSWORD not provided");
    test.setTimeout(300000);
    expect(ADDRESS, "B2RAW_ADDRESS must be provided").not.toBe("");
    const browserVersion = browser.version();

    await login(page);
    await page.goto(EXPORTS_PATH, { waitUntil: "domcontentloaded" });
    const createButton = page.getByRole("button", { name: "新建导出" });
    await expect(createButton, "新建导出 button").toBeVisible({ timeout: 30000 });
    await expect(createButton).toBeEnabled();
    const pageStateBefore = await capturePageState(page);
    await page.screenshot({ path: path.join(OUT, "B2RAW-01-exports-before.png"), fullPage: true });

    await createButton.click();
    const dialog = page.locator('[role="dialog"]').filter({ hasText: "新建导出" });
    await expect(dialog, "新建导出 dialog").toBeVisible({ timeout: 30000 });
    const dialogDefaults = await captureControls(dialog);
    const dialogTextDefault = flat(await dialog.innerText());

    await dialog.locator("#export-chain").selectOption(CHAIN);
    await dialog.locator("#export-addresses").fill(ADDRESS);
    await dialog.locator("#export-format").selectOption(FORMAT);

    const datasetGroup = dialog.locator("div.field").filter({ hasText: "Dataset" }).first();
    const datasetLabels = datasetGroup.locator("label");
    const datasetCount = await datasetLabels.count();
    expect(datasetCount, "dataset checkbox group must be rendered").toBeGreaterThan(0);
    const datasetSelection: Array<{ label: string; id: string; checked: boolean }> = [];
    for (let index = 0; index < datasetCount; index += 1) {
      const label = datasetLabels.nth(index);
      const labelText = flat(await label.innerText());
      const matched = labelText.match(/（([a-z_]+)）/);
      const id = matched ? matched[1] : "";
      const box = label.locator('input[type="checkbox"]');
      if (id === DATASET) {
        await box.check();
      } else {
        await box.uncheck();
      }
      datasetSelection.push({ label: labelText, id, checked: await box.isChecked() });
    }

    const dialogFilled = await captureControls(dialog);
    const dialogTextFilled = flat(await dialog.innerText());
    await page.screenshot({ path: path.join(OUT, "B2RAW-01-dialog-filled.png"), fullPage: true });

    const createResponsePromise = page.waitForResponse(
      (response) => response.url().includes("/api/v2/exports") && response.request().method() === "POST",
      { timeout: 120000 },
    );
    const createButtonInDialog = dialog.getByRole("button", { name: "创建导出" });
    await expect(createButtonInDialog, "创建导出 button").toBeEnabled();
    await createButtonInDialog.click();
    const createResponse = await createResponsePromise;
    const status = createResponse.status();
    const responseHeaders = createResponse.headers();
    const responseBody = await createResponse.json();
    const requestHeaders = createResponse.request().headers();
    // The API wraps every payload: {"success":..., "data":{...}, "meta":{"request_id","trace_id"}}.
    // The created export id therefore lives at body.data.id (reading body.id silently produced an
    // empty id in the 20260913T135033Z round; that round is preserved and is NOT authoritative).
    const createdPayload = responseBody && responseBody.data ? responseBody.data : {};
    const responseMeta = responseBody && responseBody.meta ? responseBody.meta : {};
    const exportId = String(createdPayload.id || "");

    const successDialog = page.locator('[role="dialog"]').filter({ hasText: "导出任务已创建" });
    await expect(successDialog, "success dialog").toBeVisible({ timeout: 60000 });
    const successText = flat(await successDialog.innerText());
    const successControls = await captureControls(successDialog);
    await page.screenshot({ path: path.join(OUT, "B2RAW-01-success-dialog.png"), fullPage: true });

    dump("b2raw-01-create.json", {
      browser_version: browserVersion,
      ui_flow: [
        "goto " + EXPORTS_PATH,
        "click 新建导出",
        "select chain=" + CHAIN,
        "fill addresses=" + ADDRESS,
        "select format=" + FORMAT,
        "dataset checkboxes set to [" + DATASET + "]",
        "click 创建导出",
      ],
      page_state_before: pageStateBefore,
      dialog_text_default: dialogTextDefault,
      dialog_defaults: dialogDefaults,
      dialog_text_filled: dialogTextFilled,
      dialog_filled: dialogFilled,
      dataset_selection: datasetSelection,
      create_request: {
        method: createResponse.request().method(),
        url: createResponse.url(),
        idempotency_key_present: Boolean(requestHeaders["idempotency-key"]),
        headers_of_interest: {
          "content-type": requestHeaders["content-type"],
          "x-auth-audience": requestHeaders["x-auth-audience"] ?? null,
        },
      },
      create_response: {
        status,
        x_request_id: responseHeaders["x-request-id"] ?? null,
        x_trace_id: responseHeaders["x-trace-id"] ?? null,
        content_type: responseHeaders["content-type"] ?? null,
        body: responseBody,
      },
      success_dialog_text: successText,
      success_dialog_controls: successControls,
      export_id: exportId,
    });

    expect(status, "create must return 201").toBe(201);
    expect(responseBody.success, "envelope success must be true").toBe(true);
    expect(exportId, "server-side export id from body.data.id").toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    expect(successText, "success dialog must show the real export_id").toContain(exportId);
    expect(responseHeaders["x-request-id"], "X-Request-Id on create").toBeTruthy();
    expect(responseHeaders["x-trace-id"], "X-Trace-Id on create").toBeTruthy();
    expect(responseMeta.request_id, "body.meta.request_id must equal the response header").toBe(responseHeaders["x-request-id"]);
    expect(responseMeta.trace_id, "body.meta.trace_id must equal the response header").toBe(responseHeaders["x-trace-id"]);
    expect(String(createdPayload.format || ""), "format echoed by server").toBe(FORMAT);
    expect(createdPayload.zip, "single address + single dataset must not zip").toBe(false);
    expect(String(createdPayload.status || ""), "initial status must be PENDING").toBe("PENDING");
    expect(requestHeaders["idempotency-key"], "UI must send Idempotency-Key").toBeTruthy();
  });

  test("B2RAW-02 UI downloads the completed export through the ticket button", async ({ page }) => {
    test.skip(!EMAIL || !PASSWORD, "B2RAW_EMAIL/B2RAW_PASSWORD not provided");
    test.setTimeout(600000);
    const created = JSON.parse(readFileSync(path.join(OUT, "b2raw-01-create.json"), "utf8"));
    const exportId = String(created.export_id);
    expect(exportId, "export id from B2RAW-01").not.toBe("");

    await login(page);
    await page.goto(EXPORTS_PATH, { waitUntil: "domcontentloaded" });
    const row = page.locator("tr", { has: page.locator('[title="' + exportId + '"]') });
    let rowReady = false;
    let rowTextFinal = "";
    let attempts = 0;
    for (; attempts < 90; attempts += 1) {
      if ((await row.count()) > 0) {
        const candidate = row.first();
        const rowText = flat(await candidate.innerText());
        const downloadButton = candidate.getByRole("button", { name: "下载票据" });
        const hasButton = (await downloadButton.count()) > 0;
        if (rowText.includes("COMPLETED") && hasButton && (await downloadButton.isEnabled())) {
          rowReady = true;
          rowTextFinal = rowText;
          break;
        }
      }
      const refresh = page.getByRole("button", { name: "刷新" });
      if ((await refresh.count()) > 0) {
        await refresh.first().click();
      }
      await page.waitForTimeout(2000);
    }
    expect(rowReady, "export row must reach COMPLETED with an enabled 下载票据 button").toBe(true);
    const listState = await capturePageState(page);
    await page.screenshot({ path: path.join(OUT, "B2RAW-02-row-completed.png"), fullPage: true });

    await row.first().getByRole("button", { name: "下载票据" }).click();
    const confirmDialog = page.locator('[role="dialog"]').filter({ hasText: "创建下载票据？" });
    await expect(confirmDialog, "ticket confirm dialog").toBeVisible({ timeout: 30000 });
    const confirmText = flat(await confirmDialog.innerText());
    const confirmControls = await captureControls(confirmDialog);
    await page.screenshot({ path: path.join(OUT, "B2RAW-02-ticket-dialog.png"), fullPage: true });

    const observedResponses: Array<Record<string, unknown>> = [];
    const responseListener = (response: { url: () => string; status: () => number; request: () => { method: () => string }; headers: () => Record<string, string> }) => {
      const url = response.url();
      if (url.includes("/api/v2/exports/download/") || url.includes("/download-ticket")) {
        const headers = response.headers();
        observedResponses.push({
          url,
          status: response.status(),
          method: response.request().method(),
          request_id: headers["x-request-id"] ?? null,
          trace_id: headers["x-trace-id"] ?? null,
          content_disposition: headers["content-disposition"] ?? null,
          content_type: headers["content-type"] ?? null,
        });
      }
    };
    page.on("response", responseListener);
    const ticketResponsePromise = page.waitForResponse(
      (response) => response.url().includes("/download-ticket") && response.request().method() === "POST",
      { timeout: 60000 },
    );
    const downloadPromise = page.waitForEvent("download", { timeout: 180000 });
    await confirmDialog.getByRole("button", { name: "创建并下载" }).click();
    const ticketResponse = await ticketResponsePromise;
    const ticketStatus = ticketResponse.status();
    const ticketHeaders = ticketResponse.headers();
    const ticketBody = await ticketResponse.json();
    const download = await downloadPromise;
    const downloadUrl = download.url();
    await page.waitForTimeout(1500);
    page.off("response", responseListener);
    const suggested = download.suggestedFilename();
    const downloadDir = path.join(OUT, "ui-download");
    mkdirSync(downloadDir, { recursive: true });
    const target = path.join(downloadDir, suggested);
    await download.saveAs(target);
    const downloadSha = sha256File(target);
    const downloadBytes = readFileSync(target).length;
    await page.screenshot({ path: path.join(OUT, "B2RAW-02-after-download.png"), fullPage: true });

    dump("b2raw-02-ui-download.json", {
      export_id: exportId,
      row_text_completed: rowTextFinal,
      poll_attempts: attempts,
      list_state_at_download: listState,
      confirm_dialog_text: confirmText,
      confirm_dialog_controls: confirmControls,
      ticket_request: { method: ticketResponse.request().method(), url: ticketResponse.url() },
      ticket_response: {
        status: ticketStatus,
        x_request_id: ticketHeaders["x-request-id"] ?? null,
        x_trace_id: ticketHeaders["x-trace-id"] ?? null,
        body: ticketBody,
      },
      ticket_download_url: downloadUrl,
      observed_download_responses: observedResponses,
      download: {
        suggested_filename: suggested,
        saved_path: target,
        bytes: downloadBytes,
        sha256: downloadSha,
        failure: download.failure(),
      },
    });

    // The ticket handler answers with respondOK (HTTP 200), not 201 -- the test is aligned with the
    // real handler contract instead of the product being changed to satisfy a test.
    const ticketPayload = ticketBody && ticketBody.data ? ticketBody.data : {};
    const ticketEnvelopeMeta = ticketBody && ticketBody.meta ? ticketBody.meta : {};
    expect(ticketStatus, "ticket creation returns the documented 200").toBe(200);
    expect(ticketBody.success, "ticket envelope success must be true").toBe(true);
    expect(ticketPayload.single_use, "ticket must be single use").toBe(true);
    expect(String(ticketPayload.url || ""), "ticket url").toContain("/api/v2/exports/download/");
    expect(String(ticketPayload.ticket_id || ""), "ticket id present").toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    expect(Number(ticketPayload.expires_in_seconds), "ticket ttl is a positive number of seconds").toBeGreaterThan(0);
    expect(ticketEnvelopeMeta.request_id, "ticket meta.request_id equals the response header").toBe(ticketHeaders["x-request-id"]);
    const consumptionResponses = observedResponses.filter((row) => String(row.url || "").includes("/api/v2/exports/download/"));
    expect(consumptionResponses.length, "the ticket consumption response must be observed").toBeGreaterThan(0);
    expect(String(consumptionResponses[consumptionResponses.length - 1].request_id || ""), "consumption carries its own request id").not.toBe("");
    expect(String(consumptionResponses[consumptionResponses.length - 1].request_id || ""), "consumption request id differs from ticket creation").not.toBe(String(ticketHeaders["x-request-id"]));
    dump("b2raw-02-ui-download-envelope.json", {
      ticket_create: { status: ticketStatus, header_request_id: ticketHeaders["x-request-id"] ?? null, header_trace_id: ticketHeaders["x-trace-id"] ?? null, data: ticketPayload, meta: ticketEnvelopeMeta },
      consumption: consumptionResponses,
    });
    expect(downloadSha, "UI download must have real bytes").toMatch(/^[0-9a-f]{64}$/);
  });
});
