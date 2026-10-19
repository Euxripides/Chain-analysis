import assert from'node:assert/strict';import{createRequire}from'node:module';import{readFileSync,writeFileSync,mkdirSync}from'node:fs';
import{createHash}from'node:crypto';import{createServer}from'node:net';import{once}from'node:events';import{spawnSync}from'node:child_process';import{gunzipSync}from'node:zlib';
import{storageFlowFixture}from'../../analysis-engines/test/fixtures/storage-flow.mjs';
import{evaluateStorageSlot}from'../../analysis-engines/ir/storage-reference.mjs';import{localGenesisEVM}from'./local_genesis_evm.mjs';

const root=new URL('../../',import.meta.url),sha=b=>createHash('sha256').update(b).digest('hex'),image=process.env.CONTRACT_TEST_IMAGE||readFileSync(new URL('acceptance/contract-analysis-storage/engine-image.txt',root),'utf8').trim();
assert.match(image,/^sha256:[a-f0-9]{64}$/);
const storageBase=new URL((process.env.CONTRACT_STORAGE_BASE||'acceptance/contract-analysis-storage').replace(/\/?$/,'/'),root);
const out=new URL('source-execution/'+new Date().toISOString().replace(/\D/g,'')+'Z/',storageBase);mkdirSync(out,{recursive:true});
const require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js'),
 word=n=>BigInt(n).toString(16).padStart(64,'0'),hash=bytes=>BigInt('0x'+Buffer.from(keccak_256(bytes)).toString('hex')),
 hashWords=(...words)=>hash(Buffer.from(words.map(word).join(''),'hex')),selector=s=>Buffer.from(keccak_256(Buffer.from(s))).toString('hex').slice(0,8),
 sender='0x'+'1'.repeat(40),target='0x'+'2'.repeat(40),MAX=(1n<<256n)-1n,AMOUNT=(1n<<128n)-1n,
 initialWord=(0xdecafbad01234567n<<192n)|(11n<<128n)|7n,tokenWord=(0x123456789abcdef012345678n<<160n)|0x555n;
const docker=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m'];
async function port(){const server=createServer();server.listen(0,'127.0.0.1');await once(server,'listening');const value=server.address().port;server.close();await once(server,'close');return value;}
const portNumber=await port(),sourcePort=await port(),binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;
assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');
let sequence=0,vm,currentCase=null;const records=[],resetSlots=new Set();
const rpc=async(method,params=[])=>{const r=await fetch('http://127.0.0.1:'+portNumber,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(15000)}),body=await r.json();if(body.error)throw Error(method+':'+body.error.message);return body.result;};
const write=(name,value)=>writeFileSync(new URL(name,out),typeof value==='string'||Buffer.isBuffer(value)?value:JSON.stringify(value));
function analyze(input,label){
 const run=spawnSync(docker[0],[...docker.slice(1),image],{input:JSON.stringify(input),timeout:180000,maxBuffer:64<<20});write(label+'-input.json',input);write(label+'-stdout.json',run.stdout);write(label+'-stderr.log',run.stderr);
 assert.equal(run.status,0,run.stderr.toString().slice(-1500));let result=JSON.parse(run.stdout);if(result.sections.engine_transport)result=JSON.parse(gunzipSync(Buffer.from(result.sections.engine_transport[0].content_base64,'base64')));
 assert.equal(result.snapshot.engine_digest,image);assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');write(label+'-result.json',result);return result;
}
function scenarios(){
 const rows=[],amounts=[0n,1n,9007199254740993n,AMOUNT];
 for(const name of['captured','rebound','parameter','isolated','returned','named'])for(const amount of amounts)rows.push({name,k:3n,amount,args:[3n,amount]});
 for(const flag of[false,true])for(const amount of amounts)rows.push({name:'branch',k:3n,amount,flag,args:[flag?1n:0n,3n,amount]});
 for(const i of[0n,2n,3n])for(const amount of[1n,AMOUNT])rows.push({name:'arrayItem',k:3n,i,amount,args:[i,amount],revert:i>=3n});
 for(const[i,j]of[[0n,0n],[1n,11n],[2n,0n],[1n,12n]])for(const amount of[1n,(1n<<24n)-1n])rows.push({name:'arrayPacked',k:3n,i,j,amount,args:[i,j,amount],revert:i>=2n||j>=12n});
 for(const i of[0n,3n,4n])for(const amount of[1n,65535n])rows.push({name:'arrayMapped',k:3n,i,amount,args:[3n,i,amount],revert:i>=4n});
 rows.push({name:'remove',k:3n,args:[3n]});
 for(const amount of[0n,1n,AMOUNT])rows.push({name:'rollback',k:3n,amount,args:[3n,amount],revert:amount!==0n});
 assert.equal(rows.length,56);return rows.filter(c=>!process.env.P2_STORAGE_METHODS||process.env.P2_STORAGE_METHODS.split(',').includes(c.name));
}
function prepare(c){
 const user=k=>hashWords(BigInt(sender),hashWords(k,0n)),u=user(c.k),next=user(c.k+1n),list=hashWords(c.k,6n),listData=hashWords(list),pools=hashWords(1n),pre=new Map(),set=(slot,value)=>pre.set(String(slot),value);
 for(const address of[u,next]){set(address,initialWord);set(address+1n,tokenWord);}
 set(1n,3n);for(let i=0n;i<3n;i++){set(pools+2n*i,initialWord);set(pools+2n*i+1n,tokenWord);}
 for(let i=2n;i<6n;i++)set(i,MAX-0x123456789n*i);
 set(list,4n);set(listData,MAX-0xabcdefn);
 const expected=new Map(pre),touched=new Set(),mask=(slot,bits,offset,value)=>{const key=String(slot),fieldMask=((1n<<BigInt(bits))-1n)<<BigInt(offset*8),old=expected.get(key)||0n;expected.set(key,(old&~fieldMask)|(value<<BigInt(offset*8)));touched.add(key);};
 if(!c.revert){
  if(['captured','returned','named','rollback'].includes(c.name))mask(u,128,0,c.amount);
  if(c.name==='captured')mask(u,64,16,0n);
  if(c.name==='rebound')mask(next,128,0,c.amount);
  if(c.name==='parameter'){mask(u,128,0,c.amount);mask(u,64,16,0n);mask(u+1n,160,0,BigInt(sender));}
  if(c.name==='isolated'){mask(next,128,0,c.amount);mask(u,64,16,9n);}
  if(c.name==='branch')mask(c.flag?next:u,128,0,c.amount);
  if(c.name==='arrayItem')mask(pools+2n*c.i,128,0,c.amount);
  if(c.name==='arrayPacked')mask(2n+2n*c.i+c.j/10n,24,Number(c.j%10n)*3,c.amount);
  if(c.name==='arrayMapped')mask(listData+c.i/16n,16,Number(c.i%16n)*2,c.amount);
  if(c.name==='remove'){mask(u,128,0,0n);mask(u,64,16,0n);mask(u+1n,160,0,0n);}
 }
 return{pre,expected,touched};
}
function condition(text,c){const compact=text.replaceAll(/[()\s]/g,'');if(compact==='choose')return c.flag;if(compact==='!choose')return!c.flag;throw Error('UNSUPPORTED_REFERENCE_PREDICATE:'+text);}
function projectedWrites(result,m,c){
 const rows=result.sections.storage.filter(s=>s.method_id===m.method_id&&s.access==='write'&&s.storage_projection&&s.conditions.every(text=>condition(text,c))),slots=[];
 for(const row of rows){const projection=row.storage_projection,values={};for(const part of projection.path){
  if(!part.key_expression_id)continue;const name=part.key_text.replaceAll(/\s/g,''),value=({'msg.sender':BigInt(sender),current:c.k,k:c.k,'k+1':c.k+1n,i:c.i,j:c.j})[name];
  assert.notEqual(value,undefined,name);values[part.key_expression_id]=value;
 }slots.push({storage_id:row.storage_id,slot:String(evaluateStorageSlot(projection.slot_expression,values)),reference_ids:row.storage_reference_ids||[]});}
 return slots;
}
function traceFacts(trace){
 const stores=[],loads=[],hashes=[];
 for(const step of trace.structLogs){if(step.depth!==1)continue;const top=n=>BigInt('0x'+step.stack.at(-n).replace(/^0x/,''));
  if(step.op==='SSTORE')stores.push({slot:String(top(1)),value:String(top(2))});
  if(step.op==='SLOAD')loads.push({slot:String(top(1))});
  if(['KECCAK256','SHA3'].includes(step.op)){
   const offset=Number(top(1)),length=Number(top(2));assert.ok(offset+length<=65536);
   if(length%32!==0)continue;const memory=Buffer.from((step.memory||[]).map(w=>w.replace(/^0x/,'').padStart(64,'0')).join(''),'hex'),bytes=Buffer.alloc(length);memory.subarray(offset,offset+length).copy(bytes);
   const words=Array.from({length:length/32},(_,i)=>BigInt('0x'+bytes.subarray(i*32,i*32+32).toString('hex')).toString());hashes.push({words,hash:String(hash(bytes))});
  }
 }return{stores,loads,hashes};
}
try{
 vm=await localGenesisEVM({binary,port:portNumber,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 for(const version of(process.env.P2_STORAGE_VERSIONS||'0.4.26,0.6.12,0.7.6,0.8.30').split(',')){
  const fixture=storageFlowFixture(version),input={...fixture,snapshot:{...fixture.snapshot,evm_rules:'shanghai',engine_digest:image}},result=analyze(input,'compiler-'+version),s=result.sections,methods=new Map(s.methods.map(m=>[m.name,m]));await rpc('anvil_setCode',[target,fixture.bytecode]);
  for(const c of scenarios()){
   const label=version+'-'+records.length,m=methods.get(c.name);assert.ok(m);const setup=prepare(c);
   for(const slot of resetSlots)await rpc('anvil_setStorageAt',[target,'0x'+word(slot),'0x'+word(0)]);
   for(const[slot,value]of setup.pre){await rpc('anvil_setStorageAt',[target,'0x'+word(slot),'0x'+word(value)]);resetSlots.add(slot);}
   const data='0x'+selector(m.signature)+c.args.map(word).join('');currentCase={label,method:c.name,version,arguments:c.args.map(String)};
   const tx=await rpc('eth_sendTransaction',[{from:sender,to:target,data,gas:'0x1c9c380',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]),reverted=receipt.status==='0x0',trace=await rpc('debug_traceTransaction',[tx,{enableMemory:true,disableStorage:true}]);
   write(label+'-trace.json',trace);write(label+'-receipt.json',receipt);assert.equal(reverted,Boolean(c.revert),label+' oracle outcome');assert.equal(trace.failed,reverted);
   const facts=traceFacts(trace),actualSlots=new Set(facts.stores.map(v=>v.slot));for(const slot of actualSlots)resetSlots.add(slot);
   const post={};for(const[slot,expected]of setup.expected){const actual=BigInt(await rpc('eth_getStorageAt',[target,'0x'+word(slot),'latest']));post[slot]=String(actual);assert.equal(actual,expected,label+' reference word '+slot);}
   if(!reverted)assert.deepEqual([...actualSlots].sort(),[...setup.touched].sort(),label+' physical write set');
   const projections=projectedWrites(result,m,c);if(!reverted)assert.ok(projections.every(p=>actualSlots.has(p.slot)),label+' source projection');
   const transition=s.state_transitions.find(t=>t.method_id===m.method_id);assert.ok(transition?.complete,JSON.stringify(transition));const artifact=s.artifacts.find(a=>a.artifact_id===transition.semantics_artifact_id);assert.equal(sha(artifact.text),artifact.sha256);const semantics=JSON.parse(artifact.text),fixed={caller:String(BigInt(sender)),tx_origin:String(BigInt(sender)),msg_value:'0'};
   m.parameters.forEach((p,i)=>{fixed['arg_'+p.source_ast_id]=p.type==='bool'?c.args[i]!==0n:String(c.args[i]);});
   const payload={semantics,fixed_variables:fixed,pre_storage:Object.fromEntries([...setup.pre].map(([k,v])=>[k,String(v)])),hashes:facts.hashes,observe_slots:[...setup.expected.keys()],expected_reverted:reverted};
   write(label+'-model-input.json',payload);
   const verification=spawnSync(docker[0],[...docker.slice(1),'--entrypoint','/opt/contract-python/bin/python','--mount','type=bind,src='+new URL('scripts/contract-analysis/p2_storage_eval.py',root).pathname+',dst=/qa-verifier.py,readonly',image,'/qa-verifier.py'],{input:JSON.stringify(payload),timeout:20000,maxBuffer:2<<20});
   write(label+'-model-stdout.json',verification.stdout);write(label+'-model-stderr.log',verification.stderr);assert.equal(verification.status,0,verification.stderr.toString().slice(-2500));const evaluated=JSON.parse(verification.stdout);assert.equal(evaluated.status,'PASS');
   for(const path of evaluated.paths){for(const[slot,value]of Object.entries(path.final_storage))assert.equal(value,post[slot],label+' SMT final word '+slot);
    assert.deepEqual([...new Set(path.writes.map(w=>w.slot))].sort(),[...actualSlots].sort(),label+' SMT write locations');}
   records.push({...currentCase,transaction_hash:tx,calldata:data,reverted,trace_sha256:sha(JSON.stringify(trace)),result_sha256:sha(JSON.stringify(result)),source_projections:projections,
    actual_storage_loads:facts.loads,actual_storage_stores:facts.stores,final_storage:post,model_path_ids:evaluated.paths.map(p=>p.path_id),status:'PASS'});
   write('progress.json',{status:'RUNNING',image,cases:records.length,last:records.at(-1)});
  }console.log('P2_STORAGE_COMPILER_RUNTIME_PASS',version,records.length);
 }
 const complete=!process.env.P2_STORAGE_METHODS&&!process.env.P2_STORAGE_VERSIONS;if(complete)assert.equal(records.length,224);
 write('results.json',{status:'PASS',image,cases:records.length,complete_scenario_set:complete,records,genesis:vm.info,external_http:0,
  verifier_sha256:sha(readFileSync(new URL(import.meta.url))),solver_verifier_sha256:sha(readFileSync(new URL('scripts/contract-analysis/p2_storage_eval.py',root))),fixture_sha256:sha(readFileSync(new URL('analysis-engines/test/fixtures/storage-flow.mjs',root))),
  scope:'owned concrete storage transactions, source locations, unique SMT values and final physical words; no all-path or asset settlement proof'});
 console.log('P2_STORAGE_REAL_EVM_PASS',records.length,out.pathname);
}catch(error){write('failure.json',{image,error:error.message,stack:error.stack,current_case:currentCase,completed_cases:records.length});throw error;}
finally{if(vm)await vm.close();}
