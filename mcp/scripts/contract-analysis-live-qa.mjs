import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {StreamableHTTPClientTransport} from '@modelcontextprotocol/sdk/client/streamableHttp.js';
const root=new URL('../../',import.meta.url),out=new URL((process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis')+'/',root);
const env=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const url=process.env.CONTRACT_QA_MCP||'http://127.0.0.1:18082/mcp';
const client=new Client({name:'contract-analysis-real-qa',version:'1.0.0'}),rows=[];
mkdirSync(out,{recursive:true});
function check(name,condition,evidence){rows.push({scenario:name,status:condition?'PASS':'FAIL',evidence});writeFileSync(new URL('mcp-live-qa.json',out),JSON.stringify(rows,null,2));console.log(name,rows.at(-1).status);if(!condition)throw new Error(name)}
function payload(result){const data=result.structuredContent||JSON.parse(result.content.find(c=>c.type==='text').text);if(result.isError||data.success===false)throw new Error(JSON.stringify(data.error));return data.data??data}
try{
 await client.connect(new StreamableHTTPClientTransport(new URL(url),{requestInit:{headers:{Authorization:'Bearer '+env.MCP_API_KEY}}}));
 const names=(await client.listTools()).tools.map(t=>t.name);
 const required=['get_contract_analysis_capabilities','analyze_contract','get_contract_analysis','list_contract_methods','get_contract_method_analysis','get_contract_execution_flow','explain_contract_transaction','compare_contract_versions','get_contract_analysis_artifact'];
 check('discovery_120_unique_tools',names.length===120&&new Set(names).size===120&&required.every(n=>names.includes(n)),{count:names.length,added:required});
 const templates=(await client.listResourceTemplates()).resourceTemplates;
 check('artifact_resource_discovered',templates.some(t=>t.uriTemplate==='contract-analysis://{analysis_id}/{artifact_id}/{offset}/{chunk_size}'));
 const before='c1ccd54b-71c7-4efa-97a0-b85077e1bdea';
 const submitted=payload(await client.callTool({name:'analyze_contract',arguments:{chain:'ethereum',address:'0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2',block:19000000,idempotency_key:'mcp-contract-live-'+readFileSync(new URL('engine-image.txt',out),'utf8').trim().slice(7,19),budget:{max_contracts:2,max_rpc_requests:80,max_state_reads:24}}}));
 const id=submitted.analysis_id;writeFileSync(new URL('mcp-analysis-id.txt',out),id);check('real_mcp_submission',Boolean(id),id);
 let status;for(let n=0;n<300;n++){status=payload(await client.callTool({name:'get_contract_analysis',arguments:{analysis_id:id}}));if(!['queued','running'].includes(status.status))break;await new Promise(r=>setTimeout(r,2000))}
 check('mcp_durable_completed',status.status==='completed',{analysis_id:id,status:status.status,error:status.error_code});
 const methods=payload(await client.callTool({name:'list_contract_methods',arguments:{analysis_id:id,page_size:200}}));
 check('real_methods_match_count',methods.items.length===status.metadata.sections.methods,methods.record_count);
 const method=methods.items.find(m=>m.signature==='withdraw(uint256)'&&m.verified_signature);
 const detail=payload(await client.callTool({name:'get_contract_method_analysis',arguments:{analysis_id:id,method_id:method.method_id}}));check('real_method_detail',detail.items[0].method_id===method.method_id);
 const flows=payload(await client.callTool({name:'get_contract_execution_flow',arguments:{analysis_id:id,method_id:method.method_id,page_size:200}}));check('real_method_flow',flows.items.length>0&&flows.items.every(f=>f.method_id===method.method_id||f.from===method.method_id||f.to===method.method_id));
 const transactions=payload(await client.callTool({name:'explain_contract_transaction',arguments:{analysis_id:id,tx_hash:'0x'+'1'.repeat(64)}}));check('unselected_transaction_stays_empty',transactions.record_count===0);
 const compared=payload(await client.callTool({name:'compare_contract_versions',arguments:{analysis_id:id,compare_analysis_id:before,page_size:200}}));check('real_version_comparison',compared.before_snapshot.block_number===19000000&&compared.after_snapshot.block_number===19000000,compared.record_count);
 const artifacts=payload(await client.callTool({name:'get_contract_analysis',arguments:{analysis_id:id,section:'artifacts',page_size:200}}));const source=artifacts.items.find(a=>a.kind==='verified_source');
 const result=await client.callTool({name:'get_contract_analysis_artifact',arguments:{analysis_id:id,artifact_id:source.artifact_id,chunk_size:4096}});
 const resource=result.content.find(c=>c.type==='resource').resource,metadata=payload(result),bytes=Buffer.from(resource.blob,'base64');
 check('mcp_embedded_artifact_sha256',createHash('sha256').update(bytes).digest('hex')===metadata.chunk_sha256&&metadata.sha256===source.sha256,{bytes:bytes.length,sha256:metadata.sha256});
 const reread=await client.readResource({uri:resource.uri});const copied=Buffer.from(reread.contents[0].blob,'base64');check('real_resource_read',bytes.equals(copied));
 const batch=await client.callTool({name:'batch_call_tools',arguments:{requests:[{tool:'get_contract_analysis',arguments:{analysis_id:id}},{tool:'get_contract_method_analysis',arguments:{analysis_id:id,method_id:method.method_id}}]}});check('generic_batch_contract_tools',!batch.isError);
 console.log('MCP_LIVE_QA_PASS',rows.length);
}finally{await client.close()}
