import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {createServer} from 'node:net';
import {once} from 'node:events';
import {analyze} from '../../analysis-engines/analyze.mjs';
import {localGenesisEVM} from './local_genesis_evm.mjs';

const root=process.cwd(),base=path.join(root,'acceptance/contract-analysis-semantics22'),out=path.join(base,new Date().toISOString().replace(/[:.]/g,''));fs.mkdirSync(out,{recursive:true});
const sha=b=>createHash('sha256').update(b).digest('hex'),write=(n,v)=>fs.writeFileSync(path.join(out,n),typeof v==='string'?v:JSON.stringify(v,(_,x)=>typeof x==='bigint'?x.toString():x,2));
const source=`pragma solidity ^0.8.30;
interface IQuote { function quote(uint256 x) external view returns (uint256); }
contract Oracle is IQuote { function quote(uint256 x) external pure returns (uint256) { return x * 2 + 1; } }
contract Consumer {
 IQuote public oracle;uint256 public total;uint256 public reads;
 constructor(IQuote o){oracle=o;}
 function settle(uint256 x) external { total = oracle.quote(x); reads += 1; }
 function peek(uint256 x) external view returns (uint256) { return oracle.quote(x); }
}`;
const require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30');
const compilerInput={language:'Solidity',sources:{'Runtime22.sol':{content:source}},settings:{evmVersion:'shanghai',optimizer:{enabled:false},outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers'],'':['ast']}}}};
const compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);
const oracleContract=compiled.contracts['Runtime22.sol'].Oracle,consumer=compiled.contracts['Runtime22.sol'].Consumer;
const material={kind:'source',source:{language:'Solidity',files:{'Runtime22.sol':source},compiler_version:solc.version().split('.Emscripten')[0],target:'Runtime22.sol:Consumer',settings:{evmVersion:'shanghai',optimizer:{enabled:false}}}},raw=JSON.stringify(material);
const model=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:'local-source'},engines:['solc-ast']});
assert.equal(model.rule_version,'funds-semantics-22');write('model.json',model);
const method=model.sections.methods.find(m=>m.name==='settle'&&m.dispatch_role==='external_dispatch');assert.ok(method);
const state=model.sections.state_transitions.find(t=>t.method_id===method.method_id);assert.ok(state);
assert.equal(state.complete,true,JSON.stringify(state.unresolved));
assert.equal(state.paths[0].writes.length,2);
const effect=state.effects.find(e=>e.kind==='static_call');assert.ok(effect);
assert.equal(effect.call_type,'STATICCALL');assert.equal(effect.signature,'quote(uint256)');assert.equal(effect.return_binding,'requires_target_identity_and_execution');
assert.ok(state.paths[0].writes.some(w=>w.new_value.startsWith('static_return_')),JSON.stringify(state.paths[0].writes));

const sender='0x'+'1'.repeat(40);let vm,id=0,oracleAddress,consumerAddress;
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const port=await freePort(),sourcePort=await freePort();
async function rpc(method_,params=[]){const r=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method:method_,params}),signal:AbortSignal.timeout(20000)}),v=await r.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
const word=n=>BigInt(n).toString(16).padStart(64,'0');
async function deploy(code,args=''){const hash=await rpc('eth_sendTransaction',[{from:sender,data:'0x'+code+args,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[hash]);assert.equal(receipt.status,'0x1');return receipt.contractAddress;}
async function call(to,data){const hash=await rpc('eth_sendTransaction',[{from:sender,to,data,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');return{hash,receipt:await rpc('eth_getTransactionReceipt',[hash])};}
async function getter(to,signature,type='uint256'){const returned=await rpc('eth_call',[{to,data:'0x'+consumer.evm.methodIdentifiers[signature]},'latest']);const value=BigInt(returned);return type==='address'?'0x'+value.toString(16).padStart(40,'0'):value;}
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 oracleAddress=await deploy(oracleContract.evm.bytecode.object);assert.equal(await rpc('eth_getCode',[oracleAddress,'latest']),'0x'+oracleContract.evm.deployedBytecode.object);
 consumerAddress=await deploy(consumer.evm.bytecode.object,word(BigInt(oracleAddress)));assert.equal(await rpc('eth_getCode',[consumerAddress,'latest']),'0x'+consumer.evm.deployedBytecode.object);
 assert.equal(await getter(consumerAddress,'oracle()','address'),oracleAddress.toLowerCase());
 const x=5n,settle=await call(consumerAddress,'0x'+consumer.evm.methodIdentifiers['settle(uint256)']+word(x));
 assert.equal(settle.receipt.status,'0x1');assert.equal(await getter(consumerAddress,'total()'),11n);assert.equal(await getter(consumerAddress,'reads()'),1n);
 const trace=await rpc('debug_traceTransaction',[settle.hash,{disableMemory:true,disableStorage:true}]),ops=(trace.structLogs||[]).map(log=>log.op);
 assert.ok(ops.includes('STATICCALL'),'STATICCALL opcode missing from the traced transaction');
 const peek=await rpc('eth_call',[{to:consumerAddress,data:'0x'+consumer.evm.methodIdentifiers['peek(uint256)']+word(x)},'latest']);assert.equal(BigInt(peek),11n);
 write('report.json',{status:'PASS_STATICCALL_MODEL_AND_ACTUAL_EXECUTION',rule_version:'funds-semantics-22',model:{state_complete:state.complete,write_new_values:state.paths[0].writes.map(w=>w.new_value),static_call:effect},
  execution:{oracle:oracleAddress,consumer:consumerAddress,transaction:settle.hash,total:(await getter(consumerAddress,'total()')).toString(),reads:(await getter(consumerAddress,'reads()')).toString(),peak:peek,staticscall_opcode:true,other_opcodes:['CALL','STATICCALL'].filter(op=>ops.includes(op))},
  trace_sha256:sha(JSON.stringify(trace)),provider_requests:0,scope:'One owned two-contract implementation. The model claims a read-only STATICCALL with execution-bound returns; the traced transaction proves the opcode and the executed write value while the model value stays symbolic.'});
 console.log('SEMANTICS22_ACTUAL_PASS',JSON.stringify({directory:out,total:'11',staticcall:true}));
}catch(error){write('failure.json',{error:error.message,stack:error.stack});throw error;}finally{await vm?.close();}
