session_id: e117cde8-f5a2-4375-a0e8-4571f0fb33c2
truncated: false
size: 4170

import { chromium } from '@playwright/test';
import crypto from 'node:crypto';
import fs from 'node:fs';

const BASE = 'http://127.0.0.1:8088';
const SHOTS = '/tmp/admin-ui-shots';
fs.mkdirSync(SHOTS, { recursive: true });

// ---- RFC 6238 TOTP（与后端 mfa/totp.go 同参数：SHA1 / 6 位 / 30 秒）----
const B32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
function base32Decode(s) {
  let bits = '';
  for (const ch of s.toUpperCase().replace(/=+$/, '')) {
    const idx = B32.indexOf(ch);
    if (idx < 0) continue;
    bits += idx.toString(2).padStart(5, '0');
  }
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(bytes);
}
function totp(secret, unixSeconds = Math.floor(Date.now() / 1000)) {
  const counter = Math.floor(unixSeconds / 30);
  const buf = Buffer.alloc(8);
  buf.writeUInt32BE(Math.floor(counter / 2 ** 32), 0);
  buf.writeUInt32BE(counter % 2 ** 32, 4);
  const mac = crypto.createHmac('sha1', base32Decode(secret)).update(buf).digest();
  const off = mac[mac.length - 1] & 0x0f;
  const code = ((mac[off] & 0x7f) << 24 | mac[off + 1] << 16 | mac[off + 2] << 8 | mac[off + 3]) % 1e6;
  return String(code).padStart(6, '0');
}

let pass = 0, fail = 0;
const check = (n, ok, extra = '') => { ok ? pass++ : fail++; console.log('  ' + (ok ? 'PASS' : 'FAIL') + '  ' + n + (extra ? '  ' + extra : '')); };
const waitText = (page, t, ms = 20000) => page.waitForFunction((x) => document.body.innerText.includes(x), t, { timeout: ms }).then(() => true).catch(() => false);

const browser = await chromium.launch();
const page = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
await page.goto(BASE + '/admin/login', { waitUntil: 'domcontentloaded' });
await page.fill('#identifier', process.env.UI_EMAIL);
await page.fill('#password', process.env.UI_PW);
await page.click('button[type=submit]');
await page.waitForURL((u) => !u.pathname.endsWith('/admin/login'), { timeout: 25000 });
await page.goto(BASE + '/admin/account/security', { waitUntil: 'domcontentloaded' });
check('账号安全页加载', await waitText(page, '多因素认证（MFA）'));

// 若已启用先关闭，保证从"未启用"开始
if (await waitText(page, '关闭 MFA', 1500)) {
  await page.getByRole('button', { name: '关闭 MFA' }).click();
  await waitText(page, '关闭多因素认证');
  await page.getByRole('button', { name: '关闭 MFA' }).last().click();
  await waitText(page, 'MFA 已关闭');
  await page.getByRole('button', { name: '知道了' }).first().click().catch(() => {});
  await page.waitForTimeout(800);
}

// ---- 开始注册 ----
await page.getByRole('button', { name: '启用 MFA' }).click();
check('注册第一步：出现二维码与密钥', await waitText(page, '密钥（手动输入时使用）'));
check('二维码是本地内联 SVG（无外部图片请求）', (await page.locator('svg[role=img]').count()) > 0);
const secretRaw = (await page.locator('#mfa-secret').innerText()).replace(/\s+/g, '');
check('密钥非空且为 Base32', secretRaw.length >= 16 && /^[A-Z2-7]+$/.test(secretRaw), 'len=' + secretRaw.length);
await page.screenshot({ path: SHOTS + '/09-mfa-enroll.png', fullPage: true });

// ---- 用真实 TOTP 验证码确认 ----
const code = totp(secretRaw);
await page.fill('#mfa-code', code);
await page.getByRole('button', { name: '确认绑定' }).click();
check('确认绑定成功：弹出恢复码', await waitText(page, '请立即保存恢复码'));
const codes = await page.locator('ol li').allInnerTexts();
check('恢复码数量 = 10', codes.length === 10, 'got=' + codes.length);
await page.screenshot({ path: SHOTS + '/10-recovery-codes.png', fullPage: true });
await page.locator('input[type=checkbox]').first().check().catch(() => {});
await page.getByRole('button', { name: /我已保存，关闭/ }).click();
await page.waitForTimeout(1200);
check('页面状态变为已启用', await waitText(page, '已启用'));

console.log('');
console.log('===== 管理员 TOTP 注册验证：PASS=' + pass + ' FAIL=' + fail + ' =====');
await browser.close();
process.exit(fail === 0 ? 0 : 1);
