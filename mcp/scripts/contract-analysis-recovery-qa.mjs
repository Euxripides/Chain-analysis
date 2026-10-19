import {readFileSync,writeFileSync,mkdirSync,existsSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {gunzipSync} from 'node:zlib';
import assert from 'node:assert/strict';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {StreamableHTTPClientTransport} from '@modelcontextprotocol/sdk/client/streamableHttp.js';

const root=new URL('../../',import.meta.url),base=new URL('acceptance/contract-analysis-partial-recovery/',root);
const materials=new URL('acceptance/contract-analysis-general/corpus/materials/',root);
const image=readFileSync(new URL('engine-image.txt',base),'utf8').trim();
const out=new URL('mcp-'+image.slice(7,23)+'/',base);mkdirSync(out,{recursive:true});
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const client=new Client({name:'contract-partial-recovery-qa',version:'1.0.0'});
const sha=b=>createHash('sha256').update(b).digest('hex'),save=(name,value)=>writeFileSync(new URL(name,out),JSON.stringify(value,null,2));
const checksFile=new URL('checks.json',out),checks=existsSync(checksFile)?JSON.parse(readFileSync(checksFile,'utf8')):[];
function check(name,ok,evidence){checks.push({name,status:ok?'PASS':'FAIL',evidence});save('checks.json',checks);console.log(name,ok?'PASS':'FAIL');assert.ok(ok,name);}
function payload(r){const e=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);if(r.isError||e.success===false)throw Error(JSON.stringify(e.error||e));return e.data??e;}
let last=0;
async function rawCall(name,args={}){const delay=Math.max(0,last+1250-Date.now());if(delay)await new Promise(r=>setTimeout(r,delay));last=Date.now();return client.callTool({name,arguments:args});}
const call=async(name,args={})=>payload(await rawCall(name,args));
async function all(name,args,page_size=200){const rows=[],seen=new Set();let cursor,total;do{const p=await call(name,{...args,page_size,...(cursor?{cursor}:{})});total??=p.record_count;assert.equal(total,p.record_count);rows.push(...p.items);cursor=p.has_more?p.next_cursor:null;if(cursor){assert.ok(!seen.has(cursor));seen.add(cursor)}}while(cursor);assert.equal(rows.length,total);return rows;}
async function artifact(aid,item){const parts=[];let offset=0;for(;;){const r=await rawCall('get_contract_analysis_artifact',{analysis_id:aid,artifact_id:item.artifact_id,offset,chunk_size:4<<20});const m=payload(r),b=Buffer.from(r.content.find(c=>c.type==='resource').resource.blob,'base64');assert.equal(m.offset,offset);assert.equal(m.byte_length,b.length);assert.equal(m.chunk_sha256,sha(b));parts.push(b);if(m.complete)break;assert.equal(m.next_offset,offset+b.length);offset=m.next_offset;}const bytes=Buffer.concat(parts);assert.equal(bytes.length,item.byte_size);assert.equal(sha(bytes),item.sha256);return bytes;}
async function wait(aid,predicate=j=>!['queued','running'].includes(j.status)){for(let i=0;i<400;i++){const j=await call('get_contract_analysis',{analysis_id:aid});if(predicate(j))return j;await new Promise(r=>setTimeout(r,500));}throw Error('TASK_OBSERVATION_TIMEOUT:'+aid);}
const budget={max_contracts:1,max_rpc_requests:10,max_state_reads:1,max_credits:5000,timeout_seconds:300};
function source(key){const d=JSON.parse(readFileSync(new URL(key+'/source-provider.json',materials),'utf8')),settings={...d.stdJsonInput.settings};delete settings.outputSelection;return{kind:'source',source:{language:'Solidity',files:Object.fromEntries(Object.entries(d.stdJsonInput.sources).map(([k,v])=>[k,v.content])),compiler_version:d.compilation.compilerVersion,target:d.compilation.fullyQualifiedName,settings}};}
const runtime=readFileSync(new URL('uniswap-v3-pool/runtime.hex',materials),'utf8').trim();
async function submit(key,input){const q={input,engines:['whatsabi','sevm','solc-ast'],budget,idempotency_key:'recovery-'+image.slice(7,19)+'-'+key};const s=await call('analyze_contract',q);save(key+'-submission.json',s);save(key+'-request.json',q);return s;}
async function complete(key,input){const s=await submit(key,input),job=await wait(s.analysis_id);save(key+'-job.json',job);check(key+'-persisted-completed',job.status==='completed'&&job.metadata.rule_version==='funds-semantics-15'&&job.metadata.metrics.engine_digest===image,{analysis_id:s.analysis_id,error_code:job.error_code});return job;}
function zero(job){const s=job.metadata.snapshot,m=job.metadata.metrics;check(job.analysis_id+'-zero-provider-and-symbolic-state',s.identity_kind==='code_only'&&m.rpc_http_requests===0&&Number(m.credits)===0&&m.durable_budget.reserved_rpc_http_requests===0&&m.durable_budget.state_read_attempts===0&&!['block_number','block_hash','block_time','code_address','storage_address'].some(k=>Object.hasOwn(s,k)),m.durable_budget);
 const resources=(m.engine_resources||[]).map(r=>r.metrics?.container_execution);
 check(job.analysis_id+'-completed-container-memory-and-CPU-bound-to-wire',resources.length>0&&resources.every(r=>r?.scope==='after_engine_exit_and_complete_output_forwarding'&&r.memory_peak_bytes>0&&(r.cpu_stat.usage_usec>0||r.cpu_stat.usage_nanoseconds>0)&&/^[a-f0-9]{64}$/.test(r.stdout_sha256)),resources);
}
async function verify(key,job){
 const files=await all('get_contract_analysis',{analysis_id:job.analysis_id,section:'artifacts'}),machine=files.find(a=>a.kind==='machine_result');assert.ok(machine);
 const bytes=await artifact(job.analysis_id,machine);writeFileSync(new URL(key+'-machine.json',out),bytes);const full=JSON.parse(bytes);
 let sections=full.sections;
 if(full.representation==='complete_section_artifacts_v1'){
  sections={};
  for(const [section,parts]of Object.entries(full.section_artifacts)){
   let count=0;const rows=[];
   for(const part of parts){assert.equal(part.from_ordinal,count);const b=await artifact(job.analysis_id,part),p=JSON.parse(b);assert.equal(p.length,part.record_count);count+=p.length;rows.push(...p);}
   assert.equal(count,job.metadata.sections[section]);sections[section]=rows;
  }
 } else for(const [section,rows]of Object.entries(sections))if(section!=='artifacts')assert.equal(rows.length,job.metadata.sections[section],section);
 // The machine document predates its own artifact and the evidence ZIP. Both
 // are added afterwards and must be reconciled, rather than counted as loss.
 const archived=sections.artifacts||full.artifacts,archivedIDs=new Set(archived.map(a=>a.artifact_id));
 assert.equal(files.length,job.metadata.sections.artifacts);
 assert.equal(archived.length+2,files.length);
 const extra=files.filter(a=>!archivedIDs.has(a.artifact_id));
 assert.deepEqual(extra.map(a=>a.kind).sort(),['evidence_bundle','machine_result']);
 for(const a of archived){const actual=files.find(f=>f.artifact_id===a.artifact_id);assert.ok(actual);assert.equal(actual.sha256,a.sha256);assert.equal(actual.byte_size,a.byte_size);assert.equal(actual.kind,a.kind);}
 const bundle=files.find(a=>a.kind==='evidence_bundle'),zip=await artifact(job.analysis_id,bundle);
 const expected=files.filter(a=>a.kind!=='evidence_bundle').map(a=>({artifact_id:a.artifact_id,sha256:a.sha256,byte_size:a.byte_size}));
 const zipCheck=JSON.parse(execFileSync('python3',[new URL('scripts/contract-analysis/p2_recovery_bundle.py',root).pathname],{input:Buffer.concat([Buffer.from(JSON.stringify(expected)+'\n'),zip]),maxBuffer:65536}));
 save(key+'-evidence-bundle-verification.json',zipCheck);
 const methods=await all('list_contract_methods',{analysis_id:job.analysis_id},37);assert.deepEqual(methods,sections.methods);
 const flows=await all('get_contract_execution_flow',{analysis_id:job.analysis_id});assert.deepEqual(flows,sections.flows);
 const ids=new Set(files.map(a=>a.artifact_id)),evidence=new Set(sections.evidence.map(e=>e.evidence_id));
 for(const row of sections.evidence)if(row.location?.artifact_id)assert.ok(ids.has(row.location.artifact_id));
 for(const row of methods)for(const ref of row.evidence_ids||[])assert.ok(evidence.has(ref));
 if(sections.dependency_artifacts?.length){
  for(const d of sections.dependency_artifacts){const b=gunzipSync(await artifact(job.analysis_id,files.find(f=>f.artifact_id===d.artifact_id)));
   assert.equal(b.length,d.uncompressed_bytes);assert.equal(sha(b),d.uncompressed_sha256);const document=JSON.parse(b);
   assert.equal(document.conclusions.length,job.metadata.sections.conclusions);assert.equal(document.analysis_gaps.length,job.metadata.sections.analysis_gaps);
   for(const m of methods)if(m.analysis_dependencies?.dependency_artifact_id)assert.equal(document.conclusions[m.analysis_dependencies.dependency_record].conclusion_id,m.analysis_dependencies.conclusion_id);
  }
 }
 check(key+'-all-machine-parts-counts-SHAs-method-and-flow-pages-match',true,{machine_sha256:machine.sha256,section_counts:job.metadata.sections});
 return{sections,methods,files};
}
const state=aid=>JSON.parse(execFileSync('python3',[new URL('scripts/contract-analysis/p2_recovery_state.py',root).pathname,aid],{encoding:'utf8'}));
const unit='blockchain-contract-coverage-qa-worker.service';
try{
 await client.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:18092/mcp'),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
 const caps=await call('get_contract_analysis_capabilities');const expectedImage=process.argv[2]==='race'?process.env.RECOVERY_RACE_IMAGE:image;
 check('actual-candidate-recovery-capabilities',caps.engine_digest===expectedImage&&caps.semantic_rules_version==='funds-semantics-15'&&caps.execution_isolation.sevm_heap_mib===512&&caps.execution_isolation.source_method_expressions===8000);
 if((process.argv[2]||'core')==='core'){
  const normal=await complete('normal-weth',source('weth'));zero(normal);const n=await verify('normal-weth',normal);
  const withdraw=n.methods.find(m=>m.signature==='withdraw(uint256)');assert.ok(withdraw);
  const detail=await call('get_contract_method_analysis',{analysis_id:normal.analysis_id,method_id:withdraw.method_id});check('normal-source-guard-and-payment-preserved',detail.items.length===1&&n.sections.funds.some(f=>f.method_id===withdraw.method_id&&f.asset==='native'&&f.amount_expression==='wad'&&f.to_expression==='msg.sender'));
  const submitted=await complete('v3-source',source('uniswap-v3-pool'));zero(submitted);const s=await verify('v3-source',submitted);
  check('v3-partial-source-retains-completed-bodies-and-compiled-entries',s.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='partial')&&s.methods.some(m=>m.source_checkpoint==='method_body_saved')&&s.methods.some(m=>m.source_checkpoint==='compiled_entry_only')&&s.files.some(a=>a.kind==='submitted_source'));
  const failed=s.methods.find(m=>m.source_checkpoint==='compiled_entry_only');const d=await call('get_contract_method_analysis',{analysis_id:submitted.analysis_id,method_id:failed.method_id});check('failed-method-and-specific-recovery-gap-remain-readable',d.items.length===1&&d.items[0].completeness.unresolved.some(g=>g.code.startsWith('SOURCE_')));
  const hidden=await complete('v3-runtime',{kind:'bytecode',code_phase:'runtime',bytecode:runtime});zero(hidden);const h=await verify('v3-runtime',hidden);
  check('v3-SEVM-failure-preserves-selectors-disassembly-and-method-gaps',h.sections.engines.some(e=>e.engine==='sevm'&&e.status==='failed'&&e.error_code.startsWith('SEVM_CHILD_'))&&h.methods.length>=26&&h.methods.every(m=>m.completeness.unresolved.some(g=>g.code.startsWith('SEVM_CHILD_')))&&h.files.some(a=>a.kind==='disassembly'));
  const old=await call('get_contract_analysis',{analysis_id:'3235632e-8e6c-4b3b-85c6-80d0a281b362'});check('frozen-P1-task-still-readable-with-original-rule',old.status==='completed'&&old.metadata.rule_version==='funds-semantics-14');
  const selected=await call('explain_contract_transaction',{analysis_id:'2401259f-d5e1-4947-9374-aa45d22919e0',tx_hash:'0xecdc668e16ada76f439b09c9b8c2faa7b76f80244933193c7691ac797e26eae7'});check('saved-selected-transaction-explanation-compatible-without-query',selected.items.length>0);
  save('core-task-ids.json',[normal.analysis_id,submitted.analysis_id,hidden.analysis_id]);
 } else if(process.argv[2]==='lifecycle'){
  const stamp=Date.now().toString();let stopped=false;
  try{
   execFileSync('systemctl',['--user','stop',unit]);stopped=true;
   const submission=await submit('lifecycle-'+stamp,{kind:'bytecode',code_phase:'runtime',bytecode:runtime}),aid=submission.analysis_id;
   const queued=await call('get_contract_analysis',{analysis_id:aid});check('lifecycle-confirmed-queued',queued.status==='queued');
   await call('cancel_analysis',{analysis_id:aid});const cancelled=await call('get_contract_analysis',{analysis_id:aid});
   check('queued-cancel-preserves-input',cancelled.status==='cancelled'&&cancelled.snapshot.input_sha256===submission.snapshot.input_sha256);await artifact(aid,cancelled.snapshot.input_artifact);
   await call('resume_analysis',{analysis_id:aid});check('queued-resume-confirmed',(await call('get_contract_analysis',{analysis_id:aid})).status==='queued');
   execFileSync('systemctl',['--user','start',unit]);stopped=false;
   await wait(aid,j=>j.status==='running'&&j.phase.startsWith('decompiling'));const before=state(aid);save('lifecycle-before-running-cancel.json',before);assert.ok(before.owned_containers.length);
   await call('cancel_analysis',{analysis_id:aid});await call('resume_analysis',{analysis_id:aid});
   await wait(aid,j=>j.status==='running'&&j.phase.startsWith('decompiling'));let replacement;
   for(let i=0;i<100;i++){replacement=state(aid);if(replacement.job.fence>before.job.fence&&replacement.owned_containers.length)break;await new Promise(r=>setTimeout(r,250));}
   check('running-cancel-fence-replaced-before-publication',replacement.job.fence>before.job.fence&&replacement.owned_containers.length>0,replacement);
   save('lifecycle-before-worker-loss.json',replacement);
   execFileSync('systemctl',['--user','kill','--kill-whom=main','--signal=SIGKILL',unit]);
   await new Promise(r=>setTimeout(r,1000));execFileSync('systemctl',['--user','start',unit]);
   const lost=state(aid);save('lifecycle-after-worker-loss.json',lost);
   const job=await wait(aid);save('lifecycle-job.json',job);const final=state(aid);save('lifecycle-final-state.json',final);
   check('worker-loss-recovered-under-new-fence',job.status==='completed'&&final.job.fence>replacement.job.fence&&final.job.attempts>replacement.job.attempts,final);
   check('all-owned-old-and-current-containers-removed',final.owned_containers.length===0,final.owned_containers);
   check('lifecycle-preserves-input-and-original-processing-budget',job.metadata.snapshot.input_sha256===submission.snapshot.input_sha256&&job.metadata.metrics.durable_budget.processing_ms>0&&job.metadata.metrics.durable_budget.processing_ms<=budget.timeout_seconds*1000,job.metadata.metrics.durable_budget);
   zero(job);await verify('lifecycle',job);save('lifecycle-task-id.json',aid);
  }finally{if(stopped)execFileSync('systemctl',['--user','start',unit]);}
 }else if(process.argv[2]==='race'){
  assert.ok(/^sha256:[a-f0-9]{64}$/.test(expectedImage)&&expectedImage!==image);
  const stamp=Date.now().toString(),raceUnit=process.env.RECOVERY_RACE_UNIT;
  assert.ok(/^blockchain-contract-publication-race-[0-9]+\.service$/.test(raceUnit||''));
  save('publication-race-unit.json',{unit:raceUnit,image:expectedImage,main:unit});
  let mainPaused=false,secondPaused=false,created=false;
  try{
   execFileSync('systemctl',['--user','stop',unit]);
   const submission=await submit('publication-race-'+stamp,{kind:'bytecode',code_phase:'creation',bytecode:'0x00'}),aid=submission.analysis_id;
   execFileSync('systemctl',['--user','start',unit]);
   await wait(aid,j=>j.status==='running'&&j.phase.startsWith('decompiling'));
   let old;
   for(let i=0;i<100;i++){old=state(aid);if(old.owned_containers.length)break;await new Promise(r=>setTimeout(r,50));}
   assert.equal(old.owned_containers.length,1);save('race-old-running.json',old);
   execFileSync('systemctl',['--user','kill','--kill-whom=main','--signal=SIGSTOP',unit]);mainPaused=true;
   const exit=execFileSync('docker',['wait',old.owned_containers[0]],{encoding:'utf8',timeout:20000}).trim();
   check('old-engine-completed-successfully-while-old-worker-paused',exit==='0',{exit,old_fence:old.job.fence});
   const sessionLoss=JSON.parse(execFileSync('python3',[new URL('scripts/contract-analysis/p2_recovery_lease_loss.py',root).pathname,aid],{encoding:'utf8'}));
   save('race-paused-worker-database-session-loss.json',sessionLoss);
   execFileSync('systemctl',['--user','start',raceUnit]);created=true;
   let replacement;
   for(let i=0;i<250;i++){replacement=state(aid);if(replacement.job.fence>old.job.fence&&replacement.job.phase.startsWith('decompiling')&&replacement.owned_containers.includes('contract-analysis-'+aid+'-'+replacement.job.fence))break;await new Promise(r=>setTimeout(r,100));}
   assert.ok(replacement.job.fence>old.job.fence&&replacement.owned_containers.includes('contract-analysis-'+aid+'-'+replacement.job.fence));
   execFileSync('systemctl',['--user','kill','--kill-whom=main','--signal=SIGSTOP',raceUnit]);secondPaused=true;
   save('race-replacement-paused.json',replacement);
   execFileSync('systemctl',['--user','kill','--kill-whom=main','--signal=SIGCONT',unit]);mainPaused=false;
   await new Promise(r=>setTimeout(r,2000));const guarded=state(aid);save('race-after-old-result-unblocked.json',guarded);
   check('completed-old-result-cannot-publish-over-new-live-fence',guarded.job.status==='running'&&guarded.job.fence===replacement.job.fence&&guarded.job.published_rows===0&&guarded.job.engine_runs.filter(r=>r.fence===old.job.fence).every(r=>!r.has_output),guarded);
   execFileSync('systemctl',['--user','kill','--kill-whom=main','--signal=SIGCONT',raceUnit]);secondPaused=false;
   const job=await wait(aid);save('race-job.json',job);const final=state(aid);save('race-final-state.json',final);
   check('new-fence-alone-publishes-complete-durable-result',job.status==='completed'&&final.job.fence===replacement.job.fence&&job.metadata.metrics.engine_digest===expectedImage&&final.job.engine_runs.filter(r=>r.has_output).every(r=>r.fence===replacement.job.fence),final);
   zero(job);await verify('publication-race',job);
  }finally{
   if(mainPaused)execFileSync('systemctl',['--user','kill','--kill-whom=main','--signal=SIGCONT',unit]);
   if(secondPaused)execFileSync('systemctl',['--user','kill','--kill-whom=main','--signal=SIGCONT',raceUnit]);
   if(created){try{execFileSync('systemctl',['--user','stop',raceUnit]);}catch(error){console.error('REPLICA_STOP_FAILED',error.status);}}
   execFileSync('systemctl',['--user','start',unit]);
  }
 }else if(process.argv[2]==='compat'){
  const ids=JSON.parse(readFileSync(new URL('core-task-ids.json',out),'utf8'));
  const comparison=await call('compare_contract_versions',{analysis_id:ids[0],compare_analysis_id:'3235632e-8e6c-4b3b-85c6-80d0a281b362',page_size:37});
  check('P2-versus-frozen-P1-comparison-readable',Array.isArray(comparison.items));save('version-comparison.json',comparison);
 }else throw Error('Choose core, lifecycle or compat');
}catch(error){save('failure-'+Date.now()+'.json',{phase:process.argv[2]||'core',message:String(error.message),stack:String(error.stack)});throw error;}
finally{await client.close();}
