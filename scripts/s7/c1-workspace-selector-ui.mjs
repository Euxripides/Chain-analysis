// C1 workspace selector (multi-member login) -- real browser verification.
// Runs the deployed user console at /app-v2/ on desktop and mobile viewports.
// Fixture credentials are read from the controlled env files on this host and are NEVER printed.
import fs from 'node:fs';
// @playwright/test is the pinned devDependency; a bare "playwright" import is not
// resolvable under pnpm's strict node_modules (preserved failure 20260913T152704Z).
import { chromium } from '@playwright/test';

const BASE = process.env.C1_BASE || 'https://127.0.0.1:8448';
const EV = process.env.EV;
const WS_B = process.env.C1_WS_B || '62a87634-da99-40b9-a08e-9edbd339551c';
const MULTI = '/home/euripides/.p1gate-multi.env';
const SINGLE = '/home/euripides/.p1gate-view-b.env';

function creds(file) {
  const txt = fs.readFileSync(file, 'utf8');
  const email = (txt.match(/_EMAIL=(.*)/) || [])[1];
  const pw = (txt.match(/_PW=(.*)/) || [])[1];
  if (!email || !pw) throw new Error('fixture credentials missing in ' + file);
  return { email: email.trim(), pw: pw.trim() };
}

const checks = [];
function chk(id, cond, detail) {
  checks.push({ id, result: cond ? 'PASS' : 'FAIL', detail: String(detail) });
  console.log((cond ? 'PASS ' : 'FAIL ') + id + ' :: ' + detail);
}

async function sessionWorkspace(page) {
  return page.evaluate(async () => {
    const r = await fetch('/api/auth/v1/session', {
      credentials: 'same-origin',
      headers: { 'X-Auth-Audience': 'user' },
    });
    if (!r.ok) return 'http_' + r.status;
    const j = await r.json();
    return ((j && j.data) || {}).workspace_id || '';
  });
}

async function login(page, who) {
  await page.goto(BASE + '/app-v2/login', { waitUntil: 'domcontentloaded' });
  await page.fill('#identifier', who.email);
  await page.fill('#password', who.pw);
  await page.click('form button[type=submit]');
}

async function runViewport(browser, label, viewport, suffix) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport });
  const page = await ctx.newPage();
  const tag = label + '_' + suffix;
  try {
    await login(page, creds(MULTI));
    let optionsSeen = 0;
    let dialogVisible = false;
    try {
      await page.waitForSelector('button:has-text("s7js-")', { timeout: 20000 });
      dialogVisible = true;
      optionsSeen = await page.locator('button:has-text("s7js-")').count();
    } catch (err) {
      dialogVisible = false;
    }
    chk(tag + '_selector_dialog_shown', dialogVisible, 'dialog=' + dialogVisible);
    chk(tag + '_selector_options_count', optionsSeen === 2, 'options=' + optionsSeen);
    await page.screenshot({ path: EV + '/shot-' + tag + '-selector.png', fullPage: true });

    if (dialogVisible) {
      await page.locator('button:has-text("s7js-b")').first().click();
    }
    await page.waitForURL(/\/app-v2\/(home|$)/, { timeout: 20000 }).catch(() => undefined);
    await page.waitForTimeout(1500);
    const ws = await sessionWorkspace(page);
    chk(tag + '_session_bound_to_selected_workspace', ws === WS_B, 'workspace_id=' + ws);
    const url = page.url();
    chk(tag + '_landed_in_app', /\/app-v2\//.test(url) && !/login/.test(url), 'url=' + url);
    await page.screenshot({ path: EV + '/shot-' + tag + '-after.png', fullPage: true });
  } catch (err) {
    chk(tag + '_no_exception', false, 'error=' + (err && err.message));
  } finally {
    await ctx.close();
  }
}

async function runSingleControl(browser) {
  // negative control: a single-membership account must NOT be asked to choose.
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  try {
    await login(page, creds(SINGLE));
    await page.waitForTimeout(4000);
    const optionsSeen = await page.locator('button:has-text("s7js-")').count();
    chk('control_single_member_no_selector', optionsSeen === 0, 'option_buttons=' + optionsSeen);
    await page.waitForURL(/\/app-v2\/(home|$)/, { timeout: 15000 }).catch(() => undefined);
    const ws = await sessionWorkspace(page);
    chk('control_single_member_session_workspace', ws === WS_B, 'workspace_id=' + ws);
    await page.screenshot({ path: EV + '/shot-control-single.png', fullPage: true });
  } catch (err) {
    chk('control_single_member_no_exception', false, 'error=' + (err && err.message));
  } finally {
    await ctx.close();
  }
}

// The ms-playwright cache is intentionally empty on this host and msedge is not installed:
// every established acceptance round drives the system chromium via E2E_CHROMIUM_PATH, so this
// script uses the same executable. Attempt 2 failed on the missing headless shell and attempt 3
// on the missing msedge channel; both failures are preserved in their evidence directories.
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.C1_CHROMIUM || '/usr/bin/chromium-browser',
});
try {
  await runViewport(browser, 'desktop', { width: 1440, height: 900 }, '1440x900');
  await runViewport(browser, 'mobile', { width: 390, height: 844 }, '390x844');
  await runSingleControl(browser);
} finally {
  await browser.close();
}

const pass = checks.filter((c) => c.result === 'PASS').length;
const fail = checks.length - pass;
fs.writeFileSync(EV + '/c1-ui-results.json', JSON.stringify({ checks, pass, fail }, null, 1));
console.log('C1_UI_PASS=' + pass + ' C1_UI_FAIL=' + fail);
process.exit(fail === 0 && pass > 0 ? 0 : 4);
