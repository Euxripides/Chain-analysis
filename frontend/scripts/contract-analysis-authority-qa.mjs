import {chromium} from '@playwright/test';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
const root=new URL('../../',import.meta.url),out=new URL((process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-semantics')+'/authority-browser/',root);mkdirSync(out,{recursive:true});
const aid=readFileSync(new URL('../authority-analysis-id.txt',out),'utf8').trim(),checks=[];
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify(checks,null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw new Error(scenario)}
const browser=await chromium.launch({headless:true});
try {
 for(const profile of [{name:'desktop',viewport:{width:1440,height:900}},{name:'mobile',viewport:{width:390,height:844}}]) {
  const context=await browser.newContext({viewport:profile.viewport,storageState:'/tmp/contract-analysis-private-20261014/investigator-browser.json'}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto('http://127.0.0.1:18093/app-v2/contract-analyses/'+aid+'?tab=permissions',{waitUntil:'domcontentloaded'});
  await page.getByRole('columnheader',{name:'调用地址条件',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
  check(profile.name+'-real-address-conditions',/0x[0-9a-f]{40}/i.test(await page.locator('.ca-table tbody').innerText()));
  await page.goto('http://127.0.0.1:18093/app-v2/contract-analyses/'+aid+'?tab=runtime&section=control_effects',{waitUntil:'domcontentloaded'});
  await page.getByRole('columnheader',{name:'资金影响',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
  check(profile.name+'-real-control-impact-labels',/资金计算参数|收款对象|执行条件|调用地址权限/.test(await page.locator('.ca-table tbody').innerText()));
  check(profile.name+'-full-address-in-control-results',/0x[0-9a-f]{40}/i.test(await page.locator('.ca-table tbody').innerText()));
  check(profile.name+'-viewport-has-no-overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  const configuredRate=page.locator('.ca-table tbody tr').filter({hasText:'basisPointsRate'}).first();await configuredRate.getByRole('button',{name:'详情',exact:true}).click();const modal=page.locator('[role=dialog], [role=alertdialog]').first();await modal.waitFor({state:'visible'});
  check(profile.name+'-details-preserve-snapshot-fields',(await modal.innerText()).includes('snapshot_observations'));
  const box=await modal.boundingBox();check(profile.name+'-dialog-in-viewport',box.x>=-1&&box.y>=-1&&box.x+box.width<=profile.viewport.width+1&&box.y+box.height<=profile.viewport.height+1);
  await page.keyboard.press('Tab');check(profile.name+'-dialog-traps-focus',await modal.evaluate(e=>e.contains(document.activeElement)));
  await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});check(profile.name+'-dialog-dismisses',true);
  await page.screenshot({path:new URL(profile.name+'.png',out).pathname,fullPage:true});check(profile.name+'-no-runtime-errors',errors.length===0,errors);await context.close();
 }
} finally {await browser.close()}
