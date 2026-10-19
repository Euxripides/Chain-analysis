import{readFileSync,readdirSync,writeFileSync,mkdirSync}from'node:fs';import{createHash}from'node:crypto';import{execFileSync}from'node:child_process';import{gunzipSync}from'node:zlib';import assert from'node:assert/strict';
import{Client}from'@modelcontextprotocol/sdk/client/index.js';import{StreamableHTTPClientTransport}from'@modelcontextprotocol/sdk/client/streamableHttp.js';import{storageFlowFixture,storageFlowSource}from'../../analysis-engines/test/fixtures/storage-flow.mjs';import{scalarFlowFixture}from'../../analysis-engines/test/fixtures/scalar-flow.mjs';
const root=new URL('../../',import.meta.url),base=new URL('acceptance/contract-analysis-aggregates/',root),image=readFileSync(new URL('engine-image.txt',base),'utf8').trim(),out=new URL('mcp/'+image.slice(7,23)+'/'+new Date().toISOString().replace(/\D/g,'')+'Z/',base);mkdirSync(out,{recursive:true});
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]})),client=new Client({name:'contract-source-scalars-qa',version:'1.0.0'}),sha=b=>createHash('sha256').update(b).digest('hex'),save=(name,value)=>writeFileSync(new URL(name,out),JSON.stringify(value,null,2)),checks=[];
function check(name,ok,evidence){checks.push({name,status:ok?'PASS':'FAIL',evidence});save('checks.json',checks);console.log(name,ok?'PASS':'FAIL');assert.ok(ok,name);}
let last=0;const actions=new Set();
async function raw(name,args={}){const delay=Math.max(0,last+1250-Date.now());if(delay)await new Promise(r=>setTimeout(r,delay));last=Date.now();actions.add(name);return client.callTool({name,arguments:args});}
function payload(r){const e=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);if(r.isError||e.success===false)throw Error(JSON.stringify(e.error||e));return e.data??e;}
const call=async(name,args={})=>payload(await raw(name,args));
async function all(name,args,size=200){const rows=[],seen=new Set();let cursor,count;do{const p=await call(name,{...args,page_size:size,...(cursor?{cursor}:{})});count??=p.record_count;assert.equal(p.record_count,count);rows.push(...p.items);cursor=p.has_more?p.next_cursor:null;if(cursor){assert.ok(!seen.has(cursor));seen.add(cursor);}}while(cursor);assert.equal(rows.length,count);return rows;}
async function artifact(aid,item){const parts=[];let offset=0;for(;;){const r=await raw('get_contract_analysis_artifact',{analysis_id:aid,artifact_id:item.artifact_id,offset,chunk_size:4<<20}),m=payload(r),b=Buffer.from(r.content.find(c=>c.type==='resource').resource.blob,'base64');assert.equal(m.offset,offset);assert.equal(m.byte_length,b.length);assert.equal(m.chunk_sha256,sha(b));parts.push(b);if(m.complete)break;assert.equal(m.next_offset,offset+b.length);offset=m.next_offset;}const bytes=Buffer.concat(parts);assert.equal(bytes.length,item.byte_size);assert.equal(sha(bytes),item.sha256);return bytes;}
const jobs=[],budget={max_contracts:1,max_rpc_requests:10,max_state_reads:1,max_records:20000,max_credits:5000,timeout_seconds:300};
async function complete(key,input,engines){const request={input,engines,budget,idempotency_key:'scalar-v1-'+image.slice(7,23)+'-'+key},s=await call('analyze_contract',request);save(key+'-request.json',request);save(key+'-submission.json',s);let job;
 for(let i=0;i<400;i++){job=await call('get_contract_analysis',{analysis_id:s.analysis_id});if(!['queued','running'].includes(job.status))break;await new Promise(r=>setTimeout(r,500));}
 save(key+'-job.json',job);jobs.push(job.analysis_id);check(key+'-completed-frozen-candidate',job.status==='completed'&&job.metadata.rule_version==='funds-semantics-19'&&job.metadata.metrics.engine_digest===image,{analysis_id:s.analysis_id,error:job.error_message});
 const m=job.metadata.metrics,snapshot=job.metadata.snapshot;check(key+'-code-only-zero-provider-usage',snapshot.identity_kind==='code_only'&&m.rpc_http_requests===0&&Number(m.credits)===0&&m.durable_budget.reserved_rpc_http_requests===0&&m.durable_budget.state_read_attempts===0&&!['block_number','block_hash','code_address','storage_address'].some(k=>Object.hasOwn(snapshot,k)),m.durable_budget);
 check(key+'-complete-container-observer',m.engine_resources?.length&&m.engine_resources.every(r=>r.metrics?.container_execution?.scope==='after_engine_exit_and_complete_output_forwarding'&&r.metrics.container_execution.memory_peak_bytes>0));return job;}
async function verify(key,job,extraSections=[]){const files=await all('get_contract_analysis',{analysis_id:job.analysis_id,section:'artifacts'}),machine=files.find(f=>f.kind==='machine_result'),bytes=await artifact(job.analysis_id,machine);writeFileSync(new URL(key+'-machine.json',out),bytes);const full=JSON.parse(bytes);let sections=full.sections;
 if(full.representation==='complete_section_artifacts_v1'){sections={};for(const[name,parts]of Object.entries(full.section_artifacts)){const rows=[];for(const part of parts){assert.equal(part.from_ordinal,rows.length);const p=JSON.parse(await artifact(job.analysis_id,part));assert.equal(p.length,part.record_count);rows.push(...p);}assert.equal(rows.length,job.metadata.sections[name]);sections[name]=rows;}}
 else for(const[name,rows]of Object.entries(sections))if(name!=='artifacts')assert.equal(rows.length,job.metadata.sections[name]);
 const archived=sections.artifacts||full.artifacts,archivedIDs=new Set(archived.map(a=>a.artifact_id));assert.equal(archived.length+2,files.length);assert.deepEqual(files.filter(a=>!archivedIDs.has(a.artifact_id)).map(a=>a.kind).sort(),['evidence_bundle','machine_result']);
 for(const a of archived){const actual=files.find(f=>f.artifact_id===a.artifact_id);assert.ok(actual);assert.equal(actual.sha256,a.sha256);assert.equal(actual.byte_size,a.byte_size);}
 const bundle=files.find(f=>f.kind==='evidence_bundle'),zip=await artifact(job.analysis_id,bundle),expected=files.filter(f=>f.kind!=='evidence_bundle').map(f=>({artifact_id:f.artifact_id,sha256:f.sha256,byte_size:f.byte_size}));
 save(key+'-bundle-verification.json',JSON.parse(execFileSync('python3',[new URL('scripts/contract-analysis/p2_recovery_bundle.py',root).pathname],{input:Buffer.concat([Buffer.from(JSON.stringify(expected)+'\n'),zip]),maxBuffer:65536})));
 const methods=await all('list_contract_methods',{analysis_id:job.analysis_id},13),flows=await all('get_contract_execution_flow',{analysis_id:job.analysis_id},97);assert.deepEqual(methods,sections.methods);assert.deepEqual(flows,sections.flows);
 for(const name of extraSections)if(job.metadata.sections[name])assert.deepEqual(await all('get_contract_analysis',{analysis_id:job.analysis_id,section:name},89),sections[name],name);
 const ids=new Set((sections.memory_versions||[]).map(v=>v.memory_version_id)),sourceIDs=new Set((sections.source_memory_versions||[]).map(v=>v.source_memory_version_id)),evidence=new Set(sections.evidence.map(e=>e.evidence_id));
 for(const v of sections.memory_versions||[]){if(v.parent_version_id)assert.ok(ids.has(v.parent_version_id));assert.ok((v.evidence_ids||[]).every(e=>evidence.has(e)));}
 for(const v of sections.source_memory_versions||[])if(v.object_memory_version_id)assert.ok(ids.has(v.object_memory_version_id));
 for(const binding of sections.call_input_bindings||[]){assert.ok((binding.source_memory_version_ids||[]).every(v=>sourceIDs.has(v)));if(binding.memory_version_id)assert.ok(ids.has(binding.memory_version_id));}
 let conclusions=sections.conclusions;
 for(const d of sections.dependency_artifacts||[]){const raw=gunzipSync(await artifact(job.analysis_id,files.find(f=>f.artifact_id===d.artifact_id)));assert.equal(raw.length,d.uncompressed_bytes);assert.equal(sha(raw),d.uncompressed_sha256);const doc=JSON.parse(raw);assert.equal(doc.conclusions.length,sections.conclusions.length);assert.equal(doc.analysis_gaps.length,sections.analysis_gaps.length);conclusions=doc.conclusions;}
 for(const name of['calls','call_input_bindings','source_memory_versions','source_memory_phis','memory_reads','abi_decodings','call_argument_models','fund_call_models','storage_references','storage_reference_phis','storage_access_bindings','source_scalar_versions','source_scalar_phis'])if(sections[name]?.length)assert.equal(conclusions.filter(c=>c.section===name).length,sections[name].length,name+' conclusion identities');
 const referenceIDs=new Set((sections.storage_references||[]).map(r=>r.storage_reference_id)),bindingIDs=new Set((sections.storage_access_bindings||[]).map(r=>r.storage_access_binding_id)),storageIDs=new Set(sections.storage.map(r=>r.storage_id));
 for(const phi of sections.storage_reference_phis||[])for(const c of phi.cases)if(c.storage_reference_id)assert.ok(referenceIDs.has(c.storage_reference_id));
 for(const b of sections.storage_access_bindings||[]){assert.ok((b.storage_ids||[]).every(id=>storageIDs.has(id)));for(const c of b.cases||[])assert.ok((c.storage_reference_ids||[]).every(id=>referenceIDs.has(id)));}
 for(const r of sections.storage)if(r.storage_access_binding_id)assert.ok(bindingIDs.has(r.storage_access_binding_id));
 const scalarIDs=new Set((sections.source_scalar_versions||[]).map(v=>v.source_scalar_version_id)),expressionIDs=new Set(sections.expressions.map(e=>e.expression_id)),dataflows=new Map(sections.dataflow.map(d=>[d.dataflow_id,d]));
 for(const v of sections.source_scalar_versions||[]){
  assert.ok((v.parent_source_scalar_version_ids||[]).every(id=>scalarIDs.has(id)));if(v.previous_source_scalar_version_id)assert.ok(scalarIDs.has(v.previous_source_scalar_version_id));
  if(v.frame_id)assert.match(v.frame_id,/^[a-f0-9]{40}$/);if(v.value_expression_id)assert.ok(expressionIDs.has(v.value_expression_id));
  if(v.value_derivation_id)assert.equal(dataflows.get(v.value_derivation_id)?.source_scalar_version_id,v.source_scalar_version_id);
  assert.ok((v.evidence_ids||[]).every(id=>evidence.has(id)));
 }
 for(const p of sections.source_scalar_phis||[]){assert.ok(scalarIDs.has(p.source_scalar_version_id));assert.ok(p.cases.every(c=>!c.source_scalar_version_id||scalarIDs.has(c.source_scalar_version_id)));}
 check(key+'-all-sections-pages-artifacts-and-version-references-match',true,{sections:job.metadata.sections,machine_sha256:machine.sha256});return{methods,sections,files};}
function sourceInput(fixture,source){return{kind:'source',source:{language:'Solidity',files:{'StorageFlow.sol':source},compiler_version:fixture.source.compilation.compilerVersion.split('.Emscripten')[0],settings:fixture.source.stdJsonInput.settings,target:'StorageFlow.sol:StorageFlow'}};}
const scalarSections=['source_scalar_versions','source_scalar_phis','dataflow'];
const storageSections=['storage_references','storage_reference_phis','storage_access_bindings','state_transitions','method_details'];
async function details(job,records,name){
 const method=records.methods.find(m=>m.name===name);assert.ok(method);
 const response=await call('get_contract_method_analysis',{analysis_id:job.analysis_id,method_id:method.method_id});assert.equal(response.items[0].method_id,method.method_id);
 assert.deepEqual(response.items[0].storage_reference_ids,records.sections.storage_references.filter(r=>r.method_id===method.method_id).map(r=>r.storage_reference_id));
 assert.deepEqual(response.items[0].source_scalar_version_ids||[],records.sections.source_scalar_versions.filter(v=>v.method_id===method.method_id).map(v=>v.source_scalar_version_id));
 const detail=(await all('get_contract_analysis',{analysis_id:job.analysis_id,section:'method_details',method_id:method.method_id},2))[0];
 for(const[section,query]of Object.entries({...detail.storage_model_references,...detail.scalar_model_references})){
  assert.equal(query.query.method_id,method.method_id);const expected=records.sections[section].filter(r=>r.method_id===method.method_id);assert.equal(query.record_count,expected.length);
  assert.deepEqual(await all('get_contract_analysis',{analysis_id:job.analysis_id,section,method_id:method.method_id},2),expected);
 }
 return{method,detail};
}
async function semantics(job,records,name){
 const method=records.methods.find(m=>m.name===name),transition=records.sections.state_transitions.find(t=>t.method_id===method.method_id),file=records.files.find(f=>f.artifact_id===transition.semantics_artifact_id);
 assert.ok(transition.complete);assert.ok(file);const bytes=await artifact(job.analysis_id,file),value=JSON.parse(bytes);
 assert.equal(value.method_id,method.method_id);assert.equal(value.model_id,transition.model_id);assert.equal(value.complete,true);assert.ok(value.declarations.includes('(Array Int Int)'));
 assert.deepEqual(value.paths,transition.paths);assert.deepEqual(value.reverted_paths,transition.reverted_paths);return transition;
}
try{
 await client.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:18092/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
 const caps=await call('get_contract_analysis_capabilities');check('scalar-capabilities-and-proof-limits-match-frozen-image',caps.engine_digest===image&&caps.semantic_rules_version==='funds-semantics-19'&&caps.source_scalar_analysis.version==='source-scalar-v1'&&caps.source_scalar_analysis.all_execution_paths_verified===false&&caps.state_model_verification.whole_authorization_proved===false&&caps.state_model_verification.whole_protocol_economics_proved===false);
 const fixture=scalarFlowFixture(),job=await complete('source-scalars',fixture.material,['whatsabi','sevm','solc-ast']),s=await verify('source-scalars',job,[...scalarSections,'method_details']),cap=await details(job,s,'cap');
 const fund=s.sections.funds.find(f=>f.method_id===cap.method.method_id&&f.kind==='native_payment');assert.equal(fund.amount_derivation_status,'represented_source_dependencies_resolved');
 const expression=fund.expanded_amount_expression;assert.match(expression,/^[A-Za-z0-9_\s()+*/?:<>=!%&|.\-]+$/);for(const name of expression.match(/[A-Za-z_]\w*/g)||[])assert.ok(['due','requested'].includes(name));
 const evaluate=new Function('due','requested','return '+expression.replace(/\b[0-9]+\b/g,'$&n')),vectors=[];
 for(const due of[0n,1n,7n,9007199254740993n,(1n<<256n)-1n])for(const requested of[0n,1n,3n,(1n<<256n)-1n]){const expected=requested>0n&&requested<due?requested:due,actual=evaluate(due,requested);assert.equal(actual,expected);vectors.push({due:String(due),requested:String(requested),expected:String(expected),actual:String(actual)});}
 save('saved-cap-formula-vectors.json',{analysis_id:job.analysis_id,fund_id:fund.fund_id,expression,vectors});
 check('saved-conditional-amount-uses-both-captured-values',vectors.length===20&&cap.detail.scalar_model_references.source_scalar_phis.record_count>0);
 const modifier=await details(job,s,'modifierFrames'),modifierFunds=s.sections.funds.filter(f=>f.method_id===modifier.method.method_id&&f.kind==='native_payment');
 assert.equal(modifierFunds.length,2);assert.equal(new Function('first','second','return '+modifierFunds[0].expanded_amount_expression)(3n,7n),7n);assert.equal(new Function('first','second','return '+modifierFunds[1].expanded_amount_expression)(3n,7n),3n);
 check('repeated-modifier-parameters-remain-distinct-after-saving',true);
 const runtime=await complete('same-runtime',{kind:'bytecode',code_phase:'runtime',bytecode:fixture.bytecode},['whatsabi','sevm']);await verify('same-runtime',runtime);
 check('bytecode-does-not-acquire-source-scalar-versions',!(runtime.metadata.sections.source_scalar_versions||0));
 const comparison=await call('compare_contract_versions',{analysis_id:job.analysis_id,compare_analysis_id:runtime.analysis_id,page_size:37});check('source-runtime-comparison-remains-readable',Array.isArray(comparison.items));
 const storageFixture=storageFlowFixture(),storage=await complete('source-storage',sourceInput(storageFixture,storageFlowSource),['whatsabi','sevm','solc-ast']),v=await verify('source-storage',storage,[...storageSections,...scalarSections]),selected=await details(storage,v,'captured');
 const writes=v.sections.storage.filter(w=>w.method_id===selected.method.method_id&&w.access==='write'&&w.storage_projection);
 check('captured-storage-keys-and-field-writes-keep-their-identity',writes.length===2&&writes.every(w=>w.variable==='users'&&w.storage_reference_ids.length===1));
 const captured=await semantics(storage,v,'captured'),rollback=await semantics(storage,v,'rollback');check('full-state-artifacts-and-revert-paths-still-match-saved-rows',captured.paths[0].writes.length===2&&rollback.reverted_paths.some(p=>p.attempted_writes.length===1)&&rollback.reverted_paths.every(p=>p.storage_after==='storage_0'));
 const previousPath=readFileSync(new URL('acceptance/contract-analysis-storage/mcp-current.txt',root),'utf8').trim(),previous=JSON.parse(readFileSync(previousPath+'/source-effects-job.json','utf8')),old=await call('get_contract_analysis',{analysis_id:previous.analysis_id});
 check('saved-previous-storage-result-retains-rule-17',old.status==='completed'&&old.metadata.rule_version==='funds-semantics-17');
 const transaction=await call('explain_contract_transaction',{analysis_id:'2401259f-d5e1-4947-9374-aa45d22919e0',tx_hash:'0xecdc668e16ada76f439b09c9b8c2faa7b76f80244933193c7691ac797e26eae7'});check('saved-transaction-explanation-remains-readable',transaction.items.length>0);
 check('all-nine-contract-MCP-tools-used',actions.size===9,[...actions]);save('task-ids.json',jobs);save('result.json',{status:'PASS',image,jobs,checks:checks.length,external_provider_requests:0,quality:'offline_scalar_and_storage_fixtures_not_general_or_fresh_blind_acceptance'});writeFileSync(new URL('mcp-current.txt',base),out.pathname+'\n');
}catch(error){save('failure.json',{error:error.message,stack:error.stack,jobs});throw error;}finally{await client.close();}
