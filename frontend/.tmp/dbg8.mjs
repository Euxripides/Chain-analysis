import { chromium } from '@playwright/test';
const b = await chromium.launch({ channel: 'msedge' });
const p = await b.newPage({ viewport: { width: 390, height: 844 } });
await p.goto('http://127.0.0.1:18088/app-v2/login', { waitUntil: 'domcontentloaded' });
await p.locator('#identifier').fill('admin@local');
await p.locator('#password').fill(process.env.PW);
await p.getByRole('button', { name: /^登录$/ }).click();
await p.waitForURL(/\/home$/);
const d = p.getByRole('dialog');
// 触屏/窄屏下登录后的提示弹窗仍会打开，先关掉再测量
if (await d.isVisible().catch(()=>false)) await p.keyboard.press('Escape');
await p.waitForTimeout(500);

const overflow = await p.evaluate(() => {
  const vw = document.documentElement.clientWidth;
  const out = [];
  document.querySelectorAll('body *').forEach(el => {
    const r = el.getBoundingClientRect();
    if (r.right > vw + 1 && !el.closest('.table-scroll')) {
      out.push({ tag: el.tagName, cls: (el.className||'').toString().slice(0,45), w: Math.round(r.width), right: Math.round(r.right), pos: getComputedStyle(el).position });
    }
  });
  return { vw, sw: document.documentElement.scrollWidth, out: out.slice(0,6) };
});
console.log('OVERFLOW:', JSON.stringify(overflow, null, 1));

const colors = await p.evaluate(() => {
  const pick = (sel) => {
    const el = document.querySelector(sel);
    if (!el) return null;
    const walk = (n) => { while (n && n !== document.documentElement) { const c = getComputedStyle(n).backgroundColor; const m = c.match(/rgba?\(([^)]+)\)/); if (m) { const v = m[1].split(',').map(s=>parseFloat(s)); if (v.length < 4 || v[3] >= 0.999) return c; } n = n.parentElement; } return 'rgb(255,255,255)'; };
    return { sel, color: getComputedStyle(el).color, bg: walk(el) };
  };
  return [pick('.shell__link'), pick('.shell__nav'), pick('.shell__env'), pick('.metric__label'), pick('.shell__group-label')];
});
console.log('COLORS:', JSON.stringify(colors, null, 1));
await b.close();
