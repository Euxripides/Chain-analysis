import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL || "https://127.0.0.1:8448";
const INV = process.env.INV;
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || "/usr/bin/chromium-browser" });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
page.on("pageerror", (e) => console.log("pageerror:", String(e).slice(0, 200)));
page.on("response", (r) => { const u = r.url(); if (u.includes("/findings") || u.includes("/verify")) console.log("RESP", r.status(), r.request().method(), u.replace(BASE, "").slice(0, 80)); });

async function login(email, password) {
  let res = await page.request.post(BASE + "/api/auth/v1/login", { headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" }, data: { identifier: email, password, audience: "user" } });
  if (res.status() === 409) {
    const body = await res.json();
    const ws = (body?.error?.details?.workspaces ?? [])[0];
    res = await page.request.post(BASE + "/api/auth/v1/login", { headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" }, data: { identifier: email, password, audience: "user", workspace_id: ws?.id } });
  }
  return res.status();
}

await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(process.env.INV_EMAIL);
await page.locator("#password").fill(process.env.INV_PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
const wsDialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
await wsDialog.waitFor({ state: "visible", timeout: 8000 }).catch(() => {});
if (await wsDialog.isVisible().catch(() => false)) await wsDialog.getByRole("button", { name: /acme-lab/ }).first().click();
await page.locator(".auth-card").waitFor({ state: "detached", timeout: 20000 }).catch(() => {});
console.log("login switch:", await login(process.env.REV_EMAIL, process.env.REV_PASSWORD));

await page.goto(`${BASE}/app-v2/investigations/${INV}/workbench`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(3000);
await page.getByRole("button", { name: "Findings" }).click();
await page.waitForTimeout(2500);
const target = process.env.TITLE;
const item = page.locator(".s4-list__item").filter({ hasText: target }).first();
console.log("items matching target:", await item.count());
await item.click();
await page.waitForTimeout(1500);
console.log("引用 Evidence visible:", await page.getByText("引用 Evidence").isVisible().catch(() => false));
console.log("verify buttons:", await page.locator('[data-testid^="finding-evidence-verify-"]').count());
console.log("badges:", JSON.stringify(await page.locator(".badge").allInnerTexts().catch(() => [])).slice(0, 200));
const vb = page.locator('[data-testid^="finding-evidence-verify-"]').first();
if (await vb.count()) {
  await vb.click();
  const dlg = page.getByRole("dialog");
  await dlg.waitFor({ state: "visible", timeout: 5000 });
  console.log("dialog text:", (await dlg.innerText()).replace(/\n+/g, " | ").slice(0, 220));
  console.log("dialog buttons:", JSON.stringify(await dlg.getByRole("button").allInnerTexts().catch(() => [])));
  page.on("request", (r) => { if (r.url().includes("/api/v2/")) console.log("REQ", r.method(), r.url().replace(BASE, "").slice(0, 80)); });
  page.on("console", (m) => { if (m.type() === "error") console.log("console.error:", m.text().slice(0, 200)); });
  await dlg.getByRole("button", { name: "确认" }).click();
  for (let i = 0; i < 5; i++) {
    await page.waitForTimeout(2000);
    console.log(`t=${(i + 1) * 2}s dialogs=${await page.getByRole("dialog").count()} badges=${JSON.stringify(await page.locator(".badge").allInnerTexts().catch(() => [])).slice(0, 180)}`);
  }
}
await browser.close();
