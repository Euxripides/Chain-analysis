import{spawn,spawnSync}from'node:child_process';
import{once}from'node:events';
import{readFileSync,writeFileSync,mkdirSync}from'node:fs';
import{fileURLToPath}from'node:url';
import{createRequire}from'node:module';
import{analyze}from'../../analysis-engines/analyze.mjs';
import{compileVerificationFixture}from'../../analysis-engines/test/verification-fixtures.mjs';
import{evaluateReturn}from'./expression_model_eval.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),WORD=1n<<256n;
const word=n=>((BigInt(n)%WORD+WORD)%WORD).toString(16).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-model-closure')+'/nonlinear-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const host=process.env.CONTRACT_CLOSURE_ENGINE_MODE==='host',image=host?null:process.env.CONTRACT_TEST_IMAGE;
if(!host&&!/^sha256:[0-9a-f]{64}$/.test(image||''))throw new Error('EXACT_CANDIDATE_IMAGE_REQUIRED');
const checks=[],runs=[];let sequence=0;
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({checks,runs,image,
 execution_mode:host?'candidate_source_with_frozen_solver':'frozen_candidate_image',solver_image:host?process.env.CONTRACT_TEST_IMAGE:null,
 network:'local_anvil',complete:scenario==='nonlinear-runtime-complete'&&ok,whole_goal_complete:false},null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw new Error(scenario);}
async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18770',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(20000)}),body=await r.json();
 if(body.error){const error=new Error(body.error.message);error.rpc_error=body.error;throw error;}return body.result;}
const admin='0x'+'1'.repeat(40),user='0x'+'3'.repeat(40);
async function send(to,data,from=admin){const txHash=await rpc('eth_sendTransaction',[{from,...(to?{to}:{}),data,value:'0x0',gas:'0x1c9c380',gasPrice:'0x77359400'}]);await rpc('evm_mine');return rpc('eth_getTransactionReceipt',[txHash]);}
function engine(input){if(host)return analyze(input);const r=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 if(r.status!==0)throw new Error('FROZEN_ENGINE_FAILED:'+r.stderr.slice(-2000));return JSON.parse(r.stdout);}
async function deploy(source,optimized,label){const fixture=compileVerificationFixture(optimized,source),receipt=await send(null,'0x'+fixture.compiled.evm.bytecode.object);check(label+'-deploy',receipt.status==='0x1');
 const address=receipt.contractAddress,code=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
 const input={...fixture,compiled:undefined,output:undefined,bytecode:code,snapshot:{...fixture.snapshot,entry_address:address,code_address:address,storage_address:address,
  code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash,evm_rules:'shanghai'}};
 const started=performance.now(),model=await engine(input);check(label+'-source-identity',model.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed')&&code==='0x'+fixture.compiled.evm.deployedBytecode.object);
 writeFileSync(new URL(label+'-analysis.json',out),JSON.stringify(model));writeFileSync(new URL(label+'-input.json',out),JSON.stringify(input));
 const creation={receipt,transaction:await rpc('eth_getTransactionByHash',[receipt.transactionHash]),block,trace:await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'callTracer'}]),
  prestate:await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer'}]),state_diff:await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),runtime_bytecode:code};
 writeFileSync(new URL(label+'-creation.json',out),JSON.stringify(creation));runs.push({label,address,code_hash:input.snapshot.code_hash,wall_ms:Math.round(performance.now()-started)});return{fixture,address,model};}
const cases=[
 ['tri','for(uint256 i;i<n;i++)a+=i;'],
 ['squares','for(uint256 i;i<n;i++)a+=c*i*i+3*i+7;'],
 ['word-cubes','unchecked{for(uint256 i;i<n;i++)a+=c*i*i*i-i;}'],
 ['hidden-overflow','for(uint256 i;i<n;i++)a+=(c+i)*i*0;'],
 ['while','uint256 i;while(i<n){a+=i*i;i++;}'],
 ['subtract','for(uint256 i;i<n;i++)a-=i*i;'],
 ['coefficient','for(uint256 i;i<n;i++)a+=c*(2*i+1);'],
 ['word-eight','unchecked{for(uint8 i;i<n;i++)a+=c*i*i;}'],
];
const binary=process.env.ANVIL_BINARY||fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url));
const anvil=spawn(binary,['--host','127.0.0.1','--port','18770','--accounts','0','--threads','2','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--steps-tracing','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('nonlinear-local-evm-ready',ready);
 for(const from of[admin,user])await rpc('anvil_setBalance',[from,'0x'+(10n**30n).toString(16)]);
 for(const optimized of[false,true])for(const[name,body]of cases){
  const type=name==='word-eight'?'uint8':'uint256',bits=type==='uint8'?8n:256n,max=(1n<<bits)-1n,label=name+'-'+optimized;
  const source='pragma solidity ^0.8.24;contract Verification{function calc('+type+' n,'+type+' c,'+type+' initial)external pure returns('+type+' a){a=initial;'+body+'}}';
  const item=await deploy(source,optimized,label),loop=item.model.sections.loop_invariants[0];
  check(label+'-all-induction-obligations',loop?.complete&&loop.polynomial_certificates?.length===1,loop);
  const method=item.fixture.compiled.abi.find(m=>m.type==='function'&&m.name==='calc'),signature='calc('+method.inputs.map(p=>p.type).join(',')+')';
  for(const n of(type==='uint8'?[0n,1n,17n,64n,128n,255n]:[0n,1n,17n,64n,128n,512n]))for(const c of[0n,3n,max])for(const initial of[0n,type==='uint8'?19n:1000000n,max]){
   let expected,actual,modeledError,evmError;
   try{expected=evaluateReturn(item.model,'calc',{n,c,initial});}catch(error){modeledError=error;}
   try{const value=await rpc('eth_call',[{from:user,to:item.address,data:selector(signature)+[n,c,initial].map(word).join(''),gas:'0x1c9c380'},'latest']);actual=[BigInt(value)];}catch(error){evmError=error;}
   const panic=JSON.stringify(evmError?.rpc_error||{}).toLowerCase().includes('4e487b71'+word(17n));
   check(label+'-'+n+'-'+c+'-'+initial,modeledError?modeledError.kind==='panic_0x11'&&panic:!evmError&&expected.length===1&&expected[0]===actual[0],
    {n:String(n),c:String(c),initial:String(initial),model:expected?.map(String),evm:actual?.map(String),model_error:modeledError?.kind,evm_error:evmError?.rpc_error});
  }
 }
 const ledger=`pragma solidity ^0.8.24;contract Verification{address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;
 constructor(){owner=msg.sender;}function balanceOf(address who)external view returns(uint256){return balances[who];}
 function mint(address to,uint256 n)external{require(msg.sender==owner);uint256 sum;for(uint256 i;i<n;i++)sum+=i*i;balances[to]+=sum;totalSupply+=sum;}}`;
 for(const broken of[false,true]){
  const label=broken?'broken-ledger':'closed-ledger',item=await deploy(broken?ledger.replace('totalSupply+=sum;','totalSupply+=sum+1;'):ledger,true,label);
  const invariant=item.model.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');check(label+'-solver-result',broken?invariant.status==='counterexample':invariant.complete);
  const receipt=await send(item.address,selector('mint(address,uint256)')+word(user)+word(17n));
  const balance=BigInt(await rpc('eth_call',[{to:item.address,data:selector('balanceOf(address)')+word(user)},'latest']));
  const supply=BigInt(await rpc('eth_call',[{to:item.address,data:selector('totalSupply()')},'latest']));
  check(label+'-actual-state-and-expected-delta',receipt.status==='0x1'&&balance===1496n&&supply===(broken?1497n:1496n),{tx_hash:receipt.transactionHash,balance:String(balance),totalSupply:String(supply)});
 }
 check('nonlinear-runtime-complete',true);
}finally{anvil.kill('SIGTERM');if(anvil.exitCode===null)await once(anvil,'exit');}
