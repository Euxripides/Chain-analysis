import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {StreamableHTTPClientTransport} from '@modelcontextprotocol/sdk/client/streamableHttp.js';
const root=new URL('../../',import.meta.url),out=new URL('acceptance/contract-analysis/release/',root);mkdirSync(out,{recursive:true});const rows=[];
const env=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
function check(name,ok,evidence){rows.push({scenario:name,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('production-qa.json',out),JSON.stringify(rows,null,2));console.log(name,rows.at(-1).status);if(!ok)throw new Error(name)}
function payload(result){const envelope=result.structuredContent||JSON.parse(result.content.find(c=>c.type==='text').text);if(result.isError||envelope.success===false)throw new Error(JSON.stringify(envelope.error));return envelope.data??envelope}
const old=new Client({name:'contract-legacy-inventory-qa',version:'1.0.0'}),live=new Client({name:'contract-production-qa',version:'1.0.0'});
try{
 for(const [client,url]of [[old,'http://127.0.0.1:8090/mcp'],[live,'https://aurore.online/mcp']])await client.connect(new StreamableHTTPClientTransport(new URL(url),{requestInit:{headers:{Authorization:'Bearer '+env.MCP_API_KEY}}}));
 const prior=(await old.listTools()).tools,current=(await live.listTools()).tools;writeFileSync(new URL('mcp-tools-live.json',out),JSON.stringify({result:{tools:current}},null,2));
 check('public-mcp-discovers-119-unique-tools',current.length===119&&new Set(current.map(t=>t.name)).size===119,{prior:prior.length,current:current.length});
 const allowed=new Set(['start_analysis','batch_call_tools']);const changes=prior.filter(a=>{const b=current.find(b=>a.name===b.name);return!b||a.description!==b.description||JSON.stringify(a.inputSchema)!==JSON.stringify(b.inputSchema)}).map(t=>t.name);
 check('legacy-tool-definitions-preserved',changes.every(name=>allowed.has(name)),changes);
 const required=['analyze_contract','get_contract_analysis','list_contract_methods','get_contract_method_analysis','get_contract_execution_flow','explain_contract_transaction','compare_contract_versions','get_contract_analysis_artifact'];check('all-eight-new-tools-present',required.every(name=>current.some(t=>t.name===name)));
 const address='0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2',image=readFileSync(new URL('../engine-image.txt',out),'utf8').trim();
 const submitted=payload(await live.callTool({name:'analyze_contract',arguments:{chain:'ethereum',address,block:19000000,idempotency_key:'contract-release-production-'+image.slice(7,19),budget:{max_contracts:2,max_rpc_requests:100,max_state_reads:24}}}));
 check('public-mcp-submits-real-production-job',Boolean(submitted.analysis_id));const aid=submitted.analysis_id;writeFileSync(new URL('production-analysis-id.txt',out),aid);
 let status;for(let n=0;n<120;n++){status=payload(await live.callTool({name:'get_contract_analysis',arguments:{analysis_id:aid}}));if(!['running','queued'].includes(status.status))break;await new Promise(resolve=>setTimeout(resolve,2000))}
 writeFileSync(new URL('production-status.json',out),JSON.stringify(status,null,2));check('production-worker-completes-pinned-job',status.status==='completed'&&status.metadata.metrics.engine_digest===image,{analysis_id:aid,status:status.status,error:status.error_code});
 const methods=payload(await live.callTool({name:'list_contract_methods',arguments:{analysis_id:aid,page_size:200}}));check('production-methods-and-code-identity',methods.items.length===12&&methods.items.every(m=>m.code_hash==='0xd0a06b12ac47863b5c7be4185c2deaad1c61557033f56c7d4ea74429cbb25e23'));
 const withdraw=methods.items.find(m=>m.signature==='withdraw(uint256)');const detail=payload(await live.callTool({name:'get_contract_method_analysis',arguments:{analysis_id:aid,method_id:withdraw.method_id}}));check('production-withdrawal-has-guards-steps-and-funds',detail.items[0].guard_ids.length>0&&detail.items[0].step_ids.length>=4&&detail.items[0].fund_ids.length>0);
 const artifacts=payload(await live.callTool({name:'get_contract_analysis',arguments:{analysis_id:aid,section:'artifacts',page_size:200}}));const source=artifacts.items.find(a=>a.kind==='verified_source');
 const file=await live.callTool({name:'get_contract_analysis_artifact',arguments:{analysis_id:aid,artifact_id:source.artifact_id,chunk_size:4096}}),resource=file.content.find(c=>c.type==='resource').resource,meta=payload(file),bytes=Buffer.from(resource.blob,'base64');check('production-source-bytes-and-sha256',createHash('sha256').update(bytes).digest('hex')===meta.chunk_sha256&&meta.sha256===source.sha256);
 const reread=await live.readResource({uri:resource.uri});check('production-resource-authorized-read',Buffer.from(reread.contents[0].blob,'base64').equals(bytes));
 check('production-cost-and-budgets-recorded',Number.isFinite(status.metadata.metrics.estimated_usd)&&status.metadata.metrics.durable_budget.reserved_rpc_http_requests<=100,status.metadata.metrics);
 const spec=await fetch('https://aurore.online/contract-analysis-openapi.json');const doc=await spec.json();check('public-contract-openapi',spec.ok&&Object.keys(doc.paths).length===8);
 const health=await fetch('https://aurore.online/mcp-health');const data=await health.json();check('public-mcp-health-matches-candidate',health.ok&&data.tools===119);
 const codeTool=current.find(t=>t.name==='read_rpc');if(codeTool){
  const args={chain:'ethereum',method:'eth_getCode',params:[address,'0x121eac0']};
  const before=payload(await old.callTool({name:'read_rpc',arguments:args})),after=payload(await live.callTool({name:'read_rpc',arguments:args}));
  check('legacy-rpc-tool-routes-to-original-api',JSON.stringify(before)===JSON.stringify(after));
 }
 console.log('PRODUCTION_QA_PASS',rows.length,aid);
}finally{await old.close();await live.close()}
