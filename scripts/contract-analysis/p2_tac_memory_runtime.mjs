import assert from'node:assert/strict';import{readFileSync,readdirSync,writeFileSync,mkdirSync}from'node:fs';import{createRequire}from'node:module';import{createHash}from'node:crypto';import{createServer}from'node:net';import{once}from'node:events';
import{localGenesisEVM}from'./local_genesis_evm.mjs';
const root=new URL('../../',import.meta.url),memoryBase=process.env.CONTRACT_MEMORY_BASE||'acceptance/contract-analysis-memory',sha=b=>createHash('sha256').update(b).digest('hex'),runs=new URL(memoryBase+'/tac-runs/',root);
const run=readdirSync(runs).sort().at(-1),directory=new URL(run+'/',runs),result=JSON.parse(readFileSync(new URL('result.json',directory))),input=JSON.parse(readFileSync(new URL('input.json',directory))),material=JSON.parse(input.submitted_input_json);
const binding=result.sections.call_input_bindings.find(b=>b.status==='exact'&&b.call_type==='CALL');assert.ok(binding);assert.equal(binding.cases.length,1);assert.equal(binding.cases[0].bytes.length,68);
if(process.env.CONTRACT_TEST_IMAGE)assert.equal(result.snapshot.engine_digest,process.env.CONTRACT_TEST_IMAGE);
const candidate=result.sections.fund_call_models?.find(m=>m.call_input_binding_id===binding.call_input_binding_id);assert.ok(candidate);assert.equal(candidate.amount.value_expression,'calldataload(68)');assert.equal(candidate.recipient.value_expression,'calldataload(36)');assert.equal(candidate.status,'pending');assert.equal(candidate.target_implementation_verified,false);
const method=result.sections.methods.find(m=>m.method_id===binding.method_id),code=material.bytecode,target='0x'+'2'.repeat(40),callee='0x'+'3'.repeat(40),sender='0x'+'1'.repeat(40),eoa='0x'+'4'.repeat(40);
const out=new URL(memoryBase+'/tac-execution/'+new Date().toISOString().replace(/\D/g,'')+'Z/',root);mkdirSync(new URL('../',out),{recursive:true});mkdirSync(out);
const binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');
const require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js');assert.equal('0x'+Buffer.from(keccak_256(Buffer.from('pay(address,address,uint256)'))).toString('hex').slice(0,8),method.selector);
async function port(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const endpointPort=await port(),sourcePort=await port();let vm,sequence=0;const records=[];
const rpc=async(method,params=[])=>{const r=await fetch('http://127.0.0.1:'+endpointPort,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(10000)}),d=await r.json();if(d.error)throw Error(method+':'+d.error.message);return d.result;};
const word=n=>BigInt(n).toString(16).padStart(64,'0'),dataFor=(asset,to,amount)=>method.selector+word(BigInt(asset))+word(BigInt(to))+word(amount);
const load=(bytes,offset)=>{const out=Buffer.alloc(32);bytes.subarray(offset,offset+32).copy(out);return out;};
function evaluateByte(b,calldata){if(b.kind==='constant')return b.value;assert.equal(b.kind,'word_byte');const match=/^calldataload\((\d+)\)$/.exec(b.expression);assert.ok(match,'Independent fixture evaluator rejects unsupported expressions');return load(calldata,Number(match[1]))[b.byte_index];}
function evaluateTarget(calldata){const match=/^calldataload\((\d+)\)$/.exec(binding.target_expression);assert.ok(match);return'0x'+load(calldata,Number(match[1])).subarray(12).toString('hex');}
try{
 vm=await localGenesisEVM({binary,port:endpointPort,sourcePort});await rpc('anvil_setCode',[target,code]);await rpc('anvil_setCode',[callee,'0x600160005260206000f3']);
 let ordinal=0;
 for(const asset of[callee,eoa])for(const recipient of['0x'+'0'.repeat(40),'0x'+'5'.repeat(40),'0x'+'f'.repeat(40)])for(const amount of[0n,1n,9007199254740993n,(1n<<128n)+9n,(1n<<256n)-1n]){
  const data=dataFor(asset,recipient,amount),calldata=Buffer.from(data.slice(2),'hex'),request={from:sender,to:target,data,gas:'0x989680'};
  const trace=await rpc('debug_traceCall',[request,'latest',{enableMemory:true,disableStorage:true}]),returned=await rpc('eth_call',[request,'latest']);assert.equal(trace.failed,false);assert.equal(BigInt(returned),1n);
  const calls=trace.structLogs.filter(r=>r.op==='CALL'&&r.pc===Number(BigInt(binding.original_statement)));assert.equal(calls.length,1);const step=calls[0],top=n=>BigInt('0x'+step.stack.at(-1-n).replace(/^0x/,''));
  const at=Number(top(3)),length=Number(top(4));assert.equal(at,128);assert.equal(length,68);assert.equal(top(2),0n);
  const observed=Buffer.from(step.memory.map(s=>s.replace(/^0x/,'')).join(''),'hex').subarray(at,at+length),reference=Buffer.from('a9059cbb'+word(BigInt(recipient))+word(amount),'hex'),modeled=Buffer.from(binding.cases[0].bytes.map(b=>evaluateByte(b,calldata)));
  assert.deepEqual(observed,reference);assert.deepEqual(modeled,reference);assert.equal(evaluateTarget(calldata),asset);assert.equal('0x'+top(1).toString(16).padStart(40,'0'),asset);
  const name='case-'+ordinal++,raw=Buffer.from(JSON.stringify(trace));writeFileSync(new URL(name+'-trace.json',out),raw);
  records.push({name,asset,recipient,amount:String(amount),calldata:data,call_input:observed.toString('hex'),model_input:modeled.toString('hex'),reference_input:reference.toString('hex'),trace_sha256:sha(raw),target_has_code:asset===callee,claim:'exact_encoded_CALL_input_only_not_asset_settlement',status:'PASS'});
 }
 for(const[name,data]of[['short',method.selector],['dirty-recipient',method.selector+word(BigInt(callee))+word(1n<<200n)+word(1)],['dirty-asset',method.selector+word(1n<<200n)+word(BigInt(callee))+word(1)]]){
  const trace=await rpc('debug_traceCall',[{from:sender,to:target,data,gas:'0x989680'},'latest',{enableMemory:true,disableStorage:true}]);assert.equal(trace.failed,true);assert.equal(trace.structLogs.some(s=>s.op==='CALL'&&s.pc===Number(BigInt(binding.original_statement))),false);
  const raw=Buffer.from(JSON.stringify(trace));writeFileSync(new URL(name+'-trace.json',out),raw);records.push({name,calldata:data,expected_revert_before_call:true,trace_sha256:sha(raw),status:'PASS'});
 }
 const document={status:'PASS',cases:records.length,engine_digest:result.snapshot.engine_digest,analysis_result_sha256:sha(readFileSync(new URL('result.json',directory))),input_sha256:result.snapshot.input_sha256,code_sha256:sha(Buffer.from(code.slice(2),'hex')),
  method_id:binding.method_id,call_input_binding_id:binding.call_input_binding_id,analysis_context_frame_id:binding.frame_id,observed_execution_address:target,scope:'frozen TAC model vs independent ABI words and actual CALL memory; full model, asset settlement, arbitrary types and MCP not accepted',genesis:vm.info,external_http:0,records};
 writeFileSync(new URL('results.json',out),JSON.stringify(document,null,2));console.log('P2_TAC_REAL_CALL_INPUT_PASS',records.length,out.pathname);
}catch(error){writeFileSync(new URL('failure.json',out),JSON.stringify({error:String(error.message),stack:error.stack,completed_cases:records.length},null,2));throw error;}
finally{if(vm)await vm.close();}
