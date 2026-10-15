import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createRequire} from 'node:module';
import {compileDeepeningFixture} from '../../analysis-engines/test/deepening-fixtures.mjs';
import {evaluateReturn} from './expression_model_eval.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js'),solc=require('solc-0.8.30');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),mod=1n<<256n,max=mod-1n,word=n=>((BigInt(n)%mod+mod)%mod).toString(16).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-deepening')+'/deepening-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const image=readFileSync(new URL('../engine-image.txt',out),'utf8').trim(),checks=[],runs=[],transactions=[];
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({image,checks,runs,transactions,complete:scenario==='deepening-runtime-complete'&&ok,simulation:true,network:'local_anvil',all_paths_proved:false},null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw new Error(scenario);}
let sequence=0;
async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18749',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(10000)});const d=await r.json();if(d.error)throw new Error(d.error.message);return d.result;}
const admin='0x'+'1'.repeat(40),user='0x'+'3'.repeat(40);
async function send(from,to,data){const tx=await rpc('eth_sendTransaction',[{from,...(to?{to}:{}),data,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);transactions.push({tx_hash:tx,block_hash:receipt.blockHash,status:receipt.status});return receipt;}
async function analyze(fixture,address,name){const bytecode=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
 const input={...fixture,compiled:undefined,bytecode,snapshot:{...fixture.snapshot,code_hash:hash(Buffer.from(bytecode.slice(2),'hex')),code_address:address,storage_address:address,entry_address:address,block_number:Number(BigInt(block.number)),block_hash:block.hash}};
 const started=performance.now(),run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 check(name+'-engine-exit',run.status===0,run.stderr.slice(-1500));const model=JSON.parse(run.stdout);writeFileSync(new URL(name+'-analysis.json',out),run.stdout);writeFileSync(new URL(name+'-input.json',out),JSON.stringify(input));
 runs.push({name,code_hash:input.snapshot.code_hash,wall_ms:Math.round(performance.now()-started),metrics:model.metrics});return model;
}
async function compare(model,fixture,address,name,args,label){const method=fixture.compiled.abi.find(m=>m.type==='function'&&m.name===name),signature=name+'('+method.inputs.map(p=>p.type).join(',')+')',inputs=Object.fromEntries(method.inputs.map((p,i)=>[p.name,args[i]]));
 let expected,actual,modelError=false,evmError=false;
 try{expected=evaluateReturn(model,name,inputs);}catch{modelError=true;}
 try{const raw=await rpc('eth_call',[{from:user,to:address,data:selector(signature)+args.map(word).join('')},'latest']);actual=raw.slice(2).match(/.{64}/g)?.map(w=>BigInt('0x'+w))||[];}catch{evmError=true;}
 check(name+'-'+label,modelError===evmError&&(modelError||expected.length===actual.length&&expected.every((v,i)=>v===actual[i])),{inputs:Object.fromEntries(Object.entries(inputs).map(([k,v])=>[k,String(v)])),expected:expected?.map(String),actual:actual?.map(String),model_error:modelError,execution_error:evmError});
}
const anvil=spawn(fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url)),['--host','127.0.0.1','--port','18749','--accounts','0','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('local-evm-ready',ready);
 for(const a of [admin,user])await rpc('anvil_setBalance',[a,'0x'+(10n**30n).toString(16)]);
 for(const optimized of [false,true]){
  const fixture=compileDeepeningFixture(optimized),receipt=await send(admin,null,'0x'+fixture.compiled.evm.bytecode.object),address=receipt.contractAddress,name='Deepening-'+optimized;
  check(name+'-deploy',receipt.status==='0x1');const model=await analyze(fixture,address,name);
  for(let n=0n;n<=9n;n++)for(const x of [0n,10n,max])for(const method of ['finite','finiteWhile','decrement','localBound'])await compare(model,fixture,address,method,[n,x],optimized+'-'+n+'-'+x);
  for(let n=0n;n<=8n;n++)for(const method of ['finiteDo','boundedBreak'])await compare(model,fixture,address,method,[n],optimized+'-'+n);
  for(const n of [0n,1n,8n,16n])await compare(model,fixture,address,'dynamic',[n],optimized+'-'+n);
  for(const [x,y]of [[0n,0n],[7n,3n],[max,2n],[1n<<255n,max],[max,max]]){
   for(const denominator of [0n,1n,3n,max])for(const method of ['wordMath','wordMod'])await compare(model,fixture,address,method,[x,y,denominator],optimized+'-'+x+'-'+y+'-'+denominator);
   await compare(model,fixture,address,'wordSigned',[x,y],optimized+'-'+x+'-'+y);
  }
  for(const x of [0n,1n,1n<<255n,max]){
   for(const method of ['wordBranch','wordLoop','wordTuple','wordRevert'])await compare(model,fixture,address,method,[x],optimized+'-'+x);
   for(const bits of [0n,1n,31n,32n,255n,256n,300n]){
    await compare(model,fixture,address,'wordShift',[x,bits],optimized+'-'+x+'-'+bits);
    await compare(model,fixture,address,'wordSignExtend',[x,bits],optimized+'-'+x+'-'+bits);
    await compare(model,fixture,address,'wordByte',[x,bits],optimized+'-'+x+'-'+bits);
   }
  }
  const property=(method,kind)=>model.sections.property_obligations.find(p=>model.sections.methods.find(m=>m.method_id===p.method_id)?.name===method&&p.property===kind);
  for(const [method,kind,status]of [['guarded','successful_guard_requires_caller_equality','proved_under_declared_model'],['alternative','successful_guard_requires_caller_equality','counterexample'],['impossible','successful_guard_requires_caller_equality','infeasible_assumptions'],['split','returned_components_conserve_input','proved_under_declared_model'],['brokenSplit','returned_components_conserve_input','counterexample']])check(name+'-proof-'+method,property(method,kind)?.status===status,property(method,kind));
  check(name+'-unauthorized-guard-reverts',(await send(user,address,selector('guarded()'))).status==='0x0');
  check(name+'-owner-enables-state',(await send(admin,address,selector('guarded()'))).status==='0x1');
  check(name+'-alternative-counterexample-executes',(await send(user,address,selector('alternative()'))).status==='0x1');
  const counterexample=property('brokenSplit','returned_components_conserve_input').counterexample,found={};
  for(const input of counterexample.inputs){const match=new RegExp('\\('+input.name+'\\s+(\\d+)\\)').exec(counterexample.values_smt);if(match)found[input.expression]=BigInt(match[1]);}
  check(name+'-solver-counterexample-inputs',found.amount!==undefined&&found.fee!==undefined,Object.fromEntries(Object.entries(found).map(([k,v])=>[k,String(v)])));
  const bad=await rpc('eth_call',[{from:user,to:address,data:selector('brokenSplit(uint8,uint8)')+word(found.amount)+word(found.fee)},'latest']),parts=bad.slice(2).match(/.{64}/g).map(w=>BigInt('0x'+w));
  check(name+'-economic-counterexample-executes',parts[0]===(found.amount-found.fee+256n)%256n&&parts[1]===found.fee&&parts[0]+parts[1]!==found.amount,{proof:property('brokenSplit','returned_components_conserve_input').property_id,inputs:Object.fromEntries(Object.entries(found).map(([k,v])=>[k,String(v)])),returned:parts.map(String)});
  if(optimized){
   const qfile='Quotes.sol',qsource=`pragma solidity ^0.8.24; contract Quotes {function quote(address who,uint256 amount) external view returns(uint256){require(msg.sender!=who);return amount+block.number;} function list(uint256 amount) external view returns(uint256[] memory values){values=new uint256[](3);values[0]=amount;values[1]=block.number;values[2]=uint160(msg.sender);}}`;
   const std={language:'Solidity',sources:{[qfile]:{content:qsource}},settings:{optimizer:{enabled:true,runs:200},evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers']}}}},output=JSON.parse(solc.compile(JSON.stringify(std))),compiled=output.contracts[qfile].Quotes;
   const deployed=await send(admin,null,'0x'+compiled.evm.bytecode.object),target=deployed.contractAddress,bytecode='0x'+compiled.evm.deployedBytecode.object;
   const qfixture={compiled,bytecode,engines:fixture.engines,snapshot:fixture.snapshot,source:{stdJsonInput:std,compilation:{compilerVersion:solc.version(),fullyQualifiedName:qfile+':Quotes'},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi}};
   await analyze(qfixture,target,'Quotes');
   const records=[];
   for(const method of ['quote','twiceQuote','dynamicList','quoteThenRevert']){
    const receipt=await send(user,address,selector(method+'(address,uint256)')+word(target)+word(9007199254740993n));
    const trace=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'callTracer'}]),prestate=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer'}]);
    records.push({name:method,receipt,trace,prestate});check('external-return-trace-'+method,trace.calls?.length===(method==='twiceQuote'?2:1)&&trace.calls.every(c=>c.type==='STATICCALL'&&c.from.toLowerCase()===address&&c.output.length>2),{tx_hash:receipt.transactionHash,trace});
   }
   writeFileSync(new URL('external-return-traces.json',out),JSON.stringify(records));
  }
 }
 check('deepening-runtime-complete',true);
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');}
