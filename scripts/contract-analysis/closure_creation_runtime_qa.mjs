import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {writeFileSync,mkdirSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {analyze} from '../../analysis-engines/analyze.mjs';
import {replay} from '../../analysis-engines/replay.mjs';
import {compileVerificationFixture} from '../../analysis-engines/test/verification-fixtures.mjs';
import {inheritedInitializationSource,diamondCreationSource,immutableCreationSource,stateArgumentCreationSource} from '../../analysis-engines/test/creation-fixtures.mjs';
import {scalarCastsSource} from '../../analysis-engines/test/scalar-casts-fixtures.mjs';
import {evaluateReturn} from './expression_model_eval.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),word=n=>BigInt(n).toString(16).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-model-closure')+'/creation-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const host=process.env.CONTRACT_CLOSURE_ENGINE_MODE==='host',image=host?null:process.env.CONTRACT_TEST_IMAGE,checks=[],runs=[];
if(!host&&!/^sha256:[0-9a-f]{64}$/.test(image||''))throw new Error('EXACT_CANDIDATE_IMAGE_REQUIRED');
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({checks,runs,image,execution_mode:host?'candidate_source_with_frozen_solver':'frozen_candidate_image',solver_image:host?process.env.CONTRACT_TEST_IMAGE:null,network:'local_anvil',complete:scenario==='creation-runtime-complete'&&ok,whole_goal_complete:false},null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok){console.log(JSON.stringify(evidence)?.slice(0,3000));throw new Error(scenario);}}
let sequence=0;async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18772',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(20000)}),body=await r.json();if(body.error)throw new Error(JSON.stringify(body.error));return body.result;}
const admin='0x'+'1'.repeat(40),user='0x'+'3'.repeat(40);
async function engine(input){if(host)return input.operation==='replay'?replay(input):analyze(input);const r=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});if(r.status!==0)throw new Error('FROZEN_ENGINE_FAILED:'+r.stderr.slice(-1500));return JSON.parse(r.stdout);}
async function deploy(source,optimized,viaIR,label,args=[],{ledger=true}={}){
 const fixture=compileVerificationFixture(optimized,source,'shanghai',{viaIR}),tx=await rpc('eth_sendTransaction',[{from:admin,data:'0x'+fixture.compiled.evm.bytecode.object+args.map(word).join(''),value:'0x0',gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);check(label+'-deploy',receipt.status==='0x1');
 const address=receipt.contractAddress,code=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]),parent=await rpc('eth_getBlockByNumber',['0x'+(BigInt(block.number)-1n).toString(16),false]);
 const input={...fixture,compiled:undefined,output:undefined,bytecode:code,source:{...fixture.source,runtimeBytecode:{...fixture.source.runtimeBytecode,onchainBytecode:code}},snapshot:{...fixture.snapshot,entry_address:address,code_address:address,storage_address:address,code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash,evm_rules:'shanghai'}};
 const model=await engine(input);check(label+'-source-identity',model.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed'),model.sections.engines);
 const initial=model.sections.initial_state_models[0],policy=model.sections.protocol_invariants.find(m=>m.kind==='erc20_source_initialization');check(label+(ledger?'-source-initialization-and-ledger-proof':'-source-initialization-model'),initial?.complete&&(!ledger||policy?.complete),{initial:initial?.unresolved,policy});
 const creation={receipt,transaction:await rpc('eth_getTransactionByHash',[tx]),block,parent_block:parent,trace:await rpc('debug_traceTransaction',[tx,{tracer:'callTracer'}]),prestate:await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer'}]),state_diff:await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),runtime_bytecode:code};
 // Record an absent creation target from the actual parent state so the
 // offline fork can check CREATE collision rules without an invented account.
 if(!creation.prestate[address])creation.prestate[address]={balance:await rpc('eth_getBalance',[address,parent.number]),nonce:await rpc('eth_getTransactionCount',[address,parent.number]),code:await rpc('eth_getCode',[address,parent.number]),storage:{}};
 const witness={raw_transaction:creation.transaction,raw_receipt:receipt,raw_block:block,parent_block:parent,prestate:creation.prestate,state_diff:creation.state_diff,call_trace:creation.trace,hardfork:'shanghai',simulation:true};
 const replayed=await engine({operation:'replay',snapshot:{...input.snapshot,state_granularity:'transaction_prestate'},witness}),row=replayed.sections.replays[0];
 check(label+'-isolated-creation-replay',row?.complete&&row.original_call_trace_matches&&row.observed.contract_address===address&&row.replayed.contract_address===address,{status:row?.status,differences:row?.differences,gaps:row?.witness_gaps,error:row?.error});
 writeFileSync(new URL(label+'-binding-case.json',out),JSON.stringify({snapshot:input.snapshot,model:initial,witness,replay:replayed}));
 writeFileSync(new URL(label+'-replay.json',out),JSON.stringify(replayed));
 writeFileSync(new URL(label+'-analysis.json',out),JSON.stringify(model));writeFileSync(new URL(label+'-input.json',out),JSON.stringify(input));writeFileSync(new URL(label+'-creation.json',out),JSON.stringify(creation));runs.push({label,address,tx_hash:tx,code_hash:input.snapshot.code_hash});return{address,model,creation,input};
}
async function view(item,signature,args=[]){return BigInt(await rpc('eth_call',[{to:item.address,data:selector(signature)+args.map(word).join('')},'latest']));}
const binary=process.env.ANVIL_BINARY||fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url)),anvil=spawn(binary,['--host','127.0.0.1','--port','18772','--accounts','0','--threads','2','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('creation-local-evm-ready',ready);await rpc('anvil_setBalance',[admin,'0x'+(10n**30n).toString(16)]);
 for(const optimized of [false,true])for(const viaIR of [false,true]){
  const label='initializers-'+optimized+'-ir-'+viaIR,item=await deploy(inheritedInitializationSource,optimized,viaIR,label),expected=viaIR?42n:1n;
  check(label+'-compiler-order-on-evm',await view(item,'x()')===42n&&await view(item,'y()')===expected&&await view(item,'balanceOf(address)',[admin])===expected&&await view(item,'totalSupply()')===expected,{tx_hash:item.creation.receipt.transactionHash,expected_y:String(expected)});
  const diamond=await deploy(diamondCreationSource,optimized,viaIR,'diamond-'+optimized+'-ir-'+viaIR,[17n]);
  check('diamond-'+optimized+'-ir-'+viaIR+'-real-constructor-order-and-arguments',await view(diamond,'marker()')===1234n&&await view(diamond,'totalSupply()')===37n&&await view(diamond,'balanceOf(address)',[admin])===37n);
  const immutable=await deploy(immutableCreationSource,optimized,viaIR,'immutable-'+optimized+'-ir-'+viaIR);
  check('immutable-'+optimized+'-ir-'+viaIR+'-runtime-bound-values',await view(immutable,'owner()')===BigInt(admin)&&await view(immutable,'deploymentChain()')===1n&&immutable.model.sections.authorization_models[0]?.complete&&immutable.model.sections.state_transitions.every(t=>t.complete));
  const stateArgument=await deploy(stateArgumentCreationSource,optimized,viaIR,'state-argument-'+optimized+'-ir-'+viaIR),argument=await view(stateArgument,'argument()');
  check('state-argument-'+optimized+'-ir-'+viaIR+'-actual-evaluation-point',argument===(viaIR?0n:7n)&&await view(stateArgument,'sourceValue()')===7n,{argument:String(argument)});
  const castLabel='scalar-casts-'+optimized+'-ir-'+viaIR,casts=await deploy(scalarCastsSource,optimized,viaIR,castLabel,[],{ledger:false});
  for(const x of[0n,1n,0x123456789abcdef0n,(1n<<128n)-1n]){
   for(const name of['widen','local','assigned','passed']){
    const actual=await view(casts,name+'(bytes16)',[x<<128n]),modeled=evaluateReturn(casts.model,name,{x})[0];check(castLabel+'-'+name+'-'+x,actual===modeled&&actual===x<<128n);
   }
   const narrowRaw=await view(casts,'narrow(bytes32)',[(x<<128n)+99n]);check(castLabel+'-narrow-'+x,(narrowRaw>>128n)===evaluateReturn(casts.model,'narrow',{x:(x<<128n)+99n})[0]&&(narrowRaw&((1n<<128n)-1n))===0n);
   const tuple=await rpc('eth_call',[{to:casts.address,data:selector('tupleValue(bytes16)')+word(x<<128n)},'latest']),words=tuple.slice(2).match(/.{64}/g).map(w=>BigInt('0x'+w)),modeledTuple=evaluateReturn(casts.model,'tupleValue',{x});
   check(castLabel+'-tuple-'+x,words[0]===modeledTuple[0]&&(words[1]>>128n)===modeledTuple[1]);
   for(const c of[true,false]){
    const actual=await view(casts,'conditional(bool,bytes16,bytes32)',[c?1n:0n,x<<128n,7n]);check(castLabel+'-conditional-'+c+'-'+x,actual===evaluateReturn(casts.model,'conditional',{c,a:x,b:7n})[0]);
   }
   check(castLabel+'-comparison-'+x,await view(casts,'equal(bytes16,bytes32)',[x<<128n,x<<128n])===1n&&evaluateReturn(casts.model,'equal',{a:x,b:x<<128n})[0]===true);
  }
  check(castLabel+'-lazy-unselected-zero-division',await view(casts,'lazy(bool,uint256)',[1n,0n])===1n&&evaluateReturn(casts.model,'lazy',{c:true,denominator:0n})[0]===1n);
  let failure;try{await view(casts,'lazy(bool,uint256)',[0n,0n]);}catch(e){failure=e.message;}check(castLabel+'-selected-zero-division-panics',failure?.toLowerCase().includes('4e487b71'+word(18n)));
  const key=0x123456789abcdef0n,base=casts.model.sections.storage.find(s=>s.variable==='buckets')?.slot,slot=hash(Buffer.from(word(key<<128n)+word(base),'hex'));
  const set=await rpc('eth_sendTransaction',[{from:admin,to:casts.address,data:selector('set(bytes16,uint256)')+word(key<<128n)+word(9n),gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');
  check(castLabel+'-physical-mapping-key-padding',BigInt(await rpc('eth_getStorageAt',[casts.address,slot,'latest']))===9n&&await view(casts,'buckets(bytes16)',[key<<128n])===9n,{tx_hash:set,key:String(key),slot});
  const bypassCaller='0x'+(((BigInt(admin)&((1n<<64n)-1n))<<96n).toString(16).padStart(40,'0'));await rpc('anvil_setBalance',[bypassCaller,'0x'+(10n**30n).toString(16)]);
  const bypass=await rpc('eth_sendTransaction',[{from:bypassCaller,to:casts.address,data:selector('seize(address)')+word(user),gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const bypassReceipt=await rpc('eth_getTransactionReceipt',[bypass]);
  check(castLabel+'-cast-permission-bypass-reproduces',casts.model.sections.authorization_models[0]?.status==='counterexample'&&bypassCaller!==admin&&bypassReceipt.status==='0x1'&&await view(casts,'owner()')===BigInt(user),{tx_hash:bypass,caller:bypassCaller,prior_owner:admin,next_owner:user});
  writeFileSync(new URL(castLabel+'-execution.json',out),JSON.stringify({set:{tx_hash:set,slot,value:'9'},bypass:{receipt:bypassReceipt,trace:await rpc('debug_traceTransaction',[bypass,{tracer:'callTracer'}]),state_diff:await rpc('debug_traceTransaction',[bypass,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}])}}));
 }
 check('creation-runtime-complete',true);
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');}
