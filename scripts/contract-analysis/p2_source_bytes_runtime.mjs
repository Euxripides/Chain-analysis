import assert from'node:assert/strict';import{createRequire}from'node:module';import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{createHash}from'node:crypto';import{createServer}from'node:net';import{once}from'node:events';import{spawnSync}from'node:child_process';import{gunzipSync}from'node:zlib';
import{sourceBytesFixture,source}from'../../analysis-engines/test/fixtures/source-bytes.mjs';import{localGenesisEVM}from'./local_genesis_evm.mjs';
const root=new URL('../../',import.meta.url),memoryBase=process.env.CONTRACT_MEMORY_BASE||'acceptance/contract-analysis-memory',sha=b=>createHash('sha256').update(b).digest('hex'),image=process.env.CONTRACT_TEST_IMAGE||readFileSync(new URL(memoryBase+'/development-image.txt',root),'utf8').trim(),
 out=new URL(memoryBase+'/source-execution/'+new Date().toISOString().replace(/\D/g,'')+'Z/',root);mkdirSync(out,{recursive:true});
const fixture=sourceBytesFixture(),input={bytecode:fixture.bytecode,snapshot:{...fixture.snapshot,engine_digest:image},source:fixture.source,engines:['whatsabi','sevm','solc-ast']};
writeFileSync(new URL('input.json',out),JSON.stringify(input));writeFileSync(new URL('source.sol',out),source);
const engine=spawnSync('docker',['run','--rm','-i','--pull','never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),timeout:150000,maxBuffer:64<<20});
writeFileSync(new URL('stdout.json',out),engine.stdout);writeFileSync(new URL('stderr.log',out),engine.stderr);assert.equal(engine.status,0,engine.stderr.toString().slice(-1000));
let result=JSON.parse(engine.stdout);if(result.sections.engine_transport)result=JSON.parse(gunzipSync(Buffer.from(result.sections.engine_transport[0].content_base64,'base64')));writeFileSync(new URL('result.json',out),JSON.stringify(result));
assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');const require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js');
const word=n=>BigInt(n).toString(16).padStart(64,'0'),selector=s=>Buffer.from(keccak_256(Buffer.from(s))).toString('hex').slice(0,8),target='0x'+'2'.repeat(40),callee='0x'+'3'.repeat(40),sender='0x'+'1'.repeat(40),a='0x'+'5'.repeat(40),b='0x'+'6'.repeat(40);
async function port(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const portNumber=await port(),sourcePort=await port(),binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');let vm,sequence=0,currentCase=null;const records=[];
const rpc=async(method,params=[])=>{const r=await fetch('http://127.0.0.1:'+portNumber,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(15000)}),d=await r.json();if(d.error)throw Error(method+':'+d.error.message);return d.result;};
const amounts=[0n,1n,9007199254740993n,(1n<<128n)+9n,(1n<<256n)-100n],methods=new Map(result.sections.methods.map(m=>[m.name,m]));
// Independent fixture grammar: unsupported formulas stop this verifier. It
// never evaluates arbitrary source text and does not reuse the ABI encoder.
function formula(text,values){const compact=text.replaceAll(/[()\s]/g,'');if(compact in values)return values[compact];if(/^\d+$/.test(compact))return BigInt(compact);
 const match=/^(amount)([+/])(\d+)$/.exec(compact);assert.ok(match,'UNSUPPORTED_REFERENCE_FORMULA:'+text);return match[2]==='+'?values.amount+BigInt(match[3]):values.amount/BigInt(match[3]);}
function modelBytes(binding,caseIndex,values){const selected=binding.cases[caseIndex],versions=new Map(result.sections.source_memory_versions.map(v=>[v.source_memory_version_id,v])),ancestors=new Set();let version=selected.source_memory_version_id;
 while(version&&!ancestors.has(version)){ancestors.add(version);version=versions.get(version)?.parent_source_memory_version_id;}
 const referenced=new Set(selected.bytes.map(b=>b.source_expression_id).filter(Boolean));
 const candidates=result.sections.dataflow.filter(d=>d.kind==='source_abi_argument_derivation'&&d.method_id===binding.method_id&&(ancestors.has(d.source_memory_version_id)||referenced.has(d.expression_id)));
 const derivations=new Map();for(const d of candidates){if(derivations.has(d.expression_id))assert.equal(derivations.get(d.expression_id).symbolic_expression,d.symbolic_expression);derivations.set(d.expression_id,d);}
 return Buffer.from(selected.bytes.map(cell=>{if(cell.kind==='constant')return cell.value;assert.equal(cell.kind,'word_byte');const d=derivations.get(cell.source_expression_id);assert.ok(d,cell.source_expression_id);
  if(d.status!=='represented_source_dependencies_resolved'){
   assert.ok(d.status==='partial'&&d.symbolic_expression==='i'&&values.i!==undefined);
   if(['funds-semantics-18','funds-semantics-19','funds-semantics-20'].includes(result.rule_version))assert.deepEqual([...d.unresolved].sort(),['SOURCE_SCALAR_LOOP_REQUIRES_INVARIANT','SOURCE_SCALAR_VALUE_UNAVAILABLE'].sort());
   else assert.ok(d.unresolved.includes('LOCAL_DEFINITION_UNAVAILABLE'));
  }
  let n=formula(d.symbolic_expression,values);
  if(cell.abi_type==='address')n=BigInt.asUintN(160,n);else assert.equal(cell.abi_type,'uint256');return Buffer.from(word(n),'hex')[cell.byte_index];}));}
function conditionValue(text,flag){text=text.trim();if(text==='flag')return flag;if(text.startsWith('!(')&&text.endsWith(')'))return!conditionValue(text.slice(2,-1),flag);throw Error('UNSUPPORTED_REFERENCE_CONDITION:'+text);}
try{
 vm=await localGenesisEVM({binary,port:portNumber,sourcePort});await rpc('anvil_setCode',[target,fixture.bytecode]);await rpc('anvil_setCode',[callee,'0x600160005260206000f3']);await rpc('anvil_setBalance',[sender,'0x'+(10n**24n).toString(16)]);
 const names=['direct','helper','returned','named','tupled','tupleAssigned','rebound','mutated','unrelated','forked','missing','surviving','repeated','heapClobbered','raw','scratch','assemblyPointer','shadow','ternary','decoded','dynamic','compoundMutated','shortAnd','shortOr','afterModifier','loopIndex','zeroValue','valueAndTokens','zeroCallOnly','wrappedCall'];
 const selected=process.env.P2_SOURCE_METHODS?process.env.P2_SOURCE_METHODS.split(','):names;assert.ok(selected.length&&selected.every(n=>names.includes(n)));
 let ordinal=0;
 for(const name of selected)for(const amount of name==='loopIndex'?[0n,1n,2n,3n,5n]:name==='zeroCallOnly'?[0n]:amounts){
  const m=methods.get(name);assert.ok(m,name);const variants=['forked','missing','surviving','ternary','shortAnd','shortOr'].includes(name)?[false,true]:[false];
  for(const flag of variants){const two=['tupled','tupleAssigned','unrelated','forked','shadow','ternary'].includes(name),parameters=name==='zeroCallOnly'?[callee]:two?[callee,a,b,amount]:[callee,a,amount];if(['forked','missing','surviving','ternary','shortAnd','shortOr'].includes(name))parameters.push(flag?1n:0n);if(name==='repeated')parameters.push(2n);
   const data='0x'+selector(m.signature)+parameters.map(v=>word(typeof v==='string'?BigInt(v):v)).join(''),trace=await rpc('debug_traceCall',[{from:sender,to:target,data,gas:'0x1c9c380',...(name==='valueAndTokens'?{value:'0x5'}:{})},'latest',{enableMemory:true,disableStorage:true}]),calls=trace.structLogs.filter(s=>s.depth===1&&s.op==='CALL');
   const label='case-'+ordinal++,raw=Buffer.from(JSON.stringify(trace));writeFileSync(new URL(label+'-trace.json',out),raw);currentCase={label,name,amount:String(amount),flag,calldata:data,trace_sha256:sha(raw)};
   const bindings=result.sections.call_input_bindings.filter(v=>v.method_id===m.method_id&&v.representation_scope==='source_object');assert.ok(bindings.length,name);
   const expectedAbsent=name==='surviving'&&flag;if(expectedAbsent){assert.equal(calls.length,0);assert.equal(trace.failed,false);}else if(['heapClobbered','assemblyPointer','afterModifier'].includes(name)){assert.ok(bindings.every(v=>v.status==='partial'));}
   else{assert.equal(trace.failed,false,name);assert.equal(calls.length,name==='loopIndex'?Number(amount):bindings.length,name);for(const [i,call]of calls.entries()){
     const top=n=>BigInt('0x'+call.stack.at(-1-n).replace(/^0x/,'')),offset=Number(top(3)),length=Number(top(4)),observed=Buffer.from(call.memory.map(v=>v.replace(/^0x/,'')).join(''),'hex').subarray(offset,offset+length);
     assert.equal('0x'+top(1).toString(16).padStart(40,'0'),callee);const binding=bindings[name==='loopIndex'?0:i];
     assert.equal(top(2),name==='valueAndTokens'?5n:0n);
     if(binding.status==='partial')assert.ok(['missing','repeated','dynamic','compoundMutated'].includes(name),name);
     else{const values={token:BigInt(callee),to:BigInt(a),a:BigInt(a),b:BigInt(b),amount,...(name==='loopIndex'?{i:BigInt(i)}:{})},caseIndex=binding.cases.findIndex(c=>c.conditions.every(condition=>conditionValue(condition,flag)));assert.ok(caseIndex>=0);
      const modeled=modelBytes(binding,caseIndex,values);assert.deepEqual(modeled,observed,name+':model/actualCALLbytes');}
     let recipient=two&&['unrelated','shadow'].includes(name)?a:a,quantity=name==='helper'?amount/3n:name==='decoded'?amount/7n:amount;
     if(name==='tupled'||name==='tupleAssigned'){recipient=i?b:a;quantity=amount+BigInt(i);}if(name==='forked'||name==='ternary'){recipient=flag?a:b;quantity=amount+(flag?0n:1n);}
     if(name==='repeated')quantity=amount+1n;
     if(name==='loopIndex')quantity=BigInt(i);
     let expected=Buffer.from('a9059cbb'+word(BigInt(recipient))+word(quantity),'hex');if(name==='mutated'||name==='dynamic'||name==='shortAnd'&&flag||name==='shortOr'&&!flag)expected[0]=0;
     if(name==='compoundMutated')expected[0]^=0xff;if(name==='missing'&&!flag||name==='zeroCallOnly')expected=Buffer.alloc(0);
     if(name==='wrappedCall'){const payload=expected.toString('hex');expected=Buffer.from(selector('call(bytes)')+word(32)+word(expected.length)+payload.padEnd(Math.ceil(payload.length/64)*64,'0'),'hex');}
     assert.deepEqual(observed,expected,name+':independentABIreference');
    }}
   records.push({label,name,amount:String(amount),flag,calldata:data,actual_reverted:trace.failed,calls:calls.length,call_input_binding_ids:bindings.map(v=>v.call_input_binding_id),statuses:bindings.map(v=>v.status),trace_sha256:sha(raw),status:'PASS'});
  }
 }
 const document={status:'PASS',cases:records.length,engine_digest:image,source_result_sha256:sha(readFileSync(new URL('result.json',out))),runtime_code_sha256:sha(Buffer.from(fixture.bytecode.slice(2),'hex')),
  fixture_sha256:sha(readFileSync(new URL('analysis-engines/test/fixtures/source-bytes.mjs',root))),verifier_sha256:sha(readFileSync(new URL(import.meta.url))),selected_methods:selected,external_http:0,
  scope:'source bytes versions vs independent fixture ABI words and actual CALL memory; no target asset settlement or all-path proof',genesis:vm.info,records};writeFileSync(new URL('results.json',out),JSON.stringify(document,null,2));console.log('P2_SOURCE_BYTES_RUNTIME_PASS',records.length,out.pathname);
}catch(error){writeFileSync(new URL('failure.json',out),JSON.stringify({engine_digest:image,error:error.message,stack:error.stack,completed_cases:records.length,current_case:currentCase},null,2));throw error;}
finally{if(vm)await vm.close();}
