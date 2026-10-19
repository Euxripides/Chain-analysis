import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {createServer} from 'node:net';
import {once} from 'node:events';
import {gzipSync} from 'node:zlib';
import {spawnSync} from 'node:child_process';
import {evaluateReturn} from './expression_model_eval.mjs';
import {localGenesisEVM} from './local_genesis_evm.mjs';


const runtimeImage=process.env.CONTRACT_TEST_IMAGE||fs.readFileSync(path.join(process.cwd(),'acceptance/contract-analysis-heap-writes/engine-image.txt'),'utf8').trim();

async function runLegacy(){
const root=process.cwd(),base=path.join(root,'acceptance/contract-analysis-heap-writes'),image=runtimeImage,out=path.join(base,'source-byte-consumer-legacy',image.slice(7,23),new Date().toISOString().replace(/[:.]/g,''));fs.mkdirSync(out,{recursive:true});const sha=b=>createHash('sha256').update(b).digest('hex'),write=(name,v)=>fs.writeFileSync(path.join(out,name),Buffer.isBuffer(v)||typeof v==='string'?v:JSON.stringify(v,null,2));
const source='pragma solidity ^0.4.26;contract TupleNegative{function _select(bytes memory a,bytes memory b,bool flag)internal pure returns(bytes memory){if(flag)return a;return b;}function invertedArgument(address to,bool flag)external{bytes memory a=hex"01";bytes memory b=hex"02";bytes memory c;if(flag){c=_select(a,b,!flag);}else{c=_select(a,b,!flag);}c[0]=0x07;bool x=to.call(a);require(x);bool y=to.call(b);require(y);}function constantArgument(address to,bool flag)external{bytes memory a=hex"01";bytes memory b=hex"02";bytes memory c;if(flag){c=_select(a,b,false);}else{c=_select(a,b,true);}c[0]=0x07;bool x=to.call(a);require(x);bool y=to.call(b);require(y);}function twoArguments(address to,bool flag)external{bytes memory a=hex"01";bytes memory b=hex"02";bytes memory c=_select(a,b,flag);c[0]=0x07;bool x=to.call(a,b);require(x);}}';
function analyze(input){const run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:180000,maxBuffer:64<<20});assert.equal(run.status,0,run.stderr);return JSON.parse(run.stdout);}
const material={kind:'source',source:{language:'Solidity',files:{'TupleNegative.sol':source},compiler_version:'0.4.26+commit.4563c3fc',target:'TupleNegative.sol:TupleNegative',settings:{}}},raw=JSON.stringify(material),input={submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:image},engines:['solc-ast']},r=analyze(input),require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.4.26'),compilerInput={language:'Solidity',sources:{'TupleNegative.sol':{content:source}},settings:{outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers']}}}},compiled=JSON.parse((solc.compileStandardWrapper||solc.compile).call(solc,JSON.stringify(compilerInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);const contract=compiled.contracts['TupleNegative.sol'].TupleNegative;write('input.json',input);write('output.json.gz',gzipSync(Buffer.from(JSON.stringify(r))));write('compiler-input.json',compilerInput);write('compiler-output.json',compiled);assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const port=s.address().port;s.close();await once(s,'close');return port;}
const port=await freePort(),sourcePort=await freePort(),sender='0x'+'1'.repeat(40),to='0x'+'2'.repeat(40),sourceEvidence=new Set(r.sections.evidence.filter(e=>e.kind==='source').map(e=>e.evidence_id));let vm,id=0;const records=[];
async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method,params}),signal:AbortSignal.timeout(20000)}),v=await response.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
function predicateValue(p,flag,method){
 if(!p)return true;if(p.op==='constant')return p.value;if(p.op==='and')return p.terms.every(t=>predicateValue(t,flag,method));if(p.op==='or')return p.terms.some(t=>predicateValue(t,flag,method));if(p.op==='not')return!predicateValue(p.value,flag,method);
 assert.equal(p.op,'atom','Real source predicates must identify a saved expression, not an opaque branch choice');assert.ok(r.sections.expressions.some(e=>e.expression_id===p.expression_id&&e.method_id===method.method_id));const old=method.entry_return_expression_id;let value;
 try{method.entry_return_expression_id=p.expression_id;value=evaluateReturn(r,method.name,{flag,to:BigInt(to)})[0];}finally{method.entry_return_expression_id=old;}
 return(typeof value==='boolean'?value:value!==0n)===p.expected;
}
function conditionValue(text,flag){text=text.trim();if(text==='flag')return flag;if(text.startsWith('!(')&&text.endsWith(')'))return!conditionValue(text.slice(2,-1),flag);if(/^[!()&|\sflag]+$/.test(text))return Function('flag','return ('+text+');')(flag);throw Error('UNSUPPORTED_ALIAS_CONDITION:'+text);}
const top=(step,n)=>BigInt('0x'+step.stack.at(-1-n).replace(/^0x/,''));
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);const deploymentHash=await rpc('eth_sendTransaction',[{from:sender,data:'0x'+contract.evm.bytecode.object,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const deployment=await rpc('eth_getTransactionReceipt',[deploymentHash]);assert.equal(deployment.status,'0x1');const target=deployment.contractAddress,code=await rpc('eth_getCode',[target,'latest']);assert.equal(code,'0x'+contract.evm.deployedBytecode.object);assert.equal(code,r.sections.artifacts.find(a=>a.kind==='bytecode').text);write('deployment.json',{receipt:deployment,compiler:solc.version(),runtime_sha256:sha(Buffer.from(code.slice(2),'hex'))});await rpc('anvil_setBalance',[target,'0x'+(10n**24n).toString(16)]);
 for(const {abi,flag} of contract.abi.filter(a=>a.type==='function').flatMap(abi=>[false,true].map(flag=>({abi,flag})))){
  const name=abi.name,method=r.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch'),funds=r.sections.funds.filter(f=>f.method_id===method.method_id&&f.kind==='native_payment'&&f.evidence_ids.some(id=>sourceEvidence.has(id))),quotes=funds.map(f=>{let quote=null,error=null;if(f.amount_derivation_status==='represented_source_dependencies_resolved'){const old=method.entry_return_expression_id;try{method.entry_return_expression_id=f.amount_expression_id;quote=String(evaluateReturn(r,name,{to:BigInt(to)})[0]);}catch(e){error=e.message;}finally{method.entry_return_expression_id=old;}}return{fund_id:f.fund_id,status:f.amount_derivation_status,expression:f.expanded_amount_expression,quote,quote_error:error};}),bindings=r.sections.call_input_bindings.filter(b=>b.method_id===method.method_id&&b.representation_scope==='source_object');
  const data='0x'+contract.evm.methodIdentifiers[name+'(address,bool)']+to.slice(2).padStart(64,'0')+(flag?'1':'0').padStart(64,'0'),before=await Promise.all([rpc('eth_getBalance',[target,'latest']),rpc('eth_getBalance',[to,'latest'])]),tx=await rpc('eth_sendTransaction',[{from:sender,to:target,data,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]),trace=await rpc('debug_traceTransaction',[tx,{enableMemory:true}]);write(name+'-'+flag+'-receipt.json',receipt);const traceRaw=Buffer.from(JSON.stringify(trace));write(name+'-'+flag+'-trace.json.gz',gzipSync(traceRaw));assert.equal(receipt.status,'0x1');const calls=trace.structLogs.filter(s=>s.op==='CALL'&&s.depth===1).map(s=>{const start=Number(top(s,3)),length=Number(top(s,4)),memory=Buffer.from((s.memory||[]).map(s=>s.replace(/^0x/,'')).join(''),'hex');assert.ok(length===0||memory.length>=start+length,"Actual nonempty CALL input memory must be available");return{value:String(top(s,2)),to:'0x'+top(s,1).toString(16).padStart(40,'0'),input_hex:'0x'+memory.subarray(start,start+length).toString('hex')};});assert.ok(calls.length);assert.ok(calls.every(c=>c.to===to));const after=await Promise.all([rpc('eth_getBalance',[target,'latest']),rpc('eth_getBalance',[to,'latest'])]),amount=calls.reduce((sum,c)=>sum+BigInt(c.value),0n);assert.equal(BigInt(before[0])-BigInt(after[0]),amount);assert.equal(BigInt(after[1])-BigInt(before[1]),amount);
  if(!bindings.length)assert.equal(funds.length,calls.length);else assert.equal(bindings.length,calls.length);
  const amountChecks=bindings.length?[]:quotes.map((q,index)=>({...q,observed_amount:calls[index].value,wrong_resolved_amount:q.quote!==null&&q.quote!==calls[index].value})),inputChecks=bindings.map((b,index)=>{let modeled=null;const selected=b.cases.filter(c=>c.predicate?predicateValue(c.predicate,flag,method):b.cases.length===1&&!c.conditions.length);if(b.status!=='partial'&&selected.length===1&&selected[0].bytes.every(c=>c.kind==='constant'))modeled='0x'+Buffer.from(selected[0].bytes.map(c=>c.value)).toString('hex');return{call_input_binding_id:b.call_input_binding_id,status:b.status,unresolved:b.unresolved,modeled_input_hex:modeled,observed_input_hex:calls[index].input_hex,wrong_resolved_input:modeled!==null&&modeled!==calls[index].input_hex};});
  records.push({name,flag,transaction_hash:tx,calls,before,after,amount_checks:amountChecks,input_checks:inputChecks,trace_sha256:sha(traceRaw)});write('partial-results.json',records);
 }
}finally{await vm?.close();}
const wrongAmounts=records.flatMap(r=>r.amount_checks).filter(q=>q.wrong_resolved_amount),wrongInputs=records.flatMap(r=>r.input_checks).filter(q=>q.wrong_resolved_input),precise=records.length===6&&records.every(r=>r.input_checks.length===r.calls.length&&r.input_checks.every(c=>c.modeled_input_hex!==null&&c.modeled_input_hex===c.observed_input_hex)),report={status:wrongAmounts.length||wrongInputs.length?'FAIL_CONFIRMED_CALL_VALUES':precise?'PASS_PRECISE_BYTE_ALIAS_RUNTIME':'FAIL_INCOMPLETE_PRECISE_BYTES',image,actual_transactions:records.length,wrong_resolved_amounts:wrongAmounts.length,wrong_resolved_inputs:wrongInputs.length,records,scope:'Owned compiled runtime identity and six actual alias/branch transactions with exact saved predicate evaluation and CALL memory/balance checks; no blind or whole-model acceptance'};write('results.json',report);console.log('TUPLE_LVALUE_OBSERVATION',JSON.stringify({status:report.status,image,actual_transactions:records.length,wrong_resolved_amounts:wrongAmounts.length,wrong_resolved_inputs:wrongInputs.length}),out);assert.equal(wrongAmounts.length+wrongInputs.length,0,'Tuple lvalue source model contradicts actual CALL');

assert.equal(records.length,6);assert.ok(records.every(r=>r.input_checks.length===r.calls.length&&r.input_checks.every(c=>c.modeled_input_hex!==null&&c.modeled_input_hex===c.observed_input_hex)),'Every condition-selected modeled input must match actual CALL bytes');console.log('LEGACY_SOURCE_BYTE_PREDICATE_PRECISE_PASS',records.length);

assert.equal(report.status,'PASS_PRECISE_BYTE_ALIAS_RUNTIME');

return {name:"legacy",image,out,actual_transactions:records.length};
}

async function runEffects(){
const root=process.cwd(),base=path.join(root,'acceptance/contract-analysis-heap-writes'),image=runtimeImage,out=path.join(base,'source-byte-consumer-effects',image.slice(7,23),new Date().toISOString().replace(/[:.]/g,''));fs.mkdirSync(out,{recursive:true});const sha=b=>createHash('sha256').update(b).digest('hex'),write=(name,v)=>fs.writeFileSync(path.join(out,name),Buffer.isBuffer(v)||typeof v==='string'?v:JSON.stringify(v,null,2));
const source='pragma solidity ^0.8.30;contract TupleNegative{function _exists(address target)internal view returns(bool){uint256 size;assembly{size:=extcodesize(target)}return size>0;}function _smash(bytes memory a)internal pure returns(bool){assembly{mstore(add(a,32),7)}return true;}function pureHostCheck(address payable to)external{bytes memory a=hex"0102";require(_exists(address(this)),"code required");(bool ok,)=to.call(a);require(ok);}function opaqueHostWrite(address payable to)external{bytes memory a=hex"0102";require(_smash(a),"write done");(bool ok,)=to.call(a);require(ok);}function _allowed()internal pure virtual returns(bool){return true;}function virtualPureCheck(address payable to)external{bytes memory a=hex"0102";require(_allowed(),"allowed");(bool ok,)=to.call(a);require(ok);}}';
function analyze(input){const run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:180000,maxBuffer:64<<20});assert.equal(run.status,0,run.stderr);return JSON.parse(run.stdout);}
const material={kind:'source',source:{language:'Solidity',files:{'TupleNegative.sol':source},compiler_version:'0.8.30+commit.73712a01',target:'TupleNegative.sol:TupleNegative',settings:{evmVersion:'shanghai'}}},raw=JSON.stringify(material),input={submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:image},engines:['solc-ast']},r=analyze(input),require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30'),compilerInput={language:'Solidity',sources:{'TupleNegative.sol':{content:source}},settings:{evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers']}}}},compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);const contract=compiled.contracts['TupleNegative.sol'].TupleNegative;write('input.json',input);write('output.json.gz',gzipSync(Buffer.from(JSON.stringify(r))));write('compiler-input.json',compilerInput);write('compiler-output.json',compiled);assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const port=s.address().port;s.close();await once(s,'close');return port;}
const port=await freePort(),sourcePort=await freePort(),sender='0x'+'1'.repeat(40),to='0x'+'2'.repeat(40),sourceEvidence=new Set(r.sections.evidence.filter(e=>e.kind==='source').map(e=>e.evidence_id));let vm,id=0;const records=[];
async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method,params}),signal:AbortSignal.timeout(20000)}),v=await response.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
const top=(step,n)=>BigInt('0x'+step.stack.at(-1-n).replace(/^0x/,''));
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);const deploymentHash=await rpc('eth_sendTransaction',[{from:sender,data:'0x'+contract.evm.bytecode.object,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const deployment=await rpc('eth_getTransactionReceipt',[deploymentHash]);assert.equal(deployment.status,'0x1');const target=deployment.contractAddress,code=await rpc('eth_getCode',[target,'latest']);assert.equal(code,'0x'+contract.evm.deployedBytecode.object);assert.equal(code,r.sections.artifacts.find(a=>a.kind==='bytecode').text);write('deployment.json',{receipt:deployment,compiler:solc.version(),runtime_sha256:sha(Buffer.from(code.slice(2),'hex'))});await rpc('anvil_setBalance',[target,'0x'+(10n**24n).toString(16)]);
 for(const abi of contract.abi.filter(a=>a.type==='function')){
  const name=abi.name,method=r.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch'),funds=r.sections.funds.filter(f=>f.method_id===method.method_id&&f.kind==='native_payment'&&f.evidence_ids.some(id=>sourceEvidence.has(id))),quotes=funds.map(f=>{let quote=null,error=null;if(f.amount_derivation_status==='represented_source_dependencies_resolved'){const old=method.entry_return_expression_id;try{method.entry_return_expression_id=f.amount_expression_id;quote=String(evaluateReturn(r,name,{to:BigInt(to)})[0]);}catch(e){error=e.message;}finally{method.entry_return_expression_id=old;}}return{fund_id:f.fund_id,status:f.amount_derivation_status,expression:f.expanded_amount_expression,quote,quote_error:error};}),bindings=r.sections.call_input_bindings.filter(b=>b.method_id===method.method_id&&b.representation_scope==='source_object');
  const data='0x'+contract.evm.methodIdentifiers[name+'(address)']+to.slice(2).padStart(64,'0'),before=await Promise.all([rpc('eth_getBalance',[target,'latest']),rpc('eth_getBalance',[to,'latest'])]),tx=await rpc('eth_sendTransaction',[{from:sender,to:target,data,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]),trace=await rpc('debug_traceTransaction',[tx,{enableMemory:true}]);write(name+'-receipt.json',receipt);const traceRaw=Buffer.from(JSON.stringify(trace));write(name+'-trace.json.gz',gzipSync(traceRaw));assert.equal(receipt.status,'0x1');const calls=trace.structLogs.filter(s=>s.op==='CALL'&&s.depth===1).map(s=>{const start=Number(top(s,3)),length=Number(top(s,4)),memory=Buffer.from((s.memory||[]).map(s=>s.replace(/^0x/,'')).join(''),'hex');assert.ok(length===0||memory.length>=start+length,"Actual nonempty CALL input memory must be available");return{value:String(top(s,2)),to:'0x'+top(s,1).toString(16).padStart(40,'0'),input_hex:'0x'+memory.subarray(start,start+length).toString('hex')};});assert.ok(calls.length);assert.ok(calls.every(c=>c.to===to));const after=await Promise.all([rpc('eth_getBalance',[target,'latest']),rpc('eth_getBalance',[to,'latest'])]),amount=calls.reduce((sum,c)=>sum+BigInt(c.value),0n);assert.equal(BigInt(before[0])-BigInt(after[0]),amount);assert.equal(BigInt(after[1])-BigInt(before[1]),amount);
  if(!bindings.length)assert.equal(funds.length,calls.length);else assert.equal(bindings.length,calls.length);
  const amountChecks=bindings.length?[]:quotes.map((q,index)=>({...q,observed_amount:calls[index].value,wrong_resolved_amount:q.quote!==null&&q.quote!==calls[index].value})),inputChecks=bindings.map((b,index)=>{let modeled=null;if(b.status!=='partial'&&b.cases.length===1&&b.cases[0].bytes.every(c=>c.kind==='constant'))modeled='0x'+Buffer.from(b.cases[0].bytes.map(c=>c.value)).toString('hex');return{call_input_binding_id:b.call_input_binding_id,status:b.status,unresolved:b.unresolved,modeled_input_hex:modeled,observed_input_hex:calls[index].input_hex,wrong_resolved_input:modeled!==null&&modeled!==calls[index].input_hex};});
  records.push({name,transaction_hash:tx,calls,before,after,amount_checks:amountChecks,input_checks:inputChecks,trace_sha256:sha(traceRaw)});write('partial-results.json',records);
 }
}finally{await vm?.close();}
const wrongAmounts=records.flatMap(r=>r.amount_checks).filter(q=>q.wrong_resolved_amount),wrongInputs=records.flatMap(r=>r.input_checks).filter(q=>q.wrong_resolved_input),report={status:wrongAmounts.length||wrongInputs.length?'FAIL_CONFIRMED_TUPLE_LVALUE_VALUES':'PASS_NO_OBSERVED_WRONG_RESOLVED_VALUES',image,actual_transactions:records.length,wrong_resolved_amounts:wrongAmounts.length,wrong_resolved_inputs:wrongInputs.length,records,scope:'Owned compiled runtime identity, actual transactions/CALL memory/value/recipient/balances versus source graph; fixed negative cases, no general support proof'};write('results.json',report);console.log('TUPLE_LVALUE_OBSERVATION',JSON.stringify({status:report.status,image,actual_transactions:records.length,wrong_resolved_amounts:wrongAmounts.length,wrong_resolved_inputs:wrongInputs.length}),out);assert.equal(wrongAmounts.length+wrongInputs.length,0,'Tuple lvalue source model contradicts actual CALL');

assert.equal(records.length,3);const pure=records.find(r=>r.name==='pureHostCheck'),opaque=records.find(r=>r.name==='opaqueHostWrite');assert.ok(pure.input_checks.length===1&&pure.input_checks.every(c=>c.status==='exact'&&c.modeled_input_hex==='0x0102'&&c.modeled_input_hex===c.observed_input_hex));assert.ok(opaque.input_checks.length===1&&opaque.input_checks.every(c=>c.status==='partial'&&c.unresolved.length>0));console.log('PRIVATE_STACK_ONLY_ASM_AND_OPAQUE_WRITE_ACTUAL_PASS',records.length);

const virtual=records.find(r=>r.name==='virtualPureCheck');assert.ok(virtual.input_checks.length===1&&virtual.input_checks.every(c=>c.status==='exact'&&c.modeled_input_hex===c.observed_input_hex),'A pure virtual body with no assembly must retain the already captured bytes');console.log('PURE_VIRTUAL_BYTE_EFFECT_ACTUAL_PASS');

return {name:"effects",image,out,actual_transactions:records.length};
}

async function runAbi(){
const root=process.cwd(), image=runtimeImage;
const out=path.join(root,'acceptance/contract-analysis-heap-writes/source-byte-consumer-abi',image.slice(7,23),new Date().toISOString().replace(/[:.]/g,''));
fs.mkdirSync(out,{recursive:true});
const sha=bytes=>createHash('sha256').update(bytes).digest('hex');
const write=(name,value)=>fs.writeFileSync(path.join(out,name),Buffer.isBuffer(value)||typeof value==='string'?value:JSON.stringify(value,null,2));
const source=`pragma solidity ^0.8.30;
contract ConditionalABI {
 function decodeBranch(bool flag,address target) external pure returns(uint256,bool) {
  bytes memory data;
  if(flag)data=abi.encode(uint256(7),true);else data=abi.encode(uint256(9),false);
  return abi.decode(data,(uint256,bool));
 }
 function decodeTernary(bool flag,address target) external pure returns(uint256,bool) {
  bytes memory data=flag?abi.encode(uint256(17),false):abi.encode(uint256(19),true);
  return abi.decode(data,(uint256,bool));
 }
 function callBranch(bool flag,address target) external {
  bytes memory data;
  if(flag)data=abi.encodeWithSelector(bytes4(0x12345678),uint256(27),true);
  else data=abi.encodeWithSelector(bytes4(0x12345678),uint256(29),false);
  (bool ok,)=target.call(data);require(ok);
 }
 function callTernary(bool flag,address target) external {
  bytes memory data=flag?abi.encodeWithSelector(bytes4(0x12345678),uint256(37),false):abi.encodeWithSelector(bytes4(0x12345678),uint256(39),true);
  (bool ok,)=target.call(data);require(ok);
 }
}`;
const material={kind:'source',source:{language:'Solidity',files:{'ConditionalABI.sol':source},compiler_version:'0.8.30+commit.73712a01',target:'ConditionalABI.sol:ConditionalABI',settings:{evmVersion:'shanghai'}}};
const raw=JSON.stringify(material),input={submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:image},engines:['solc-ast']};
const engine=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:180000,maxBuffer:64<<20});
assert.equal(engine.status,0,engine.stderr);
const model=JSON.parse(engine.stdout),require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30');
const compilerInput={language:'Solidity',sources:{'ConditionalABI.sol':{content:source}},settings:{evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers']}}}};
const compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));
assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);
const contract=compiled.contracts['ConditionalABI.sol'].ConditionalABI;
write('input.json',input);write('output.json.gz',gzipSync(Buffer.from(JSON.stringify(model))));write('compiler-input.json',compilerInput);write('compiler-output.json',compiled);
assert.equal(model.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');
const values=new Map((model.sections.abi_values||[]).map(v=>[v.abi_value_id,v]));
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const port=await freePort(),sourcePort=await freePort(),sender='0x'+'1'.repeat(40),recipient='0x'+'2'.repeat(40);let vm,id=0;
async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method,params}),signal:AbortSignal.timeout(20000)}),value=await response.json();if(value.error)throw Error(JSON.stringify(value.error));return value.result;}
function accepts(predicate,method,flag){
 assert.ok(predicate,'Conditional ABI alternatives must retain their captured identity');
 if(predicate.op==='constant')return predicate.value;
 if(predicate.op==='and')return predicate.terms.every(p=>accepts(p,method,flag));
 if(predicate.op==='or')return predicate.terms.some(p=>accepts(p,method,flag));
 if(predicate.op==='not')return !accepts(predicate.value,method,flag);
 assert.equal(predicate.op,'atom');
 assert.ok(model.sections.expressions.some(e=>e.expression_id===predicate.expression_id&&e.method_id===method.method_id));
 const old=method.entry_return_expression_id;let value;
 try{method.entry_return_expression_id=predicate.expression_id;value=evaluateReturn(model,method.name,{flag,target:BigInt(recipient)})[0];}
 finally{method.entry_return_expression_id=old;}
 return(typeof value==='boolean'?value:value!==0n)===predicate.expected;
}
const top=(step,index)=>BigInt('0x'+step.stack.at(-1-index).replace(/^0x/,''));
const records=[];
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});
 await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 const deploymentHash=await rpc('eth_sendTransaction',[{from:sender,data:'0x'+contract.evm.bytecode.object,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');
 const deployment=await rpc('eth_getTransactionReceipt',[deploymentHash]);assert.equal(deployment.status,'0x1');
 const address=deployment.contractAddress,code=await rpc('eth_getCode',[address,'latest']);
 assert.equal(code,'0x'+contract.evm.deployedBytecode.object);assert.equal(code,model.sections.artifacts.find(a=>a.kind==='bytecode').text);
 write('deployment.json',{receipt:deployment,compiler:solc.version(),runtime_sha256:sha(Buffer.from(code.slice(2),'hex'))});
 for(const abi of contract.abi.filter(a=>a.type==='function'))for(const flag of[false,true]){
  const name=abi.name,method=model.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(method);
  const data='0x'+contract.evm.methodIdentifiers[name+'(bool,address)']+(flag?'1':'0').padStart(64,'0')+recipient.slice(2).padStart(64,'0');
  const transaction={from:sender,to:address,data,gas:'0x989680',gasPrice:'0x77359400'},returned=await rpc('eth_call',[transaction,'latest']);
  const hash=await rpc('eth_sendTransaction',[transaction]);await rpc('evm_mine');
  const receipt=await rpc('eth_getTransactionReceipt',[hash]),trace=await rpc('debug_traceTransaction',[hash,{enableMemory:true}]);
  assert.equal(receipt.status,'0x1');assert.equal('0x'+trace.returnValue.replace(/^0x/,''),returned);
  const traceBytes=Buffer.from(JSON.stringify(trace));write(name+'-'+flag+'-trace.json.gz',gzipSync(traceBytes));write(name+'-'+flag+'-receipt.json',receipt);
  let record;
  if(name.startsWith('decode')){
   const decodings=model.sections.abi_decodings.filter(d=>d.method_id===method.method_id);assert.equal(decodings.length,1);
   const decoded=decodings[0];assert.equal(decoded.status,'conditional');assert.deepEqual(decoded.unresolved,[]);assert.equal(decoded.cases.length,2);
   const selected=decoded.cases.filter(c=>accepts(c.predicate,method,flag));assert.equal(selected.length,1);
   const selectedCase=selected[0],nodes=selectedCase.value_ids.map(id=>values.get(id));assert.equal(nodes.length,2);assert.ok(nodes.every(Boolean));
   for(const node of nodes){assert.equal(node.status,'decoded');assert.deepEqual(node.predicate,selectedCase.predicate);assert.equal(node.source_memory_version_id,selectedCase.source_memory_version_id);assert.equal(node.abi_decoding_id,decoded.abi_decoding_id);}
   const hex=returned.slice(2);assert.equal(hex.length,128);
   const observed=[BigInt('0x'+hex.slice(0,64)).toString(),BigInt('0x'+hex.slice(64))===1n];
   assert.deepEqual(nodes.map(n=>n.value),observed);
   record={name,flag,transaction_hash:hash,abi_decoding_id:decoded.abi_decoding_id,selected_predicate:selectedCase.predicate,value_ids:selectedCase.value_ids,observed_return:observed};
  }else{
   const bindings=model.sections.call_input_bindings.filter(b=>b.method_id===method.method_id&&b.representation_scope==='source_object');assert.equal(bindings.length,1);
   const binding=bindings[0];assert.equal(binding.status,'conditional');assert.deepEqual(binding.unresolved,[]);assert.equal(binding.cases.length,2);
   const selected=binding.cases.filter(c=>accepts(c.predicate,method,flag));assert.equal(selected.length,1);
   const selectedCase=selected[0];assert.ok(selectedCase.bytes.every(b=>b.kind==='constant'));
   const calls=trace.structLogs.filter(s=>s.op==='CALL'&&s.depth===1);assert.equal(calls.length,1);
   const step=calls[0],start=Number(top(step,3)),length=Number(top(step,4)),memory=Buffer.from(step.memory.map(w=>w.replace(/^0x/,'')).join(''),'hex');
   assert.ok(memory.length>=start+length);assert.equal(top(step,1),BigInt(recipient));assert.equal(top(step,2),0n);
   const observed='0x'+memory.subarray(start,start+length).toString('hex'),modeled='0x'+Buffer.from(selectedCase.bytes.map(b=>b.value)).toString('hex');assert.equal(modeled,observed);
   const nodes=model.sections.abi_values.filter(v=>v.call_input_binding_id===binding.call_input_binding_id&&v.source_memory_version_id===selectedCase.source_memory_version_id);
   assert.equal(nodes.length,2);for(const node of nodes){assert.equal(node.status,'decoded');assert.deepEqual(node.predicate,selectedCase.predicate);assert.ok(accepts(node.predicate,method,flag));}
   const hex=observed.slice(10),observedValues=[BigInt('0x'+hex.slice(0,64)).toString(),BigInt('0x'+hex.slice(64))===1n];assert.deepEqual(nodes.map(n=>n.value),observedValues);
   record={name,flag,transaction_hash:hash,call_input_binding_id:binding.call_input_binding_id,selected_predicate:selectedCase.predicate,value_ids:nodes.map(n=>n.abi_value_id),observed_input_hex:observed,observed_values:observedValues};
  }
  record.trace_sha256=sha(traceBytes);records.push(record);write('partial-results.json',records);
 }
}finally{await vm?.close();}
assert.equal(records.length,8);
write('results.json',{status:'PASS_CONDITIONAL_ABI_IDENTITY_AND_ACTUAL_VALUES',image,actual_transactions:records.length,records,scope:'Conditional decode and typed CALL value nodes retain saved predicate identity; exact compiled runtime and actual returns/CALL memory. Conditional decoded scalar formula propagation remains a separate gap.'});
console.log('CONDITIONAL_ABI_PREDICATES_ACTUAL_PASS',records.length,out);

return {name:"abi",image,out,actual_transactions:records.length};
}

const completed=[];for(const run of[runLegacy,runEffects,runAbi])completed.push(await run());
assert.equal(completed.reduce((n,c)=>n+c.actual_transactions,0),17);console.log('P2_SOURCE_BYTE_CONSUMERS_RUNTIME_PASS',JSON.stringify(completed));
