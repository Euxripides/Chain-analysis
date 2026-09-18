#!/usr/bin/env node
// MFA 登录第二步的「真实浏览器 + 真实后端 + 真实数据库」端到端回归。
//
// 与另外两层证据的分工：
//   * Go live 测试（internal/api/mfa_second_step*_live_test.go）证明 API 契约与服务层语义；
//   * 本脚本证明**页面集成**：浏览器真的把 202 挑战渲染成第二步、真的把验证码送到 /mfa/verify、
//     真的在通过后拿到会话 Cookie 并跳转；错误验证码真的停在第二步并弹窗。
//
// 夹具：调用 backend/cmd/mfa-e2e-fixture（setup 打印随机口令 + TOTP 密钥；teardown 停用夹具）。
//
// 运行（VM，需在 frontend/ 目录下以解析 @playwright/test）：
//
//   MFA_E2E_BASE=https://127.0.0.1:8448 \
//   MFA_E2E_FIXTURE_BIN=/path/to/mfa-e2e-fixture \
//   node scripts/mfa-e2e-real-backend.mjs

import { chromium } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { createHmac } from 'node:crypto'
import { mkdirSync } from 'node:fs'

const BASE = (process.env.MFA_E2E_BASE ?? 'https://127.0.0.1:8448').replace(/\/$/, '')
const APP = process.env.MFA_E2E_APP ?? '/app-v2'
const REPO = process.env.MFA_E2E_REPO ?? '/home/euripides/projects/blockchain-platform'
const SHOTS = process.env.MFA_E2E_SHOTS ?? `${REPO}/acceptance-evidence/dual-system-v1/sprint7/mfa-e2e-${Date.now()}`
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

// RFC 6238 TOTP（SHA-1 / 6 位 / 30 秒），与后端 internal/mfa/totp.go 同参数。
function totp(secret, digits = 6, period = 30) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const ch of secret.replace(/=+$/, '').toUpperCase()) {
    const idx = alphabet.indexOf(ch)
    if (idx < 0) throw new Error(`bad base32 char ${ch}`)
    bits += idx.toString(2).padStart(5, '0')
  }
  const bytes = []
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2))
  const counter = Math.floor(Date.now() / 1000 / period)
  const msg = Buffer.alloc(8)
  msg.writeBigUInt64BE(BigInt(counter))
  const mac = createHmac('sha1', Buffer.from(bytes)).update(msg).digest()
  const offset = mac[mac.length - 1] & 0x0f
  const value = (mac.readUInt32BE(offset) & 0x7fffffff) % 10 ** digits
  return value.toString().padStart(digits, '0')
}

async function main() {
  mkdirSync(SHOTS, { recursive: true })
  const creds = fixture('setup')
  console.log(`fixture identity=${creds.identity_id} email=${creds.email} env=${creds.env}`)
  const browser = await chromium.launch()
  try {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } })
    const page = await ctx.newPage()
    // 控制台/未捕获异常必须进证据：空白页最常见的原因就是渲染期抛异常。
    const browserErrors = [];
    page.on('console', (m) => {
      if (m.type() === 'error') browserErrors.push(m.text());
    });
    page.on('pageerror', (err) => browserErrors.push(`pageerror: ${err.message}`));
    // 非 2xx 响应也要留痕：401/403 是「页面空白」最常见的伴生现象。
    const badResponses = [];
    page.on('response', (res) => {
      if (res.status() >= 400) badResponses.push(`${res.status()} ${res.request().method()} ${res.url()}`);
    });

    // ---- 1) 第一步：密码登录 -> 第二步视图（真实 202） ----
    await page.goto(`${BASE}${APP}/login`, { waitUntil: 'domcontentloaded' });
    await page.fill('#identifier', creds.email);
    await page.fill('#password', creds.password);
    await page.click('button[type=submit]');
    await page.waitForSelector('#mfa_code', { timeout: 20000 });
    chk(true, '密码通过后进入第二步视图', `url=${page.url()}`);
    const sub = await page.locator('.auth-card__sub').first().innerText();
    chk(sub.includes('两步验证'), '第二步副标题为两步验证', sub.replace(/\s+/g, ' '));
    const cookiesBefore = (await ctx.cookies()).filter((c) => c.name.includes('session'));
    chk(cookiesBefore.length === 0, '第二步之前浏览器没有会话 Cookie', `cookies=${cookiesBefore.length}`);
    await page.screenshot({ path: `${SHOTS}/01-step2.png` });

    // ---- 2) 错误验证码：停在第二步 + 弹窗 ----
    const good = totp(creds.secret, creds.digits, creds.period_seconds);
    const wrong = good === '000000' ? '111111' : '000000';
    await page.fill('#mfa_code', wrong);
    await page.click('button[type=submit]');
    const dialog = page.locator('[role="dialog"]').first();
    await dialog.waitFor({ state: 'visible', timeout: 15000 });
    const dlgText = (await dialog.innerText()).replace(/\s+/g, ' ');
    chk(true, '错误验证码弹出对话框', dlgText.slice(0, 60));
    await page.screenshot({ path: `${SHOTS}/02-wrong-code.png` });
    await page.locator('.dialog__close').first().click();
    await dialog.waitFor({ state: 'hidden', timeout: 10000 });
    chk(await page.locator('#mfa_code').isVisible(), '关闭弹窗后仍停留在第二步');
    const cookiesAfterWrong = (await ctx.cookies()).filter((c) => c.name.includes('session'));
    chk(cookiesAfterWrong.length === 0, '错误验证码后仍无会话 Cookie', `cookies=${cookiesAfterWrong.length}`);

    // ---- 3) 正确验证码：建立会话并进入工作台 ----
    const code = totp(creds.secret, creds.digits, creds.period_seconds);
    // 记住本次成功校验所在的时间步：TOTP 防重放是同一步只能用一次（跨受众同样生效）。
    const usedStep = Math.floor(Date.now() / 1000 / creds.period_seconds);
    await page.fill('#mfa_code', code);
    await page.click('button[type=submit]');
    await page.waitForURL((u) => u.pathname.startsWith(`${APP}/home`), { timeout: 20000 });
    chk(true, '第二步通过后跳转到工作台', page.url());
    const sessionCookies = (await ctx.cookies()).filter((c) => c.name.includes('session') && c.value);
    chk(sessionCookies.length > 0, '第二步通过后浏览器拿到会话 Cookie', sessionCookies.map((c) => c.name).join(','));
    await page.waitForLoadState('networkidle', { timeout: 20000 }).catch(() => {});
    const diag = await page.evaluate(() => ({
      url: location.href,
      shell: Boolean(document.querySelector('.shell')),
      main: Boolean(document.querySelector('main#main-content')),
      text: document.body.innerText.replace(/\s+/g, ' ').slice(0, 160),
      html: document.body.innerHTML.slice(0, 240),
    }));
    chk(diag.shell && diag.main, '工作台外壳已渲染（会话真实可用）',
      `url=${diag.url} text=${diag.text} errors=${browserErrors.slice(0, 4).join(' | ')} http=${badResponses.slice(0, 4).join(' | ')}`);
    await page.screenshot({ path: `${SHOTS}/03-home.png`, fullPage: false });

    // ---- 4) 会话可被自助接口确认（mfa_satisfied / mfa_enrolled） ----
    const session = await page.evaluate(async () => {
      const r = await fetch('/api/auth/v1/session', { credentials: 'include' });
      return { status: r.status, body: await r.json() };
    });
    chk(session.status === 200 && session.body?.data?.mfa_satisfied === true,
      'GET /session 返回 mfa_satisfied=true', `status=${session.status}`);
    chk(session.body?.data?.mfa_enrolled === true, 'GET /session 返回 mfa_enrolled=true');

    // ---- 5) 管理台：同一个身份在 admin 受众下也必须走第二步 ----
    await ctx.clearCookies();
    await page.goto(`${BASE}/admin/login`, { waitUntil: 'domcontentloaded' });
    await page.fill('#identifier', creds.email);
    await page.fill('#password', creds.password);
    await page.click('button[type=submit]');
    await page.waitForSelector('#mfa_code', { timeout: 20000 });
    chk(true, '管理台密码通过后同样进入第二步视图', page.url());
    await page.screenshot({ path: `${SHOTS}/04-admin-step2.png` });
    // 等下一个时间步：同一步的验证码已被用户台那次成功校验消费（防重放跨受众同样生效）。
    const waitStart = Date.now();
    while (Math.floor(Date.now() / 1000 / creds.period_seconds) <= usedStep) {
      if (Date.now() - waitStart > 45000) throw new Error('waited too long for the next TOTP step');
      await page.waitForTimeout(500);
    }
    await page.fill('#mfa_code', totp(creds.secret, creds.digits, creds.period_seconds));
    await page.click('button[type=submit]');
    let adminOk = true;
    try {
      await page.waitForURL((u) => u.pathname.startsWith('/admin/overview'), { timeout: 20000 });
    } catch {
      adminOk = false;
    }
    if (adminOk) {
      const adminCookies = (await ctx.cookies()).filter((c) => c.name.includes('admin_session') && c.value);
      chk(adminCookies.length > 0, '管理台第二步通过后拿到 admin 会话 Cookie', adminCookies.map((c) => c.name).join(','));
      await page.screenshot({ path: `${SHOTS}/04-admin.png` });
    } else {
      const dlg = await page.locator('[role="dialog"]').first().innerText().catch(() => '');
      chk(false, '管理台第二步通过后跳转 /admin/overview',
        `url=${page.url()} dialog=${dlg.replace(/\s+/g, ' ').slice(0, 140)}`);
    }

    // ---- 5) 退出后再次登录：仍然要求第二步（不是一次性开关） ----
    await ctx.clearCookies();
    await page.goto(`${BASE}${APP}/login`, { waitUntil: 'domcontentloaded' });
    await page.fill('#identifier', creds.email);
    await page.fill('#password', creds.password);
    await page.click('button[type=submit]');
    await page.waitForSelector('#mfa_code', { timeout: 20000 });
    chk(true, '再次登录仍然要求第二步');
  } finally {
    await browser.close();
    const out = fixture('teardown');
    console.log(`teardown=${JSON.stringify(out)}`);
  }
  console.log(`=== summary === PASS=${pass} FAIL=${fail} SHOTS=${SHOTS}`);
  if (fail > 0) process.exit(1);
}

main().catch((err) => {
  console.error('E2E aborted:', err);
  process.exit(1);
});
