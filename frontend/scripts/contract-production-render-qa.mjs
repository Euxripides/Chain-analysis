import {chromium} from '@playwright/test';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
const root=new URL('../../',import.meta.url),out=new URL('acceptance/contract-analysis/release/',root),rows=[];
function check(name,ok,evidence){rows.push({scenario:name,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('production-render-qa.json',out),JSON.stringify(rows,null,2));console.log(name,rows.at(-1).status);if(!ok)throw new Error(name)}
const expected=readFileSync(new URL('frontend/apps/user-console/dist/index.html',root)),base='https://aurore.online';
const response=await fetch(base+'/app-v2/contract-analyses'),html=Buffer.from(await response.arrayBuffer());check('public-app-serves-frozen-candidate-index',response.ok&&createHash('sha256').update(html).digest('hex')===createHash('sha256').update(expected).digest('hex'));
const paths=[...html.toString().matchAll(/(?:src|href)="([^"]*\/assets\/[^" ]+)"/g)].map(m=>m[1]);
for(const path of paths){const response=await fetch(base+path),body=Buffer.from(await response.arrayBuffer()),local=readFileSync(new URL('frontend/apps/user-console/dist/'+path.split('/app-v2/')[1],root));check('public-entry-asset-'+path.split('/').at(-1),response.ok&&body.equals(local))}
const oldIndex=readFileSync('/home/euripides/projects/blockchain-platform/frontend/apps/user-console/dist/index.html','utf8');const oldEntry=[...oldIndex.matchAll(/src="([^"]*\/assets\/[^" ]+\.js)"/g)][0][1];const retained=await fetch(base+oldEntry);check('previous-browser-entry-assets-retained',retained.ok);
const browser=await chromium.launch({headless:true});
try{for(const viewport of [{width:1440,height:900},{width:390,height:844}]){const context=await browser.newContext({viewport});const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(base+'/app-v2/contract-analyses',{waitUntil:'domcontentloaded'});await page.locator('.auth-card').waitFor({timeout:20000});check('public-contract-route-requires-session-'+viewport.width,new URL(page.url()).pathname==='/app-v2/login');check('public-login-renders-without-errors-'+viewport.width,errors.length===0,errors);await page.screenshot({path:new URL('public-login-'+viewport.width+'.png',out).pathname,fullPage:true});await context.close()}}finally{await browser.close()}
console.log('PRODUCTION_RENDER_QA_PASS',rows.length);
