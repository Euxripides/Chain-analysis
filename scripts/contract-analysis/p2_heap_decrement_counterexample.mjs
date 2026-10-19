// Concrete counterexamples for a prior final-payment formula that omitted --.
import assert from'node:assert/strict';import{createHash}from'node:crypto';import{createRequire}from'node:module';
import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{createServer}from'node:net';import{once}from'node:events';
import{localGenesisEVM}from'./local_genesis_evm.mjs';
const root=new URL('../../',import.meta.url),base=new URL('acceptance/contract-analysis-heap-writes/',root),sha=x=>createHash('sha256').update(x).digest('hex'),
 image=readFileSync(new URL('engine-image.txt',base),'utf8').trim(),out=new URL('development-counterexamples/'+image.slice(7,23)+'/prior-decrement-'+new Date().toISOString().replace(/\D/g,'')+'Z/',base),
 require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js'),hash=x=>Buffer.from(keccak_256(x)).toString('hex'),
 word=n=>BigInt(n).toString(16).padStart(64,'0'),hex=n=>'0x'+word(n),selector=s=>hash(Buffer.from(s)).slice(0,8),mapSlot=(address,slot)=>'0x'+hash(Buffer.from(word(address)+word(slot),'hex')),
 target='0x'+'2'.repeat(40),sender='0x'+'1'.repeat(40),receiver='0x'+'4'.repeat(40),MAX=(1n<<256n)-1n,MAX128=(1n<<128n)-1n;
mkdirSync(out,{recursive:true});const write=(n,v)=>writeFileSync(new URL(n,out),typeof v==='string'?v:JSON.stringify(v,(_,x)=>typeof x==='bigint'?String(x):x));
const material=new URL('acceptance/contract-analysis-general/corpus/materials/uniswap-v3-pool/',root),runtime=readFileSync(new URL('runtime.hex',material),'utf8').trim(),
 seal=JSON.parse(readFileSync(new URL('acceptance/contract-analysis-general/corpus/seal.json',root))),manifestRaw=readFileSync(new URL('acceptance/contract-analysis-general/corpus/corpus-manifest.json',root));
assert.equal(sha(manifestRaw),seal.manifest_sha256);const context=JSON.parse(manifestRaw).samples.flatMap(s=>s.contexts).find(c=>c.key==='uniswap-v3-pool');for(const[n,h]of Object.entries(context.files))assert.equal(sha(readFileSync(new URL(n,material))),h);
const currentRaw=readFileSync(new URL('corpus/candidate-20-'+image.slice(7,23)+'/uniswap-v3-pool-source-output.json',base)),oldRaw=readFileSync(new URL('acceptance/contract-analysis-storage/corpus/candidate-17-e0bef21df97cca9c/uniswap-v3-pool-source-output.json',root)),
 current=JSON.parse(currentRaw),old=JSON.parse(oldRaw);assert.equal(current.snapshot.engine_digest,image);
function funds(r){const m=r.sections.methods.find(m=>m.name==='collectProtocol');return r.sections.funds.filter(f=>f.method_id===m.method_id&&f.kind==='token_transfer_intent');}
const before=funds(old),after=funds(current);assert.equal(before.length,2);assert.equal(after.length,2);assert.ok(before.every(f=>f.amount_derivation_status==='represented_source_dependencies_resolved'));assert.ok(after.every(f=>f.amount_derivation_status==='partial'));
for(const f of after){const d=current.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id);assert.ok(d.unresolved.includes('SOURCE_SCALAR_INCREMENT_REQUIRES_ORDERED_VALUE_MODEL'));}
const source=`pragma solidity 0.8.30;contract WitnessToken{mapping(address=>uint256)public balances;function transfer(address to,uint256 amount)external returns(bool){require(balances[msg.sender]>=amount);balances[msg.sender]-=amount;balances[to]+=amount;return true;}}`,
 solc=require('solc-0.8.30'),input={language:'Solidity',sources:{'WitnessToken.sol':{content:source}},settings:{evmVersion:'shanghai',outputSelection:{'*':{'*':['evm.deployedBytecode']}}}},compiled=JSON.parse(solc.compile(JSON.stringify(input)));
assert.equal((compiled.errors||[]).filter(e=>e.severity==='error').length,0);const witness='0x'+compiled.contracts['WitnessToken.sol'].WitnessToken.evm.deployedBytecode.object;
write('witness-source.sol',source);write('witness-compiler-input.json',input);write('witness-compiler-output.json',compiled);write('old-and-new-funds.json',{old_image:old.snapshot.engine_digest,image,old_result_sha256:sha(oldRaw),current_result_sha256:sha(currentRaw),before,after});
async function port(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const portNumber=await port(),sourcePort=await port(),binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;
assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');let sequence=0,vm,currentCase;const records=[];
const rpc=async(method,params=[])=>{const b=await(await fetch('http://127.0.0.1:'+portNumber,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(15000)})).json();if(b.error)throw Error(method+':'+b.error.message);return b.result;},
 set=async(a,s,v)=>rpc('anvil_setStorageAt',[a,typeof s==='string'?s:hex(s),hex(v)]),get=async(a,s)=>BigInt(await rpc('eth_getStorageAt',[a,typeof s==='string'?s:hex(s),'latest']));
function oldQuantity(f,requested,due){const expression=f.expanded_amount_expression;assert.match(expression,/^[A-Za-z0-9_\s().?:<>]+$/);return new Function('amount0Requested','amount1Requested','protocolFees','return '+expression)(requested,requested,{token0:due,token1:due});}
try{vm=await localGenesisEVM({binary,port:portNumber,sourcePort});await rpc('anvil_setCode',[target,runtime]);await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 const immutable={};for(const name of['factory','token0','token1']){const value=await rpc('eth_call',[{to:target,data:'0x'+selector(name+'()')},'latest']);assert.equal(value.length,66);immutable[name]='0x'+value.slice(-40);}
 assert.notEqual(immutable.token0,immutable.token1);await rpc('anvil_setCode',[immutable.factory,'0x7f'+word(sender)+'60005260206000f3']);for(const token of[immutable.token0,immutable.token1])await rpc('anvil_setCode',[token,witness]);write('owned-immutable-getter-values.json',immutable);
 for(const[due,requested]of[[0n,0n],[1n,0n],[1n,1n],[7n,3n],[7n,7n],[7n,99n],[9007199254740993n,9007199254740993n],[MAX128,MAX128]]){
  currentCase={due,requested};const snapshot=await rpc('evm_snapshot'),initial=requested>due?due:requested,paid=initial>0n&&initial===due?initial-1n:initial;
  // Uniswap V3's compiler layout: slot0.unlocked at byte30, packed protocolFees in slot3.
  await set(target,0n,1n<<240n);await set(target,3n,due+(due<<128n));for(const token of[immutable.token0,immutable.token1]){await set(token,mapSlot(target,0n),MAX);await set(token,mapSlot(receiver,0n),0n);}
  const tx=await rpc('eth_sendTransaction',[{from:sender,to:target,data:'0x'+selector('collectProtocol(address,uint128,uint128)')+word(receiver)+word(requested)+word(requested),gas:'0x1c9c380',gasPrice:'0x77359400'}]);await rpc('evm_mine');
  const receipt=await rpc('eth_getTransactionReceipt',[tx]),trace=await rpc('debug_traceTransaction',[tx,{enableMemory:true}]),label='case-'+records.length;write(label+'-receipt.json',receipt);write(label+'-trace.json',trace);assert.equal(receipt.status,'0x1');assert.equal(trace.failed,false);
  const calls=trace.structLogs.filter(l=>l.depth===1&&l.op==='CALL').map(l=>{const n=i=>BigInt('0x'+l.stack.at(i).replace(/^0x/,'')),offset=Number(n(-4)),length=Number(n(-5));return{to:'0x'+word(n(-2)).slice(-40),input:'0x'+l.memory.map(x=>x.replace(/^0x/,'')).join('').slice(offset*2,(offset+length)*2)};});
  assert.equal(calls.length,initial>0n?2:0);if(initial>0n){assert.deepEqual(calls.map(c=>c.to),[immutable.token0,immutable.token1]);assert.ok(calls.every(c=>c.input==='0xa9059cbb'+word(receiver)+word(paid)));}
  assert.equal(await get(target,3n),(due-paid)+((due-paid)<<128n));assert.equal(await get(target,0n),1n<<240n);for(const token of[immutable.token0,immutable.token1]){assert.equal(await get(token,mapSlot(target,0n)),MAX-paid);assert.equal(await get(token,mapSlot(receiver,0n)),paid);}
  const oldValues=before.map(f=>oldQuantity(f,requested,due));assert.ok(oldValues.every(v=>v===initial));const row={...currentCase,status:'PASS',transaction_hash:tx,expected_final_quantity:paid,old_formula_quantities:oldValues,
   old_formula_counterexample:paid!==initial,actual_calls:calls,trace_sha256:sha(JSON.stringify(trace)),receipt_sha256:sha(JSON.stringify(receipt))};records.push(row);write(label+'-comparison.json',row);assert.equal(await rpc('evm_revert',[snapshot]),true);
 }
 const counterexamples=records.filter(r=>r.old_formula_counterexample);assert.ok(counterexamples.length>=4);write('results.json',{status:'PASS',image,old_image:old.snapshot.engine_digest,cases:records.length,records,counterexamples:counterexamples.length,
  current_result_sha256:sha(currentRaw),old_result_sha256:sha(oldRaw),frozen_runtime_sha256:sha(runtime),genesis:vm.info,verifier_sha256:sha(readFileSync(new URL(import.meta.url))),new_provider_requests:0,
  scope:'Concrete owned instances refute old final-payment formulas that omit amount--; current final quantity remains partial with an exact source-scalar increment frontier. No current formula, full state machine or protocol economics proof is claimed'});
 console.log('P2_AGGREGATE_PRIOR_DECREMENT_COUNTEREXAMPLE_PASS',records.length,counterexamples.length,out.pathname);
}catch(error){write('failure.json',{image,current_case:currentCase,completed_cases:records.length,error:error.message,stack:error.stack});throw error;}finally{if(vm)await vm.close();}
