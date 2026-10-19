import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {createServer} from 'node:net';
import {once} from 'node:events';

const root=process.cwd();
process.env.CONTRACT_TEST_IMAGE=process.env.CONTRACT_TEST_IMAGE||'sha256:4440baadedb3f57d5994da131f65007c85b691288dff9e6b0715f21921c7e485';
process.env.CONTRACT_PYTHON=process.env.CONTRACT_PYTHON||path.join(root,'scripts/contract-analysis/engine_python_bridge.py');
const {analyze}=await import('../../analysis-engines/analyze.mjs');
const {localGenesisEVM}=await import('./local_genesis_evm.mjs');
const {evaluateReturn}=await import('./expression_model_eval.mjs');

const out=path.join(root,'acceptance/contract-analysis-semantics24',new Date().toISOString().replace(/[:.]/g,''));
const sha=b=>createHash('sha256').update(b).digest('hex'),write=(n,v)=>fs.writeFileSync(path.join(out,n),typeof v==='string'?v:JSON.stringify(v,(_,x)=>typeof x==='bigint'?x.toString():x,2));
fs.mkdirSync(out,{recursive:true});
const source=`pragma solidity ^0.8.30;
contract Loops {
 uint256 public total;uint256 public tail;
 function chain(uint256 n,uint256 seed) external pure returns (uint256 a,uint256 b){ a=seed;b=seed+1;uint256 c=3;unchecked{for(uint256 i;i<n;i++){a+=b;b+=c;}} return (a,b); }
 function settle(uint256 n,uint256 seed) external { uint256 a=seed;uint256 b=seed+1;uint256 c=3;unchecked{for(uint256 i;i<n;i++){a+=b;b+=c;}} total=a;tail=b; }
}`;
const require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30');
const compilerInput={language:'Solidity',sources:{'Loops.sol':{content:source}},settings:{evmVersion:'shanghai',optimizer:{enabled:false},outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers'],'':['ast']}}}};
const compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);
const contract=compiled.contracts['Loops.sol'].Loops,material={kind:'source',source:{language:'Solidity',files:{'Loops.sol':source},compiler_version:solc.version().split('.Emscripten')[0],target:'Loops.sol:Loops',settings:{evmVersion:'shanghai',optimizer:{enabled:false}}}},raw=JSON.stringify(material);
const model=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:'local-source'},engines:['solc-ast']});
assert.equal(model.rule_version,process.env.CONTRACT_EXPECTED_RULE_VERSION||'funds-semantics-24');write('model.json',model);
assert.equal(model.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');
const loops=model.sections.loop_invariants;assert.equal(loops.length,2);assert.ok(loops.every(m=>m.complete),JSON.stringify(loops.map(m=>({unresolved:m.unresolved,obligations:m.obligations}))));
assert.ok(loops.every(m=>m.coupled_certificates.every(c=>c.arithmetic==='unchecked_word_ring')));
const settleModel=model.sections.state_transitions.find(t=>t.method_id===model.sections.methods.find(m=>m.name==='settle').method_id);assert.ok(settleModel);assert.equal(settleModel.complete,true,JSON.stringify(settleModel.unresolved));

const sender='0x'+'1'.repeat(40);let vm,id=0,address;
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const port=await freePort(),sourcePort=await freePort();
async function rpc(method_,params=[]){const r=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method:method_,params}),signal:AbortSignal.timeout(20000)}),v=await r.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
const word=n=>BigInt(n).toString(16).padStart(64,'0'),M=1n<<256n;
function simulate(n,seed){let a=seed%M,b=(seed+1n)%M;for(let i=0n;i<n;i++){a=(a+b)%M;b=(b+3n)%M;}return[a,b];}
const vectors=[[0n,5n],[1n,5n],[5n,7n],[1000n,5n],[1000n,1n<<250n],[17n,M-2n]],records=[];
async function getter(signature){const returned=await rpc('eth_call',[{to:address,data:'0x'+contract.evm.methodIdentifiers[signature]},'latest']);return BigInt(returned);}
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 const hash=await rpc('eth_sendTransaction',[{from:sender,data:'0x'+contract.evm.bytecode.object,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');
 const receipt=await rpc('eth_getTransactionReceipt',[hash]);assert.equal(receipt.status,'0x1');address=receipt.contractAddress;
 for(const [n,seed] of vectors){
  const [expectedA,expectedB]=simulate(n,seed);
  const returned=await rpc('eth_call',[{to:address,data:'0x'+contract.evm.methodIdentifiers['chain(uint256,uint256)']+word(n)+word(seed)},'latest']);
  const words=returned.slice(2).match(/.{64}/g).map(w=>BigInt('0x'+w));assert.deepEqual(words,[expectedA,expectedB],'chain:'+n+':'+seed);
  const modeled=evaluateReturn(model,'chain',{n,seed});assert.deepEqual(modeled,[expectedA,expectedB],'model:'+n+':'+seed);
  const settlement=await rpc('eth_sendTransaction',[{from:sender,to:address,data:'0x'+contract.evm.methodIdentifiers['settle(uint256,uint256)']+word(n)+word(seed),gas:'0x989680',gasPrice:'0x77359400'}]);
  await rpc('evm_mine');assert.equal((await rpc('eth_getTransactionReceipt',[settlement])).status,'0x1');
  const total=await getter('total()'),tail=await getter('tail()');assert.deepEqual([total,tail],[expectedA,expectedB],'settle:'+n+':'+seed);
  records.push({n:n.toString(),seed:seed.toString(),chain:[expectedA.toString(),expectedB.toString()],modeled:modeled.map(String),settled:[total.toString(),tail.toString()],transaction:settlement});
 }
 write('report.json',{status:'PASS_UNCHECKED_COUPLED_LOOP_ACTUAL_EXECUTION',rule_version:process.env.CONTRACT_EXPECTED_RULE_VERSION||'funds-semantics-24',records,actual_transactions:vectors.length,provider_requests:0,loop_models:loops.map(m=>({complete:m.complete,arithmetic:m.coupled_certificates.map(c=>c.arithmetic),obligations:m.obligations.length})),scope:'One owned implementation. Word-ring polynomial summaries proved by SMT and compared with actual EVM return values and storage after execution, including overflow-wrapping inputs.'});
 console.log('SEMANTICS24_ACTUAL_PASS',JSON.stringify({directory:out,vectors:vectors.length}));
}catch(error){write('failure.json',{error:error.message,stack:error.stack,records});throw error;}finally{await vm?.close();}
