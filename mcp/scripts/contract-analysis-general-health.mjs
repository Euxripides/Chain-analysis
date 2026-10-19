import{readFileSync,writeFileSync}from'node:fs';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';
import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const client=new Client({name:'post-storage-production-read-check',version:'1.0.0'}),responses={};
let last=0;
async function call(name,args={}){
 const delay=Math.max(0,last+1250-Date.now());if(delay)await new Promise(r=>setTimeout(r,delay));last=Date.now();
 const r=await client.callTool({name,arguments:args}),e=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);
 if(r.isError||e.success===false)throw Error(JSON.stringify(e.error||e));return e.data??e;
}
try{
 await client.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:18085/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
 responses.capabilities=await call('get_contract_analysis_capabilities');
 if(responses.capabilities.semantic_rules_version!=='funds-semantics-8')throw Error('Production rule changed');
 const analysis_id='5611309e-14b5-44db-a0c5-5a8426be5350';responses.job=await call('get_contract_analysis',{analysis_id});
 if(responses.job.status!=='completed')throw Error('Old production job unavailable');
 responses.methods=await call('list_contract_methods',{analysis_id,page_size:2});
 if(responses.methods.items.length!==2||!responses.methods.has_more)throw Error('Old production pagination unavailable');
 const method_id=responses.methods.items[0].method_id;responses.method=await call('get_contract_method_analysis',{analysis_id,method_id});
 responses.flow=await call('get_contract_execution_flow',{analysis_id,method_id,page_size:2});
 if(responses.method.items.length!==1||responses.method.items[0].method_id!==method_id)throw Error('Old method changed');
 const result={checked_at_utc:new Date().toISOString(),status:'PASS',scope:'read-only old production capabilities, saved task, paginated methods, method detail and flow; no new analysis or provider fetch',responses};
 writeFileSync(new URL('../../acceptance/contract-analysis-general/post-storage-production-read-check.json',import.meta.url),JSON.stringify(result,null,2));
 console.log('PRODUCTION_SAVED_CONTRACT_READ_CHECK PASS');
}finally{await client.close();}
