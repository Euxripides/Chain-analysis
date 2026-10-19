import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{spawnSync}from'node:child_process';import{createHash}from'node:crypto';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';
const root=new URL('../../',import.meta.url),base=new URL('acceptance/contract-analysis-permit-nonces/',root),basis=JSON.parse(readFileSync(new URL('interface-fix-basis.json',base),'utf8'));
const prior=new URL('file://'+basis.prior_directory+'/service-validation/'),job=JSON.parse(readFileSync(new URL('pair-mcp-job.json',prior),'utf8')),out=new URL('interface-reproducer/',base);mkdirSync(out,{recursive:true});
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const client=new Client({name:'saved-contract-interface-reproduction',version:'1.0.0'}),checks=[];
const payload=r=>{const e=r.structuredContent||JSON.parse(r.content.find(x=>x.type==='text').text);if(r.isError||e.success===false)throw Error('SAVED_ARTIFACT_READ_FAILED');return e.data??e;};
async function download(item){const r=await client.callTool({name:'get_contract_analysis_artifact',arguments:{analysis_id:job.analysis_id,artifact_id:item.artifact_id,chunk_size:1<<20}}),meta=payload(r),b=Buffer.from(r.content.find(x=>x.type==='resource').resource.blob,'base64');if(!meta.complete||b.length!==item.byte_size||createHash('sha256').update(b).digest('hex')!==item.sha256)throw Error('FROZEN_SOURCE_ARTIFACT_MISMATCH');return b;}
try{
 await client.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:18092/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
 const files=payload(await client.callTool({name:'get_contract_analysis',arguments:{analysis_id:job.analysis_id,section:'artifacts',page_size:200}})).items;
 const source=JSON.parse((await download(files.find(x=>x.kind==='source_provider_document'))).toString('utf8')),bytecode=(await download(files.find(x=>x.kind==='bytecode'))).toString('utf8');
 const image=readFileSync(new URL('frozen-engine/engine-image.txt',base),'utf8').trim();
 for(const[label,digest]of[['prior',basis.prior_image],['fixed',image]]){
  if(label==='prior'&&spawnSync('docker',['image','inspect',digest],{stdio:'ignore'}).status!==0){
   const saved=JSON.parse(readFileSync(new URL('pair-diagnosis-authorization_models.json',prior),'utf8'));
   if(saved.data.has_more||saved.data.record_count!==saved.data.items.length||saved.data.items.some(m=>m.kind==='permit_nonce_monotonicity')||saved.data.metadata.snapshot.code_hash!==job.metadata.snapshot.code_hash||job.metadata.metrics.engine_digest!==digest)throw Error('PRIOR_MCP_BASELINE_MISMATCH');
   checks.push({label:'prior_saved_MCP_result',image:digest,status:'verified_saved_evidence',nonce_model_present:false,runtime_image_available:false});continue;
  }
  const input={snapshot:{...job.metadata.snapshot,engine_digest:digest},source,bytecode,engines:['whatsabi','solc-ast']};
  const p=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--pids-limit','128','--tmpfs','/tmp:rw,nosuid,size=192m',digest],{input:JSON.stringify(input),encoding:'utf8',timeout:180000,maxBuffer:64<<20});if(p.status!==0)throw Error(p.stderr);
  const result=JSON.parse(p.stdout),nonce=result.sections.authorization_models.find(m=>m.kind==='permit_nonce_monotonicity');
  if(!result.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed'))throw Error('SOURCE_IDENTITY_NOT_VERIFIED');
  if(label==='prior'?nonce!==undefined:!nonce||!Number.isInteger(nonce.nonce_state_ast_id))throw Error('INTERFACE_GETTER_DIFFERENTIAL_FAILED');
  writeFileSync(new URL(label+'-input.json',out),JSON.stringify(input));writeFileSync(new URL(label+'-result.json',out),JSON.stringify(result));checks.push({label,image:digest,status:'PASS',nonce_model_present:Boolean(nonce),nonce_status:nonce?.status,nonce_state_ast_id:nonce?.nonce_state_ast_id});
 }
 writeFileSync(new URL('report.json',out),JSON.stringify({checks,saved_analysis_id:job.analysis_id,code_hash:job.metadata.snapshot.code_hash,additional_provider_requests:0,source:'actual_MCP_saved_source_and_deployed_bytecode',whole_goal_complete:false},null,2));console.log('SAVED_REAL_CONTRACT_INTERFACE_DIFFERENTIAL_PASS',JSON.stringify(checks));
}finally{await client.close();}
