import {spawn,spawnSync}from'node:child_process';
import{once}from'node:events';
import{readFileSync,writeFileSync,mkdirSync,existsSync}from'node:fs';
import{fileURLToPath}from'node:url';
import{createRequire}from'node:module';
import{analyze}from'../../analysis-engines/analyze.mjs';
import{replay}from'../../analysis-engines/replay.mjs';
import{buildOpcodeWitness}from'../../analysis-engines/opcode-witness.mjs';
import{compileVerificationFixture,verificationSource,rolesSource}from'../../analysis-engines/test/verification-fixtures.mjs';
import{evaluateReturn}from'./expression_model_eval.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),WORD=1n<<256n;
const word=n=>((BigInt(n)%WORD+WORD)%WORD).toString(16).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-verification')+'/verification-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const host=process.env.CONTRACT_QA_ENGINE_MODE==='host',image=host?null:readFileSync(new URL('../engine-image.txt',out),'utf8').trim(),checks=[],records=[],runs=[];
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({checks,records,runs,image,
 execution_mode:host?'candidate_source_with_frozen_solver':'frozen_candidate_image',network:'local_anvil',complete:scenario==='verification-runtime-complete'&&ok,all_paths_proved:false},null,2));
 console.log(scenario,ok?'PASS':'FAIL');if(!ok){console.log(JSON.stringify(evidence)?.slice(0,6000));throw new Error(scenario);}}
let sequence=0,rpcPort=18750,supplementalAnvil=null;
async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:'+rpcPort,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(20000)});const body=await response.json();if(body.error)throw new Error(body.error.message);return body.result;}
const admin='0x'+'1'.repeat(40),user='0x'+'3'.repeat(40);
async function send(to,data,from=admin,value='0x0'){const txHash=await rpc('eth_sendTransaction',[{from,...(to?{to}:{}),data,value,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');return await rpc('eth_getTransactionReceipt',[txHash]);}
function engine(input){if(host)return input.operation==='replay'?replay(input):analyze(input);
 const run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 if(run.status!==0)throw new Error('FROZEN_ENGINE_FAILED:'+run.stderr.slice(-1500));return JSON.parse(run.stdout);}
async function deploy(fixture,label){const receipt=await send(null,'0x'+fixture.compiled.evm.bytecode.object);check(label+'-deploy',receipt.status==='0x1');const address=receipt.contractAddress;
 const code=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
 const input={...fixture,compiled:undefined,output:undefined,bytecode:code,snapshot:{...fixture.snapshot,code_hash:hash(Buffer.from(code.slice(2),'hex')),
  entry_address:address,code_address:address,storage_address:address,block_number:Number(BigInt(block.number)),block_hash:block.hash,evm_rules:rpcPort===18751?'cancun':'shanghai'}};
 const started=performance.now(),model=await engine(input);check(label+'-source-engine',model.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed'));
 writeFileSync(new URL(label+'-analysis.json',out),JSON.stringify(model));writeFileSync(new URL(label+'-input.json',out),JSON.stringify(input));runs.push({label,code_hash:input.snapshot.code_hash,wall_ms:Math.round(performance.now()-started)});return {address,model,input,fixture};}
async function compare(item,name,args,label){const method=item.fixture.compiled.abi.find(m=>m.type==='function'&&m.name===name),signature=name+'('+method.inputs.map(p=>p.type).join(',')+')',inputs=Object.fromEntries(method.inputs.map((p,i)=>[p.name,args[i]]));
 let modeled,actual,modelFailed=false,evmFailed=false;
 try{modeled=evaluateReturn(item.model,name,inputs);}catch{modelFailed=true;}
 try{const raw=await rpc('eth_call',[{from:user,to:item.address,data:selector(signature)+args.map(word).join(''),gas:'0x989680'},'latest']);actual=raw.slice(2).match(/.{64}/g)?.map(w=>BigInt('0x'+w))||[];}catch{evmFailed=true;}
 check(name+'-'+label,modelFailed===evmFailed&&(modelFailed||modeled.length===actual.length&&modeled.every((v,i)=>v===actual[i])),{inputs:Object.fromEntries(Object.entries(inputs).map(([k,v])=>[k,String(v)])),model_failed:modelFailed,evm_failed:evmFailed,modeled:modeled?.map(String),actual:actual?.map(String)});
}
async function capture(item,name,args,label,{replayCheck=false,value='0x0'}={}){
 const method=item.fixture.compiled.abi.find(m=>m.type==='function'&&m.name===name),signature=name+'('+method.inputs.map(p=>p.type).join(',')+')';
 const receipt=await send(item.address,selector(signature)+args.map(word).join(''),admin,value);
 const trace=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'callTracer'}]),prestate=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer'}]),
  diff=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),steps=await rpc('debug_traceTransaction',[receipt.transactionHash,{disableStorage:true,enableMemory:false,disableStack:false,enableReturnData:false,limit:20001}]);
 const witness=buildOpcodeWitness(steps,trace,{txHash:receipt.transactionHash,prestate});
 const record={name,label,receipt,trace,prestate,diff,witness,steps};
 writeFileSync(new URL(label+'-execution.json',out),JSON.stringify(record));
 check(label+'-opcode-identity',witness.calls_complete,{gaps:witness.gaps,calls:witness.calls.length,steps:witness.steps});
 check(label+'-memory-reconstruction',witness.memory_complete,{gaps:witness.memory_gaps});
 check(label+'-storage-reconstruction',witness.storage_complete,{gaps:witness.storage_gaps});
 records.push({name:label,tx_hash:receipt.transactionHash,calls:witness.calls.length,memory_effects:witness.memory_effects.length,storage_effects:witness.storage_effects.length});
 if(replayCheck){
  const transaction=await rpc('eth_getTransactionByHash',[receipt.transactionHash]),block=await rpc('eth_getBlockByNumber',[receipt.blockNumber,false]),parent=await rpc('eth_getBlockByNumber',['0x'+(BigInt(receipt.blockNumber)-1n).toString(16),false]);
  const input={operation:'replay',snapshot:{...item.input.snapshot,block_number:Number(BigInt(block.number)),block_hash:block.hash,state_granularity:'transaction_prestate'},
   witness:{raw_transaction:transaction,raw_receipt:receipt,raw_block:block,parent_block:parent,prestate,state_diff:diff,call_trace:trace,hardfork:item.input.snapshot.evm_rules,simulation:true}};
  writeFileSync(new URL(label+'-replay-input.json',out),JSON.stringify(input));const result=await engine(input),row=result.sections.replays[0];
  writeFileSync(new URL(label+'-replay.json',out),JSON.stringify(result));
  check(label+'-complete-offline-replay',row.complete,{status:row.status,error:row.error,gaps:row.witness_gaps,differences:row.differences,opcode:row.opcode_witness_status});
  check(label+'-replay-opcode-witness',result.sections.opcode_witnesses?.[0]?.calls_complete===true,{status:row.opcode_witness_status});
 }
 return record;
}

const binary=process.env.ANVIL_BINARY||fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url));
const anvilArgs=port=>['--host','127.0.0.1','--port',String(port),'--accounts','0','--threads','2','--chain-id','1','--hardfork',port===18751?'cancun':'shanghai','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'];
const anvil=spawn(binary,anvilArgs(18750),{stdio:['ignore','ignore','pipe']});
const oracleSource=`pragma solidity ^0.8.24;contract Verification {uint256 public saved;function quote(uint256 x)external view returns(uint256){return x+block.number;}function put(uint256 v)external payable{saved=v;}function fail(uint256 v)external{saved=v;revert();}}`;
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('local-verification-evm-ready',ready);
 for(const address of [admin,user])await rpc('anvil_setBalance',[address,'0x'+(10n**30n).toString(16)]);
 const oracle=await deploy(compileVerificationFixture(true,oracleSource),'Oracle');
 const native=await deploy(compileVerificationFixture(true,'pragma solidity ^0.8.24;contract Verification{function pay(address payable recipient)external payable{recipient.transfer(msg.value);}function tryPay(address payable recipient)external payable returns(bool){return recipient.send(msg.value);}}'),'NativeCalls');
 for(const name of ['pay','tryPay']){
  const actual=await capture(native,name,[user],'native-'+name,{value:'0x11'});
  check('native-call-has-exact-source-pc-'+name,actual.receipt.status==='0x1'&&actual.witness.calls.length===1&&actual.witness.calls[0].value_raw==='17'&&native.model.sections.source_pc_maps.some(m=>m.pc===actual.witness.calls[0].pc&&m.status==='unique_source_callsite'));
 }
 for(const optimized of [false,true]){
  const item=await deploy(compileVerificationFixture(optimized),'Verification-'+optimized),loop=name=>item.model.sections.loop_invariants.find(l=>item.model.sections.methods.find(m=>m.method_id===l.method_id)?.name===name);
  for(const name of ['affine','wrapped','subtract'])check('induction-'+optimized+'-'+name,loop(name)?.complete===true,loop(name));
  for(const n of [0n,1n,17n,64n,128n,512n])for(const c of [0n,3n,WORD-1n]){
   await compare(item,'affine',[n,c],optimized+'-'+n+'-'+c);await compare(item,'wrapped',[n,c],optimized+'-'+n+'-'+c);
   await compare(item,'subtract',[n,c,1000000n],optimized+'-'+n+'-'+c);
  }
  for(const p of [128n,129n,159n,256n,1024n])await compare(item,'memoryWord',[p,(1n<<255n)+7n],optimized+'-'+p);
  for(const v of [0n,1n,WORD-1n])await compare(item,'memoryByte',[v],optimized+'-'+v);
  const two=await capture(item,'twoSites',[oracle.address,9007199254740993n],'two-sites-'+optimized,{replayCheck:true});
  check('distinct-static-call-pcs-'+optimized,new Set(two.witness.calls.map(c=>c.pc)).size===2);
  const repeated=await capture(item,'repeated',[oracle.address,3n],'repeated-'+optimized,{replayCheck:true});
  check('repeated-call-same-source-pc-'+optimized,repeated.witness.calls.length===3&&new Set(repeated.witness.calls.map(c=>c.pc)).size===1);
  for(const c of [...two.witness.calls,...repeated.witness.calls])check('verified-pc-'+optimized+'-'+c.trace_path+'-'+c.pc,c.verified&&item.model.sections.source_pc_maps.some(m=>m.pc===c.pc&&m.status==='unique_source_callsite'&&m.source_dependency_ids.length));
  const saved=await capture(item,'storageWrite',[7n,9007199254740993n],'assembly-store-'+optimized);
  check('actual-sstore-precise-word-'+optimized,saved.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&e.slot==='0x'+word(7n)&&e.value_raw==='9007199254740993'&&e.settled));
 }
 const closed=verificationSource.split('\n').filter(line=>!line.includes('function storageWrite')).join('\n');
 const good=await deploy(compileVerificationFixture(true,closed),'ClosedLedger');check('closed-ledger-all-entry-proof',good.model.sections.protocol_invariants[0].complete);
 const bad=await deploy(compileVerificationFixture(true,closed.replace('totalSupply+=value;','totalSupply+=value+1;')),'BrokenLedger');
 check('broken-ledger-counterexample',bad.model.sections.protocol_invariants[0].status==='counterexample');
 await send(bad.address,selector('mint(address,uint256)')+word(user)+word(13n));
 const supply=BigInt(await rpc('eth_call',[{to:bad.address,data:selector('totalSupply()')},'latest'])),balance=BigInt(await rpc('eth_call',[{to:bad.address,data:selector('balanceOf(address)')+word(user)},'latest']));
 check('economic-defect-executes-on-evm',balance===13n&&supply===14n,{balance:balance.toString(),totalSupply:supply.toString()});
 const bypass=await deploy(compileVerificationFixture(true,closed.replace('contract Verification {','contract Verification {function seize(address next)external{owner=next;}')),'OwnershipBypass');
 check('owner-bypass-counterexample',bypass.model.sections.authorization_models[0].status==='counterexample');
 const badMethod=bypass.model.sections.methods.find(m=>m.name==='seize'),badProof=bypass.model.sections.verification_obligations.find(p=>p.method_id===badMethod.method_id&&p.status==='counterexample');
 const values=new Map([...badProof.counterexample.values_smt.matchAll(/\(([^\s()]+)\s+(\d+)\)/g)].map(m=>[m[1],BigInt(m[2])]));
 const nextInput=badProof.counterexample.inputs.find(i=>i.expression==='next'),nextOwner=values.get(nextInput?.name),caller=values.get('caller');
 check('owner-counterexample-inputs-preserved',nextOwner!==undefined&&caller!==undefined);
 for(const v of badProof.counterexample.inputs.filter(i=>i.kind==='entry_storage_word')){
  const slot=values.get(v.slot_symbol),entryWord=values.get(v.name);check('owner-counterexample-storage-preserved-'+v.name,slot!==undefined&&entryWord!==undefined);
  await rpc('anvil_setStorageAt',[bypass.address,'0x'+word(slot),'0x'+word(entryWord)]);
 }
 const from='0x'+caller.toString(16).padStart(40,'0');await rpc('anvil_setBalance',[from,'0x'+(10n**30n).toString(16)]);
 const before=await rpc('eth_call',[{to:bypass.address,data:selector('owner()')},'latest']);
 const receipt=await send(bypass.address,selector('seize(address)')+word(nextOwner),from),owner=await rpc('eth_call',[{to:bypass.address,data:selector('owner()')},'latest']);
 check('owner-solver-counterexample-executes-on-evm',receipt.status==='0x1'&&BigInt(before)!==caller&&BigInt(owner)===nextOwner&&owner!==before,{tx_hash:receipt.transactionHash,owner,before,caller:from,proof_id:badProof.property_id});
 const roles=await deploy(compileVerificationFixture(true,rolesSource),'RoleModel');check('roles-all-entry-proof',roles.model.sections.authorization_models[0].complete);
 supplementalAnvil=spawn(binary,anvilArgs(18751),{stdio:['ignore','ignore','pipe']});rpcPort=18751;
 let cancunReady=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');cancunReady=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('local-cancun-evm-ready',cancunReady);
 for(const address of [admin,user])await rpc('anvil_setBalance',[address,'0x'+(10n**30n).toString(16)]);
 const assemblyOracle=await deploy(compileVerificationFixture(true,oracleSource),'CancunOracle');
 const assembly=await deploy(compileVerificationFixture(true,`pragma solidity ^0.8.24;contract Verification {
 uint128 public low;uint128 public high;uint256 public result;
 function packed(uint128 a,uint128 b)external{assembly{sstore(low.slot,or(a,shl(128,b)))}}
 function effects(uint256 v)external returns(uint256 out){assembly{mstore(128,v)mstore(160,7)mcopy(129,128,63)out:=mload(128)tstore(7,v)if iszero(eq(tload(7),v)){revert(0,0)}sstore(result.slot,out)}}
 function failedChild(address target,uint256 v)external{(bool ok,)=target.call(abi.encodeWithSignature("fail(uint256)",v));require(!ok);}
 function delegated(address target,uint256 v)external payable{(bool ok,)=target.delegatecall(abi.encodeWithSignature("put(uint256)",v));require(ok);}
 function forbiddenStatic(address target,uint256 v)external view{(bool ok,)=target.staticcall(abi.encodeWithSignature("put(uint256)",v));require(!ok);}
 function ancestorRevert(address target,uint256 v)external{(bool ok,)=target.delegatecall(abi.encodeWithSignature("put(uint256)",v));require(ok);revert();}
 function legacyContext(address target,uint256 v)external payable{assembly{mstore(0,shl(224,${selector('put(uint256)')}))mstore(4,v)if iszero(callcode(gas(),target,0,0,36,0,0)){revert(0,0)}}}
 }`,'cancun'),'AssemblyEffects');
 const packed=await capture(assembly,'packed',[9007199254740993n,123n],'packed-storage');
 check('packed-word-matches-evm',packed.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&e.value_raw===String((123n<<128n)+9007199254740993n)));
 const effects=await capture(assembly,'effects',[(1n<<255n)+7n],'overlapping-copy-transient');
 check('mcopy-and-transient-effects-recorded',effects.witness.memory_effects.some(e=>e.opcode==='MCOPY')&&['TLOAD','TSTORE'].every(op=>effects.witness.storage_effects.some(e=>e.opcode===op)));
 const failed=await capture(assembly,'failedChild',[assemblyOracle.address,31n],'caught-revert');
 check('child-writes-reverted',failed.receipt.status==='0x1'&&failed.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&!e.settled));
 const delegated=await capture(assembly,'delegated',[assemblyOracle.address,33n],'delegate-storage',{value:'0x11'});
 check('delegate-write-keeps-caller-storage',delegated.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&e.code_address===assemblyOracle.address&&e.storage_address===assembly.address&&e.settled));
 check('delegate-inherits-value-and-effective-caller',delegated.witness.calls.some(c=>c.call_type==='DELEGATECALL'&&c.value_raw==='17'&&c.effective_caller===admin));
 const forbidden=await capture(assembly,'forbiddenStatic',[assemblyOracle.address,35n],'static-write-rejected');
 check('static-context-has-no-settled-write',forbidden.trace.calls.some(c=>c.error)&&!forbidden.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&e.settled));
 const ancestor=await capture(assembly,'ancestorRevert',[assemblyOracle.address,37n],'ancestor-revert');
 check('ancestor-revert-unsettles-child-write',ancestor.receipt.status==='0x0'&&ancestor.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&!e.settled));
 const legacy=await capture(assembly,'legacyContext',[assemblyOracle.address,39n],'callcode-storage',{value:'0x13'});
 check('callcode-write-keeps-caller-storage',legacy.witness.calls.some(c=>c.call_type==='CALLCODE')&&legacy.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&e.code_address===assemblyOracle.address&&e.storage_address===assembly.address&&e.settled));
 check('callcode-value-and-caller-follow-stack-context',legacy.witness.calls.some(c=>c.call_type==='CALLCODE'&&c.value_raw==='0'&&c.effective_caller===assembly.address));
 check('verification-runtime-complete',true);
}finally{for(const child of [anvil,supplementalAnvil].filter(Boolean)){child.kill('SIGTERM');if(child.exitCode===null)await once(child,'exit');}}
