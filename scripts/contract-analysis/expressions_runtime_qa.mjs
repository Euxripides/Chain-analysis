import {spawn,spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createRequire} from 'node:module';
import {compileExpressionFixture} from '../../analysis-engines/test/expression-fixtures.mjs';
import {evaluateReturn} from './expression_model_eval.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),word=n=>((BigInt(n)%(1n<<256n)+(1n<<256n))%(1n<<256n)).toString(16).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-expressions')+'/expression-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const image=readFileSync(new URL('../engine-image.txt',out),'utf8').trim(),checks=[],transactions=[],runs=[];
function check(scenario,condition,evidence){checks.push({scenario,status:condition?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({image,checks,transactions,runs,complete:scenario==='expression-runtime-complete'&&condition,simulation:true,network:'local_anvil',all_paths_proved:false},null,2));if(!condition)throw new Error(scenario);if(checks.length%25===0||scenario==='expression-runtime-complete')console.log('EXPRESSION_CHECKS',checks.length,scenario);}
let seq=0;
async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18748',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++seq,method,params}),signal:AbortSignal.timeout(10000)});const d=await r.json();if(d.error){const e=new Error(d.error.message);e.data=d.error.data;throw e;}return d.result;}
const user='0x'+'1'.repeat(40),max=(1n<<256n)-1n;
async function send(to,data,value='0x0'){const tx=await rpc('eth_sendTransaction',[{from:user,...(to?{to}:{}),data,value,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const r=await rpc('eth_getTransactionReceipt',[tx]);transactions.push({tx_hash:tx,block_hash:r.blockHash,status:r.status});return r;}
function signature(fixture,name){const abi=fixture.compiled.abi.find(a=>a.type==='function'&&a.name===name);return {abi,text:name+'('+abi.inputs.map(a=>a.type).join(',')+')'};}
function classify(e){if(typeof e.data==='string'&&e.data.startsWith('0x4e487b71'))return 'panic_0x'+BigInt('0x'+e.data.slice(10)).toString(16);if(typeof e.data==='string'&&e.data.startsWith('0x08c379a0')||e.data==='0x'&&/^execution reverted/.test(e.message))return 'revert';if(e.kind)return e.kind;return 'UNCLASSIFIED:'+e.message;}
async function compare(model,fixture,address,name,args,label){const {abi,text}=signature(fixture,name),inputs=Object.fromEntries(abi.inputs.map((a,i)=>[a.name,a.type==='bool'?!!args[i]:BigInt(args[i])])),data=selector(text)+args.map(word).join('');
 let expected,actual,modelError,actualError;
 try{expected=evaluateReturn(model,name,inputs);}catch(e){modelError=classify(e);}
 try{const raw=await rpc('eth_call',[{to:address,from:user,data},'latest']);actual=raw.slice(2).match(/.{64}/g)?.map((v,i)=>{let n=BigInt('0x'+v);if(/^int/.test(abi.outputs[i].type)&&n>=(1n<<255n))n-=1n<<256n;return n;})||[];}catch(e){actualError=classify(e);}
 // Pre-0.8 invalid-opcode division by zero has no panic ABI; preserve its class.
 const rawExecutionError=actualError;
 if(fixture.source.compilation.compilerVersion.startsWith('0.5.')&&modelError==='panic_0x12'&&/invalid.?opcode|InvalidFEOpcode/i.test(actualError||'')){actualError='legacy_division_failure';modelError='legacy_division_failure';}
 const same=modelError||actualError?modelError===actualError:expected.length===actual.length&&expected.every((v,i)=>v===actual[i]);
 check(name+'-'+label,same,{inputs:Object.fromEntries(Object.entries(inputs).map(([k,v])=>[k,String(v)])),expected:expected?.map(String),actual:actual?.map(String),model_error:modelError,execution_error:actualError,raw_execution_error:rawExecutionError});
 return {data,modelError,actualError};
}
const anvil=spawn(fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url)),['--host','127.0.0.1','--port','18748','--accounts','0','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break}catch{await new Promise(r=>setTimeout(r,50));}}check('local-evm-ready',ready);await rpc('anvil_setBalance',[user,'0x'+(10n**30n).toString(16)]);
 for(const legacy of [false,true])for(const optimizer of [false,true]){
  const name=(legacy?'legacy':'modern')+'-'+optimizer,fixture=compileExpressionFixture(legacy,optimizer),deployed=await send(null,'0x'+fixture.compiled.evm.bytecode.object);check(name+'-deployed',deployed.status==='0x1');
  const address=deployed.contractAddress,bytecode=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
  const input={...fixture,compiled:undefined,bytecode,snapshot:{...fixture.snapshot,code_hash:hash(Buffer.from(bytecode.slice(2),'hex')),code_address:address,entry_address:address,storage_address:address,block_number:Number(BigInt(block.number)),block_hash:block.hash}};
  const started=performance.now(),run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
  check(name+'-engine-exit',run.status===0,run.stderr.slice(-1500));const model=JSON.parse(run.stdout);writeFileSync(new URL(name+'-analysis.json',out),run.stdout);writeFileSync(new URL(name+'-input.json',out),JSON.stringify(input));runs.push({name,compiler:fixture.source.compilation.compilerVersion,code_hash:input.snapshot.code_hash,wall_ms:Math.round(performance.now()-started),metrics:model.metrics});
  for(const pair of [[0n,0n],[0n,1n],[1n,0n],[1n,1n],[7n,3n],[max,0n],[max,1n],[max,max],[1n<<128n,1n<<128n],[900719925474099312345n,125n]])for(const method of legacy?['add','sub','mul','fee','payout']:['add','compound'])await compare(model,fixture,address,method,pair,name+'-'+pair.join('-'));
  if(legacy){
   for(const x of [0n,1n,127n,255n]){await compare(model,fixture,address,'wrappedPost',[x],name+'-'+x);for(const y of [0n,1n,2n,255n])for(const method of ['sub8','mul8'])await compare(model,fixture,address,method,[x,y],name+'-'+x+'-'+y);}
   for(const flag of [0n,1n])for(const y of [0n,2n])await compare(model,fixture,address,'skipped',[12n,y,flag],name+'-'+flag+'-'+y);
   for(const x of [0n,1n,11n])await compare(model,fixture,address,'unusedDivision',[x],name+'-'+x);
   await rpc('anvil_setBalance',[address,'0x'+(10n**25n).toString(16)]);
   for(const rate of [125n,10001n]){const data=selector('pay(uint256,uint256)')+word(100000003n)+word(rate),receipt=await send(address,data),trace=await rpc('debug_traceTransaction',[receipt.transactionHash,{tracer:'callTracer'}]);writeFileSync(new URL(name+'-payment-'+rate+'.json',out),JSON.stringify(trace));const expected=100000003n-(100000003n*rate/10000n);check(name+'-payment-'+rate,rate===125n?receipt.status==='0x1'&&trace.calls.some(c=>c.to.toLowerCase()===user&&BigInt(c.value)===expected):receipt.status==='0x0'&&!trace.calls?.some(c=>c.value&&BigInt(c.value)>0n));}
  }else{
   for(const x of [0n,1n,255n,max])for(const method of ['post','pre','decrement','unusedOverflow','overwrittenDivision','discardedCall','assignCast'])await compare(model,fixture,address,method,[x],name+'-'+x);
   for(const x of [0n,1n,255n])await compare(model,fixture,address,'uncheckedPost',[x],name+'-'+x);
   for(const x of [-128n,-1n,0n,127n])await compare(model,fixture,address,'negative',[x],name+'-'+x);
   for(const flag of [0n,1n])for(const pair of [[7n,0n],[7n,2n],[1n,3n],[max,0n]])for(const method of ['andUpdate','orUpdate','conditional','protectedDivision','protectedConditional'])await compare(model,fixture,address,method,[...pair,flag],name+'-'+pair.join('-')+'-'+flag);
   for(const pair of [[2n,3n],[max,1n],[1n,max]])for(const method of ['assignArgument','independentTuple'])await compare(model,fixture,address,method,pair,name+'-'+pair.join('-'));
  }
 }
 check('expression-runtime-complete',true);
}finally{anvil.kill('SIGTERM');}
