// C1 switch UI -- post-login workspace switch in the real user console.
//
// Proves, with the deployed build: the switcher exists, cancel is safe, a real
// switch rebinds the session, the header label follows, and -- with the dashboard
// response artificially delayed -- the page shows NO data from the previous
// workspace while the new one loads (no stale-workspace flash).
// Non-ASCII UI literals are \u escapes so this file stays ASCII-only.

import fs from 'node:fs';
import { chromium } from '@playwright/test';

const BASE = process.env.C1_BASE || 'https://127.0.0.1:8448';
const EV = process.env.EV;
const WS_A = process.env.C1_WS_A;
const WS_B = process.env.C1_WS_B || '62a87634-da99-40b9-a08e-9edbd339551c';
const MULTI = '/home/euripides/.p1gate-multi.env';

const T_SCOPE_BTN = '\u53e3\u5f84\u8bf4\u660e';
const T_SWITCH_TITLE = '\u5207\u6362\u5de5\u4f5c\u533a';
const DASH = '\u2014';

function creds(file) {
  const txt = fs.readFileSync(file, 'utf8');
  const email = (txt.match(/_EMAIL=(.*)/) || [])[1];
  const pw = (txt.match(/_PW=(.*)/) || [])[1];
  if (!email || !pw) throw new Error('fixture credentials missing');
  return { email: email.trim(), pw: pw.trim() };
}

const checks = [];
function chk(id, cond, detail) {
  checks.push({ id, result: cond ? 'PASS' : 'FAIL', detail: String(detail) });
  console.log((cond ? 'PASS ' : 'FAIL ') + id + ' :: ' + detail);
}

const sessionWorkspace = (page) => page.evaluate(async () => {
  const r = await fetch('/api/auth/v1/session', {
    credentials: 'same-origin', headers: { 'X-Auth-Audience': 'user' },
  });
  if (!r.ok) return 'http_' + r.status;
  const j = await r.json();
  return ((j && j.data) || {}).workspace_id || '';
});

// The dashboard scope is surfaced in the "scope" dialog: its value rows are
// organization / workspace / statistics range, in that order.
async function readScopeDialog(page) {
  await page.locator('button:has-text("' + T_SCOPE_BTN + '")').first().click();
  await page.waitForSelector('.dialog__kv dd', { timeout: 10000 });
  const values = await page.evaluate(() =>
    Array.from(document.querySelectorAll('.dialog__kv dd')).map((d) => d.textContent.trim()));
  return { org: values[0] || '', ws: values[1] || '', rows: values };
}
async function closeDialog(page) {
  await page.keyboard.press('Escape');
  await page.waitForTimeout(250);
}

// The fixture account still carries its initial password, so the console raises a
// post-login notice dialog. It must be dismissed before the page underneath is
// clickable (attempt 1 timed out here: the backdrop intercepted pointer events).
async function dismissDialogs(page, max = 4) {
  for (let i = 0; i < max; i += 1) {
    if ((await page.locator('.dialog').count()) === 0) return;
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  }
}

const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.C1_CHROMIUM || '/usr/bin/chromium-browser',
});
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
const dashScopes = [];
page.on('response', async (res) => {
  if (!res.url().includes('/api/v2/dashboard')) return;
  try {
    const body = await res.json();
    dashScopes.push(((body || {}).data || {}).scope && body.data.scope.workspace_id);
  } catch { /* ignore */ }
});

try {
  // ---- login (multi-member -> selector -> workspace B) ----
  await page.goto(BASE + '/app-v2/login', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#identifier', { timeout: 20000 });
  const who = creds(MULTI);
  await page.fill('#identifier', who.email);
  await page.fill('#password', who.pw);
  await page.locator('#password').press('Enter');
  await page.waitForSelector('.dialog', { timeout: 20000 });
  await page.locator('.dialog__foot button:has-text("s7js-b")').first().click();
  await page.waitForURL(/\/app-v2\/(home|$)/, { timeout: 25000 });
  await page.waitForTimeout(1500);
  await dismissDialogs(page);
  chk('login_lands_on_home', /\/app-v2\/home/.test(page.url()), 'url=' + page.url());
  chk('initial_session_workspace_is_B', (await sessionWorkspace(page)) === WS_B, 'ws=' + (await sessionWorkspace(page)));

  const before = await readScopeDialog(page);
  chk('initial_ui_shows_workspace_B', before.ws === WS_B, 'ui_ws=' + before.ws);
  await closeDialog(page);

  // ---- switcher control exists ----
  const switcher = page.locator('[data-testid="workspace-switcher"]');
  chk('switcher_present', (await switcher.count()) === 1, 'count=' + (await switcher.count()));
  const labelBefore = (await switcher.first().textContent()) || '';

  // ---- cancel path ----
  await switcher.first().click();
  await page.waitForSelector('.dialog', { timeout: 10000 });
  const options = await page.locator('.dialog__foot button:has-text("s7js-")').count();
  chk('switch_dialog_lists_two_members', options === 2, 'options=' + options);
  await page.screenshot({ path: EV + '/shot-switch-dialog.png', fullPage: true });
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
  chk('esc_closes_switch_dialog', (await page.locator('.dialog').count()) === 0, 'dialog_closed');
  chk('cancel_keeps_session_B', (await sessionWorkspace(page)) === WS_B, 'ws=' + (await sessionWorkspace(page)));

  // ---- real switch with a delayed dashboard response ----
  await page.route('**/api/v2/dashboard', async (route) => {
    await new Promise((r) => setTimeout(r, 2500));
    await route.continue();
  });
  await switcher.first().click();
  await page.waitForSelector('.dialog', { timeout: 10000 });
  await page.locator('.dialog__foot button:has-text("s7js-a")').first().click();
  // A real switch also raises a success notice; clear it before driving the page
  // again (attempt 2 was blocked by its backdrop at this exact point).
  await page.waitForTimeout(700);
  await dismissDialogs(page);

  const midWs = await sessionWorkspace(page);
  chk('switch_applies_session_A', midWs === WS_A, 'ws=' + midWs);

  // While the new dashboard payload is still in flight the page must not render the
  // previous workspace's scope. Data is cleared on scope change, so the dialog shows
  // the placeholder instead of workspace B.
  const during = await readScopeDialog(page);
  chk('no_stale_workspace_during_reload', during.ws !== WS_B && during.ws === DASH,
    'ui_ws_during_reload=' + JSON.stringify(during.ws) + ' rows=' + JSON.stringify(during.rows));
  await page.screenshot({ path: EV + '/shot-switch-during-reload.png', fullPage: true });
  await closeDialog(page);

  await page.waitForTimeout(3500);
  await dismissDialogs(page);
  const after = await readScopeDialog(page);
  chk('after_reload_shows_workspace_A', after.ws === WS_A, 'ui_ws=' + after.ws);
  await closeDialog(page);
  chk('dashboard_refetched_for_new_scope', dashScopes.includes(WS_A) && dashScopes.includes(WS_B),
    'observed_dashboard_scopes=' + JSON.stringify(dashScopes.slice(-6)));

  const labelAfter = (await switcher.first().textContent()) || '';
  chk('switcher_label_changed', labelAfter !== labelBefore && labelAfter.includes('s7js-a'),
    'before=' + JSON.stringify(labelBefore) + ' after=' + JSON.stringify(labelAfter));

  const overflow = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, iw: window.innerWidth }));
  chk('no_horizontal_overflow_after_switch', overflow.sw <= overflow.iw + 1, JSON.stringify(overflow));
  await page.screenshot({ path: EV + '/shot-after-switch.png', fullPage: true });

  // ---- restore fixture state (switch back to B) ----
  await dismissDialogs(page);
  await switcher.first().click();
  await page.waitForSelector('.dialog', { timeout: 10000 });
  await page.locator('.dialog__foot button:has-text("s7js-b")').first().click();
  await page.waitForTimeout(1200);
  chk('fixture_restored_to_B', (await sessionWorkspace(page)) === WS_B, 'ws=' + (await sessionWorkspace(page)));
} catch (err) {
  chk('no_exception', false, 'error=' + (err && err.message));
} finally {
  await ctx.close();
  await browser.close();
}

const pass = checks.filter((c) => c.result === 'PASS').length;
const fail = checks.length - pass;
fs.writeFileSync(EV + '/c1-switch-ui-results.json', JSON.stringify({ checks, pass, fail }, null, 1));
console.log('C1_SWITCH_UI_PASS=' + pass + ' C1_SWITCH_UI_FAIL=' + fail);
process.exit(fail === 0 && pass > 0 ? 0 : 4);
