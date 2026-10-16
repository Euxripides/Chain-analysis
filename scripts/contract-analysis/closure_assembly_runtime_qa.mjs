import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {writeFileSync,mkdirSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {analyze} from '../../analysis-engines/analyze.mjs';
import {buildOpcodeWitness} from '../../analysis-engines/opcode-witness.mjs';
import {compileVerificationFixture,rolesSource} from '../../analysis-engines/test/verification-fixtures.mjs';
import {ADDRESS_MASK,assemblyLedgerSource} from '../../analysis-engines/test/assembly-storage-fixtures.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),W=1n<<256n;
const word=n=>((BigInt(n)%W+W)%W).toString(16).padStart(64,'0'),mapping=(key,slot)=>hash(Buffer.from(word(key)+word(slot),'hex'));
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-model-closure')+'/assembly-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const host=process.env.CONTRACT_CLOSURE_ENGINE_MODE==='host',image=host?null:process.env.CONTRACT_TEST_IMAGE,checks=[],runs=[];
if(!host&&!/^sha256:[0-9a-f]{64}$/.test(image||''))throw new Error('EXACT_CANDIDATE_IMAGE_REQUIRED');
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({checks,runs,image,
 execution_mode:host?'candidate_source_with_frozen_solver':'frozen_candidate_image',solver_image:host?process.env.CONTRACT_TEST_IMAGE:null,network:'local_anvil',
 complete:scenario==='assembly-runtime-complete'&&ok,whole_goal_complete:false},null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok){console.log(JSON.stringify(evidence)?.slice(0,3000));throw new Error(scenario);}}
let sequence=0;
async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18771',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(20000)}),body=await r.json();if(body.error)throw new Error(JSON.stringify(body.error));return body.result;}
const admin='0x'+'1'.repeat(40),user='0x'+'3'.repeat(40),recipient='0x'+'4'.repeat(40);
async function send(to,data,from=admin){const tx=await rpc('eth_sendTransaction',[{from,...(to?{to}:{}),data,value:'0x0',gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');return rpc('eth_getTransactionReceipt',[tx]);}
function engine(input){if(host)return analyze(input);const r=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});if(r.status!==0)throw new Error('FROZEN_ENGINE_FAILED:'+r.stderr.slice(-1500));return JSON.parse(r.stdout);}
async function deploy(source,optimized,label){
 const fixture=compileVerificationFixture(optimized,source,'cancun'),receipt=await send(null,'0x'+fixture.compiled.evm.bytecode.object);check(label+'-deploy',receipt.status==='0x1');
 const address=receipt.contractAddress,code=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
 const input={...fixture,compiled:undefined,output:undefined,bytecode:code,snapshot:{...fixture.snapshot,entry_address:address,code_address:address,storage_address:address,
  code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash,evm_rules:'cancun'}};
 const model=engine(input);check(label+'-source-identity',code==='0x'+fixture.compiled.evm.deployedBytecode.object&&model.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed'));
 writeFileSync(new URL(label+'-analysis.json',out),JSON.stringify(model));writeFileSync(new URL(label+'-input.json',out),JSON.stringify(input));
 const creation={receipt,transaction:await rpc('eth_getTransactionByHash',[receipt.transactionHash]),block,trace:await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'callTracer'}]),prestate:await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer'}]),state_diff:await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),runtime_bytecode:code};
 writeFileSync(new URL(label+'-creation.json',out),JSON.stringify(creation));runs.push({label,address,code_hash:input.snapshot.code_hash});return {label,address,fixture,model,input};
}
const data=(signature,args)=>selector(signature)+args.map(word).join('');
async function view(item,signature,args=[]){return BigInt(await rpc('eth_call',[{to:item.address,data:data(signature,args)},'latest']));}
async function capture(item,signature,args,label,from=admin){
 const receipt=await send(item.address,data(signature,args),from),parent='0x'+(BigInt(receipt.blockNumber)-1n).toString(16);
 const trace=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'callTracer'}]),prestate=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer'}]),diff=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),steps=await rpc('debug_traceTransaction',[receipt.transactionHash,{disableStorage:true,enableMemory:false,disableStack:false,limit:20001}]);
 const slots=[...new Set(steps.structLogs.filter(l=>['SLOAD','SSTORE'].includes(l.op)).map(l=>'0x'+l.stack.at(-1).replace(/^0x/,'').padStart(64,'0')))];
 prestate[item.address]||={};prestate[item.address].code=await rpc('eth_getCode',[item.address,parent]);prestate[item.address].storage||={};
 const reads=[];for(const slot of slots){const value=await rpc('eth_getStorageAt',[item.address,slot,parent]);prestate[item.address].storage[slot]=value;reads.push({address:item.address,slot,value,block_number:parent});}
 const witness=buildOpcodeWitness(steps,trace,{txHash:receipt.transactionHash,prestate}),record={receipt,trace,prestate,diff,steps,witness,exact_parent_storage_reads:reads};
 writeFileSync(new URL(label+'-execution.json',out),JSON.stringify(record));check(label+'-actual-opcode-storage-and-memory',witness.complete,{gaps:witness.gaps,memory_gaps:witness.memory_gaps,storage_gaps:witness.storage_gaps});return record;
}
const binary=process.env.ANVIL_BINARY||fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url)),anvil=spawn(binary,['--host','127.0.0.1','--port','18771','--accounts','0','--threads','2','--chain-id','1','--hardfork','cancun','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('assembly-local-evm-ready',ready);
 for(const address of [admin,user,recipient])await rpc('anvil_setBalance',[address,'0x'+(10n**30n).toString(16)]);
 for(const optimized of [false,true])for(const copied of [false,true]){
  const label='ledger-'+optimized+'-copy-'+copied,item=await deploy(assemblyLedgerSource({copied}),optimized,label),invariant=item.model.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');
  check(label+'-all-entries-ledger-and-owner-proved',invariant?.complete&&item.model.sections.authorization_models[0]?.complete&&item.model.sections.state_transitions.every(t=>t.complete));
  const base=item.model.sections.storage.find(s=>s.variable==='balances')?.slot,supply=item.model.sections.storage.find(s=>s.variable==='totalSupply')?.slot;check(label+'-compiler-storage-layout',base!==null&&base!==undefined&&supply!==null&&supply!==undefined);
  const mint=await capture(item,'mint(address,uint256)',[user,100n],label+'-mint'),slot=mapping(user,base);
  check(label+'-mint-hash-slot-and-getters',mint.receipt.status==='0x1'&&mint.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&e.slot===slot&&e.value_raw==='100')&&await view(item,'balanceOf(address)',[user])===100n&&await view(item,'totalSupply()')===100n,{tx_hash:mint.receipt.transactionHash,mapping_base:base,slot});
  const transfer=await capture(item,'transfer(address,uint256)',[recipient,23n],label+'-transfer',user);
  check(label+'-transfer-all-balances',transfer.receipt.status==='0x1'&&await view(item,'balanceOf(address)',[user])===77n&&await view(item,'balanceOf(address)',[recipient])===23n&&await view(item,'totalSupply()')===100n);
  const self=await capture(item,'transfer(address,uint256)',[user,11n],label+'-self',user);check(label+'-aliased-self-transfer',self.receipt.status==='0x1'&&await view(item,'balanceOf(address)',[user])===77n);
  const forbidden=await capture(item,'mint(address,uint256)',[user,1n],label+'-unauthorized',user);check(label+'-unauthorized-rollback',forbidden.receipt.status==='0x0'&&await view(item,'balanceOf(address)',[user])===77n&&await view(item,'totalSupply()')===100n);
  const overflow=await capture(item,'mint(address,uint256)',[user,W-1n],label+'-overflow');check(label+'-overflow-rollback',overflow.receipt.status==='0x0'&&await view(item,'totalSupply()')===100n);
 }
 for(const optimized of [false,true]){
  const label='broken-'+optimized,item=await deploy(assemblyLedgerSource({broken:true}),optimized,label);check(label+'-solver-finds-issuance-defect',item.model.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation')?.status==='counterexample');
  const mint=await capture(item,'mint(address,uint256)',[user,7n],label+'-mint');check(label+'-real-issuance-defect',mint.receipt.status==='0x1'&&await view(item,'balanceOf(address)',[user])===7n&&await view(item,'totalSupply()')===8n,{tx_hash:mint.receipt.transactionHash});
 }
 for(const [label,settings]of [['dirty-key',{key:'to'}],['base-byte-overwrite',{between:'mstore8(63,127)'}],['key-byte-overwrite',{between:'mstore8(31,127)'}]]){
  const item=await deploy(assemblyLedgerSource(settings),true,label),model=item.model.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');check(label+'-does-not-close-ledger',model?.complete===false&&model.unresolved.some(s=>s.includes('UNCLASSIFIED_RAW_STORAGE_WRITE')));
  const mint=await capture(item,'mint(address,uint256)',[user,7n],label+'-mint'),balance=await view(item,'balanceOf(address)',[user]);
  check(label+'-actual-state-recorded',mint.receipt.status==='0x1'&&balance===(label==='dirty-key'?7n:0n)&&await view(item,'totalSupply()')===7n,{tx_hash:mint.receipt.transactionHash,balance:String(balance)});
 }
 const abort=await deploy(assemblyLedgerSource({extra:`function abort(address to,uint256 value)external{assembly{mstore(0,and(to,${ADDRESS_MASK}))mstore(32,balances.slot)sstore(keccak256(0,64),value)revert(0,0)}}`}),true,'abort');
 const rejected=await capture(abort,'abort(address,uint256)',[user,9n],'abort-after-write');check('raw-storage-writes-roll-back-on-revert',rejected.receipt.status==='0x0'&&rejected.witness.storage_effects.some(e=>e.opcode==='SSTORE'&&!e.settled)&&await view(abort,'balanceOf(address)',[user])===0n);
 const rawGrant=`function rawGrant(bytes32 role,address account)external{require(hasRole(getRoleAdmin(role),msg.sender));assembly{mstore(0,role)mstore(32,roles.slot)let parent:=keccak256(0,64)mstore(0,and(account,${ADDRESS_MASK}))mstore(32,parent)sstore(keccak256(0,64),1)}}`;
 for(const broken of [false,true]){
  const label='roles-'+broken,source=rolesSource.replace('contract Verification {','contract Verification {'+(broken?rawGrant.replace('require(hasRole(getRoleAdmin(role),msg.sender));',''):rawGrant)),item=await deploy(source,true,label);
  check(label+'-admin-proof-result',broken?item.model.sections.authorization_models[0]?.status==='counterexample':item.model.sections.authorization_models[0]?.complete);
  const root=item.model.sections.storage.find(s=>s.variable==='roles')?.slot;check(label+'-role-layout',root!==undefined&&root!==null);
  const seed=mapping(admin,mapping(0n,root));await rpc('anvil_setStorageAt',[item.address,seed,'0x'+word(1n)]);writeFileSync(new URL(label+'-declared-entry-state.json',out),JSON.stringify({address:item.address,role:word(0n),account:admin,slot:seed,value:'1',source:'local_anvil_declared_entry_state'}));
  const grant=await capture(item,'rawGrant(bytes32,address)',[17n,user],label+'-grant',broken?user:admin);check(label+'-nested-membership-write',grant.receipt.status==='0x1'&&await view(item,'hasRole(bytes32,address)',[17n,user])===1n);
  const unauthorized=await capture(item,'rawGrant(bytes32,address)',[18n,recipient],label+'-unauthorized',user);check(label+'-real-authorization-result',unauthorized.receipt.status===(broken?'0x1':'0x0')&&await view(item,'hasRole(bytes32,address)',[18n,recipient])===(broken?1n:0n));
 }
 check('assembly-runtime-complete',true);
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');}
