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

const root=process.cwd(),out=path.join(root,'acceptance/contract-analysis-semantics23',new Date().toISOString().replace(/[:.]/g,''));
const sha=b=>createHash('sha256').update(b).digest('hex'),write=(n,v)=>fs.writeFileSync(path.join(out,n),typeof v==='string'?v:JSON.stringify(v,(_,x)=>typeof x==='bigint'?x.toString():x,2));
fs.mkdirSync(out,{recursive:true});
const source=fs.readFileSync(path.join(root,'analysis-engines/test/fixtures/storage-memory-copy.sol'),'utf8'),require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30');
const settings={evmVersion:'shanghai',optimizer:{enabled:false},outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}};
const compilerInput={language:'Solidity',sources:{'StorageCopyCases.sol':{content:source}},settings},compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));
assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);const contract=compiled.contracts['StorageCopyCases.sol'].StorageCopyCases;
const material={kind:'source',source:{language:'Solidity',files:{'StorageCopyCases.sol':source},compiler_version:solc.version().split('.Emscripten')[0],target:'StorageCopyCases.sol:StorageCopyCases',settings:{evmVersion:'shanghai',optimizer:{enabled:false}}}},raw=JSON.stringify(material);
const model=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:'local-source'},engines:['solc-ast']});
assert.equal(model.rule_version,'funds-semantics-23');write('model.json',model);
const method=name=>{const row=model.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(row,name);return row;};
const fund=name=>{const rows=model.sections.funds.filter(f=>f.method_id===method(name).method_id&&f.kind==='native_payment');assert.equal(rows.length,1,name);return rows[0];};
const state=name=>model.sections.state_transitions.find(t=>t.method_id===method(name).method_id);
for(const name of['implicitStorageArgument','structReturn','wholeStructWrite','storageStructCopy']){
 const transition=state(name);assert.ok(transition,name);assert.equal(transition.complete,true,name+':'+JSON.stringify(transition.unresolved));
 assert.equal(fund(name).amount_derivation_status,'represented_source_dependencies_resolved',name);
}
assert.equal(state('wholeStructWrite').paths[0].writes.length,2);
assert.equal(state('storageStructCopy').paths[0].writes.length,2);

const sender='0x'+'1'.repeat(40),receiver='0x'+'4'.repeat(40);let vm,id=0,address;
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const port=await freePort(),sourcePort=await freePort();
async function rpc(method_,params=[]){const r=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method:method_,params}),signal:AbortSignal.timeout(20000)}),v=await r.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
const word=n=>BigInt(n).toString(16).padStart(64,'0');
async function getter(signature,args=[]){const entry=contract.abi.find(a=>a.type==='function'&&a.name===signature.split('(')[0]);
 const returned=await rpc('eth_call',[{to:address,data:'0x'+contract.evm.methodIdentifiers[signature]+args.map(word).join('')},'latest']);
 const words=(returned.slice(2).match(/.{64}/g)||[]);assert.equal(words.length,entry.outputs.length,signature);
 return words.map((w,i)=>{const type=entry.outputs[i].type,integer=/^(u?int)(\d*)$/.exec(type);
  return integer?BigInt.asUintN(Number(integer[2]||256),BigInt('0x'+w)):type==='address'?'0x'+BigInt('0x'+w).toString(16).padStart(40,'0'):type==='bool'?BigInt('0x'+w)===1n:BigInt('0x'+w);});}
const cases=[
 {name:'implicitStorageArgument',signature:'implicitStorageArgument(address)',vectors:[0n],witness:(x,offset)=>offset===0?7n:offset===16?5n:null,payment:()=>12n,expectedCell:[7n,5n]},
 {name:'structReturn',signature:'structReturn(uint128,address)',vectors:[0n,7n,23n],witness:()=>null,payment:x=>x+2n,expectedCell:[7n,5n]},
 {name:'wholeStructWrite',signature:'wholeStructWrite(uint128,address)',vectors:[0n,9n,31n],witness:()=>null,payment:x=>x+3n,expectedCell:x=>[x,3n]},
 {name:'storageStructCopy',signature:'storageStructCopy(address)',vectors:[0n],witness:(x,offset)=>offset===0?11n:offset===16?3n:null,payment:()=>14n,expectedCell:[11n,3n]},
];
const records=[];
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 const deploymentData='0x'+contract.evm.bytecode.object+word(BigInt(receiver)),hash=await rpc('eth_sendTransaction',[{from:sender,data:deploymentData,gas:'0x989680',gasPrice:'0x77359400'}]);
 await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[hash]);assert.equal(receipt.status,'0x1');address=receipt.contractAddress;
 assert.equal(await rpc('eth_getCode',[address,'latest']),'0x'+contract.evm.deployedBytecode.object);
 for(const spec of cases)for(const x of spec.vectors){
  const snapshot=await rpc('evm_snapshot');await rpc('anvil_setBalance',[address,'0x'+(10n**24n).toString(16)]);
  const before=BigInt(await rpc('eth_getBalance',[receiver,'latest'])),data='0x'+contract.evm.methodIdentifiers[spec.signature]+(spec.signature.endsWith('(address)')?word(BigInt(receiver)):word(x)+word(BigInt(receiver)));
  const tx=await rpc('eth_sendTransaction',[{from:sender,to:address,data,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');
  const txReceipt=await rpc('eth_getTransactionReceipt',[tx]);assert.equal(txReceipt.status,'0x1',spec.name+':'+x);
  const after=BigInt(await rpc('eth_getBalance',[receiver,'latest'])),actual=after-before,expected=spec.payment(x);assert.equal(actual,expected,spec.name+':'+x);
  const modeled=evaluateReturn({sections:{...model.sections,methods:[{...method(spec.name),entry_return_expression_id:fund(spec.name).amount_expression_id}]}},spec.name,{x,to:BigInt(receiver),next:BigInt(receiver),__storageSnapshot:event=>spec.witness(x,event.storage_projection?.offset)})[0];
  assert.equal(modeled,actual,spec.name+':'+x);
  const cell=await getter('cell()'),want=typeof spec.expectedCell==='function'?spec.expectedCell(x):spec.expectedCell;assert.deepEqual(cell,want,spec.name+':'+x);
  records.push({method:spec.name,input:x.toString(),transaction:tx,payment:actual.toString(),modeled:modeled.toString(),cell:cell.map(String),formula:fund(spec.name).expanded_amount_expression});
  assert.equal(await rpc('evm_revert',[snapshot]),true);
 }
 assert.equal(records.length,8);
 write('report.json',{status:'PASS_MEMORY_STRUCT_PLUMBING_ACTUAL_EXECUTION',rule_version:'funds-semantics-23',records,actual_transactions:records.length,provider_requests:0,scope:'One owned fixture. Implicit storage-to-memory arguments, internal struct parameters and returns, struct constructors and whole-struct storage writes are compared with actual balances and storage after execution.'});
 console.log('SEMANTICS23_ACTUAL_PASS',JSON.stringify({directory:out,transactions:records.length}));
}catch(error){write('failure.json',{error:error.message,stack:error.stack,completed:records});throw error;}finally{await vm?.close();}
