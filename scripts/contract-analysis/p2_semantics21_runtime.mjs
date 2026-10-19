import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {createServer} from 'node:net';
import {once} from 'node:events';
import {analyze} from '../../analysis-engines/analyze.mjs';
import {localGenesisEVM} from './local_genesis_evm.mjs';
import {evaluateReturn} from './expression_model_eval.mjs';

const root=process.cwd(),base=path.join(root,'acceptance/contract-analysis-semantics21'),stamp=new Date().toISOString().replace(/[:.]/g,''),out=path.join(base,stamp);fs.mkdirSync(out,{recursive:true});
const sha=b=>createHash('sha256').update(b).digest('hex'),write=(n,v)=>fs.writeFileSync(path.join(out,n),Buffer.isBuffer(v)||typeof v==='string'?v:JSON.stringify(v,(_,x)=>typeof x==='bigint'?x.toString():x,2));
const source=fs.readFileSync(path.join(root,'analysis-engines/test/fixtures/storage-memory-copy.sol'),'utf8');
const require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30');
const material={kind:'source',source:{language:'Solidity',files:{'StorageCopyCases.sol':source},compiler_version:solc.version().split('.Emscripten')[0],target:'StorageCopyCases.sol:StorageCopyCases',settings:{evmVersion:'shanghai',optimizer:{enabled:false},outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}}}};
const raw=JSON.stringify({kind:'source',source:material.source}),model=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:'local-source'},engines:['solc-ast']});
assert.equal(model.rule_version,'funds-semantics-21');assert.equal(model.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');write('model.json',model);
const compilerInput={language:'Solidity',sources:{'StorageCopyCases.sol':{content:source}},settings:{evmVersion:'shanghai',optimizer:{enabled:false},outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}}};
const compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);
const contract=compiled.contracts['StorageCopyCases.sol'].StorageCopyCases;
const method=name=>{const row=model.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(row,name);return row;};
const fund=name=>{const m=method(name),rows=model.sections.funds.filter(f=>f.method_id===m.method_id&&f.kind==='native_payment');assert.equal(rows.length,1,name);return rows[0];};
const state=name=>{const m=method(name),row=model.sections.state_transitions.find(t=>t.method_id===m.method_id);assert.ok(row,name);return row;};
const bound=name=>model.sections.expressions.filter(e=>e.method_id===method(name).method_id&&e.storage_value_bound===true);
const snapshots=name=>model.sections.expressions.filter(e=>e.method_id===method(name).method_id&&e.symbolic_format==='source_storage_snapshot');

assert.equal(fund('storeThenCopy').amount_derivation_status,'represented_source_dependencies_resolved');
assert.match(fund('storeThenCopy').expanded_amount_expression,/(^|[^A-Za-z_])x([^A-Za-z_]|$)/);
assert.equal(bound('storeThenCopy').length,1);assert.equal(bound('storeThenCopy')[0].storage_value_basis,'dominating_scalar_storage_write_before_copy');
assert.equal(snapshots('storeThenCopy').length,1);
assert.equal(fund('compoundThenCopy').amount_derivation_status,'represented_source_dependencies_resolved');
assert.equal(bound('compoundThenCopy').length,0);assert.equal(snapshots('compoundThenCopy').length,2);
assert.equal(fund('enumCopy').amount_derivation_status,'represented_source_dependencies_resolved');
assert.equal(fund('dynamicFieldCopy').amount_derivation_status,'partial');
for(const name of['storeThenCopy','compoundThenCopy','enumCopy']){assert.equal(state(name).complete,true,name);assert.deepEqual(state(name).unresolved,[],name);}
assert.equal(state('dynamicFieldCopy').complete,false);assert.ok(state('dynamicFieldCopy').unresolved.some(v=>v.startsWith('STATE_MEMORY_STRUCT_ARRAY_FIELD_UNSUPPORTED')));

const sender='0x'+'1'.repeat(40),receiver='0x'+'4'.repeat(40);let vm,id=0,address;
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const port=await freePort(),sourcePort=await freePort();
async function rpc(method_,params=[]){const r=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method:method_,params}),signal:AbortSignal.timeout(20000)}),v=await r.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
const word=n=>BigInt(n).toString(16).padStart(64,'0');
const cases={storeThenCopy:{signature:'storeThenCopy(uint128,address)',vectors:[0n,1n,23n],witness:(x,offset)=>offset===16?5n:null,payment:x=>x+5n},
  compoundThenCopy:{signature:'compoundThenCopy(uint128,address)',vectors:[0n,1n,23n],witness:(x,offset)=>offset===0?7n:offset===16?5n+x:null,payment:x=>7n+5n+x},
  enumCopy:{signature:'enumCopy(address)',vectors:[0n],witness:(x,offset)=>offset===0?19n:null,payment:()=>19n}};
const records=[];
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 const deploymentData='0x'+contract.evm.bytecode.object+word(BigInt(receiver)),hash=await rpc('eth_sendTransaction',[{from:sender,data:deploymentData,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');
 const receipt=await rpc('eth_getTransactionReceipt',[hash]);assert.equal(receipt.status,'0x1');address=receipt.contractAddress;
 assert.equal(await rpc('eth_getCode',[address,'latest']),'0x'+contract.evm.deployedBytecode.object);
 write('deployment.json',{receipt,initcode_sha256:sha(Buffer.from(deploymentData.slice(2),'hex'))});
 for(const[name,spec]of Object.entries(cases))for(const x of spec.vectors){
  const snapshot=await rpc('evm_snapshot');await rpc('anvil_setBalance',[address,'0x'+(10n**24n).toString(16)]);
  const before=BigInt(await rpc('eth_getBalance',[receiver,'latest'])),data='0x'+contract.evm.methodIdentifiers[spec.signature]+(spec.signature.endsWith('(address)')?word(BigInt(receiver)):word(x)+word(BigInt(receiver))),
   tx=await rpc('eth_sendTransaction',[{from:sender,to:address,data,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');
  const txReceipt=await rpc('eth_getTransactionReceipt',[tx]);assert.equal(txReceipt.status,'0x1',name+':'+x);
  const after=BigInt(await rpc('eth_getBalance',[receiver,'latest'])),actual=after-before;
  const witness=(event)=>spec.witness(x,event.storage_projection?.offset);
  const modeled=evaluateReturn({sections:{...model.sections,methods:[{...method(name),entry_return_expression_id:fund(name).amount_expression_id}]}},name,{x,to:BigInt(receiver),next:BigInt(receiver),__storageSnapshot:witness})[0];
  assert.equal(actual,spec.payment(x),name+':'+x);assert.equal(modeled,actual,name+':'+x);
  records.push({method:name,input:x.toString(),transaction:tx,payment:actual.toString(),modeled:modeled.toString(),formula:fund(name).expanded_amount_expression,storage_value_bound:bound(name).length,transaction_sha256:sha(JSON.stringify(txReceipt))});
  assert.equal(await rpc('evm_revert',[snapshot]),true);
 }
 assert.equal(records.length,7);
 write('report.json',{status:'PASS_STRUCT_COPY_AND_NATIVE_HOST_EFFECT_ACTUAL_EXECUTION',rule_version:'funds-semantics-21',implementation_count:1,records,actual_transactions:records.length,provider_requests:0,preceding_write_folded:true,compound_write_kept_at_copy_point:true,enum_copy_supported:true,dynamic_struct_field_explicit_gap:true,memory_struct_state_complete:true,scope:'One owned multi-method implementation. In-process rule 21 model plus anvil execution of the same compiled source; balances and formulas compared numerically. No historical state or whole-protocol proof is claimed.'});
 console.log('SEMANTICS21_ACTUAL_PASS',JSON.stringify({directory:out,transactions:records.length}));
}catch(error){write('failure.json',{error:error.message,stack:error.stack,completed:records});throw error;}finally{await vm?.close();}
