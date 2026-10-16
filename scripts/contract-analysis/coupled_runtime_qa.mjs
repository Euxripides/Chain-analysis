import{spawn,spawnSync}from'node:child_process';import{once}from'node:events';
import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{createRequire}from'node:module';
import{createHash}from'node:crypto';
import{compileVerificationFixture}from'../../analysis-engines/test/verification-fixtures.mjs';
import{evaluateReturn}from'./expression_model_eval.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js'),hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),word=v=>BigInt(v).toString(16).padStart(64,'0'),M=1n<<256n;
const out=(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-coupled-invariants/frozen-validation')+'/coupled-runtime';mkdirSync(out,{recursive:true});
const image=process.env.CONTRACT_TEST_IMAGE;if(!/^sha256:[0-9a-f]{64}$/.test(image||''))throw Error('EXACT_FROZEN_IMAGE_REQUIRED');
const checks=[],runs=[],admin='0x'+'1'.repeat(40),other='0x'+'2'.repeat(40),beneficiary='0x'+'3'.repeat(40),gas={gas:'0x989680',gasPrice:'0x77359400'};
function save(){writeFileSync(out+'/report.json',JSON.stringify({checks,runs,image,execution_mode:'frozen_candidate_image',network:'local_anvil',complete:checks.at(-1)?.scenario==='coupled-runtime-complete'&&checks.at(-1).status==='PASS',whole_goal_complete:false},null,2));}
function check(name,ok,evidence){checks.push({scenario:name,status:ok?'PASS':'FAIL',evidence});save();console.log(name,ok?'PASS':'FAIL');if(!ok){console.log(JSON.stringify(evidence));throw Error(name);}}
let sequence=0;async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:18781',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(25000)}),body=await response.json();if(body.error){const e=Error(method+':'+body.error.message);e.rpc_error=body.error;throw e;}return body.result;}
async function send(data,to,from=admin){const tx=await rpc('eth_sendTransaction',[{from,data,...gas,...(to?{to}:{})}]);await rpc('evm_mine');return{tx,receipt:await rpc('eth_getTransactionReceipt',[tx])};}
function engine(input){const r=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:200000,maxBuffer:64<<20});if(r.status!==0)throw Error('FROZEN_ENGINE_FAILED:'+r.stderr.slice(-2000));return JSON.parse(r.stdout);}
function decode(raw){if(!/^0x(?:[0-9a-f]{64})*$/i.test(raw))throw Error('INVALID_STATIC_ABI');return raw.slice(2).match(/.{64}/g)?.map(s=>BigInt('0x'+s))||[];}
function callData(signature,values){return selector(signature)+values.map(word).join('');}
async function compare(model,address,method,signature,inputs,label){
 const values=Object.values(inputs),data=callData(signature,values);let actual,actualError,expected,expectedError;
 try{actual=decode(await rpc('eth_call',[{from:admin,to:address,data,...gas},'latest']));}catch(e){actualError=e;}
 try{expected=evaluateReturn(model,method,inputs);}catch(e){expectedError=e;}
 if(expectedError){const errorData=actualError?.rpc_error?.data;check(label+'-source-overflow-matches-real-evm',expectedError.kind==='panic_0x11'&&actualError!==undefined&&typeof errorData==='string'&&errorData.startsWith('0x4e487b71')&&BigInt('0x'+errorData.slice(-64))===17n,{model:expectedError.kind,evm:actualError?.rpc_error});}
 else check(label+'-model-matches-real-evm',!actualError&&JSON.stringify(actual?.map(String))===JSON.stringify(expected.map(String)),{inputs:Object.fromEntries(Object.entries(inputs).map(([k,v])=>[k,String(v)])),actual:actual?.map(String),expected:expected.map(String),error:actualError?.rpc_error});
}
const ledger=broken=>'address public owner;mapping(address=>uint)public balances;uint public totalSupply;constructor(){owner=msg.sender;}function balanceOf(address user)external view returns(uint){return balances[user];}function mint(address to,uint n,uint c)external{require(msg.sender==owner);uint a;uint b;for(uint i;i<n;i++){a+=c;b+=a;}balances[to]+=b;totalSupply+=b'+(broken?'+1':'')+';}';
const groups=[
 ['ordered','function forward(uint n,uint c,uint x,uint y)external pure returns(uint a,uint b){a=x;b=y;for(uint i;i<n;i++){a+=c;b+=a;}}function reverse(uint n,uint c,uint x,uint y)external pure returns(uint a,uint b){a=x;b=y;for(uint i;i<n;i++){b+=a;a+=c;}}'],
 ['four','function calc(uint n,uint c)external pure returns(uint a,uint b,uint d,uint e){a=2;b=3;d=5;e=7;for(uint i;i<n;i++){a+=c;b+=a;d+=b;e+=d;}}'],
 ['safety','function hidden(uint n,uint c,uint initial)external pure returns(uint b){uint a=initial;for(uint i;i<n;i++){a+=c;b+=(a+i)*0;}}function unused(uint n,uint c,uint initial)external pure returns(uint b){uint a=initial;for(uint i;i<n;i++){b+=a;a+=c;}}'],
 ['nonlinear','function calc(uint n,uint c,uint initial)external pure returns(uint b){uint a=initial;for(uint i;i<n;i++){a+=c;b+=a*a;}}'],
 ['while','function calc(uint n,uint c)external pure returns(uint a,uint b){uint i;while(i<n){a+=c;b+=a;i++;}}'],
 ['narrow','function calc(uint8 n)external pure returns(uint a,uint b){for(uint8 i;i<n;i++){a+=i*i;b+=a;}}'],
 ['ledger',ledger(false)],['broken-ledger',ledger(true)]
];
async function replayTransaction(item,snapshot,label){
 const tx=await rpc('eth_getTransactionByHash',[item.tx]),block=await rpc('eth_getBlockByHash',[item.receipt.blockHash,false]),parent=await rpc('eth_getBlockByHash',[block.parentHash,false]),witness={raw_transaction:tx,raw_receipt:item.receipt,raw_block:block,parent_block:parent,
  prestate:await rpc('debug_traceTransaction',[item.tx,{tracer:'prestateTracer',tracerConfig:{includeEmpty:true}}]),state_diff:await rpc('debug_traceTransaction',[item.tx,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),
  call_trace:await rpc('debug_traceTransaction',[item.tx,{tracer:'callTracer'}]),hardfork:'shanghai',simulation:true};
 const result=engine({operation:'replay',snapshot:{...snapshot,block_number:Number(BigInt(block.number)),block_hash:block.hash,state_granularity:'transaction_prestate'},witness}),row=result.sections.replays[0];
 writeFileSync(out+'/'+label+'-witness.json',JSON.stringify(witness));writeFileSync(out+'/'+label+'-replay.json',JSON.stringify(result));
 check(label+'-real-transaction-complete-replay',row.complete&&row.original_call_trace_matches&&row.state_diff_complete&&row.differences.length===0,{status:row.status,gaps:row.witness_gaps,differences:row.differences,error:row.error});
 check(label+'-real-storage-side-effects-captured',result.sections.execution_storage_effects?.length>=2&&result.sections.artifacts.some(a=>a.kind==='transaction_state_diff'));
 return item.tx;
}
const binary=process.env.ANVIL_BINARY;if(!binary)throw Error('PINNED_ANVIL_REQUIRED');
const anvil=spawn(binary,['--host','127.0.0.1','--port','18781','--accounts','0','--threads','2','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});let diagnostic='';anvil.stderr.on('data',b=>{if(diagnostic.length<8192)diagnostic+=b.toString();});
try{
 let ready=false;for(let i=0;i<100;i++){if(anvil.exitCode!==null)throw Error('ANVIL_EXIT:'+diagnostic);try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('coupled-local-evm-ready',ready);for(const address of[admin,other])await rpc('anvil_setBalance',[address,'0x'+(10n**30n).toString(16)]);
 for(const[optimized,viaIR]of[[false,false],[true,false],[false,true],[true,true]])for(const[group,body]of groups){
  const label=group+'-'+optimized+'-ir-'+viaIR,source='pragma solidity ^0.8.24;contract Verification{'+body+'}',fixture=compileVerificationFixture(optimized,source,'shanghai',{viaIR}),created=await send('0x'+fixture.compiled.evm.bytecode.object),address=created.receipt.contractAddress;
  check(label+'-actually-deployed',created.receipt.status==='0x1'&&typeof address==='string');const code=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
  const input={...fixture,compiled:undefined,output:undefined,bytecode:code,source:{...fixture.source,runtimeBytecode:{...fixture.source.runtimeBytecode,onchainBytecode:code}},snapshot:{...fixture.snapshot,entry_address:address,code_address:address,storage_address:address,code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash,evm_rules:'shanghai'}};
  const model=engine(input);writeFileSync(out+'/'+label+'-analysis.json',JSON.stringify(model));
  check(label+'-all-coupled-obligations-discharged',model.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed')&&model.sections.loop_invariants.length>0&&model.sections.loop_invariants.every(m=>m.complete&&m.coupled_certificates.length>0),{loops:model.sections.loop_invariants.map(m=>({status:m.status,gaps:m.unresolved})),engines:model.sections.engines});
  const proofs=model.sections.artifacts.filter(a=>a.kind==='property_proof');check(label+'-proof-file-integrity',proofs.length>0&&proofs.every(a=>JSON.parse(a.text).property.proof_artifact_id===a.artifact_id&&createHash('sha256').update(a.text).digest('hex')===a.sha256));
  if(group==='ordered')for(const method of['forward','reverse'])for(const n of[0n,1n,17n,128n])await compare(model,address,method,method+'(uint256,uint256,uint256,uint256)',{n,c:3n,x:7n,y:11n},label+'-'+method+'-'+n);
  if(group==='ordered'){
   await compare(model,address,'forward','forward(uint256,uint256,uint256,uint256)',{n:0n,c:M-1n,x:M-1n,y:M-1n},label+'-empty');
   await compare(model,address,'reverse','reverse(uint256,uint256,uint256,uint256)',{n:1n,c:1n,x:M-1n,y:0n},label+'-unused-final-overflow');
  }
  if(group==='four')for(const n of[0n,1n,17n,128n])await compare(model,address,'calc','calc(uint256,uint256)',{n,c:11n},label+'-'+n);
  if(group==='while')for(const n of[0n,1n,17n,128n])await compare(model,address,'calc','calc(uint256,uint256)',{n,c:3n},label+'-'+n);
  if(group==='narrow')for(const n of[0n,1n,15n,16n,17n,255n])await compare(model,address,'calc','calc(uint8)',{n},label+'-'+n);
  if(group==='nonlinear'){
   for(const n of[0n,1n,17n,128n])await compare(model,address,'calc','calc(uint256,uint256,uint256)',{n,c:3n,initial:2n},label+'-'+n);
   await compare(model,address,'calc','calc(uint256,uint256,uint256)',{n:1n,c:0n,initial:1n<<128n},label+'-multiply-overflow');
  }
  if(group==='safety'){
   for(const n of[0n,1n,2n])await compare(model,address,'hidden','hidden(uint256,uint256,uint256)',{n,c:0n,initial:M-1n},label+'-hidden-'+n);
   await compare(model,address,'unused','unused(uint256,uint256,uint256)',{n:1n,c:1n,initial:M-1n},label+'-unused-overflow');
  }
  if(group.endsWith('ledger')){
   const broken=group==='broken-ledger',policy=model.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');check(label+'-ledger-proof-or-counterexample',broken?policy?.status==='counterexample':policy?.complete,{status:policy?.status});
   const mint=await send(callData('mint(address,uint256,uint256)',[beneficiary,17n,3n]),address);check(label+'-actual-mint-succeeded',mint.receipt.status==='0x1');
   const balance=BigInt(await rpc('eth_call',[{to:address,data:callData('balanceOf(address)',[beneficiary])},'latest'])),supply=BigInt(await rpc('eth_call',[{to:address,data:selector('totalSupply()')},'latest']));
   check(label+'-actual-ledger-delta',balance===459n&&supply===balance+(broken?1n:0n),{balance:String(balance),supply:String(supply)});
   await replayTransaction(mint,input.snapshot,label);
   const denied=await send(callData('mint(address,uint256,uint256)',[beneficiary,1n,1n]),address,other);check(label+'-actual-unauthorized-caller-reverted',denied.receipt.status==='0x0');
   check(label+'-revert-restores-real-storage',BigInt(await rpc('eth_call',[{to:address,data:selector('totalSupply()')},'latest']))===supply);
  }
  runs.push({label,address,deployment_tx:created.tx,optimized,viaIR,loops:model.sections.loop_invariants.length});save();
 }
 check('coupled-runtime-complete',true);
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');writeFileSync(out+'/anvil.log',diagnostic);}
