import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {mkdirSync,writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {analyze} from '../../analysis-engines/analyze.mjs';
import {replay} from '../../analysis-engines/replay.mjs';
import {compileVerificationFixture} from '../../analysis-engines/test/verification-fixtures.mjs';
import {factorySource} from '../../analysis-engines/test/factory-fixtures.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),word=n=>BigInt(n).toString(16).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-dependency-models')+'/factory-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const host=process.env.CONTRACT_DEPENDENCY_ENGINE_MODE!=='frozen',image=process.env.CONTRACT_TEST_IMAGE,checks=[],runs=[];
if(!/^sha256:[0-9a-f]{64}$/.test(image||''))throw new Error('EXACT_CANDIDATE_OR_SOLVER_IMAGE_REQUIRED');
function save(){writeFileSync(new URL('report.json',out),JSON.stringify({checks,runs,image,execution_mode:host?'candidate_source_with_frozen_solver':'frozen_candidate_image',network:'local_anvil',complete:checks.at(-1)?.scenario==='factory-runtime-complete'&&checks.at(-1)?.status==='PASS',whole_goal_complete:false},null,2));}
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});save();console.log(scenario,ok?'PASS':'FAIL');if(!ok){console.log(JSON.stringify(evidence)?.slice(0,5000));throw new Error(scenario);}}
let sequence=0;async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:18775',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(25000)}),body=await response.json();if(body.error)throw new Error(method+':'+JSON.stringify(body.error));return body.result;}
const admin='0x'+'1'.repeat(40),gas={gas:'0x989680',gasPrice:'0x77359400'};
async function send(data,to){const tx=await rpc('eth_sendTransaction',[{from:admin,data,...gas,...(to?{to}:{})}]);await rpc('evm_mine');return{tx,receipt:await rpc('eth_getTransactionReceipt',[tx])};}
async function engine(input){if(host)return input.operation==='replay'?replay(input):analyze(input);const r=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});if(r.status!==0)throw new Error('FROZEN_ENGINE_FAILED:'+r.stderr.slice(-2000));return JSON.parse(r.stdout);}
function creationFrames(root){const rows=[];function walk(frame,path='0',settled=true){settled=settled&&!frame.error;if(['CREATE','CREATE2'].includes(frame.type))rows.push({frame,path,settled});(frame.calls||[]).forEach((c,i)=>walk(c,path+'.'+i,settled));}walk(root);return rows;}
async function witnessFor(item){
 const {tx,receipt}=item,transaction=await rpc('eth_getTransactionByHash',[tx]),block=await rpc('eth_getBlockByHash',[receipt.blockHash,false]),parent=await rpc('eth_getBlockByHash',[block.parentHash,false]);
 const trace=await rpc('debug_traceTransaction',[tx,{tracer:'callTracer'}]),prestate=await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer'}]),diff=await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]);
 // Query real parent values for absent creation accounts and touched slots.
 // Missing witness data is never replaced with an assumed empty account.
 const addresses=new Set();function walk(frame){for(const key of['from','to'])if(/^0x[0-9a-f]{40}$/i.test(frame[key]||''))addresses.add(frame[key].toLowerCase());(frame.calls||[]).forEach(walk);}walk(trace);
 for(const address of addresses){
  const key=Object.keys(prestate).find(a=>a.toLowerCase()===address)||address;
  if(!prestate[key])prestate[key]={balance:await rpc('eth_getBalance',[address,parent.number]),nonce:await rpc('eth_getTransactionCount',[address,parent.number]),code:await rpc('eth_getCode',[address,parent.number]),storage:{}};
  const state=prestate[key];state.storage||={};
  for(const table of[diff.pre,diff.post])for(const[a,value]of Object.entries(table||{}))if(a.toLowerCase()===address)for(const slot of Object.keys(value.storage||{}))if(!Object.keys(state.storage).some(k=>BigInt(k)===BigInt(slot)))state.storage[slot]=await rpc('eth_getStorageAt',[address,slot,parent.number]);
 }
 return{raw_transaction:transaction,raw_receipt:receipt,raw_block:block,parent_block:parent,prestate,state_diff:diff,call_trace:trace,hardfork:'shanghai',simulation:true};
}
async function sample(fixture,item,label,{creator,delegate=false,creationPhase=false,seed=17n,mutated=false,expectedError=null}={}){
 const witness=await witnessFor(item),frames=creationFrames(witness.call_trace),live=frames.filter(f=>f.settled&&f.path!=='0');
 const selected=expectedError?frames.find(f=>f.path!=='0'):live.at(-1);check(label+'-actual-frame',Boolean(selected)&&(!expectedError?live.length===1:true),frames.map(f=>({path:f.path,to:f.frame.to,settled:f.settled})));
 const address=selected.frame.to,code=expectedError?'0x'+fixture.compiled.evm.deployedBytecode.object:await rpc('eth_getCode',[address,item.receipt.blockNumber]);
 const input={...fixture,compiled:undefined,output:undefined,bytecode:code,source:{...fixture.source,runtimeBytecode:{...fixture.source.runtimeBytecode,onchainBytecode:code}},snapshot:{...fixture.snapshot,entry_address:address,code_address:address,storage_address:address,code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(witness.raw_block.number)),block_hash:witness.raw_block.hash,evm_rules:'shanghai'}};
 const analyzed=await engine(input),initial=analyzed.sections.initial_state_models[0];check(label+'-recompiled-child-model',initial?.complete&&analyzed.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed'),{initial:initial?.unresolved,engines:analyzed.sections.engines});
 const executed=await engine({operation:'replay',snapshot:{...input.snapshot,state_granularity:'transaction_prestate'},witness}),row=executed.sections.replays[0];
 check(label+'-actual-isolated-replay',row?.complete&&row.original_call_trace_matches,{status:row?.status,gaps:row?.witness_gaps,differences:row?.differences,error:row?.error});
 const opcode=executed.sections.execution_callsite_witnesses.find(c=>c.trace_path===selected.path);
 if(!expectedError){
  check(label+'-exact-create-instruction',opcode?.execution_opcode_identity_verified&&opcode.settled&&opcode.target===address&&opcode.caller_storage_address===creator,{opcode,gaps:executed.sections.opcode_witnesses[0]?.gaps});
  if(delegate)check(label+'-delegate-storage-creator',opcode.caller_code_address!==creator&&opcode.caller_storage_address===creator&&selected.path==='0.0.0',opcode);
  if(creationPhase)check(label+'-creation-phase-code-identity',opcode.caller_code_phase==='creation'&&opcode.verified===false&&opcode.execution_opcode_identity_verified,opcode);
  if(opcode.call_type==='CREATE2')check(label+'-create2-address-and-salt',opcode.derived_create2_address===address&&opcode.create2_salt_raw==='7',opcode);
  const owner=await rpc('eth_call',[{to:address,data:selector('owner()')},item.receipt.blockNumber]),supply=await rpc('eth_call',[{to:address,data:selector('totalSupply()')},item.receipt.blockNumber]);
  check(label+'-actual-owner-and-state',BigInt(owner)===BigInt(creator)&&BigInt(supply)===seed+(mutated?10n:0n),{owner,supply,constructor_seed:String(seed),mutation_after_creation:mutated});
  if(label.startsWith('caught'))check(label+'-ancestor-rollback-excluded',frames.some(f=>!f.settled&&f.frame.to===address)&&frames.filter(f=>f.settled&&f.frame.to===address).length===1);
 }
 const corpus={snapshot:input.snapshot,model:initial,witness,replay:executed,expected_error:expectedError,expected_scope:'one_recompiled_and_replayed_factory_creation_frame',expected_creator:creator,mutation_after_creation:mutated};
 writeFileSync(new URL(label+'-binding-case.json',out),JSON.stringify(corpus));writeFileSync(new URL(label+'-analysis.json',out),JSON.stringify(analyzed));
 runs.push({label,address,tx_hash:item.tx,trace_path:selected.path,creation_type:selected.frame.type,creator,expected_error:expectedError});save();
 return address;
}
const binary=process.env.ANVIL_BINARY||fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url)),anvil=spawn(binary,['--host','127.0.0.1','--port','18775','--accounts','0','--threads','2','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
let diagnostics='';anvil.stderr.on('data',b=>{if(diagnostics.length<8192)diagnostics+=b.toString();});
try{
 let ready=false;for(let i=0;i<100;i++){if(anvil.exitCode!==null)throw new Error('ANVIL_EXIT:'+diagnostics);try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('factory-local-evm-ready',ready);await rpc('anvil_setBalance',[admin,'0x'+(10n**30n).toString(16)]);
 const variants=process.env.CONTRACT_FACTORY_QUICK==='1'?[[true,false]]:[[false,false],[true,false],[false,true],[true,true]];
 for(const[optimized,viaIR]of variants){
  const fixture=compileVerificationFixture(optimized,factorySource,'shanghai',{viaIR}),suffix='-'+optimized+'-ir-'+viaIR;
  const compiled=name=>fixture.output.contracts['Verification.sol'][name],factory=(await send('0x'+compiled('Factory').evm.bytecode.object)).receipt.contractAddress;
  const delegate=(await send('0x'+compiled('DelegateFactory').evm.bytecode.object+word(factory))).receipt.contractAddress;
  for(const[name,signature,args,options]of[
   ['create','create(uint256)',[17n],{}],['create2','create2(uint256,bytes32)',[17n,7n],{}],
   ['mutate','createAndMutate(uint256)',[17n],{mutated:true}],['caught','caught(uint256)',[16n],{}],
   ['delegate-create','create(uint256)',[17n],{delegate:true}],['delegate-create2','create2(uint256,bytes32)',[17n,7n],{delegate:true}],
   ['ancestor-revert','aborted(uint256)',[17n],{expectedError:'CREATION_TRANSACTION_NOT_SUCCESSFUL'}]
  ]){
   const creator=options.delegate?delegate:factory,item=await send(selector(signature)+args.map(word).join(''),creator);
   check(name+suffix+'-transaction-status',item.receipt.status===(options.expectedError?'0x0':'0x1'));
   await sample(fixture,item,name+suffix,{creator,...options});
  }
  const item=await send('0x'+compiled('ConstructorFactory').evm.bytecode.object+word(17n));check('constructor'+suffix+'-transaction-status',item.receipt.status==='0x1');
  await sample(fixture,item,'constructor'+suffix,{creator:item.receipt.contractAddress,creationPhase:true});
 }
 check('factory-runtime-complete',true);
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');}
