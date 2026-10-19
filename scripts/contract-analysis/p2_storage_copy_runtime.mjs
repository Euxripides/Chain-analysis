import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {spawnSync} from 'node:child_process';
import {createServer} from 'node:net';
import {once} from 'node:events';
import {gzipSync} from 'node:zlib';
import {localGenesisEVM} from './local_genesis_evm.mjs';
import {evaluateReturn} from './expression_model_eval.mjs';
import {createStorageCopyWitness} from './storage_copy_witness.mjs';

const root=process.cwd(),base=path.join(root,'acceptance/contract-analysis-heap-writes'),image=process.env.CONTRACT_TEST_IMAGE||fs.readFileSync(path.join(base,'engine-image.txt'),'utf8').trim(),out=path.join(base,'storage-copy-runtime',image.slice(7,23),new Date().toISOString().replace(/[:.]/g,''));fs.mkdirSync(out,{recursive:true});
const source=fs.readFileSync(path.join(root,'analysis-engines/test/fixtures/storage-memory-copy.sol'),'utf8'),sha=b=>createHash('sha256').update(b).digest('hex'),json=v=>JSON.stringify(v,(_,x)=>typeof x==='bigint'?x.toString():x,2),write=(n,v)=>fs.writeFileSync(path.join(out,n),Buffer.isBuffer(v)||typeof v==='string'?v:json(v));
const require=createRequire(path.join(root,'analysis-engines/package.json')),solc=require('solc-0.8.30'),{keccak_256}=require('@noble/hashes/sha3.js'),settings={evmVersion:'shanghai'},material={kind:'source',source:{language:'Solidity',files:{'StorageCopyCases.sol':source},compiler_version:solc.version().split('.Emscripten')[0],target:'StorageCopyCases.sol:StorageCopyCases',settings}},raw=JSON.stringify(material),input={submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw),engine_digest:image},engines:['solc-ast']};
write('source.sol',source);write('input.json',input);
const run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--ulimit','core=0','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),timeout:300000,maxBuffer:64<<20});write('engine-stderr.log',run.stderr);assert.equal(run.status,0,run.stderr.toString());const model=JSON.parse(run.stdout);assert.equal(model.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');write('model.json.gz',gzipSync(Buffer.from(JSON.stringify(model))));
const compilerInput={language:'Solidity',sources:{'StorageCopyCases.sol':{content:source}},settings:{...settings,outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}}},compiled=JSON.parse(solc.compile(JSON.stringify(compilerInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);const contract=compiled.contracts['StorageCopyCases.sol'].StorageCopyCases;assert.equal(model.sections.artifacts.find(a=>a.kind==='bytecode').text,'0x'+contract.evm.deployedBytecode.object);write('compiler-input.json',compilerInput);write('compiler-output.json',compiled);
const sender='0x'+'1'.repeat(40),receiver='0x'+'4'.repeat(40),alternate='0x'+'5'.repeat(40),limit=1n<<128n,mask=limit-1n,records=[];
const initial=()=>({cell:[7n,5n],items1:[11n,3n],items2:[23n,4n],nested:[17n,3n,2n],array0:[31n,2n],array1:[41n,3n],recipient:[BigInt(receiver),13n],flags:[-3n,11n,true,0x11223344n],keyCalls:[0n]});
function expected(name,x){const state=initial(),u=x&mask,sum=(...xs)=>xs.reduce((a,b)=>{const v=a+b;if(v>mask)throw Error('panic_0x11');return v;},0n);let payment=0n,error;
 try{switch(name){
  case 'plainThenStore':state.cell[0]=u;payment=12n;break;
  case 'memoryAlias':case 'callMemory':case 'implicitMemoryArgument':payment=sum(u,5n);break;
  case 'rebindMemory':payment=sum(23n,u);break;
  case 'storageAliasKey':state.items2[0]=u;payment=14n;break;
  case 'branchCopy':state.cell[0]=u;payment=x%2n===0n?12n:14n;break;
  case 'nestedCopy':state.nested[0]=u;payment=sum(17n,u,2n);break;
  case 'twoCopies':state.cell[0]=u;payment=sum(7n,u);break;
  case 'readOnlySummaryCopy':case 'dirtyAllocator':payment=12n;break;
  case 'keyDivisionDiscarded':if(x===0n)throw Error('panic_0x12');payment=3n;break;
  case 'keyEffects':state.keyCalls[0]=1n;payment=sum(u,3n);break;
  case 'arrayElementCopy':if(x>1n)throw Error('panic_0x32');payment=x===0n?33n:44n;break;
  case 'copyThenRevert':if(x===0n)throw Error('revert');state.cell[0]=u;payment=12n;break;
  case 'assignmentCopy':payment=sum(7n,u);break;
  case 'recipientSwap':state.recipient[0]=BigInt(alternate);payment=13n;break;
  case 'typedFields':{const s=BigInt.asIntN(128,u),total=-3n+s;if(total<-(1n<<127n)||total>(1n<<127n)-1n)throw Error('panic_0x11');if(total<0n)throw Error('revert');state.flags[0]=s;state.flags[2]=false;payment=total+11n;break;}
  case 'nestedIndependentCopies':payment=sum(17n,u);break;
  case 'twoCopyKeyValues':state.items2[1]=u;payment=15n;break;
  case 'conditionalReference':state.cell[0]=u;payment=x===0n?12n:14n;break;
  case 'byteKey':payment=32n;break;
  default:throw Error('Unknown owned fixture method');
 }}catch(e){assert.ok(['panic_0x11','panic_0x12','panic_0x32','revert'].includes(e.message));error=e.message;}
 return{success:!error,payment:error?0n:payment,error,state:error?initial():state};
}
const names=['plainThenStore','memoryAlias','rebindMemory','storageAliasKey','branchCopy','nestedCopy','twoCopies','callMemory','implicitMemoryArgument','readOnlySummaryCopy','keyDivisionDiscarded','keyEffects','dirtyAllocator','arrayElementCopy','copyThenRevert','assignmentCopy','recipientSwap','typedFields','nestedIndependentCopies','twoCopyKeyValues','conditionalReference','byteKey'];
function vectors(name){if(name==='byteKey')return[0n];if(name==='keyDivisionDiscarded')return[0n,1n,9n,99n,255n];if(name==='arrayElementCopy')return[0n,1n,2n,23n];if(name==='typedFields')return[0n,1n,3n,23n,limit,mask];const high=['plainThenStore','memoryAlias','rebindMemory','nestedCopy','twoCopies','callMemory','implicitMemoryArgument','keyEffects','assignmentCopy','nestedIndependentCopies'].includes(name);return[0n,1n,23n,...(high?[limit,mask]:[])];}
async function freePort(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const port=await freePort(),sourcePort=await freePort();let vm,id=0,address;
async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++id,method,params}),signal:AbortSignal.timeout(20000)}),v=await r.json();if(v.error)throw Error(JSON.stringify(v.error));return v.result;}
const word=n=>BigInt(n).toString(16).padStart(64,'0');
async function getter(signature,args=[]){const data='0x'+contract.evm.methodIdentifiers[signature]+args.map(word).join(''),returned=await rpc('eth_call',[{to:address,data},'latest']),entry=contract.abi.find(a=>a.type==='function'&&a.name===signature.split('(')[0]),words=returned.slice(2).match(/.{64}/g)||[];assert.equal(words.length,entry.outputs.length);
 return words.map((w,i)=>{const n=BigInt('0x'+w),type=entry.outputs[i].type,integer=/^(u?int)(\d*)$/.exec(type),bytes=/^bytes(\d+)$/.exec(type);if(integer)return integer[1]==='int'?BigInt.asIntN(Number(integer[2]||256),n):n;if(bytes)return n>>BigInt(256-Number(bytes[1])*8);if(type==='bool'){assert.ok(n===0n||n===1n);return n===1n;}assert.equal(type,'address');return n;});}
async function state(){return{cell:await getter('cell()'),items1:await getter('items(uint256)',[1n]),items2:await getter('items(uint256)',[2n]),nested:await getter('readNested()'),array0:await getter('arrayCells(uint256)',[0n]),array1:await getter('arrayCells(uint256)',[1n]),recipient:await getter('recipient()'),flags:await getter('flags()'),keyCalls:await getter('keyCalls()')};}
try{
 vm=await localGenesisEVM({binary:path.join(root,'../contract-verification-20261015/analysis-engines/bin/anvil'),port,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);
 const deploymentData='0x'+contract.evm.bytecode.object+word(BigInt(receiver)),deploymentHash=await rpc('eth_sendTransaction',[{from:sender,data:deploymentData,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const deployment=await rpc('eth_getTransactionReceipt',[deploymentHash]);assert.equal(deployment.status,'0x1');address=deployment.contractAddress;assert.equal(await rpc('eth_getCode',[address,'latest']),'0x'+contract.evm.deployedBytecode.object);write('deployment.json',{receipt:deployment,constructor_receiver:receiver,initcode_sha256:sha(Buffer.from(deploymentData.slice(2),'hex'))});assert.deepEqual(await state(),initial());
 for(const name of names)for(const x of vectors(name)){
  const snapshot=await rpc('evm_snapshot'),want=expected(name,x),to=name==='recipientSwap'?alternate:receiver;await rpc('anvil_setBalance',[address,'0x'+(10n**24n).toString(16)]);const before=BigInt(await rpc('eth_getBalance',[receiver,'latest'])),beforeAlternate=BigInt(await rpc('eth_getBalance',[alternate,'latest'])),data='0x'+contract.evm.methodIdentifiers[name+'(uint256,address)']+word(x)+word(BigInt(to)),tx=await rpc('eth_sendTransaction',[{from:sender,to:address,data,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]),trace=await rpc('debug_traceTransaction',[tx,{disableMemory:true,disableStorage:true}]),after=BigInt(await rpc('eth_getBalance',[receiver,'latest'])),afterAlternate=BigInt(await rpc('eth_getBalance',[alternate,'latest'])),observedState=await state();
  assert.equal(receipt.status==='0x1',want.success,name+':'+x);assert.equal(after-before,want.payment,name+':'+x);assert.equal(afterAlternate-beforeAlternate,0n);assert.deepEqual(observedState,want.state,name+':'+x);
  if(want.error?.startsWith('panic_')){const returned=(trace.returnValue||'').replace(/^0x/,'');assert.equal(returned.slice(0,8),'4e487b71');assert.equal(BigInt('0x'+returned.slice(8)),BigInt(want.error.slice(6)));}
  const method=model.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(method);const funds=model.sections.funds.filter(f=>f.method_id===method.method_id&&f.kind==='native_payment');assert.equal(funds.length,1);const fund=funds[0],flow=model.sections.dataflow.find(d=>d.dataflow_id===fund.amount_derivation_id);assert.ok(flow);const reads=[],bind=createStorageCopyWitness({compiled,contract,trace,keccak256:keccak_256,reads});
  const evaluate=key=>evaluateReturn({sections:{...model.sections,methods:[{...method,entry_return_expression_id:key}]}},method.name,{x,to:BigInt(receiver),next:BigInt(alternate),__storageSnapshot:bind})[0];
  let modeled,error,target;const guards=[];
  if(fund.amount_derivation_status==='represented_source_dependencies_resolved')try{
    for(const guard of model.sections.permissions.filter(g=>g.method_id===method.method_id&&g.failure==='revert')){const decision=evaluate(guard.expression_id);guards.push({guard_id:guard.guard_id,decision});if(!decision)throw Error('revert');}
    modeled=evaluate(fund.amount_expression_id);if(fund.to_expression_id)target=evaluate(fund.to_expression_id);
  }catch(failure){error=failure.message;}
  const negative=['keyEffects','dirtyAllocator','arrayElementCopy','keyDivisionDiscarded','conditionalReference','readOnlySummaryCopy'].includes(name);
  if(fund.amount_derivation_status==='represented_source_dependencies_resolved'){
    assert.ok(!negative,'UNSUPPORTED_FIXTURE_FALSELY_RESOLVED:'+name);
    if(want.success){assert.equal(error,undefined,name+':'+x);assert.equal(modeled,want.payment);if(target!==undefined)assert.equal(target,BigInt(receiver));}
    else assert.equal(error,want.error,name+':'+x);
  }else{assert.ok(negative,'UNEXPECTED_POSITIVE_GAP:'+name);assert.ok(flow.unresolved.length);}
  if(name==='byteKey'){assert.ok(flow.symbolic_expression.includes('storage_key<bytes4>'),'FIXED_BYTES_KEY_ENCODING_MISSING');assert.equal(modeled,32n);} 

  const ordinal=records.length+1;write(ordinal+'-receipt.json',receipt);write(ordinal+'-trace.json.gz',gzipSync(Buffer.from(JSON.stringify(trace))));records.push({name,x:x.toString(),transaction_hash:tx,success:want.success,expected_error:want.error,payment:(after-before).toString(),alternate_payment:(afterAlternate-beforeAlternate).toString(),state:observedState,modeled:modeled?.toString(),model_error:error,guards,target:target?.toString(),reads,source_amount_status:fund.amount_derivation_status,source_expanded_amount:fund.expanded_amount_expression,unresolved:flow.unresolved,method_id:method.method_id,trace_sha256:sha(JSON.stringify(trace))});assert.equal(await rpc('evm_revert',[snapshot]),true);
 }
 assert.equal(records.length,90);assert.equal(records.filter(r=>r.source_amount_status==='represented_source_dependencies_resolved').length,67);
 write('results.json',{status:'PASS_STORAGE_COPY90_ACTUAL_VALUES_AND_EXPLICIT_GAPS',image,implementation_count:1,methods:names.length,actual_transactions:records.length,records,runtime_sha256:sha(Buffer.from(contract.evm.deployedBytecode.object,'hex')),source_sha256:sha(source),local_EVM_RPC_calls:id,provider_requests:0,scope:'One owned 22-method implementation. Exact source/model/manual compilation/deployed bytecode, actual balances, all getters, checked panics, bounds, rollback and source-map storage-copy witnesses. Partial cases remain unsupported; no historical state, whole proof or fresh blind claim.'});console.log('STORAGE_COPY90_ACTUAL_PASS',JSON.stringify({directory:out,methods:names.length,transactions:records.length,succeeded:records.filter(r=>r.success).length,reverted:records.filter(r=>!r.success).length,resolved:records.filter(r=>r.source_amount_status==='represented_source_dependencies_resolved').length,explicit_partial:records.filter(r=>r.source_amount_status==='partial').length}));
}catch(error){write('failure.json',{error:error.message,stack:error.stack,completed:records});throw error;}finally{await vm?.close();}
