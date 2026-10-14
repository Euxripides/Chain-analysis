import {readFileSync,writeFileSync} from 'node:fs';
import {createHash,randomBytes} from 'node:crypto';
const root=new URL('../../',import.meta.url),out=new URL('acceptance/contract-analysis/release-oauth-qa.json',root),rows=[];
const env=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const authority='http://127.0.0.1:8090',feature=process.env.CONTRACT_QA_MCP||'http://127.0.0.1:18082/mcp',callback='https://chatgpt.com/connector_platform_oauth_redirect',resource='https://aurore.online/mcp';
function check(name,condition){rows.push({scenario:name,status:condition?'PASS':'FAIL'});writeFileSync(out,JSON.stringify(rows,null,2));console.log(name,rows.at(-1).status);if(!condition)throw new Error(name)}
const registration=await fetch(authority+'/register',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({redirect_uris:[callback],token_endpoint_auth_method:'none',grant_types:['authorization_code','refresh_token'],response_types:['code'],client_name:'Contract analysis release fixture'})});
check('original-oauth-registers-fixture-client',registration.status===201||registration.status===200);const client=await registration.json();
const verifier=randomBytes(48).toString('base64url'),challenge=createHash('sha256').update(verifier).digest('base64url');
const accessCode=(env.MCP_OAUTH_ACCESS_CODES||env.MCP_API_KEYS||env.MCP_API_KEY).split(',')[0].trim();
const params=new URLSearchParams({response_type:'code',client_id:client.client_id,redirect_uri:callback,code_challenge:challenge,code_challenge_method:'S256',resource,scope:'mcp:tools',access_code:accessCode});
const authorized=await fetch(authority+'/authorize',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:params,redirect:'manual'});
check('original-oauth-preserves-authorization-flow',authorized.status===302);const code=new URL(authorized.headers.get('location')).searchParams.get('code');
const exchange=await fetch(authority+'/token',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:new URLSearchParams({grant_type:'authorization_code',client_id:client.client_id,code,code_verifier:verifier,redirect_uri:callback,resource})});
check('original-oauth-exchanges-pkce-code',exchange.status===200);const tokens=await exchange.json();
async function initialize(token){const response=await fetch(feature,{method:'POST',headers:{authorization:'Bearer '+token,'content-type':'application/json',accept:'application/json, text/event-stream'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'initialize',params:{protocolVersion:'2025-03-26',capabilities:{},clientInfo:{name:'contract-release-fixture',version:'1.0.0'}}})});await response.body?.cancel();return response.status}
check('existing-authority-token-accepted-by-feature-mcp',await initialize(tokens.access_token)===200);
const revoke=await fetch(authority+'/revoke',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:new URLSearchParams({client_id:client.client_id,token:tokens.access_token,token_type_hint:'access_token'})});
check('original-authority-revokes-fixture-token',revoke.ok);
check('feature-mcp-respects-existing-revocation',await initialize(tokens.access_token)===401);
await new Promise(resolve=>setTimeout(resolve,1100));
const refreshed=await fetch(authority+'/token',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:new URLSearchParams({grant_type:'refresh_token',client_id:client.client_id,refresh_token:tokens.refresh_token,resource})});
check('existing-authority-refresh-remains-functional',refreshed.status===200);
const renewed=await refreshed.json();check('refreshed-token-accepted-by-feature-mcp',await initialize(renewed.access_token)===200);
await fetch(authority+'/revoke',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:new URLSearchParams({client_id:client.client_id,token:renewed.access_token,token_type_hint:'access_token'})});
await fetch(authority+'/revoke',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:new URLSearchParams({client_id:client.client_id,token:tokens.refresh_token,token_type_hint:'refresh_token'})});
console.log('RELEASE_OAUTH_QA_PASS',rows.length);
