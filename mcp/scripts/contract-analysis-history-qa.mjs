import{readFileSync,writeFileSync}from'node:fs';import{createHash}from'node:crypto';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';
const out=new URL((process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-transaction-history/service-validation')+'/',new URL('../../',import.meta.url)),checks=[];
const env=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const sha=b=>createHash('sha256').update(b).digest('hex'),client=new Client({name:'transaction-history-evidence-qa',version:'1.0.0'});
function check(name,ok,evidence){checks.push({scenario:name,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('history-mcp-qa.json',out),JSON.stringify(checks,null,2));console.log(name,checks.at(-1).status);if(!ok)throw Error(name);}
function payload(r){if(r.isError)throw Error(r.content.find(c=>c.type==='text')?.text||'MCP_TOOL_ERROR');const p=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);if(p.success===false)throw Error(JSON.stringify(p.error));return p.data??p;}
try{
 await client.connect(new StreamableHTTPClientTransport(new URL(process.env.CONTRACT_QA_MCP),{requestInit:{headers:{Authorization:'Bearer '+env.MCP_API_KEY}}}));
 const other=readFileSync(new URL('execution-analysis-id.txt',out),'utf8').trim(),denied=await client.callTool({name:'get_contract_analysis',arguments:{analysis_id:other}});
 check('mcp-cannot-read-other-workspace-transaction',denied.isError&&denied.content.some(c=>c.type==='text'&&c.text.startsWith('ANALYSIS_NOT_FOUND:')));
 const image=readFileSync(new URL('engine-image.txt',out),'utf8').trim(),submitted=payload(await client.callTool({name:'analyze_contract',arguments:{chain:'ethereum',address:'0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2',block:19000000,tx_hashes:['0xecdc668e16ada76f439b09c9b8c2faa7b76f80244933193c7691ac797e26eae7'],idempotency_key:'mcp-history-diff-'+image.slice(7,19),budget:{max_contracts:3,max_rpc_requests:120,max_state_reads:24}}}));
 const id=submitted.analysis_id;writeFileSync(new URL('history-mcp-analysis-id.txt',out),id);check('mcp-owns-actual-transaction-analysis',typeof id==='string'&&id!==other,{analysis_id:id});
 let job;for(let n=0;n<450;n++){job=payload(await client.callTool({name:'get_contract_analysis',arguments:{analysis_id:id}}));if(!['queued','running'].includes(job.status))break;await new Promise(r=>setTimeout(r,2000));}
 check('mcp-transaction-worker-completes',job.status==='completed'&&job.metadata.metrics.engine_digest===image,{analysis_id:id,status:job.status,error:job.error_code});
 const replay=payload(await client.callTool({name:'get_contract_analysis',arguments:{analysis_id:id,section:'replays',page_size:200}}));
 check('mcp-replay-compares-complete-transaction-changes',replay.record_count===1&&replay.items[0].complete&&replay.items[0].state_diff_complete&&replay.items[0].state_diff_scope==='complete_transaction_changes_including_additional_replay_effects');
 const items=[];let cursor;
 for(let n=0;n<1024;n++){
  const p=payload(await client.callTool({name:'get_contract_analysis',arguments:{analysis_id:id,section:'artifacts',page_size:200,...(cursor?{cursor}:{})}}));items.push(...p.items);if(!p.has_more)break;if(!p.next_cursor||p.next_cursor===cursor)throw Error('INVALID_ARTIFACT_CURSOR');cursor=p.next_cursor;
 }
 const artifacts=items.filter(a=>a.kind==='transaction_state_diff');check('mcp-discovers-new-transaction-diff',artifacts.length>0);
 for(const a of artifacts){
  if(a.byte_size>32<<20)throw Error('ARTIFACT_BYTE_BUDGET');const parts=[];let offset=0,complete=false,chunks=0;
  for(;chunks<513;chunks++){
   const r=await client.callTool({name:'get_contract_analysis_artifact',arguments:{analysis_id:id,artifact_id:a.artifact_id,offset,chunk_size:65536}}),p=payload(r),resource=r.content.find(c=>c.type==='resource')?.resource;
   if(!resource)throw Error('EMBEDDED_RESOURCE_REQUIRED');const b=Buffer.from(resource.blob,'base64');if(p.offset!==offset||sha(b)!==p.chunk_sha256||p.sha256!==a.sha256||b.length!==p.byte_length)throw Error('CHUNK_INTEGRITY_FAILED');
   if(offset===0){const reread=await client.readResource({uri:resource.uri});check('mcp-resource-reloads-transaction-diff',b.equals(Buffer.from(reread.contents[0].blob,'base64')));}
   parts.push(b);offset+=b.length;if(p.next_offset===null){complete=p.complete===true;break;}if(!b.length||p.next_offset!==offset)throw Error('INVALID_CHUNK_CURSOR');
  }
  const bytes=Buffer.concat(parts),saved=JSON.parse(bytes);check('mcp-full-transaction-diff-hash',complete&&bytes.length===a.byte_size&&sha(bytes)===a.sha256,{artifact_id:a.artifact_id,chunks:chunks+1,bytes:bytes.length,sha256:sha(bytes)});
  check('mcp-preserves-complete-state-comparison',saved.comparison.complete&&saved.comparison.matched&&saved.comparison.differences.length===0);
 }
}finally{await client.close();}
