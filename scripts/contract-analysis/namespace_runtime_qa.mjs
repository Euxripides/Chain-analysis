import{spawn,spawnSync}from'node:child_process';import{once}from'node:events';import{writeFileSync,mkdirSync}from'node:fs';import{createRequire}from'node:module';import{createHash}from'node:crypto';
import{compileVerificationFixture,rolesSource}from'../../analysis-engines/test/verification-fixtures.mjs';import{erc7201Root}from'../../analysis-engines/namespace-layout.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js'),hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),word=v=>BigInt(v).toString(16).padStart(64,'0');
const ROOT=0x183a6125c38840424c4a85fa12bab2ab606c4b6d0e7cc73c0c06ba5300eab500n,AUX=erc7201Root('example.aux'),WRONG=BigInt('0x'+'2'.repeat(64)),MASK=(1n<<160n)-1n;
const out=(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-namespaced-storage/frozen-validation')+'/namespace-runtime';mkdirSync(out,{recursive:true});
const image=process.env.CONTRACT_TEST_IMAGE;if(!/^sha256:[0-9a-f]{64}$/.test(image||''))throw Error('EXACT_FROZEN_IMAGE_REQUIRED');
const checks=[],runs=[],admin='0x'+'1'.repeat(40),other='0x'+'2'.repeat(40),beneficiary='0x'+'3'.repeat(40),gas={gas:'0x989680',gasPrice:'0x77359400'};
function save(){writeFileSync(out+'/report.json',JSON.stringify({checks,runs,image,execution_mode:'frozen_candidate_image',network:'local_anvil',complete:checks.at(-1)?.scenario==='namespace-runtime-complete'&&checks.at(-1).status==='PASS',whole_goal_complete:false},null,2));}
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});save();console.log(scenario,ok?'PASS':'FAIL');if(!ok){console.log(JSON.stringify(evidence));throw Error(scenario);}}
let sequence=0;async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:18782',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(25000)}),body=await response.json();if(body.error)throw Error(method+':'+body.error.message);return body.result;}
const data=(signature,values=[])=>selector(signature)+values.map(word).join('');
async function send(bytes,to,from=admin){const tx=await rpc('eth_sendTransaction',[{from,data:bytes,...gas,...(to?{to}:{})}]);await rpc('evm_mine');return{tx,receipt:await rpc('eth_getTransactionReceipt',[tx])};}
const read=(address,signature,values=[])=>rpc('eth_call',[{to:address,data:data(signature,values)},'latest']).then(BigInt),slot=(address,key)=>rpc('eth_getStorageAt',[address,'0x'+word(key),'latest']).then(BigInt);
function engine(input){const r=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:200000,maxBuffer:64<<20});if(r.status!==0)throw Error('FROZEN_ENGINE_FAILED:'+r.stderr.slice(-2000));return JSON.parse(r.stdout);}
async function replay(item,snapshot,label){
 const tx=await rpc('eth_getTransactionByHash',[item.tx]),block=await rpc('eth_getBlockByHash',[item.receipt.blockHash,false]),parent=await rpc('eth_getBlockByHash',[block.parentHash,false]),witness={raw_transaction:tx,raw_receipt:item.receipt,raw_block:block,parent_block:parent,
  prestate:await rpc('debug_traceTransaction',[item.tx,{tracer:'prestateTracer',tracerConfig:{includeEmpty:true}}]),state_diff:await rpc('debug_traceTransaction',[item.tx,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),call_trace:await rpc('debug_traceTransaction',[item.tx,{tracer:'callTracer'}]),hardfork:'shanghai',simulation:true};
 const result=engine({operation:'replay',snapshot:{...snapshot,block_number:Number(BigInt(block.number)),block_hash:block.hash,state_granularity:'transaction_prestate'},witness}),row=result.sections.replays[0];
 writeFileSync(out+'/'+label+'-witness.json',JSON.stringify(witness));writeFileSync(out+'/'+label+'-replay.json',JSON.stringify(result));
 check(label+'-complete-real-transaction-replay',row.complete&&row.original_call_trace_matches&&row.state_diff_complete&&row.differences.length===0,{status:row.status,gaps:row.witness_gaps,differences:row.differences,error:row.error});
 check(label+'-physical-effects-and-full-diff-file',result.sections.execution_storage_effects.length>0&&result.sections.artifacts.some(a=>a.kind==='transaction_state_diff'));
}
const storage=`/// @custom:storage-location erc7201:example.main
struct Store{address owner;uint8 initialized;mapping(address=>uint)balances;uint total;Config cfg;}struct Config{uint64 limit;address sink;}
bytes32 constant ROOT=0x${word(ROOT)};function store()internal pure virtual returns(Store storage $){assembly{$.slot:=ROOT}}
/// @custom:storage-location erc7201:example.aux
struct Aux{address owner;uint64 nonce;}bytes32 constant AUX=0x${word(AUX)};function aux()private pure returns(Aux storage $){assembly{$.slot:=AUX}}`;
const core=storage+`constructor(){Store storage $=store();$.owner=msg.sender;$.initialized=1;$.cfg.limit=7;$.cfg.sink=msg.sender;aux().owner=msg.sender;aux().nonce=9;}
function owner()external view returns(address){return store().owner;}function balanceOf(address user)external view returns(uint){return store().balances[user];}function totalSupply()external view returns(uint){return store().total;}
function transferOwnership(address next)external{Store storage $=store();require(msg.sender==$.owner);$.owner=next;}
function setFlag(uint8 value)external{store().initialized=value;}function setAuxOwner(address next)external{aux().owner=next;}
function config(uint64 limit,address sink)external{require(msg.sender==store().owner);Store storage $=store();$.cfg.limit=limit;$.cfg.sink=sink;}
function mint(address to,uint amount)external{Store storage $=store();require(msg.sender==$.owner);$.balances[to]+=amount;$.total+=amount;}
function helper(Store storage $,address to,uint amount)internal{$.balances[to]+=amount;$.total+=amount;}
function helperMint(address to,uint amount)external{require(msg.sender==store().owner);helper(store(),to,amount);}`;
const raw=`function rawMint(address to,uint amount)external{Store storage $=store();require(msg.sender==$.owner);uint next=$.balances[to]+amount;uint total=$.total+amount;assembly{mstore(0,and(to,0xffffffffffffffffffffffffffffffffffffffff))mstore(32,add(ROOT,1))sstore(keccak256(0,64),next)sstore(add(ROOT,2),total)}}`;
const roleDefinition=`/// @custom:storage-location erc7201:example.main
struct Store{mapping(bytes32=>Role)roles;}bytes32 constant ROOT=0x${word(ROOT)};function store()private pure returns(Store storage $){assembly{$.slot:=ROOT}}constructor(){store().roles[bytes32(0)].members[msg.sender]=true;}`;
const roles=rolesSource.replace('mapping(bytes32=>Role) private roles;',roleDefinition).replaceAll('return roles[','return store().roles[').replaceAll(';roles[',';store().roles[');
const wrap=body=>'pragma solidity ^0.8.24;contract Verification{'+body+'}',groups=[
 ['good',wrap(core)],['aliases',wrap(core.replace('return store().owner;','Store storage p=store();return p.owner;').replace('return store().balances[user];','Store storage p=store();return p.balances[user];').replace('return store().total;','Store storage p=store();return p.total;'))],
 ['assembly',wrap(core+raw)],['coupled',wrap(core+'function loopMint(address to,uint n,uint c)external{Store storage $=store();require(msg.sender==$.owner);uint a;uint b;for(uint i;i<n;i++){a+=c;b+=a;}$.balances[to]+=b;$.total+=b;}')],['inflation',wrap(core.replace('$.total+=amount;','$.total+=amount+1;'))],['bypass',wrap(core.replace('require(msg.sender==$.owner);$.owner=next;','$.owner=next;'))],
 ['pollution',wrap(core+'function pollute(uint value)external{assembly{sstore(ROOT,value)}}')],['bad-root',wrap(core.replace('bytes32 constant ROOT=0x'+word(ROOT),'bytes32 constant ROOT=0x'+word(WRONG)))],
 ['override','pragma solidity ^0.8.24;contract Base{'+core+'}contract Verification is Base{function store()internal pure override returns(Store storage $){assembly{$.slot:=0x'+word(WRONG)+'}}}'],['roles',roles]
];
const binary=process.env.ANVIL_BINARY;if(!binary)throw Error('PINNED_ANVIL_REQUIRED');
const anvil=spawn(binary,['--host','127.0.0.1','--port','18782','--accounts','0','--threads','2','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});let diagnostic='';anvil.stderr.on('data',b=>{if(diagnostic.length<8192)diagnostic+=b.toString();});
try{
 let ready=false;for(let i=0;i<100;i++){if(anvil.exitCode!==null)throw Error('ANVIL_EXIT:'+diagnostic);try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('namespace-local-evm-ready',ready);for(const address of[admin,other])await rpc('anvil_setBalance',[address,'0x'+(10n**30n).toString(16)]);
 for(const[optimized,viaIR]of[[false,false],[true,false],[false,true],[true,true]])for(const[group,source]of groups){
  const label=group+'-'+optimized+'-ir-'+viaIR,fixture=compileVerificationFixture(optimized,source,'shanghai',{viaIR}),created=await send('0x'+fixture.compiled.evm.bytecode.object),address=created.receipt.contractAddress;
  check(label+'-actually-deployed',created.receipt.status==='0x1'&&typeof address==='string');const code=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
  const input={...fixture,compiled:undefined,output:undefined,bytecode:code,source:{...fixture.source,runtimeBytecode:{...fixture.source.runtimeBytecode,onchainBytecode:code}},snapshot:{...fixture.snapshot,entry_address:address,code_address:address,storage_address:address,code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash,evm_rules:'shanghai'}};
  const model=engine(input);writeFileSync(out+'/'+label+'-input.json',JSON.stringify(input));writeFileSync(out+'/'+label+'-analysis.json',JSON.stringify(model));
  check(label+'-verified-original-runtime',model.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed')&&code==='0x'+fixture.compiled.evm.deployedBytecode.object);
  const bad=['bad-root','override'].includes(group),layout=JSON.parse(model.sections.artifacts.find(a=>a.kind==='storage_layout').text),metadata=layout.namespace_storage;
  check(label+'-namespace-identity-or-explicit-gap',metadata?.complete===!bad,{metadata});
  check(label+'-typed-transition-coverage',model.sections.state_transitions.length>0&&model.sections.state_transitions.every(t=>t.complete===(!bad||model.sections.methods.find(m=>m.method_id===t.method_id)?.name==='setAuxOwner')),{entries:model.sections.state_transitions.map(t=>({complete:t.complete,gaps:t.unresolved}))});
  check(label+'-whole-proof-flags-remain-open',model.sections.verification_closure[0].whole_authorization_proved===false&&model.sections.verification_closure[0].whole_protocol_economics_proved===false);
  const proofs=model.sections.artifacts.filter(a=>a.kind==='property_proof');check(label+'-proof-file-integrity',(bad||proofs.length>0)&&proofs.every(a=>createHash('sha256').update(a.text).digest('hex')===a.sha256));
  if(group==='roles'){
   const policy=model.sections.authorization_models.find(m=>m.kind==='access_control_admin_integrity');check(label+'-admin-model-proved',policy?.complete,{policy});
   const target=BigInt(hash(Buffer.from(word(other)+word(BigInt(hash(Buffer.from(word(17n)+word(ROOT),'hex')))),'hex')));
   const grant=await send(data('grantRole(bytes32,address)',[17n,other]),address);check(label+'-real-role-and-double-mapping-slot',grant.receipt.status==='0x1'&&await read(address,'hasRole(bytes32,address)',[17n,other])===1n&&await slot(address,target)===1n);
   await replay(grant,input.snapshot,label+'-grant');const denied=await send(data('grantRole(bytes32,address)',[19n,beneficiary]),address,other);check(label+'-unauthorized-grant-rolls-back',denied.receipt.status==='0x0'&&await read(address,'hasRole(bytes32,address)',[19n,beneficiary])===0n);
  }else{
   if(!bad){const owner=model.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity'),ledger=model.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');
    check(label+'-owner-proof-or-counterexample',['bypass','pollution'].includes(group)?owner?.status==='counterexample':owner?.complete,{status:owner?.status});check(label+'-ledger-proof-or-counterexample',group==='inflation'?ledger?.status==='counterexample':ledger?.complete,{status:ledger?.status});}
   const physical=bad?WRONG:ROOT;check(label+'-real-constructor-packed-and-nested-words',await slot(address,physical)===(1n<<160n)+BigInt(admin)&&await slot(address,physical+3n)===(BigInt(admin)<<64n)+7n&&await slot(address,AUX)===(9n<<160n)+BigInt(admin));
   if(bad)check(label+'-declared-root-does-not-explain-runtime-writes',await slot(address,ROOT)===0n);
   const flag=await send(data('setFlag(uint8)',[7n]),address,other);check(label+'-packed-flag-preserves-owner',flag.receipt.status==='0x1'&&await slot(address,physical)===(7n<<160n)+BigInt(admin)&&await read(address,'owner()')===BigInt(admin));
   const configuration=await send(data('config(uint64,address)',[31n,beneficiary]),address);check(label+'-nested-packed-config-offsets',configuration.receipt.status==='0x1'&&await slot(address,physical+3n)===(BigInt(beneficiary)<<64n)+31n);
   const quantity=group==='coupled'?459n:17n,mint=await send(group==='coupled'?data('loopMint(address,uint256,uint256)',[beneficiary,17n,3n]):data((group==='assembly'?'rawMint':'mint')+'(address,uint256)',[beneficiary,17n]),address),mapping=BigInt(hash(Buffer.from(word(beneficiary)+word(physical+1n),'hex')));
   check(label+'-real-ledger-getters-and-physical-hash-slot',mint.receipt.status==='0x1'&&await slot(address,mapping)===quantity&&await read(address,'balanceOf(address)',[beneficiary])===quantity&&await slot(address,physical+2n)===quantity+(group==='inflation'?1n:0n));
   if(group==='coupled')check(label+'-unbounded-coupled-invariants-discharged',model.sections.loop_invariants.length>0&&model.sections.loop_invariants.every(m=>m.complete));
   await replay(mint,input.snapshot,label+'-mint');const helper=await send(data('helperMint(address,uint256)',[beneficiary,5n]),address);check(label+'-storage-pointer-parameter-preserves-ledger',helper.receipt.status==='0x1'&&await read(address,'balanceOf(address)',[beneficiary])===quantity+5n&&await read(address,'totalSupply()')===quantity+5n+(group==='inflation'?1n:0n));
   const denied=await send(data('mint(address,uint256)',[beneficiary,1n]),address,other);check(label+'-unauthorized-mint-rolls-back',denied.receipt.status==='0x0'&&await slot(address,mapping)===quantity+5n);
   const aux=await send(data('setAuxOwner(address)',[beneficiary]),address,other);check(label+'-same-named-fields-in-distinct-namespaces',aux.receipt.status==='0x1'&&await slot(address,AUX)===(9n<<160n)+BigInt(beneficiary)&&await read(address,'owner()')===BigInt(admin));
   if(group==='bypass'){const changed=await send(data('transferOwnership(address)',[beneficiary]),address,other);check(label+'-real-unauthorized-owner-change',changed.receipt.status==='0x1'&&await read(address,'owner()')===BigInt(beneficiary));}
   if(group==='pollution'){const changed=await send(data('pollute(uint256)',[(11n<<160n)+BigInt(beneficiary)]),address,other);check(label+'-real-whole-word-owner-and-flag-pollution',changed.receipt.status==='0x1'&&await slot(address,ROOT)===(11n<<160n)+BigInt(beneficiary));await replay(changed,input.snapshot,label+'-pollute');}
  }
  runs.push({label,group,address,deployment_tx:created.tx,optimized,viaIR});save();
 }
 check('namespace-runtime-complete',true);
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');writeFileSync(out+'/anvil.log',diagnostic);}
