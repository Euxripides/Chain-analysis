import {chromium} from '@playwright/test';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
const root=new URL('../../',import.meta.url),base=process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-expressions',out=new URL(base+'/expression-browser/',root);mkdirSync(out,{recursive:true});
const aid=readFileSync(new URL('../dependency-analysis-id.txt',out),'utf8').trim(),image=readFileSync(new URL('../engine-image.txt',out),'utf8').trim(),checks=[];
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify(checks,null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw new Error(scenario);}
const browser=await chromium.launch({headless:true});
try{
 for(const profile of [{name:'desktop',viewport:{width:1440,height:900}},{name:'mobile',viewport:{width:375,height:812}}]){
  const context=await browser.newContext({viewport:profile.viewport,storageState:'/tmp/contract-analysis-private-20261014/investigator-browser.json',reducedMotion:'reduce'}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  const response=await context.request.get('http://127.0.0.1:18091/api/v2/contract-analyses/'+aid),job=(await response.json()).data;
  check(profile.name+'-final-engine-data',response.ok()&&job.metadata.metrics.engine_digest===image,{analysis_id:aid,image});
  const rowsResponse=await context.request.get('http://127.0.0.1:18091/api/v2/contract-analyses/'+aid+'?section=dataflow&page_size=200'),rows=(await rowsResponse.json()).data.items;
  const chosen=rows.find(r=>r.original_expression?.includes('.mul(')&&r.successful_return_paths?.some(p=>p.execution_steps?.length));check(profile.name+'-saved-ordered-calculations',!!chosen,chosen?{original_expression:chosen.original_expression,dataflow_id:chosen.dataflow_id}:undefined);
  await page.goto('http://127.0.0.1:18093/app-v2/contract-analyses/'+aid+'?tab=dataflow&method_id='+chosen.method_id,{waitUntil:'domcontentloaded'});
  await page.getByRole('columnheader',{name:'返回条件与公式',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
  let opened=false;
  for(const row of await page.locator('.ca-table tbody tr').all()){
   await row.getByRole('button',{name:'详情',exact:true}).click();const modal=page.locator('[role=dialog],[role=alertdialog]').first();await modal.waitFor({state:'visible'});
   const saved=JSON.parse(await modal.locator('pre.ca-json').innerText());
   if(saved.dataflow_id===chosen.dataflow_id&&saved.successful_return_paths?.some(p=>p.execution_steps?.length)){
    opened=true;check(profile.name+'-formula-and-ordered-domain-in-details',saved.successful_return_paths.every(p=>p.value!==undefined)&&saved.successful_return_paths.some(p=>p.execution_steps.some(s=>s.kind==='evaluate'&&s.expression_id&&s.expression)));
    const box=await modal.boundingBox();check(profile.name+'-modal-within-viewport',box.x>=-1&&box.y>=-1&&box.x+box.width<=profile.viewport.width+1&&box.y+box.height<=profile.viewport.height+1);
    await page.keyboard.press('Tab');check(profile.name+'-modal-retains-keyboard-focus',await modal.evaluate(el=>el.contains(document.activeElement)));
    await modal.locator('pre.ca-json').evaluate(el=>{const at=el.textContent.indexOf('"execution_steps"');if(at>=0&&el.firstChild){const range=document.createRange();range.setStart(el.firstChild,at);range.setEnd(el.firstChild,at+17);el.scrollTop+=range.getBoundingClientRect().top-el.getBoundingClientRect().top-20;}});
    await page.screenshot({path:new URL(profile.name+'-ordered-details.png',out).pathname});await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});break;
   }
   await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});
  }
  check(profile.name+'-ordered-record-opened',opened);check(profile.name+'-no-page-overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:new URL(profile.name+'-formulas.png',out).pathname});check(profile.name+'-no-runtime-errors',errors.length===0,errors);await context.close();
 }
}finally{await browser.close();}
