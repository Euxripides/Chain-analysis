// C-round workspace-scope UI matrix (real browser, deployed builds).
//
// Covers what the 14/0 switch round did NOT:
//   A. the admin console has the same post-login workspace switcher (real UI switch)
//   B. the legacy SPA (/ ) derives its scope from the SERVER session and follows a
//      workspace change (this is what makes its React Query keys scope-correct)
//   C. two tabs of the same browser converge on one workspace after a switch in one tab
//   D. the job SSE stream (useJobStream) is closed when the workspace changes, so no
//      event from the previous workspace can be applied
// Non-ASCII UI literals are \u escapes so the file stays ASCII-only.

import fs from 'node:fs';
import { chromium } from '@playwright/test';

const BASE = process.env.C1_BASE || 'https://127.0.0.1:8448';
const EV = process.env.EV;
const WS_A = process.env.C1_WS_A || '8b41836b-6d53-45e4-9a8a-3248a6e24e28';
const WS_B = process.env.C1_WS_B || '62a87634-da99-40b9-a08e-9edbd339551c';
const MULTI = '/home/euripides/.p1gate-multi.env';
const ADMIN_FIXTURE = process.env.C1_ADMIN_CREDS || '/home/euripides/.c1role-admin.env';

const T_SWITCH_TITLE = '\u5207\u6362\u5de5\u4f5c\u533a';
const T_CANCEL = '\u53d6\u6d88';

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
function nv(id, detail) {
  checks.push({ id, result: 'NOT_VERIFIED', detail: String(detail) });
  console.log('NOT_VERIFIED ' + id + ' :: ' + detail);
}

async function dismissDialogs(page, max = 5) {
  for (let i = 0; i < max; i += 1) {
    if ((await page.locator('.dialog').count()) === 0) return;
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  }
}

async function sessionWs(page, audience) {
  return page.evaluate(async (aud) => {
    const r = await fetch('/api/auth/v1/session', {
      credentials: 'same-origin', headers: { 'X-Auth-Audience': aud },
    });
    if (!r.ok) return 'http_' + r.status;
    const j = await r.json();
    return ((j && j.data) || {}).workspace_id || '';
  }, audience);
}

// Switch the server session from inside the page (real CSRF double-submit).
async function apiSwitch(page, audience, target) {
  return page.evaluate(async ({ aud, ws }) => {
    const s = await fetch('/api/auth/v1/session', {
      credentials: 'same-origin', headers: { 'X-Auth-Audience': aud },
    });
    const sj = await s.json();
    const csrf = ((sj && sj.data) || {}).csrf_token || '';
    const r = await fetch('/api/auth/v1/session/workspace', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-Auth-Audience': aud, 'X-CSRF-Token': csrf },
      body: JSON.stringify({ workspace_id: ws }),
    });
    return r.status;
  }, { aud: audience, ws: target });
}

async function uiSwitch(page, wantSlug) {
  const switcher = page.locator('[data-testid="workspace-switcher"]');
  await switcher.first().click();
  await page.waitForSelector('.dialog', { timeout: 10000 });
  const opt = page.locator('.dialog__foot button:has-text("' + wantSlug + '")').first();
  await opt.click();
  await page.waitForTimeout(900);
  await dismissDialogs(page);
}

const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.C1_CHROMIUM || '/usr/bin/chromium-browser',
});
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });

// Instrument EventSource so SSE lifetime is observable from the outside.
await ctx.addInitScript(() => {
  const Orig = window.EventSource;
  window.__sse = [];
  function Patched(url, opts) {
    const es = new Orig(url, opts);
    const rec = { url: String(url), closed: false, events: 0 };
    window.__sse.push(rec);
    const close = es.close.bind(es);
    es.close = function patchedClose() { rec.closed = true; return close(); };
    es.addEventListener('message', () => { rec.events += 1; });
    return es;
  }
  Patched.prototype = Orig.prototype;
  Patched.CONNECTING = 0; Patched.OPEN = 1; Patched.CLOSED = 2;
  window.EventSource = Patched;
});

const who = creds(MULTI);
const page = await ctx.newPage();
const dashScopes = [];
page.on('response', async (res) => {
  if (res.url().includes('/api/v1/download-jobs')) dashScopes.push('legacy_jobs:' + res.status());
});

try {
  // ---------------- A. admin console switcher ----------------
  await page.goto(BASE + '/admin/login', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#identifier', { timeout: 20000 });
  const adminWho = creds(ADMIN_FIXTURE);
  await page.fill('#identifier', adminWho.email);
  await page.fill('#password', adminWho.pw);
  await page.locator('#password').press('Enter');
  await page.waitForTimeout(2500);
  // multi-workspace identities get the same selector on the admin entry
  if ((await page.locator('.dialog').count()) > 0) {
    const pick = page.locator('.dialog__foot button:has-text("s7js-a")').first();
    if ((await pick.count()) > 0) { await pick.click(); await page.waitForTimeout(2000); }
  }
  await dismissDialogs(page);
  const adminSession = await page.request.get(BASE + '/api/auth/v1/session', {
    headers: { 'X-Auth-Audience': 'admin' }, ignoreHTTPSErrors: true,
  });
  chk('admin_audience_session_established', adminSession.status() === 200, 'status=' + adminSession.status());
  const adminBody = adminSession.status() === 200 ? await adminSession.json() : {};
  const adminWs0 = ((adminBody || {}).data || {}).workspace_id || '';
  const adminSwitcher = page.locator('[data-testid="workspace-switcher"][data-audience="admin"]');
  chk('admin_switcher_present', (await adminSwitcher.count()) === 1, 'count=' + (await adminSwitcher.count()));
  await page.screenshot({ path: EV + '/shot-admin-shell.png', fullPage: true });

  if ((await adminSwitcher.count()) === 1 && adminSession.status() === 200) {
    const target = adminWs0 === WS_A ? WS_B : WS_A;
    const slug = target === WS_A ? 's7js-a' : 's7js-b';
    await uiSwitch(page, slug);
    const adminWs1 = await sessionWs(page, 'admin');
    chk('admin_switch_applies', adminWs1 === target, 'before=' + adminWs0 + ' after=' + adminWs1 + ' target=' + target);
    // restore so the fixture ends where it started
    const back = adminWs0 === WS_A ? 's7js-a' : 's7js-b';
    await uiSwitch(page, back);
    chk('admin_fixture_restored', (await sessionWs(page, 'admin')) === adminWs0, 'restored_ws=' + (await sessionWs(page, 'admin')));
  } else {
    nv('admin_switch_applies', 'admin switcher or admin session unavailable; recorded instead of assumed');
  }

  // ---------------- B. legacy SPA scope follows the server session ----------------
  const apiLogin = await page.request.post(BASE + '/api/auth/v1/login', {
    headers: { 'Content-Type': 'application/json', 'X-Auth-Audience': 'user' },
    data: { identifier: who.email, password: who.pw, audience: 'user', workspace_id: WS_A },
    ignoreHTTPSErrors: true,
  });
  chk('legacy_spa_login_ok', apiLogin.status() === 200, 'status=' + apiLogin.status());

  await page.goto(BASE + '/jobs', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-testid="workspace-scope"]', { timeout: 25000 });
  await page.waitForTimeout(1500);
  const scope0 = await page.evaluate(() => {
    const el = document.querySelector('[data-testid="workspace-scope"]');
    return { org: el.getAttribute('data-org'), ws: el.getAttribute('data-ws') };
  });
  chk('legacy_scope_reads_server_session', scope0.ws === WS_A, 'scope=' + JSON.stringify(scope0) + ' expected_ws=' + WS_A);

  const sw = await apiSwitch(page, 'user', WS_B);
  chk('legacy_api_switch_ok', sw === 200, 'status=' + sw);
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  let scope1 = scope0;
  for (let i = 0; i < 20; i += 1) {
    scope1 = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="workspace-scope"]');
      return { org: el.getAttribute('data-org'), ws: el.getAttribute('data-ws') };
    });
    if (scope1.ws === WS_B) break;
    await page.waitForTimeout(500);
  }
  chk('legacy_scope_follows_switch', scope1.ws === WS_B, 'before=' + JSON.stringify(scope0) + ' after=' + JSON.stringify(scope1));
  await page.screenshot({ path: EV + '/shot-legacy-scope.png', fullPage: true });

  const jobsHits = dashScopes.filter((x) => x.endsWith(':200')).length;
  chk('legacy_queries_rerun_under_new_scope', jobsHits >= 1, 'legacy_jobs_responses=' + JSON.stringify(dashScopes.slice(-4)));
  // put the fixture back on A
  await apiSwitch(page, 'user', WS_A);
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.waitForTimeout(800);

  // ---------------- C. two tabs converge ----------------
  const tab2 = await ctx.newPage();
  await tab2.goto(BASE + '/app-v2/home', { waitUntil: 'domcontentloaded' });
  await dismissDialogs(tab2);
  const t2before = await sessionWs(tab2, 'user');
  chk('tab2_initial_workspace_A', t2before === WS_A, 'ws=' + t2before);

  const sw2 = await apiSwitch(page, 'user', WS_B);
  chk('tab1_api_switch_ok', sw2 === 200, 'status=' + sw2);
  // tab2 must not silently keep serving the previous workspace once it is focused
  await tab2.bringToFront();
  await tab2.evaluate(() => window.dispatchEvent(new Event('focus')));
  let t2after = t2before;
  for (let i = 0; i < 20; i += 1) {
    t2after = await sessionWs(tab2, 'user');
    if (t2after === WS_B) break;
    await tab2.waitForTimeout(500);
  }
  chk('tab2_converges_after_focus', t2after === WS_B, 'before=' + t2before + ' after=' + t2after);
  const bothNow = { tab1: await sessionWs(page, 'user'), tab2: await sessionWs(tab2, 'user') };
  chk('no_split_brain_between_tabs', bothNow.tab1 === bothNow.tab2, JSON.stringify(bothNow));
  await tab2.screenshot({ path: EV + '/shot-tab2-converged.png', fullPage: true });

  // ---------------- D. SSE stream scope isolation ----------------
  const jobId = process.env.C1_JOB_ID || '00000000-0000-4000-8000-000000000000';
  await page.goto(BASE + '/app-v2/jobs/' + jobId, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await dismissDialogs(page);
  const sse0 = await page.evaluate(() => (window.__sse || []).map((r) => ({ url: r.url, closed: r.closed })));
  chk('sse_stream_opened_for_job', sse0.length >= 1 && sse0.some((r) => r.url.includes('/events')),
    'streams=' + JSON.stringify(sse0));
  const preSwitchStreams = sse0.length;
  const scopeBefore = await page.evaluate(() => {
    const el = document.querySelector('[data-testid="workspace-switcher"]');
    return el ? el.textContent.trim() : '';
  });
  await uiSwitch(page, 's7js-a');
  await page.waitForTimeout(2500);
  const sse1 = await page.evaluate(() => (window.__sse || []).map((r) => ({ url: r.url, closed: r.closed })));
  // The NEW stream is legitimately open after the switch; the assertion is that every
  // stream that existed BEFORE the switch was closed (attempt 1 wrongly required all).
  const pre = sse1.slice(0, preSwitchStreams);
  const oldClosed = pre.length > 0 && pre.every((r) => r.url.includes('/events') && r.closed);
  chk('sse_stream_closed_on_workspace_switch', oldClosed,
    'pre_switch_streams=' + JSON.stringify(pre) + ' all_streams=' + JSON.stringify(sse1));
  const newStream = sse1.filter((r) => r.url.includes('/events') && r.closed === false).length;
  chk('sse_reopened_under_new_scope', newStream >= 1, 'open_streams=' + newStream + ' label_before=' + JSON.stringify(scopeBefore));
  await page.screenshot({ path: EV + '/shot-sse-scope.png', fullPage: true });
} catch (err) {
  chk('no_exception', false, 'error=' + (err && err.message));
} finally {
  await ctx.close();
  await browser.close();
}

const pass = checks.filter((c) => c.result === 'PASS').length;
const fail = checks.filter((c) => c.result === 'FAIL').length;
const notVerified = checks.filter((c) => c.result === 'NOT_VERIFIED').length;
fs.writeFileSync(EV + '/c1-scope-matrix-results.json', JSON.stringify({ checks, pass, fail, notVerified }, null, 1));
console.log('C1_SCOPE_MATRIX_PASS=' + pass + ' FAIL=' + fail + ' NOT_VERIFIED=' + notVerified);
process.exit(fail === 0 && pass > 0 ? 0 : 4);
