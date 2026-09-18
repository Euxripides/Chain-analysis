#!/usr/bin/env node
// 管理台「重置 MFA」的真实浏览器端到端：登录 → 用户详情 → 影响预览 → 执行 → 结果 → 事后验证。
//
// 与 Go live 测试（TestMFAAdminResetLive）的分工：那个证明 API 契约与服务端语义；
// 本脚本证明**页面真的把这些串起来了**：三条防线（预览 / 三项齐全才可提交 / 结果回执）
// 都在真实浏览器里走一遍，最后再用 API 证明目标账号确实不再要求第二步。
//
// 运行（VM，需在 frontend/ 目录下以解析 @playwright/test）：
//
//   MFA_E2E_BASE=https://127.0.0.1:8448 MFA_E2E_API=http://127.0.0.1:8080 \
//   MFA_E2E_FIXTURE_BIN=/tmp/mfa-e2e-fixture node scripts/mfa-reset-admin-ui-e2e.mjs

import { chromium } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'

const BASE = (process.env.MFA_E2E_BASE ?? 'https://127.0.0.1:8448').replace(/\/$/, '')
const API = (process.env.MFA_E2E_API ?? 'http://127.0.0.1:8080').replace(/\/$/, '')
const REPO = process.env.MFA_E2E_REPO ?? '/home/euripides/projects/blockchain-platform'
const SHOTS = process.env.MFA_E2E_SHOTS ?? `${REPO}/acceptance-evidence/dual-system-v1/sprint7/mfa-reset-ui-${Date.now()}`
const FIXTURE_BIN = process.env.MFA_E2E_FIXTURE_BIN ?? ''

let pass = 0
let fail = 0
function chk(ok, name, detail = '') {
  if (ok) {
    pass += 1
    console.log(`PASS ${name}${detail ? ' :: ' + detail : ''}`)
  } else {
    fail += 1
    console.log(`FAIL ${name}${detail ? ' :: ' + detail : ''}`)
  }
}

function fixture(action) {
  const env = { ...process.env, PATH: `/usr/local/go/bin:${process.env.PATH ?? ''}` }
  const out = FIXTURE_BIN
    ? execFileSync(FIXTURE_BIN, ['-action', action], { env, encoding: 'utf8' })
    : execFileSync('go', ['run', './cmd/mfa-e2e-fixture', '-action', action], { cwd: `${REPO}/backend`, env, encoding: 'utf8' })
  return JSON.parse(out.trim().split('\n').pop())
}

async function loginViaApi(identifier, password, audience) {
  const res = await fetch(`${API}/api/auth/v1/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ identifier, password, audience }),
  });
  return { status: res.status, body: await res.json() };
}

async function main() {
  mkdirSync(SHOTS, { recursive: true })
  const creds = fixture('setup')
  console.log(`fixture target=${creds.identity_id} admin=${creds.admin_identity_id}`)
  const browser = await chromium.launch()
  try {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } })
    const page = await ctx.newPage()
    // 控制台错误要留痕，但要区分「预期的匿名会话探测」：进入登录页时应用会先探一次
    // GET /api/auth/v1/session，未登录必然是 401 —— 那不是缺陷。真正的失败只有两类：
    // 未捕获异常（pageerror）与预期之外的失败响应。
    const pageErrors = [];
    const badResponses = [];
    page.on('pageerror', (err) => pageErrors.push(`${err.message}`));
    page.on('response', (res) => {
      if (res.status() >= 400) badResponses.push(`${res.status()} ${res.request().method()} ${res.url()}`);
    });

    // ---- 1) 管理员登录（夹具管理员未注册 MFA，直接 200）----
    await page.goto(`${BASE}/admin/login`, { waitUntil: 'domcontentloaded' });
    await page.fill('#identifier', creds.admin_email);
    await page.fill('#password', creds.admin_password);
    await page.click('button[type=submit]');
    await page.waitForURL((u) => u.pathname.startsWith('/admin/overview'), { timeout: 20000 });
    chk(true, '管理员登录后进入 /admin/overview');

    // ---- 2) 用户列表检索目标账号 ----
    await page.goto(`${BASE}/admin/users`, { waitUntil: 'domcontentloaded' });
    const search = page.locator('#user-search').first();
    await search.waitFor({ timeout: 20000 });
    await search.fill(creds.email);
    await search.press('Enter');
    const row = page.locator('button', { hasText: creds.email }).first();
    await row.waitFor({ timeout: 20000 });
    chk(true, '用户列表按邮箱检索到目标账号');
    await row.click();

    // ---- 3) 详情弹窗：入口可用 ----
    const detail = page.locator('[role="dialog"]').first();
    await detail.waitFor({ timeout: 10000 });
    const resetEntry = detail.getByRole('button', { name: '重置 MFA' });
    const entryEnabled = await resetEntry.isEnabled();
    chk(entryEnabled, '详情弹窗中的「重置 MFA」可用（已授权 + 目标已启用 MFA）',
      entryEnabled ? '' : `title=${await resetEntry.getAttribute('title')}`);
    await resetEntry.click();

    // ---- 4) 影响预览：破坏性后果必须逐项列出 ----
    const preview = page.locator('[role="dialog"]').filter({ hasText: '重置 MFA · 影响预览' }).first();
    await preview.waitFor({ timeout: 10000 });
    const previewText = (await preview.innerText()).replace(/\s+/g, ' ');
    chk(previewText.includes('将删除') && previewText.includes('恢复码') && previewText.includes('吊销'),
      '影响预览列出将删除的内容与将吊销的会话', previewText.slice(0, 90));
    await page.screenshot({ path: `${SHOTS}/01-preview.png` });
    await preview.getByRole('button', { name: '继续' }).click();

    // ---- 5) 执行弹窗：三项齐全前不得提交 ----
    const exec = page.locator('[role="dialog"]').filter({ hasText: '重置 MFA · 执行' }).first();
    await exec.waitFor({ timeout: 10000 });
    const submit = exec.getByRole('button', { name: '确认重置' });
    chk(await submit.isDisabled(), '未填写原因/密码/确认时「确认重置」保持禁用');
    await exec.locator('#dialog-field-reason').fill('端到端回归：管理员重置 MFA');
    await exec.locator('#dialog-field-password').fill(creds.admin_password);
    await exec.locator('#dialog-field-ack').check();
    const enabledNow = await submit.isEnabled();
    chk(enabledNow, '三项齐全后「确认重置」可用');
    await page.screenshot({ path: `${SHOTS}/02-execute.png` });
    await submit.click();

    // ---- 6) 结果回执 ----
    const result = page.locator('[role="dialog"]').filter({ hasText: 'MFA 已重置' }).first();
    await result.waitFor({ timeout: 20000 });
    const resultText = (await result.innerText()).replace(/\s+/g, ' ');
    chk(resultText.includes('已删除') || resultText.includes('恢复码'), '结果弹窗回报被删除的内容', resultText.slice(0, 90));
    await page.screenshot({ path: `${SHOTS}/03-result.png` });

    // ---- 7) 事后验证：目标账号登录不再要求第二步 ----
    const after = await loginViaApi(creds.email, creds.password, 'user');
    chk(after.status === 200 && after.body?.data?.state === 'authenticated',
      '目标账号重置后直接登录成功（不再要求第二步）', `status=${after.status} state=${after.body?.data?.state}`);

    const unexpected = badResponses.filter((line) => !(line.startsWith('401 ') && line.includes('/api/auth/v1/session')));
    chk(pageErrors.length === 0, '整个流程没有未捕获的前端异常', pageErrors.slice(0, 2).join(' | '));
    chk(unexpected.length === 0, '整个流程没有预期之外的失败响应（已排除登录页的匿名 /session 探测 401）',
      unexpected.slice(0, 3).join(' | ') || `bad=${badResponses.length}`);
  } finally {
    await browser.close();
    console.log(`teardown=${JSON.stringify(fixture('teardown'))}`);
  }
  console.log(`=== summary === PASS=${pass} FAIL=${fail} SHOTS=${SHOTS}`);
  if (fail > 0) process.exit(1);
}

main().catch((err) => {
  console.error('E2E aborted:', err);
  process.exit(1);
});
