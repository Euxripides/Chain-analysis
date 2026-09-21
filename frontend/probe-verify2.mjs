import { chromium } from "@playwright/test";
const BASE = process.env.E2E_BASE_URL || "https://127.0.0.1:8448";
const browser = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM_PATH || "/usr/bin/chromium-browser" });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
page.on("pageerror", (e) => console.log("pageerror:", String(e).slice(0, 250)));
page.on("console", (m) => { if (m.type() === "error") console.log("console.error:", m.text().slice(0, 200)); });

async function apiPost(path, data) {
  const csrf = (await ctx.cookies(BASE)).find((c) => c.name === "cs_user_session_csrf")?.value ?? "";
  const res = await page.request.fetch(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Auth-Audience": "user", "X-CSRF-Token": csrf },
    data: JSON.stringify(data),
  });
  return { status: res.status(), body: await res.json().catch(() => null) };
}
async function loginApi(email, password) {
  let res = await page.request.post(BASE + "/api/auth/v1/login", { headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" }, data: { identifier: email, password, audience: "user" } });
  if (res.status() === 409) {
    const b = await res.json();
    const ws = (b?.error?.details?.workspaces ?? [])[0];
    res = await page.request.post(BASE + "/api/auth/v1/login", { headers: { "Content-Type": "application/json", "X-Auth-Audience": "user" }, data: { identifier: email, password, audience: "user", workspace_id: ws?.id } });
  }
  return res.status();
}

// UI 登录（investigator）
await page.goto(BASE + "/app-v2/login", { waitUntil: "domcontentloaded" });
await page.locator("#identifier").fill(process.env.INV_EMAIL);
await page.locator("#password").fill(process.env.INV_PASSWORD);
await page.getByRole("button", { name: /^登录$/ }).click();
const wsDialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
await wsDialog.waitFor({ state: "visible", timeout: 8000 }).catch(() => {});
if (await wsDialog.isVisible().catch(() => false)) await wsDialog.getByRole("button", { name: /acme-lab/ }).first().click();
await page.locator(".auth-card").waitFor({ state: "detached", timeout: 20000 }).catch(() => {});

// 用真实 API 造一个带 evidence 的调查（与用例同形）
const caseName = "PROBE-" + Date.now();
const c = await apiPost("/api/v2/cases", { name: caseName, classification: "confidential", priority: "high", status: "active" });
const caseId = c.body?.data?.id;
await apiPost(`/api/v2/cases/${caseId}/subjects`, { subject_type: "ADDRESS", chain: "tron", value: "TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m", label: "probe" });
const inv = await apiPost("/api/v2/investigations", {
  case_id: caseId, goal: "probe verify", mode: "assisted",
  subjects: [{ type: "ADDRESS", chain: "tron", value: "TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m" }],
  expected_outputs: ["FINDINGS", "EVIDENCE_INDEX"],
  data_requirement: { datasets: ["token_transfers"], range: { type: "block", from_block: 78238692, to_block: 83304697 }, reuse_policy: "prefer_local" },
  budget: { max_provider_cost_usd: "0.00" }, capability_versions: { "analysis.flow": "1.0" },
});
const invId = inv.body?.data?.investigation?.id;
console.log("case", caseId, "inv", invId);
for (const cmd of ["start", "pause", "resume"]) {
  const csrf = (await ctx.cookies(BASE)).find((x) => x.name === "cs_user_session_csrf")?.value ?? "";
  await page.request.fetch(`${BASE}/api/v2/investigations/${invId}/commands/${cmd}`, { method: "POST", headers: { "Content-Type": "application/json", "X-Auth-Audience": "user", "X-CSRF-Token": csrf }, data: JSON.stringify({}) });
  await page.waitForTimeout(400);
}
for (let i = 0; i < 60; i++) {
  const csrf = (await ctx.cookies(BASE)).find((x) => x.name === "cs_user_session_csrf")?.value ?? "";
  const res = await page.request.fetch(`${BASE}/api/v2/investigations/${invId}`, { headers: { "X-Auth-Audience": "user", "X-CSRF-Token": csrf } });
  const body = await res.json();
  const st = body?.data?.status;
  if (st === "waiting_review") break;
  await page.waitForTimeout(2000);
}
console.log("investigation ready");

// 切到 reviewer，进入 findings 页
console.log("switch login:", await loginApi(process.env.REV_EMAIL, process.env.REV_PASSWORD));
page.on("response", (r) => { const u = r.url(); if (u.includes("/findings") || u.includes("/verify")) console.log("RESP", r.status(), r.request().method(), u.replace(BASE, "").slice(0, 84)); });
await page.goto(`${BASE}/app-v2/investigations/${invId}/workbench`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
await page.getByRole("button", { name: "Findings" }).click();
await page.waitForTimeout(2500);
const titles = (await page.locator(".s4-list__item .s4-list__title").allInnerTexts().catch(() => [])).map((t) => t.trim()).filter(Boolean);
console.log("titles:", JSON.stringify(titles).slice(0, 160));
await page.locator(".s4-list__item").filter({ hasText: titles[0] }).first().click();
await page.waitForTimeout(1200);
const vb = page.locator('[data-testid^="finding-evidence-verify-"]').first();
console.log("verify buttons:", await vb.count());
await vb.click();
const dlg = page.getByRole("dialog");
await dlg.waitFor({ state: "visible", timeout: 5000 });
await dlg.getByRole("button", { name: "确认" }).click();
for (let i = 0; i < 5; i++) {
  await page.waitForTimeout(2000);
  console.log(`t=${(i + 1) * 2}s dialogs=${await page.getByRole("dialog").count()} badges=${JSON.stringify(await page.locator(".badge").allInnerTexts().catch(() => [])).slice(0, 140)}`);
}
await browser.close();
