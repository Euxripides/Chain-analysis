import{readFileSync,writeFileSync,mkdirSync,existsSync}from'node:fs';import{createHash}from'node:crypto';import{gunzipSync}from'node:zlib';import assert from'node:assert/strict';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';
import{execFileSync}from'node:child_process';
const root=new URL('../../',import.meta.url),base=new URL('acceptance/contract-analysis-general/',root),image=readFileSync(new URL('engine-image.txt',base),'utf8').trim();
const out=new URL('mcp-'+image.slice(7,23)+'/',base);mkdirSync(out,{recursive:true});
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const client=new Client({name:'general-contract-analysis-acceptance',version:'1.0.0'}),checksFile=new URL('checks.json',out),checks=existsSync(checksFile)?JSON.parse(readFileSync(checksFile,'utf8')):[];
const sha=b=>createHash('sha256').update(b).digest('hex'),save=(name,value)=>writeFileSync(new URL(name,out),JSON.stringify(value,null,2));
function check(scenario,condition,evidence){checks.push({scenario,status:condition?'PASS':'FAIL',evidence});save('checks.json',checks);console.log(scenario,condition?'PASS':'FAIL');if(!condition)throw Error(scenario);}
function payload(r){const e=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);if(r.isError||e.success===false)throw Error(JSON.stringify(e.error||e));return e.data??e;}
let lastMcpCall=0;
async function rawCall(name,args={}){const delay=Math.max(0,lastMcpCall+1250-Date.now());if(delay)await new Promise(r=>setTimeout(r,delay));lastMcpCall=Date.now();return client.callTool({name,arguments:args});}
const call=async(name,args={})=>payload(await rawCall(name,args));
async function all(name,args,page_size=37){let items=[],cursor,count;const seen=new Set();do{const p=await call(name,{...args,page_size,...(cursor?{cursor}:{})});count??=p.record_count;assert.equal(count,p.record_count);items.push(...p.items);cursor=p.has_more?p.next_cursor:null;if(cursor){assert(!seen.has(cursor));seen.add(cursor)}}while(cursor);assert.equal(items.length,count);return items;}
async function artifact(id,item,chunk_size=65536){let offset=0;const parts=[];for(;;){const r=await rawCall('get_contract_analysis_artifact',{analysis_id:id,artifact_id:item.artifact_id,offset,chunk_size}),m=payload(r),b=Buffer.from(r.content.find(c=>c.type==='resource').resource.blob,'base64');assert.equal(m.offset,offset);assert.equal(b.length,m.byte_length);assert.equal(sha(b),m.chunk_sha256);parts.push(b);if(m.complete)break;assert.equal(m.next_offset,offset+b.length);offset=m.next_offset;}const bytes=Buffer.concat(parts);assert.equal(bytes.length,item.byte_size);assert.equal(sha(bytes),item.sha256);return bytes;}
async function wait(id){let job;for(let i=0;i<450;i++){job=await call('get_contract_analysis',{analysis_id:id});if(!['queued','running'].includes(job.status))return job;await new Promise(r=>setTimeout(r,1000))}throw Error('JOB_WAIT_EXHAUSTED:'+id);}
async function submit(key,request){const q={...request,idempotency_key:'general-'+image.slice(7,19)+'-'+key};const result=await call('analyze_contract',q);save(key+'-submission.json',result);save(key+'-request.json',q);return result;}
async function completed(key,request){const submission=await submit(key,request),job=await wait(submission.analysis_id);save(key+'-job.json',job);check(key+'-completed-with-pinned-candidate',job.status==='completed'&&job.metadata.rule_version==='funds-semantics-14'&&job.metadata.metrics.engine_digest===image,{id:job.analysis_id,status:job.status,error:job.error_code});return job;}
const budget={max_contracts:1,max_rpc_requests:10,max_state_reads:1,max_credits:5000,timeout_seconds:180};
const doc=JSON.parse(readFileSync(new URL('corpus/materials/weth/source-provider.json',base),'utf8'));
const settings={...doc.stdJsonInput.settings};delete settings.outputSelection;
const source={kind:'source',source:{language:'Solidity',files:Object.fromEntries(Object.entries(doc.stdJsonInput.sources).map(([k,v])=>[k,v.content])),compiler_version:doc.compilation.compilerVersion,target:doc.compilation.fullyQualifiedName,settings}};
const runtime=readFileSync(new URL('corpus/materials/weth/runtime.hex',base),'utf8').trim();
try{
 await client.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:18092/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
 const tools=(await client.listTools()).tools,definition=tools.find(t=>t.name==='analyze_contract');
 const caps=await call('get_contract_analysis_capabilities');check('three-input-capabilities-from-frozen-runtime',caps.engine_digest===image&&caps.semantic_rules_version==='funds-semantics-14'&&caps.submitted_inputs.code_only_provider_requests===0&&definition.inputSchema.properties.input);
 check('whole-proof-scopes-remain-open',caps.state_model_verification.whole_authorization_proved===false&&caps.state_model_verification.whole_protocol_economics_proved===false);
 if(process.argv[2]==='offline'){
  const direct=await completed('source',{input:source,engines:['whatsabi','sevm','solc-ast'],budget});
  const snapshot=direct.metadata.snapshot;check('source-only-has-code-and-symbolic-state-without-observed-defaults',snapshot.identity_kind==='code_only'&&snapshot.state_granularity==='symbolic_state'&&/^[0-9a-f]{64}$/.test(snapshot.input_sha256)&&!['chain','chain_id','block_number','block_hash','block_time','code_address','storage_address','entry_address'].some(k=>Object.hasOwn(snapshot,k)),snapshot);
  check('source-only-billed-and-reserved-zero',direct.metadata.metrics.rpc_http_requests===0&&Number(direct.metadata.metrics.credits)===0&&direct.metadata.metrics.durable_budget.reserved_rpc_http_requests===0&&direct.metadata.metrics.durable_budget.state_read_attempts===0,direct.metadata.metrics);
  const methods=await all('list_contract_methods',{analysis_id:direct.analysis_id},2),method=methods.find(m=>m.signature==='withdraw(uint256)'&&m.verified_signature);
  check('real-source-withdraw-entry-identified',Boolean(method));
  const detail=await call('get_contract_method_analysis',{analysis_id:direct.analysis_id,method_id:method.method_id});check('source-method-and-identity-readable',detail.items.length===1&&detail.items[0].method_id===method.method_id&&detail.items[0].analysis_dependencies.code_identity.input_sha256===snapshot.input_sha256);
  const flows=await all('get_contract_execution_flow',{analysis_id:direct.analysis_id,method_id:method.method_id},3);check('source-flow-contains-payment-with-evidence',flows.some(f=>f.edge_type==='transfers'&&f.asset==='native'&&f.evidence_ids.length));
  const funds=await all('get_contract_analysis',{analysis_id:direct.analysis_id,section:'funds',method_id:method.method_id});
  const guards=await all('get_contract_analysis',{analysis_id:direct.analysis_id,section:'permissions',method_id:method.method_id});
  check('withdraw-body-guard-recipient-and-exact-formula',guards.some(g=>g.condition.includes('balanceOf[msg.sender] >= wad'))&&funds.some(f=>f.asset==='native'&&f.to_expression==='msg.sender'&&f.amount_expression==='wad'&&f.analysis_dependencies.support==='static_derivation'));
  const files=await all('get_contract_analysis',{analysis_id:direct.analysis_id,section:'artifacts'}),machine=files.find(a=>a.kind==='machine_result');
  const full=JSON.parse((await artifact(direct.analysis_id,machine)).toString('utf8'));assert.deepEqual(full.sections.methods,methods);check('MCP-pages-match-saved-machine-artifact',true,{sha256:machine.sha256});
  const raw=direct.snapshot.input_artifact,original=JSON.parse((await artifact(direct.analysis_id,raw,16384)).toString('utf8'));assert.deepEqual(original.source.files,source.source.files);check('original-source-chunks-and-whole-digest',raw.sha256===snapshot.input_sha256,{sha256:raw.sha256,bytes:raw.byte_size});
  check('no-private-object-path-in-submission-or-status',!JSON.stringify(direct.snapshot).includes('object_key'));
  const reuse=await submit('source',{input:source,engines:['whatsabi','sevm','solc-ast'],budget});check('source-idempotency-retains-original-input',reuse.analysis_id===direct.analysis_id&&reuse.reused&&!JSON.stringify(reuse).includes('object_key'));
  const hidden=await completed('runtime',{input:{kind:'bytecode',bytecode:runtime,code_phase:'runtime'},engines:['whatsabi','sevm'],budget});
  const hiddenMethods=await all('list_contract_methods',{analysis_id:hidden.analysis_id});check('runtime-only-methods-with-PC-evidence',hiddenMethods.some(m=>m.selector==='0x2e1a7d4d')&&hiddenMethods.every(m=>!m.verified_signature));
  const comparison=await call('compare_contract_versions',{analysis_id:direct.analysis_id,compare_analysis_id:hidden.analysis_id,page_size:37});check('explicit-code-only-comparison-readable',Array.isArray(comparison.items));
  const invalid={kind:'source',source:{language:'Solidity',files:{'Broken.sol':'pragma solidity ^0.8.24; contract Broken { syntax error }'},compiler_version:'0.8.30+commit.73712a01',target:'Broken.sol:Broken'}};
  const failed=await completed('compile-error',{input:invalid,engines:['whatsabi','sevm','solc-ast'],budget});
  const errors=await all('get_contract_analysis',{analysis_id:failed.analysis_id,section:'artifacts'}),diagnostic=errors.find(a=>a.kind==='compilation_diagnostics');
  const diagnosticValue=JSON.parse((await artifact(failed.analysis_id,diagnostic)).toString('utf8'));check('compiler-failure-is-a-saved-partial-result',failed.metadata.completeness==='partial'&&failed.metadata.sections.methods===0&&diagnosticValue.status==='failed'&&diagnosticValue.diagnostics.some(d=>d.severity==='error'));
  const fallback=await completed('compile-fallback',{input:{...invalid,bytecode:runtime,code_phase:'runtime'},engines:['whatsabi','sevm','solc-ast'],budget});
  const fallbackMethods=await all('list_contract_methods',{analysis_id:fallback.analysis_id});check('compile-error-preserves-runtime-analysis',fallbackMethods.some(m=>m.selector==='0x2e1a7d4d')&&fallback.metadata.sections.artifacts>0);
  const creation=await completed('creation',{input:{kind:'bytecode',bytecode:'0x600160005360016000f3',code_phase:'creation'},engines:['whatsabi','sevm'],budget});
  check('creation-is-not-reported-as-runtime-methods',creation.metadata.sections.methods===0&&creation.metadata.snapshot.code_phase==='creation'&&creation.metadata.sections.frontier>0);
  const chefDoc=JSON.parse(readFileSync(new URL('corpus/materials/sushi-masterchef/source-provider.json',base),'utf8')),chefSettings={...chefDoc.stdJsonInput.settings};delete chefSettings.outputSelection;
  const chef={kind:'source',source:{language:'Solidity',files:Object.fromEntries(Object.entries(chefDoc.stdJsonInput.sources).map(([k,v])=>[k,v.content])),compiler_version:chefDoc.compilation.compilerVersion,target:chefDoc.compilation.fullyQualifiedName,settings:chefSettings}};
  const lifecycle=await submit('lifecycle',{input:chef,engines:['whatsabi','sevm','solc-ast'],budget:{...budget,timeout_seconds:300}});
  await call('cancel_analysis',{analysis_id:lifecycle.analysis_id});let cancelled=await call('get_contract_analysis',{analysis_id:lifecycle.analysis_id});
  check('queued-cancel-retains-input-evidence',cancelled.status==='cancelled'&&cancelled.snapshot.input_sha256===lifecycle.snapshot.input_sha256);
  await artifact(cancelled.analysis_id,cancelled.snapshot.input_artifact,16384);check('cancelled-input-artifact-remains-readable',true);
  await call('resume_analysis',{analysis_id:lifecycle.analysis_id});let running;
  for(let i=0;i<200;i++){running=await call('get_contract_analysis',{analysis_id:lifecycle.analysis_id});if(running.status==='running'&&running.phase.startsWith('decompiling'))break;if(running.status==='completed')throw Error('RUNNING_CANCEL_SCENARIO_NOT_REACHED');await new Promise(r=>setTimeout(r,100));}
  check('running-cancel-scenario-reached',running.status==='running'&&running.phase.startsWith('decompiling'));
  await call('cancel_analysis',{analysis_id:lifecycle.analysis_id});await call('resume_analysis',{analysis_id:lifecycle.analysis_id});
  const resumed=await wait(lifecycle.analysis_id);save('lifecycle-job.json',resumed);check('cancel-resume-fencing-preserves-input-and-zero-budget',resumed.status==='completed'&&resumed.metadata.snapshot.input_sha256===lifecycle.snapshot.input_sha256&&resumed.metadata.metrics.durable_budget.reserved_rpc_http_requests===0);
  const old=await call('get_contract_analysis',{analysis_id:'1900c588-10c3-42b1-a3e9-6a67de38aeac'}),oldMethods=await all('list_contract_methods',{analysis_id:old.analysis_id});
  check('old-saved-nonce-task-unchanged-and-readable',old.status==='completed'&&old.metadata.rule_version==='funds-semantics-13'&&oldMethods.some(m=>m.signature==='permit(address,address,uint256,uint256,uint8,bytes32,bytes32)'));
  const first=await call('get_contract_analysis',{analysis_id:direct.analysis_id,section:'evidence',page_size:1});
  for(const [key,args]of [['cursor-section',{analysis_id:direct.analysis_id,section:'steps',cursor:first.next_cursor}],['foreign-artifact',{analysis_id:hidden.analysis_id,artifact_id:raw.artifact_id}],['changed-idempotent-input',{input:{...source,environment:{chain_id:'1'}},engines:['whatsabi','sevm','solc-ast'],budget,idempotency_key:'general-'+image.slice(7,19)+'-source'}]]){
    let rejected=false;try{await call(key==='foreign-artifact'?'get_contract_analysis_artifact':key==='cursor-section'?'get_contract_analysis':'analyze_contract',args)}catch{rejected=true}check(key+'-rejected',rejected);
  }
  save('offline-task-ids.json',[direct,hidden,failed,fallback,creation,resumed].map(j=>j.analysis_id));
 }else if(process.argv[2]==='large'){
  const d=JSON.parse(readFileSync(new URL('corpus/materials/steth-implementation/source-provider.json',base),'utf8')),s={...d.stdJsonInput.settings};delete s.outputSelection;
  const submitted={kind:'source',source:{language:'Solidity',files:Object.fromEntries(Object.entries(d.stdJsonInput.sources).map(([k,v])=>[k,v.content])),compiler_version:d.compilation.compilerVersion,target:d.compilation.fullyQualifiedName,settings:s}};
  const job=await completed('large-source-archive',{input:submitted,engines:['whatsabi','sevm','solc-ast'],budget:{...budget,timeout_seconds:300}});
  const indexes=await all('get_contract_analysis',{analysis_id:job.analysis_id,section:'dependency_artifacts'}),files=await all('get_contract_analysis',{analysis_id:job.analysis_id,section:'artifacts'});check('large-source-dependencies-use-explicit-artifact-index',indexes.length===1);
  const descriptor=indexes[0],file=files.find(a=>a.artifact_id===descriptor.artifact_id),bytes=await artifact(job.analysis_id,file),uncompressed=gunzipSync(bytes);
  check('large-source-decompressed-length-and-whole-checksum',uncompressed.length===descriptor.uncompressed_bytes&&sha(uncompressed)===descriptor.uncompressed_sha256,{compressed_bytes:bytes.length,uncompressed_bytes:uncompressed.length});
  const full=JSON.parse(uncompressed.toString('utf8')),methods=await all('list_contract_methods',{analysis_id:job.analysis_id}),ids=new Map(full.conclusions.map(c=>[c.conclusion_id,c]));
  check('large-source-retains-all-dependency-records',full.conclusions.length===job.metadata.sections.conclusions&&full.analysis_gaps.length===job.metadata.sections.analysis_gaps&&methods.every(m=>m.analysis_dependencies?.dependency_artifact_id===file.artifact_id&&full.conclusions[m.analysis_dependencies.dependency_record]?.conclusion_id===m.analysis_dependencies.conclusion_id));
  const artifacts=new Set(files.map(f=>f.artifact_id)),locations=full.analysis_gaps.flatMap(g=>[g.location,g.detail?.location]).filter(Boolean);
  check('large-source-embedded-artifact-references-are-scoped',locations.every(l=>!l.artifact_id||artifacts.has(l.artifact_id)));
  check('large-source-remains-symbolic-with-zero-provider-cost',job.metadata.metrics.rpc_http_requests===0&&job.metadata.metrics.durable_budget.reserved_rpc_http_requests===0&&full.snapshot.identity_kind==='code_only'&&full.conclusions.every(c=>c.state_context.observed===false));
  save('large-dependency-archive-check.json',{analysis_id:job.analysis_id,descriptor,artifact:file,conclusions:ids.size,gaps:full.analysis_gaps.length,checksums_verified:true});
 }else if(process.argv[2]==='compressed'){
  const d=JSON.parse(readFileSync(new URL('corpus/materials/compound-ceth/source-provider.json',base),'utf8')),s={...d.stdJsonInput.settings};delete s.outputSelection;
  const submitted={kind:'source',source:{language:'Solidity',files:Object.fromEntries(Object.entries(d.stdJsonInput.sources).map(([k,v])=>[k,v.content])),compiler_version:d.compilation.compilerVersion,target:d.compilation.fullyQualifiedName,settings:s}};
  const job=await completed('compressed-source',{input:submitted,engines:['whatsabi','sevm','solc-ast'],budget:{...budget,timeout_seconds:300}});
  const files=await all('get_contract_analysis',{analysis_id:job.analysis_id,section:'artifacts'},200),machine=files.find(a=>a.kind==='machine_result');
  const full=JSON.parse((await artifact(job.analysis_id,machine,4<<20)).toString('utf8'));
  check('compressed-engine-result-has-complete-section-artifact-index',full.representation==='complete_section_artifacts_v1');
  const checked={};
  for(const[section,parts]of Object.entries(full.section_artifacts)){
   let count=0;const reconstructed=[];
   for(const part of parts){
    const bytes=await artifact(job.analysis_id,part,4<<20),rows=JSON.parse(bytes.toString('utf8'));
    assert.equal(rows.length,part.record_count);assert.equal(part.from_ordinal,count);count+=rows.length;
    if(['methods','funds','permissions','flows','protocol_formulas'].includes(section))reconstructed.push(...rows);
   }
   assert.equal(count,job.metadata.sections[section]);checked[section]=count;
   if(['methods','funds','permissions','flows','protocol_formulas'].includes(section)){
    const rows=await all(section==='methods'?'list_contract_methods':'get_contract_analysis',{analysis_id:job.analysis_id,...(section==='methods'?{}:{section})},200);
    assert.deepEqual(rows,reconstructed);
   }
  }
  check('compressed-engine-all-section-parts-and-saved-counts-verified',true,checked);
  check('compressed-source-has-no-observed-defaults-or-provider-cost',job.metadata.snapshot.identity_kind==='code_only'&&job.metadata.metrics.rpc_http_requests===0&&job.metadata.metrics.durable_budget.reserved_rpc_http_requests===0);
  save('compressed-complete-artifact-check.json',{analysis_id:job.analysis_id,machine,sections:checked,all_part_and_whole_checksums_verified:true});
 }else if(process.argv[2]==='audit'){
  if(process.env.GENERAL_QA_LIFECYCLE_APPROVED!=='isolated_only')throw Error('Audit lifecycle controls only the isolated QA worker');
  const unit='blockchain-contract-coverage-qa-worker.service',stamp=Date.now().toString();let stopped=false;
  try{
   const before=execFileSync('systemctl',['--user','show',unit,'-p','MemoryPeak','-p','MemoryMax','-p','NRestarts'],{encoding:'utf8'});save('audit-worker-memory-before.json',{systemd:before});
   execFileSync('systemctl',['--user','stop',unit]);stopped=true;
   const input={kind:'source',source:{language:'Solidity',files:{'Audit.sol':'pragma solidity ^0.8.24; contract AuditLifecycle {uint public value; function set(uint n) external {value=n;}} // '+stamp},compiler_version:'0.8.30+commit.73712a01',target:'Audit.sol:AuditLifecycle',settings:{evmVersion:'shanghai'}}};
   const submission=await submit('lifecycle-audit-'+stamp,{input,engines:['whatsabi','sevm','solc-ast'],budget});
   const queued=await call('get_contract_analysis',{analysis_id:submission.analysis_id});save('audit-queued-status.json',queued);check('audit-lifecycle-is-confirmed-queued-before-cancel',queued.status==='queued');
   await call('cancel_analysis',{analysis_id:submission.analysis_id});const cancelled=await call('get_contract_analysis',{analysis_id:submission.analysis_id});save('audit-cancelled-status.json',cancelled);
   check('audit-cancel-retains-immutable-input',cancelled.status==='cancelled'&&cancelled.snapshot.input_sha256===submission.snapshot.input_sha256);
   await call('resume_analysis',{analysis_id:submission.analysis_id});const resumed=await call('get_contract_analysis',{analysis_id:submission.analysis_id});save('audit-resumed-status.json',resumed);check('audit-resume-returns-to-queue',resumed.status==='queued');
   execFileSync('systemctl',['--user','start',unit]);stopped=false;
   const job=await wait(submission.analysis_id);save('lifecycle-audit-job.json',job);
   check('audited-lifecycle-completes-with-same-input-and-zero-provider',job.status==='completed'&&job.metadata.snapshot.input_sha256===submission.snapshot.input_sha256&&job.metadata.metrics.rpc_http_requests===0&&job.metadata.metrics.durable_budget.reserved_rpc_http_requests===0);
  }finally{if(stopped)execFileSync('systemctl',['--user','start',unit]);}
 }else if(process.argv[2]==='live'){
  if(process.env.GENERAL_LIVE_BUDGET_APPROVED!=='bounded_original_campaign')throw Error('Run cumulative budget preflight before live requests');
  const tx='0xecdc668e16ada76f439b09c9b8c2faa7b76f80244933193c7691ac797e26eae7';
  const live=await completed('address-selected',{chain:'ethereum',address:'0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2',block:19000000,tx_hashes:[tx],budget:{max_contracts:3,max_rpc_requests:20,max_state_reads:24,max_credits:10000},engines:['whatsabi','sevm','solc-ast']});
  const explanation=await call('explain_contract_transaction',{analysis_id:live.analysis_id,tx_hash:tx}),replays=await all('get_contract_analysis',{analysis_id:live.analysis_id,section:'replays'});
  check('actual-selected-transaction-explanation-and-complete-replay',explanation.items.some(t=>t.tx_hash===tx)&&replays.some(r=>r.tx_hash===tx&&r.complete&&r.differences.length===0));
  const methods=await all('list_contract_methods',{analysis_id:live.analysis_id}),method=methods.find(m=>m.signature==='withdraw(uint256)');
  const details=await call('get_contract_method_analysis',{analysis_id:live.analysis_id,method_id:method.method_id});const flows=await all('get_contract_execution_flow',{analysis_id:live.analysis_id,method_id:method.method_id});check('address-method-and-flow-readable',details.items.length===1&&flows.length>0);
  const files=await all('get_contract_analysis',{analysis_id:live.analysis_id,section:'artifacts'}),machine=files.find(a=>a.kind==='machine_result');const full=JSON.parse((await artifact(live.analysis_id,machine)).toString('utf8'));assert.deepEqual(full.sections.methods,methods);check('address-MCP-results-and-evidence-match',true,{sha256:machine.sha256});
  const bound=await completed('source-bound',{chain:'ethereum',address:'0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2',block:19000000,input:source,budget,engines:['whatsabi','sevm','solc-ast']});
  const verified=await all('get_contract_analysis',{analysis_id:bound.analysis_id,section:'source_verification'});check('submitted-source-matches-observed-deployment',bound.metadata.snapshot.identity_kind==='observed_deployment'&&bound.metadata.snapshot.code_hash===live.metadata.snapshot.code_hash&&verified.length>0);
  const compare=await call('compare_contract_versions',{analysis_id:live.analysis_id,compare_analysis_id:'71211aec-9b09-4180-a9ee-c5d9a593f900'});check('observed-version-comparison-remains-compatible',Array.isArray(compare.items));
 }else throw Error('Choose offline or live');
}finally{await client.close();}
