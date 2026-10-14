import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createRequire} from 'node:module';
import {compileReturnFixture} from '../../analysis-engines/test/internal-return-fixtures.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),word=n=>BigInt(n).toString(16).padStart(64,'0'),addressWord=a=>a.slice(2).padStart(64,'0');
const out=new URL('../../acceptance/contract-analysis-semantics/return-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const image=readFileSync(new URL('../engine-image.txt',out),'utf8').trim(),checks=[],transactions=[];
function check(scenario,condition,evidence){checks.push({scenario,status:condition?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({image,checks,transactions,simulation:true,network:'local_anvil',all_paths_proved:false},null,2));console.log(scenario,condition?'PASS':'FAIL');if(!condition)throw new Error(scenario)}
const anvil=spawn(new URL('../../analysis-engines/bin/anvil',import.meta.url).pathname,['--host','127.0.0.1','--port','18746','--accounts','0','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
let sequence=0;
async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18746',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(10000)});const d=await r.json();if(d.error)throw new Error(d.error.message);return d.result}
const admin='0x'+'1'.repeat(40),user='0x'+'2'.repeat(40),recipient='0x'+'3'.repeat(40);
async function send(from,to,data,value='0x0'){const tx=await rpc('eth_sendTransaction',[{from,...(to?{to}:{}),data,value,gas:'0x4c4b40',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);transactions.push({tx_hash:tx,block_hash:receipt.blockHash,status:receipt.status});return receipt}
async function call(to,signature,args){return BigInt(await rpc('eth_call',[{to,data:selector(signature)+args.map(word).join('')},'latest']))}
try {
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break}catch{await new Promise(r=>setTimeout(r,50))}}check('local-evm-ready',ready);
 for(const a of [admin,user,recipient])await rpc('anvil_setBalance',[a,'0x'+(10n**30n).toString(16)]);
 const fixture=compileReturnFixture(true),receipt=await send(admin,null,'0x'+fixture.compiled.evm.bytecode.object);check('deploy-source-fixture',receipt.status==='0x1');const address=receipt.contractAddress;
 await rpc('anvil_setBalance',[address,'0x'+(10n**29n).toString(16)]);
 const bytecode=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
 const input={...fixture,compiled:undefined,bytecode,engines:['whatsabi','sevm','solc-ast','heimdall','slither','gigahorse'],snapshot:{...fixture.snapshot,code_hash:hash(Buffer.from(bytecode.slice(2),'hex')),code_address:address,storage_address:address,entry_address:address,block_number:Number(BigInt(block.number)),block_hash:block.hash,state_granularity:'block_end'}};
 const run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 check('new-fixture-isolated-engine-exit',run.status===0,run.stderr?.slice(-700));const model=JSON.parse(run.stdout);writeFileSync(new URL('analysis.json',out),run.stdout);writeFileSync(new URL('input.json',out),JSON.stringify(input));
 check('all-six-engines-completed',model.sections.engines.every(e=>['completed','partial'].includes(e.status)),model.sections.engines);
 const mid=name=>model.sections.methods.find(m=>m.name===name).method_id;
 check('fee-formula-and-permission-dependencies',model.sections.funds.filter(f=>f.method_id===mid('withdraw')).every(f=>f.amount_derivation_status==='represented_source_dependencies_resolved')&&model.sections.control_effects.some(e=>e.configuration_key==='feeBps'&&e.impact_types.includes('amount_formula')));
 for(const n of [0n,1n,2n,99999n,100000000n,90071992547409931234567890123456789n]) {
  const expected=n*125n/10000n;check('quote-fee-'+n,await call(address,'quote(uint256,bool)',[n,0])===(expected>1000000n?1000000n:expected),{amount:n.toString()});
  check('exemption-'+n,await call(address,'quote(uint256,bool)',[n,1])===0n);
  check('multi-statement-rounding-'+n,await call(address,'local(uint256)',[n])===2n*(n/3n+7n));
  check('named-branch-return-'+n,await call(address,'named(uint256)',[n])===n/7n+(n>100n?5n:0n));
  check('isolated-repeated-call-bindings-'+n,await call(address,'two(uint256)',[n])===2n*(n/3n+7n)+2n*((n+3n)/3n+7n));
 }
 const max=(1n<<256n)-1n;check('unchecked-max-wrap',await call(address,'wrapped(uint256)',[max])===0n);
 for(const [name,args] of [['callerUnchecked(uint256)',[max]],['quote(uint256,bool)',[max,0]],['reject(uint256)',[0]],['reject(uint256)',[2]]]) {
  let rejected=false;try{await call(address,name,args)}catch{rejected=true}check('revert-'+name+'-'+args.join('-'),rejected);
 }
 check('exempt-max-does-not-evaluate-fee-overflow',await call(address,'quote(uint256,bool)',[max,1])===0n);
 check('positive-return-truncates',await call(address,'reject(uint256)',[3])===1n);
 const configureData=selector('configure(uint16,address)')+word(250)+addressWord(recipient);
 check('non-owner-config-rejected',(await send(user,address,configureData)).status==='0x0');
 check('owner-config-applied',(await send(admin,address,configureData)).status==='0x1');
 const amount=100000003n,fee=amount*250n/10000n>1000000n?1000000n:amount*250n/10000n;
 check('changed-state-fee',await call(address,'quote(uint256,bool)',[amount,0])===fee);
 const before=BigInt(await rpc('eth_getBalance',[recipient,'latest'])),withdraw=await send(user,address,selector('withdraw(uint256,bool)')+word(amount)+word(0));
 const trace=await rpc('debug_traceTransaction',[withdraw.transactionHash,{tracer:'callTracer'}]);writeFileSync(new URL('withdraw-trace.json',out),JSON.stringify(trace));
 check('withdraw-recipient-exact-amount',BigInt(await rpc('eth_getBalance',[recipient,'latest']))-before===fee,{amount:amount.toString(),fee:fee.toString()});
 check('withdraw-real-call-path',withdraw.status==='0x1'&&trace.calls.some(c=>c.to.toLowerCase()===recipient&&BigInt(c.value)===fee)&&trace.calls.some(c=>c.to.toLowerCase()===user&&BigInt(c.value)===amount-fee));
 await send(admin,address,selector('toggle(bool)')+word(1));check('paused-withdraw-rejected',(await send(user,address,selector('withdraw(uint256,bool)')+word(amount)+word(0))).status==='0x0');
 check('owner-transfer-applied',(await send(admin,address,selector('transferOwner(address)')+addressWord(user))).status==='0x1');
 check('old-owner-config-rejected',(await send(admin,address,configureData)).status==='0x0');check('new-owner-config-applied',(await send(user,address,configureData)).status==='0x1');
 console.log('RETURN_RUNTIME_PASS',checks.length);
} finally {anvil.kill('SIGTERM');await once(anvil,'exit')}
