import { chromium } from '@playwright/test';
const b = await chromium.launch({ channel: 'msedge' });
const p = await b.newPage({ viewport: { width: 1440, height: 900 } });
await p.goto('http://127.0.0.1:18088/app-v2/login', { waitUntil: 'domcontentloaded' });
await p.locator('#identifier').fill('admin@local');
await p.locator('#password').fill(process.env.PW);
await p.getByRole('button', { name: /^登录$/ }).click();
await p.waitForURL(/\/home$/);
const d = p.getByRole('dialog'); if (await d.isVisible().catch(()=>false)) await p.keyboard.press('Escape');
const info = await p.evaluate(() => {
  const el = Array.from(document.querySelectorAll('span')).find(s => s.textContent.trim() === '我的概览');
  if (!el) return 'not found';
  const chain = [];
  let n = el;
  while (n && n.tagName !== 'BODY') {
    const cs = getComputedStyle(n);
    chain.push({ tag: n.tagName, cls: (n.className||'').toString().slice(0,40), color: cs.color, bg: cs.backgroundColor });
    n = n.parentElement;
  }
  return { outer: el.outerHTML.slice(0,120), chain };
});
console.log(JSON.stringify(info, null, 1));
await b.close();
