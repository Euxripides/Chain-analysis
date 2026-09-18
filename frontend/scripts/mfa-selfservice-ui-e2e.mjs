#!/usr/bin/env node
// 用户台「自助启用/关闭 MFA」的真实浏览器端到端：真实后端 + 真实数据库 + 真实验证码。
import { chromium } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { createHmac } from 'node:crypto'
import { mkdirSync } from 'node:fs'

const BASE = (process.env.MFA_E2E_BASE ?? 'https://127.0.0.1:8448').replace(/\/$/, '')
const API = (process.env.MFA_E2E_API ?? 'http://127.0.0.1:8080').replace(/\/$/, '')
const REPO = process.env.MFA_E2E_REPO ?? '/home/euripides/projects/blockchain-platform'
const SHOTS = process.env.MFA_E2E_SHOTS ?? REPO + '/acceptance-evidence/dual-system-v1/sprint7/mfa-selfservice-ui-' + Date.now()
const FIXTURE_BIN = process.env.MFA_E2E_FIXTURE_BIN ?? ''

let pass = 0
let fail = 0
function chk(ok, name, detail) {
  if (ok) {
    pass += 1
    console.log('PASS ' + name + (detail ? ' :: ' + detail : ''))
  } else {
    fail += 1
    console.log('FAIL ' + name + (detail ? ' :: ' + detail : ''))
  }
}

function fixture(action) {
  const env = { ...process.env, PATH: '/usr/local/go/bin:' + (process.env.PATH ?? '') }
  const out = FIXTURE_BIN
    ? execFileSync(FIXTURE_BIN, ['-action', action], { env, encoding: 'utf8' })
    : execFileSync('go', ['run', './cmd/mfa-e2e-fixture', '-action', action], { cwd: REPO + '/backend', env, encoding: 'utf8' })
  return JSON.parse(out.trim().split('\n').pop())
}

// 直接改库把夹具恢复成「未注册 MFA」——自助注册测试的起点只能是未启用。
function unenrollFixture(identityID) {
  const sql = "DELETE FROM mfa_recovery_codes WHERE identity_id = '" + identityID + "'; DELETE FROM identity_mfa WHERE identity_id = '" + identityID + "';";
  execFileSync('docker', ['exec', 'deploy-postgres-1', 'psql', '-U', 'blockchain', '-d', 'blockchain_platform', '-c', sql], { encoding: 'utf8' });
}

// RFC 6238 TOTP（SHA-1 / 6 位 / 30 秒），与后端 internal/mfa/totp.go 同参数。
function totp(secret, digits = 6, period = 30) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const ch of secret.replace(/[^A-Za-z2-7]/g, '').toUpperCase()) {
    const idx = alphabet.indexOf(ch)
    if (idx < 0) throw new Error('bad base32 char ' + ch)
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

async function loginStatus(identifier, password) {
  const res = await fetch(API + '/api/auth/v1/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ identifier, password, audience: 'user' }),
  });
  const body = await res.json();
  return { status: res.status, state: (body && body.data && body.data.state) || null, challenge: (body && body.data && body.data.challenge_id) || null };
}

async function main() {
  mkdirSync(SHOTS, { recursive: true });
  const creds = fixture('setup');
  unenrollFixture(creds.identity_id);
  console.log('fixture target=' + creds.identity_id + '（已复位为未注册 MFA）');
  const browser = await chromium.launch();
  try {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
    const page = await ctx.newPage();
    const pageErrors = [];
    page.on('pageerror', (err) => pageErrors.push(String(err.message)));

    // ---- 0) 起点：未注册 MFA 时登录不应要求第二步 ----
    const before = await loginStatus(creds.email, creds.password);
    chk(before.status === 200 && before.state === 'authenticated', '起点：未注册 MFA 时登录直接成功', 'status=' + before.status + ' state=' + before.state);

    // ---- 1) 登录并进入个人资料与安全 ----
    await page.goto(BASE + '/app-v2/login', { waitUntil: 'domcontentloaded' });
    await page.fill('#identifier', creds.email);
    await page.fill('#password', creds.password);
    await page.click('button[type=submit]');
    await page.waitForURL((u) => u.pathname.startsWith('/app-v2/home'), { timeout: 20000 });
    await page.goto(BASE + '/app-v2/profile', { waitUntil: 'domcontentloaded' });
    const startBtn = page.getByRole('button', { name: '启用 MFA' });
    await startBtn.waitFor({ timeout: 20000 });
    const mfaCard = page.locator('section').filter({ hasText: '多因素认证（MFA）' }).first();
    chk((await mfaCard.innerText()).includes('未启用'), '安全设置页显示「未启用」');

    // ---- 2) 启用：二维码 + 密钥 + otpauth URI ----
    await startBtn.click();
    const qr = page.locator('svg[role="img"]').first();
    await qr.waitFor({ timeout: 15000 });
    const qrPaths = await qr.locator('path').count();
    chk(qrPaths > 0, '服务端签发后页面渲染出二维码（内联 SVG）', 'path=' + qrPaths);
    // 用内容特征挑元素，而不是 .mono 的第 n 个：页面上还有邮箱、用户 ID 等同样等宽字体的元素。
    const monos = (await page.locator('.mono').allInnerTexts()).map((t) => t.trim());
    const secretText = monos.find((t) => /^[A-Z2-7]{4}(\s*[A-Z2-7]{4})+$/.test(t)) ?? '';
    const secret = secretText.replace(/\s+/g, '');
    chk(/^[A-Z2-7]{16,}$/.test(secret), '页面展示可手工录入的 Base32 密钥', secretText || JSON.stringify(monos.slice(0, 5)));
    const uriText = monos.find((t) => t.startsWith('otpauth://')) ?? '';
    chk(uriText.startsWith('otpauth://totp/'), '页面展示 otpauth URI', uriText.slice(0, 42) || JSON.stringify(monos.slice(0, 5)));
    await page.screenshot({ path: SHOTS + '/01-enroll.png' });

    // ---- 3) 用真实 TOTP 确认 ----
    const confirmBtn = page.getByRole('button', { name: '确认启用' });
    chk(await confirmBtn.isDisabled(), '未填验证码时「确认启用」禁用');
    await page.locator('#mfa_enroll_code').fill(totp(secret));
    await confirmBtn.click();

    // ---- 4) 恢复码一次性弹窗：未勾选关不掉 ----
    const recovery = page.locator('[role="dialog"]').filter({ hasText: '请立即保存恢复码' }).first();
    try {
      await recovery.waitFor({ timeout: 20000 });
    } catch (err) {
      // 失败时必须自己说明原因：把当前所有弹窗文本与页面提示带上，避免只看到一句超时。
      const dialogs = await page.locator('[role="dialog"]').allInnerTexts().catch(() => []);
      chk(false, '确认注册后弹出恢复码弹窗', 'dialogs=' + JSON.stringify(dialogs).slice(0, 200));
      throw err;
    }
    const recoveryText = (await recovery.innerText()).replace(/\s+/g, ' ');
    chk(recoveryText.includes('10 个恢复码'), '弹窗说明一次性展示 10 个恢复码', recoveryText.slice(0, 70));
    const codes = (await recovery.locator('span.mono').allInnerTexts()).map((t) => t.trim()).filter(Boolean);
    chk(codes.length === 10 && new Set(codes).size === 10, '弹窗一次性展示 10 个互不相同的恢复码', codes.slice(0, 2).join(' , '));
    const ack = recovery.locator('input[type=checkbox]').first();
    const closeBtn = recovery.locator('.dialog__close').first();
    chk(await closeBtn.isDisabled(), '未勾选「我已保存」时关闭按钮禁用');
    await page.screenshot({ path: SHOTS + '/02-recovery.png' });
    await ack.check();
    chk(await closeBtn.isEnabled(), '勾选后可以关闭恢复码弹窗');
    await closeBtn.click();
    await recovery.waitFor({ state: 'hidden', timeout: 10000 });

    // ---- 5) 状态变「已启用」，且服务端真的开始要求第二步 ----
    await page.waitForTimeout(1500);
    const cardText = await mfaCard.innerText();
    chk(cardText.includes('已启用'), '安全设置页状态变为「已启用」', cardText.replace(/\s+/g, ' ').slice(0, 60));
    await page.screenshot({ path: SHOTS + '/03-enabled.png' });
    const afterEnroll = await loginStatus(creds.email, creds.password);
    chk(afterEnroll.status === 202 && afterEnroll.state === 'mfa_required' && !!afterEnroll.challenge,
      '新登录被要求第二步（202 + challenge_id）', 'status=' + afterEnroll.status + ' state=' + afterEnroll.state);

    // ---- 5b) 恢复码在真实登录页里可用，且只能用一次 ----
    const recoveryLogin = async (code) => {
      const c2 = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 800 } });
      const p2 = await c2.newPage();
      await p2.goto(BASE + '/app-v2/login', { waitUntil: 'domcontentloaded' });
      await p2.fill('#identifier', creds.email);
      await p2.fill('#password', creds.password);
      await p2.click('button[type=submit]');
      await p2.waitForSelector('#mfa_code', { timeout: 20000 });
      await p2.fill('#mfa_code', code);
      await p2.click('button[type=submit]');
      let landed = true;
      try {
        await p2.waitForURL((u) => u.pathname.startsWith('/app-v2/home'), { timeout: 15000 });
      } catch {
        landed = false;
      }
      const dlg = landed ? '' : (await p2.locator('[role="dialog"]').allInnerTexts().catch(() => [])).join(' ').replace(/\s+/g, ' ').slice(0, 80);
      await c2.close();
      return { landed, dialog: dlg };
    };
    const first = await recoveryLogin(codes[0]);
    chk(first.landed, '恢复码可以在登录第二步使用（进入工作台）', first.dialog);
    const reuse = await recoveryLogin(codes[0]);
    chk(!reuse.landed && reuse.dialog.includes('不正确'), '同一个恢复码不能再次使用（被拒并提示验证码不正确）', reuse.dialog);

    // ---- 6) 关闭 MFA：二次确认 + 状态回退 + 登录不再要第二步 ----
    await page.getByRole('button', { name: '关闭 MFA' }).first().click();
    const confirm = page.locator('[role="dialog"]').filter({ hasText: '关闭多因素认证' }).first();
    await confirm.waitFor({ timeout: 10000 });
    await confirm.getByRole('button', { name: '关闭 MFA' }).click();
    await page.waitForTimeout(1500);
    const cardText2 = await mfaCard.innerText();
    chk(cardText2.includes('未启用'), '关闭后状态回到「未启用」', cardText2.replace(/\s+/g, ' ').slice(0, 60));
    const afterDisable = await loginStatus(creds.email, creds.password);
    chk(afterDisable.status === 200 && afterDisable.state === 'authenticated',
      '关闭后新登录不再要求第二步', 'status=' + afterDisable.status + ' state=' + afterDisable.state);

    chk(pageErrors.length === 0, '整个流程没有未捕获的前端异常', pageErrors.slice(0, 2).join(' | '));
  } finally {
    await browser.close();
    console.log('teardown=' + JSON.stringify(fixture('teardown')));
  }
  console.log('=== summary === PASS=' + pass + ' FAIL=' + fail + ' SHOTS=' + SHOTS);
  if (fail > 0) process.exit(1);
}

main().catch((err) => {
  console.error('E2E aborted:', err);
  process.exit(1);
});
