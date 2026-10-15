import {chromium} from '@playwright/test';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
const root=new URL('../../',import.meta.url),out=new URL((process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-mechanisms')+'/mechanism-browser/',root);mkdirSync(out,{recursive:true});
const dependency=readFileSync(new URL('../dependency-analysis-id.txt',out),'utf8').trim(),authority=readFileSync(new URL('../authority-analysis-id.txt',out),'utf8').trim(),checks=[];
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify(checks,null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw new Error(scenario)}
const browser=await chromium.launch({headless:true});
try{
 for(const profile of [{name:'desktop',viewport:{width:1440,height:900}},{name:'small-mobile',viewport:{width:375,height:812}},{name:'landscape',viewport:{width:844,height:390}}]){
  const context=await browser.newContext({viewport:profile.viewport,storageState:'/tmp/contract-analysis-private-20261014/investigator-browser.json',reducedMotion:'reduce'}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto('http://127.0.0.1:18093/app-v2/contract-analyses/'+authority+'?tab=permissions',{waitUntil:'domcontentloaded'});
  await page.getByRole('columnheader',{name:'角色与状态条件',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
  check(profile.name+'-saved-state-conditions-render',/要求 (true|false)/.test(await page.locator('.ca-table tbody').innerText()));
  await page.goto('http://127.0.0.1:18093/app-v2/contract-analyses/'+dependency+'?tab=dependencies',{waitUntil:'domcontentloaded'});
  await page.getByRole('columnheader',{name:'解析目标地址',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
  const resolved=page.locator('.ca-table tbody tr').filter({hasText:'已分析目标代码与状态上下文'}).first();await resolved.waitFor();
  check(profile.name+'-full-address-and-context',/0x[0-9a-f]{40}/i.test(await resolved.innerText()));
  check(profile.name+'-no-page-overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await resolved.getByRole('button',{name:'详情',exact:true}).click();const modal=page.locator('[role=dialog],[role=alertdialog]').first();await modal.waitFor({state:'visible'});
  check(profile.name+'-code-identity-in-saved-details',(await modal.innerText()).includes('dependency_code_hash'));
  const box=await modal.boundingBox();check(profile.name+'-dialog-within-viewport',box.x>=-1&&box.y>=-1&&box.x+box.width<=profile.viewport.width+1&&box.y+box.height<=profile.viewport.height+1);
  await page.keyboard.press('Tab');check(profile.name+'-focus-contained',await modal.evaluate(e=>e.contains(document.activeElement)));await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});check(profile.name+'-dialog-closes',true);
  await page.goto('http://127.0.0.1:18093/app-v2/contract-analyses/'+dependency+'?tab=runtime&section=protocols',{waitUntil:'domcontentloaded'});await page.getByRole('columnheader',{name:'协议方法公式',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
  check(profile.name+'-real-protocol-return-model',/getReserves\(\)/.test(await page.locator('.ca-table tbody').innerText()));
  check(profile.name+'-formula-column-is-readable',(await page.locator('.ca-table-protocols th').nth(2).boundingBox()).width>=360);
  check(profile.name+'-formula-table-has-no-page-overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:new URL(profile.name+'.png',out).pathname,fullPage:true});check(profile.name+'-no-script-errors',errors.length===0,errors);await context.close();
 }
}finally{await browser.close()}
console.log('MECHANISM_BROWSER_PASS',checks.length);
