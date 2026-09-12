import { chromium } from '@playwright/test';
const b = await chromium.launch({ channel: 'msedge' });
const p = await b.newPage({ viewport: { width: 1440, height: 900 } });
await p.goto('http://127.0.0.1:18088/app-v2/login', { waitUntil: 'domcontentloaded' });
await p.locator('#identifier').fill('admin@local');
await p.locator('#password').fill(process.env.PW);
await p.getByRole('button', { name: /^登录$/ }).click();
await p.waitForURL(/\/home$/);
const d = p.getByRole('dialog');
if (await d.isVisible().catch(()=>false)) await p.keyboard.press('Escape');
await p.waitForTimeout(400);
const res = await p.evaluate(() => {
  const parse = (v) => { const m = v.match(/rgba?\(([^)]+)\)/); if(!m) return null; const q=m[1].split(',').map(s=>parseFloat(s.trim())); return {rgb:q.slice(0,3), alpha: q.length>3?q[3]:1}; };
  const lum = (rgb) => { const [r,g,bl]=rgb.map(v=>{const c=v/255; return c<=0.03928?c/12.92:Math.pow((c+0.055)/1.055,2.4);}); return 0.2126*r+0.7152*g+0.0722*bl; };
  const ratio = (a,b)=>{const la=lum(a),lb=lum(b);const [hi,lo]=la>lb?[la,lb]:[lb,la];return (hi+0.05)/(lo+0.05);};
  const out=[];
  for (const el of document.querySelectorAll('body *')) {
    const text=(el.textContent||'').trim(); if(!text||el.children.length>0) continue;
    const r=el.getBoundingClientRect(); if(r.width<2||r.height<2) continue;
    const s=getComputedStyle(el); if(s.visibility==='hidden'||s.display==='none') continue;
    const fg=parse(s.color); if(!fg) continue;
    let bg=null,n=el;
    while(n&&n!==document.documentElement){const c=parse(getComputedStyle(n).backgroundColor); if(c&&c.alpha>=0.999){bg=c;break;} n=n.parentElement;}
    if(!bg) bg={rgb:[255,255,255],alpha:1};
    const fs=parseFloat(s.fontSize), bold=parseInt(s.fontWeight,10)>=700;
    const large= fs>=24||(fs>=18.66&&bold);
    const cr=ratio(fg.rgb,bg.rgb);
    const need=large?3:4.5;
    if(cr<need) out.push({text:text.slice(0,20),cls:(el.className||'').toString().slice(0,30),fg, bg, cr:+cr.toFixed(2), need, offscreen: r.left<0||r.right>document.documentElement.clientWidth});
  }
  return out.slice(0,12);
});
console.log(JSON.stringify(res,null,1));
await b.close();
