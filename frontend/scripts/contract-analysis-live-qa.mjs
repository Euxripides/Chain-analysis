import {chromium} from '@playwright/test';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
const base=process.env.CONTRACT_QA_CONSOLE||'http://127.0.0.1:18083/app-v2';
const root=new URL('../../',import.meta.url),out=new URL((process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis')+'/browser/',root);mkdirSync(out,{recursive:true});
const id=readFileSync(new URL('../ui-analysis-id.txt',out),'utf8').trim(),rows=[];
const state='/tmp/contract-analysis-private-20261014/investigator-browser.json';
const browser=await chromium.launch({headless:true});
const check=(name,condition,evidence)=>{rows.push({scenario:name,status:condition?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify(rows,null,2));console.log(name,rows.at(-1).status);if(!condition)throw new Error(name)};
async function dialogCheck(page,name){
 const modal=page.locator('[role=dialog], [role=alertdialog]').first();await modal.waitFor({state:'visible'});
 const box=await modal.boundingBox(),viewport=page.viewportSize();check(name+'-bounds',box.x>=-1&&box.y>=-1&&box.x+box.width<=viewport.width+1&&box.y+box.height<=viewport.height+1,box);
 for(let n=0;n<8;n++){await page.keyboard.press('Tab');check(name+'-focus-'+n,await modal.evaluate(element=>element.contains(document.activeElement)))}
 await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});
 check(name+'-escape-close',await page.locator('[role=dialog], [role=alertdialog]').count()===0);
}
try{
 for(const profile of [{name:'desktop',viewport:{width:1440,height:900}},{name:'mobile',viewport:{width:390,height:844}}]){
  const context=await browser.newContext({viewport:profile.viewport,storageState:state,userAgent:'contract-analysis-qa/1',isMobile:profile.name==='mobile',acceptDownloads:true});
  const page=await context.newPage(),errors=[],requests=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text())});page.on('response',r=>{if(r.url().includes('/api/v2/contract-analyses'))requests.push({url:r.url(),status:r.status()})});
  await page.goto(base+'/contract-analyses',{waitUntil:'domcontentloaded'});await page.getByRole('heading',{name:'合约分析',exact:true}).waitFor();await page.locator('.ca-table tbody tr').first().waitFor();
  check(profile.name+'-real-list',await page.locator('.ca-table tbody').innerText().then(t=>t.toLowerCase().includes('0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2')));
  await page.goto(base+'/contract-analyses/new?chain=eth&address=bad',{waitUntil:'domcontentloaded'});await page.getByRole('heading',{name:'新建合约分析'}).waitFor();
  check(profile.name+'-chain-alias',await page.locator('.ca-form select').inputValue()==='ethereum');
  await page.getByRole('button',{name:'开始分析',exact:true}).click();await dialogCheck(page,profile.name+'-validation-dialog');
  check(profile.name+'-focus-restored',await page.getByRole('button',{name:'开始分析',exact:true}).evaluate(button=>button===document.activeElement));
  await page.goto(base+'/contract-analyses/'+id+'?tab=methods',{waitUntil:'domcontentloaded'});await page.locator('.ca-method-action').first().waitFor();
  const withdraw=page.getByRole('button',{name:'withdraw(uint256)',exact:true});await withdraw.click();await page.locator('aside .ca-body-text').first().waitFor();
  check(profile.name+'-method-deeplink',new URL(page.url()).searchParams.has('method_id'));
  await page.getByRole('tab',{name:'执行步骤',exact:true}).click();await page.locator('.ca-table tbody tr').first().waitFor();
  check(profile.name+'-withdraw-step-facts',(await page.locator('.ca-table tbody').innerText()).includes('balanceOf[msg.sender]'));
  await page.getByRole('button',{name:/^查看证据 /}).first().click();await page.getByTestId('contract-source').waitFor({timeout:20000});
  check(profile.name+'-source-evidence',await page.getByTestId('contract-source').innerText().then(t=>t.includes('balanceOf')));
  const link=page.url();await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('contract-source').waitFor();check(profile.name+'-evidence-deeplink-reload',page.url()===link);
  await page.screenshot({path:new URL(profile.name+'-method-evidence.png',out).pathname,fullPage:true});
  await page.getByRole('button',{name:'数据范围',exact:true}).click();await dialogCheck(page,profile.name+'-scope-dialog');
  await page.getByRole('tab',{name:'运行流程',exact:true}).click();await page.getByRole('img',{name:'本页有证据的运行关系'}).waitFor();
  check(profile.name+'-flow-relations',await page.locator('.ca-diagram svg g').count()>0);
  await page.screenshot({path:new URL(profile.name+'-flows.png',out).pathname,fullPage:true});
  await page.getByRole('tab',{name:'证据与导出',exact:true}).click();await page.getByRole('button',{name:/^原始证据 /}).click();await page.locator('.ca-table tbody tr').first().waitFor();
  await page.getByRole('button',{name:'下一页',exact:true}).click();await page.waitForURL(url=>url.searchParams.has('cursor'));await page.locator('.ca-table tbody tr').first().waitFor();
  check(profile.name+'-evidence-next-page',new URL(page.url()).searchParams.has('cursor'));
  await page.getByRole('button',{name:/^分析文件 /}).click();await page.locator('.ca-table tbody tr').filter({hasText:'contract-evidence.zip'}).waitFor();
  const download=page.waitForEvent('download');await page.locator('.ca-table tbody tr').filter({hasText:'contract-evidence.zip'}).getByRole('button',{name:'下载',exact:true}).click();const file=await download;check(profile.name+'-real-zip-download',file.suggestedFilename()==='contract-evidence.zip');
  await file.saveAs(new URL(profile.name+'-download.zip',out).pathname);await page.locator('[role=dialog], [role=alertdialog]').first().waitFor();await page.keyboard.press('Escape');
  const overflow=await page.evaluate(()=>({scroll:document.documentElement.scrollWidth,client:document.documentElement.clientWidth}));check(profile.name+'-no-page-overflow',overflow.scroll<=overflow.client+1,overflow);
  check(profile.name+'-real-api-success',requests.length>5&&requests.every(r=>r.status===200),requests);
  check(profile.name+'-no-console-errors',errors.length===0,errors);
  await page.screenshot({path:new URL(profile.name+'-artifacts.png',out).pathname,fullPage:true});await context.close();
 }
 const viewer=await browser.newContext({viewport:{width:1440,height:900},storageState:'/tmp/contract-analysis-private-20261014/viewer-browser.json',userAgent:'contract-analysis-qa/1'});const page=await viewer.newPage();await page.goto(base+'/contract-analyses');await page.getByRole('heading',{name:'合约分析',exact:true}).waitFor();check('viewer-no-submit-action',await page.getByRole('link',{name:'新建分析',exact:true}).count()===0);await page.goto(base+'/contract-analyses/'+id+'?tab=evidence');await page.locator('.ca-table tbody tr').first().waitFor();check('viewer-no-export-action',await page.getByRole('button',{name:'下载',exact:true}).count()===0);await viewer.close();
 console.log('BROWSER_QA_PASS',rows.length);
}finally{await browser.close()}
