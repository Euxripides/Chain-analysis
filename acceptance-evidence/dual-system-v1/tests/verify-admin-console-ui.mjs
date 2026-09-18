session_id: e117cde8-f5a2-4375-a0e8-4571f0fb33c2
truncated: false
size: 5833

import { chromium } from '@playwright/test';
import fs from 'node:fs';

const BASE = 'http://127.0.0.1:8088';
const EMAIL = process.env.UI_EMAIL;
const PW = process.env.UI_PW;
const NEW_USER = 's8ui-' + Date.now() + '@s8verify6.local';
const SHOTS = '/tmp/admin-ui-shots';
fs.mkdirSync(SHOTS, { recursive: true });

let pass = 0, fail = 0;
const check = (name, ok, extra = '') => {
  if (ok) { pass++; console.log('  PASS  ' + name + (extra ? '  ' + extra : '')); }
  else { fail++; console.log('  FAIL  ' + name + (extra ? '  ' + extra : '')); }
};
const body = (page) => page.locator('body').innerText();
const waitText = (page, needle, timeout = 20000) =>
  page.waitForFunction((t) => document.body.innerText.includes(t), needle, { timeout }).then(() => true).catch(() => false);

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
page.on('pageerror', (e) => console.log('  [pageerror] ' + e.message.slice(0, 160)));

// ---- 登录 ----
await page.goto(BASE + '/admin/login', { waitUntil: 'domcontentloaded' });
await page.fill('#identifier', EMAIL);
await page.fill('#password', PW);
await page.click('button[type=submit]');
await page.waitForURL((u) => !u.pathname.endsWith('/admin/login'), { timeout: 25000 });
await waitText(page, '运营总览');
check('管理员登录成功', true, page.url());
await page.screenshot({ path: SHOTS + '/01-login.png', fullPage: true });

// ---- 导航 ----
check('导航出现「账号安全」', (await page.getByRole('link', { name: '账号安全' }).count()) > 0);
check('导航出现「用户与成员」', (await page.getByRole('link', { name: '用户与成员' }).count()) > 0);

// ---- 账号安全页 ----
await page.getByRole('link', { name: '账号安全' }).first().click();
await page.waitForURL(/account\/security/, { timeout: 15000 });
check('账号安全页：MFA 卡片渲染', await waitText(page, '多因素认证（MFA）'));
check('账号安全页：启用 MFA 按钮', (await page.getByRole('button', { name: '启用 MFA' }).count()) > 0);
check('账号安全页：密码卡片', await waitText(page, '修改密码'));
check('账号安全页：会话卡片', await waitText(page, '我的会话'));
check('账号安全页：设备卡片', await waitText(page, '我的设备'));
check('账号安全页：本页只影响自己', (await body(page)).includes('本页只影响你自己的账号'));
await page.screenshot({ path: SHOTS + '/02-security.png', fullPage: true });

// ---- 新建用户 ----
await page.getByRole('link', { name: '用户与成员' }).first().click();
await page.waitForURL(/\/users/, { timeout: 15000 });
await page.getByRole('button', { name: '新建用户' }).first().waitFor({ timeout: 15000 });
check('用户页：新建用户按钮', (await page.getByRole('button', { name: '新建用户' }).count()) > 0);
await page.getByRole('button', { name: '新建用户' }).first().click();
await waitText(page, '登录邮箱');
await page.screenshot({ path: SHOTS + '/03-create-dialog.png', fullPage: true });
check('新建用户弹窗：四个字段齐全',
  (await page.locator('#dialog-field-email').count()) === 1 &&
  (await page.locator('#dialog-field-display_name').count()) === 1 &&
  (await page.locator('#dialog-field-role_key').count()) === 1 &&
  (await page.locator('#dialog-field-reason').count()) === 1);
await page.fill('#dialog-field-email', NEW_USER);
await page.fill('#dialog-field-display_name', 'S8 UI 验证用户');
await page.fill('#dialog-field-reason', 'S8 UI 验证：后台建号');
await page.getByRole('button', { name: '创建账号' }).click();
check('创建成功：一次初始密码弹窗', await waitText(page, '初始密码只在本弹窗显示一次'), '');
const okBody = await body(page);
check('创建成功：提示个人空间', okBody.includes('个人空间'), '');
check('创建成功：密码可见性说明', okBody.includes('仅此一次'));
await page.screenshot({ path: SHOTS + '/04-created.png', fullPage: true });
await page.getByRole('button', { name: '知道了' }).first().click();
await page.waitForTimeout(1200);
check('列表出现新用户', (await body(page)).includes(NEW_USER));

// ---- 删除（离职）----
await page.getByRole('button', { name: /S8 UI 验证用户/ }).first().click();
check('详情弹窗：删除入口', await waitText(page, '删除（离职）'));
await page.screenshot({ path: SHOTS + '/05-detail.png', fullPage: true });
await page.getByRole('button', { name: '删除（离职）' }).first().click();
check('影响预览：列出归档/匿名化/保留', await waitText(page, '影响预览'));
const prev = await body(page);
check('影响预览内容完整', prev.includes('归档') && prev.includes('匿名化') && prev.includes('保留'));
await page.screenshot({ path: SHOTS + '/06-delete-preview.png', fullPage: true });
await page.getByRole('button', { name: '继续' }).first().click();
await waitText(page, '删除原因（必填）');
await page.fill('#dialog-field-reason', 'S8 UI 验证：离职删除');
await page.locator('#dialog-field-ack').check();
await page.screenshot({ path: SHOTS + '/07-delete-exec.png', fullPage: true });
await page.getByRole('button', { name: '确认删除' }).first().click();
check('删除成功：结果弹窗', await waitText(page, '用户已删除（离职）'));
const done = await body(page);
check('结果弹窗：匿名化邮箱 + 归档说明', done.includes('匿名化邮箱') && done.includes('已归档个人空间'));
await page.screenshot({ path: SHOTS + '/08-deleted.png', fullPage: true });

console.log('');
console.log('===== 后台 UI 浏览器验证：PASS=' + pass + ' FAIL=' + fail + ' =====');
console.log('NEW_USER=' + NEW_USER);
await browser.close();
process.exit(fail === 0 ? 0 : 1);
