import {chromium} from '@playwright/test';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
const root=new URL('../../',import.meta.url),out=new URL((process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-verification')+'/verification-browser/',root);mkdirSync(out,{recursive:true});
const aid=readFileSync(new URL('../verification-analysis-id.txt',out),'utf8').trim(),txaid=readFileSync(new URL('../execution-analysis-id.txt',out),'utf8').trim(),checks=[];
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify(checks,null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw new Error(scenario);}
const base='http://127.0.0.1:18093/app-v2/contract-analyses/',browser=await chromium.launch({headless:true});
try{for(const profile of [{name:'desktop',viewport:{width:1440,height:900}},{name:'mobile',viewport:{width:390,height:844}}]){
 const context=await browser.newContext({viewport:profile.viewport,storageState:'/tmp/contract-analysis-private-20261014/investigator-browser.json'}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(base+aid+'?tab=runtime&section=authorization_models',{waitUntil:'domcontentloaded'});
 await page.getByRole('columnheader',{name:'验证命题',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
 check(profile.name+'-actual-owner-model',/owner_identity_integrity/.test(await page.locator('.ca-table tbody').innerText()));
 await page.getByRole('button',{name:/^状态转换 \(/}).click();await page.getByRole('columnheader',{name:'成功路径数',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
 check(profile.name+'-actual-state-transitions',await page.locator('.ca-table tbody tr').count()>0);
 await page.getByRole('button',{name:/^证明义务 \(/}).click();await page.getByRole('columnheader',{name:'成立条件',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
 await page.locator('.ca-table tbody tr').first().getByRole('button',{name:'详情',exact:true}).click();const modal=page.locator('[role=dialog], [role=alertdialog]').first();await modal.waitFor({state:'visible'});
 check(profile.name+'-proof-details-preserve-assumptions-and-file',/proof_artifact_id/.test(await modal.innerText())&&/assumptions/.test(await modal.innerText()));
 const box=await modal.boundingBox();check(profile.name+'-proof-dialog-fits',box.x>=-1&&box.y>=-1&&box.x+box.width<=profile.viewport.width+1&&box.y+box.height<=profile.viewport.height+1);
 await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});
 await page.getByRole('button',{name:/^模型覆盖 \(/}).click();await page.getByRole('columnheader',{name:'全部经济模型已证明',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
 check(profile.name+'-whole-model-closure-is-explicit',(await page.locator('.ca-table tbody').innerText()).includes('false'));
 check(profile.name+'-model-page-no-overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
 await page.screenshot({path:new URL(profile.name+'-models.png',out).pathname,fullPage:true});
 await page.goto(base+aid+'?tab=evidence&section=artifacts',{waitUntil:'domcontentloaded'});const proofRow=page.locator('.ca-table tbody tr').filter({hasText:'verification-'}).first();await proofRow.waitFor();
 const [download]=await Promise.all([page.waitForEvent('download'),proofRow.getByRole('button',{name:'下载',exact:true}).click()]);const file=new URL(profile.name+'-proof.json',out).pathname;await download.saveAs(file);const proof=JSON.parse(readFileSync(file,'utf8'));
 check(profile.name+'-actual-proof-download',proof.schema_version==='contract-property-proof-v1'&&proof.property.property_id===proof.query.property_id&&Boolean(proof.property.proof_artifact_id));
 await page.goto(base+txaid+'?tab=transactions&section=external_return_observations',{waitUntil:'domcontentloaded'});await page.getByRole('columnheader',{name:'源码关联',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
 check(profile.name+'-actual-execution-callsite-binding',(await page.locator('.ca-table tbody').innerText()).includes('已按执行指令和上下文定位'));
 check(profile.name+'-execution-page-no-overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:new URL(profile.name+'-execution.png',out).pathname,fullPage:true});
 check(profile.name+'-no-runtime-errors',errors.length===0,errors);await context.close();
}}finally{await browser.close();}
