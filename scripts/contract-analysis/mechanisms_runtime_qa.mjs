import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {evaluateReturn} from './expression_model_eval.mjs';
import {fileURLToPath} from 'node:url';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createRequire} from 'node:module';
import {compileMechanismFixture} from '../../analysis-engines/test/mechanism-fixtures.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),selector=s=>hash(Buffer.from(s)).slice(0,10),word=n=>BigInt(n).toString(16).padStart(64,'0'),aw=a=>a.slice(2).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-mechanisms')+'/mechanism-runtime/',import.meta.url);mkdirSync(out,{recursive:true});
const image=readFileSync(new URL('../engine-image.txt',out),'utf8').trim(),checks=[],transactions=[],runs=[];
function check(scenario,condition,evidence){checks.push({scenario,status:condition?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify({image,checks,transactions,runs,complete:scenario==='mechanism-runtime-complete'&&condition,simulation:true,network:'local_anvil',all_paths_proved:false},null,2));console.log(scenario,condition?'PASS':'FAIL');if(!condition)throw new Error(scenario)}
let sequence=0;
async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18747',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(10000)});const d=await r.json();if(d.error)throw new Error(d.error.message);return d.result}
const admin='0x'+'1'.repeat(40),user='0x'+'2'.repeat(40),recipient='0x'+'3'.repeat(40);
async function send(from,to,data,value='0x0'){const tx=await rpc('eth_sendTransaction',[{from,...(to?{to}:{}),data,value,gas:'0x989680',gasPrice:'0x77359400'}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);transactions.push({tx_hash:tx,block_hash:receipt.blockHash,status:receipt.status});return receipt}
async function call(to,signature,args=[]){const raw=await rpc('eth_call',[{to,from:user,data:selector(signature)+args.map(word).join('')},'latest']);return raw.slice(2).match(/.{64}/g)?.map(x=>BigInt('0x'+x))||[]}
const evaluate=evaluateReturn;
const anvil=spawn(fileURLToPath(new URL('../../analysis-engines/bin/anvil',import.meta.url)),['--host','127.0.0.1','--port','18747','--accounts','0','--chain-id','1','--hardfork','shanghai','--no-mining','--auto-impersonate','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
async function deploy(name,constructor=''){const fixture=compileMechanismFixture(name,true),receipt=await send(admin,null,'0x'+fixture.compiled.evm.bytecode.object+constructor);check('deploy-'+name,receipt.status==='0x1');return {fixture,address:receipt.contractAddress};}
async function analyze(deployed,name){const {fixture,address}=deployed,bytecode=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
 const input={...fixture,compiled:undefined,bytecode,engines:['whatsabi','sevm','solc-ast','heimdall','slither','gigahorse'],snapshot:{...fixture.snapshot,code_hash:hash(Buffer.from(bytecode.slice(2),'hex')),code_address:address,storage_address:address,entry_address:address,block_number:Number(BigInt(block.number)),block_hash:block.hash}};
 const start=performance.now(),run=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 check(name+'-engine-exit',run.status===0,run.stderr.slice(-1000));const model=JSON.parse(run.stdout);writeFileSync(new URL(name+'-analysis.json',out),run.stdout);writeFileSync(new URL(name+'-input.json',out),JSON.stringify(input));
 runs.push({name,code_hash:input.snapshot.code_hash,wall_ms:Math.round(performance.now()-start),metrics:model.metrics});check(name+'-six-engines',model.sections.engines.every(e=>['completed','partial'].includes(e.status)),model.sections.engines);
 return model;
}
async function compare(model,address,name,signature,args,values,label=''){const expected=evaluate(model,name,values),actual=await call(address,signature,args);check('model-execution-'+name+'-'+label,expected.length===actual.length&&expected.every((v,i)=>v===actual[i]),{expected:expected.map(String),actual:actual.map(String)});}
try{
 let ready=false;for(let i=0;i<100;i++){try{await rpc('eth_chainId');ready=true;break}catch{await new Promise(r=>setTimeout(r,50))}}check('local-evm-ready',ready);
 for(const a of [admin,user,recipient])await rpc('anvil_setBalance',[a,'0x'+(10n**30n).toString(16)]);
 const mechanisms=await deploy('Mechanisms'),model=await analyze(mechanisms,'Mechanisms');await rpc('anvil_setBalance',[mechanisms.address,'0x'+(10n**29n).toString(16)]);
 for(const amount of [0n,1n,2n,99999n,100000003n,90071992547409931234567890123456789n])for(const [name,signature]of [['quote','quote(uint256)'],['selected','selected(uint256)'],['tupleSwap','tupleSwap(uint256)'],['narrow','narrow(uint256)'],['constantLoop','constantLoop(uint256)'],['fixedWhile','fixedWhile(uint256)'],['fixedDo','fixedDo(uint256)'],['flowControl','flowControl(uint256)'],['descending','descending(uint256)']])await compare(model,mechanisms.address,name,signature,[amount],{amount,feeBps:125n},amount.toString());
 for(const amount of [0n,3n]){let actualError=false,modelError=false;try{await call(mechanisms.address,'modified(uint256)',[amount])}catch{actualError=true}try{evaluate(model,'modified',{amount})}catch{modelError=true}check('modifier-domain-'+amount,actualError===modelError);}
 check('internal-modifier-payment-guard-reverts',(await send(user,mechanisms.address,selector('guardedPayment(uint256)')+word(0))).status==='0x0');
 check('internal-modifier-payment-guard-passes',(await send(user,mechanisms.address,selector('guardedPayment(uint256)')+word(3))).status==='0x1');
 const max=(1n<<256n)-1n;for(const name of ['quote','tupleSwap','fixedDo','descending']){let actualError=false,modelError=false;try{await call(mechanisms.address,name+'(uint256)',[max])}catch{actualError=true}try{evaluate(model,name,{amount:max,feeBps:125n})}catch{modelError=true}check('overflow-domain-'+name,actualError&&modelError);}
 const payData=selector('pay(uint256)')+word(100000003n);check('ungranted-role-reverts',(await send(user,mechanisms.address,payData)).status==='0x0');
 check('owner-grants-role',(await send(admin,mechanisms.address,selector('grant(address,bool)')+aw(user)+word(1))).status==='0x1');
 check('observed-membership-after-grant',(await call(mechanisms.address,'hasRole(bytes32,address)',[17n,BigInt(user)]))[0]===1n);
 check('owner-configures-fee',(await send(admin,mechanisms.address,selector('configure(uint256,address)')+word(250n)+aw(recipient))).status==='0x1');
 const before=BigInt(await rpc('eth_getBalance',[recipient,'latest'])),paid=await send(user,mechanisms.address,payData),trace=await rpc('debug_traceTransaction',[paid.transactionHash,{tracer:'callTracer'}]);writeFileSync(new URL('role-payment-trace.json',out),JSON.stringify(trace));
 check('granted-role-exact-fee-payment',paid.status==='0x1'&&BigInt(await rpc('eth_getBalance',[recipient,'latest']))-before===2500000n);
 check('tuple-payment-two-exact-calls',trace.calls.some(c=>c.to.toLowerCase()===recipient&&BigInt(c.value)===2500000n)&&trace.calls.some(c=>c.to.toLowerCase()===user&&BigInt(c.value)===97500003n));
 await send(admin,mechanisms.address,selector('setRequired(uint256)')+word(3));check('numeric-condition-rejects-payment',(await send(user,mechanisms.address,payData)).status==='0x0');
 await send(admin,mechanisms.address,selector('setRequired(uint256)')+word(2));await send(admin,mechanisms.address,selector('grant(address,bool)')+aw(user)+word(0));check('role-revocation-rejects-payment',(await send(user,mechanisms.address,payData)).status==='0x0');
 const token=await deploy('FixtureToken'),vault=await deploy('Vault',aw(token.address)),vaultModel=await analyze(vault,'Vault');
 const encoded=await deploy('EncodedCalls'),encodedModel=await analyze(encoded,'EncodedCalls');
 check('encoded-calldata-model-amounts-and-selectors',encodedModel.sections.funds.length===3&&encodedModel.sections.funds.every(f=>f.expanded_amount_expression==='amount'&&f.expanded_to_expression==='to')&&encodedModel.sections.calls.filter(c=>c.selector==='0xa9059cbb').length===3);
 await send(admin,token.address,selector('mint(address,uint256)')+aw(encoded.address)+word(1000000));
 let encodedReceived=0n;for(const name of ['encoded','signatureEncoding','typedEncoding']){const quantity=100003n,before=(await call(token.address,'balanceOf(address)',[BigInt(recipient)]))[0],receipt=await send(user,encoded.address,selector(name+'(address,address,uint256)')+aw(token.address)+aw(recipient)+word(quantity));check('encoded-actual-token-payment-'+name,receipt.status==='0x1'&&(await call(token.address,'balanceOf(address)',[BigInt(recipient)]))[0]-before===quantity);encodedReceived+=quantity;}
 const amount=1000000000000000003n;await send(admin,token.address,selector('mint(address,uint256)')+aw(user)+word(amount*10n));await send(user,token.address,selector('approve(address,uint256)')+aw(vault.address)+word(max));
 for(const assets of [0n,1n,80n,amount]){await compare(vaultModel,vault.address,'convertToShares','convertToShares(uint256)',[assets],{assets,totalSupply:0n,managed:0n});await compare(vaultModel,vault.address,'previewDeposit','previewDeposit(uint256)',[assets],{assets,totalSupply:0n,managed:0n,feeBps:125n});}
 const preview=(await call(vault.address,'previewDeposit(uint256)',[amount]))[0],deposit=await send(user,vault.address,selector('deposit(uint256,address)')+word(amount)+aw(user));
 const shares=(await call(vault.address,'balanceOf(address)',[BigInt(user)]))[0];check('vault-deposit-preview-shares-and-exact-asset',deposit.status==='0x1'&&shares===preview&&(await call(token.address,'balanceOf(address)',[BigInt(vault.address)]))[0]===amount);
 for(const assets of [1n,80n,100000003n,amount])await compare(vaultModel,vault.address,'previewDeposit','previewDeposit(uint256)',[assets],{assets,totalSupply:shares,managed:amount,feeBps:125n},'after-deposit-'+assets);
 const redeemShares=shares/3n,redeemPreview=(await call(vault.address,'previewRedeem(uint256)',[redeemShares]))[0],beforeTokens=(await call(token.address,'balanceOf(address)',[BigInt(recipient)]))[0];
 await compare(vaultModel,vault.address,'previewRedeem','previewRedeem(uint256)',[redeemShares],{shares:redeemShares,totalSupply:shares,managed:amount,feeBps:125n});
 check('wrong-owner-redeem-reverts',(await send(admin,vault.address,selector('redeem(uint256,address,address)')+word(redeemShares)+aw(recipient)+aw(user))).status==='0x0');
 const redeemed=await send(user,vault.address,selector('redeem(uint256,address,address)')+word(redeemShares)+aw(recipient)+aw(user));check('vault-redeem-preview-and-exact-receipt',redeemed.status==='0x1'&&(await call(token.address,'balanceOf(address)',[BigInt(recipient)]))[0]-beforeTokens===redeemPreview);
 const quotes=await deploy('Quotes'),quoteModel=await analyze(quotes,'Quotes');
 for(const input of [1n,100000003n,9007199254740993123456789n])await compare(quoteModel,quotes.address,'amountOut','amountOut(uint256,uint256,uint256)',[input,10n**25n,3n*10n**25n],{input,reserveIn:10n**25n,reserveOut:3n*10n**25n},String(input));
 for(const output of [1n,100000003n,9007199254740993123456789n])await compare(quoteModel,quotes.address,'amountIn','amountIn(uint256,uint256,uint256)',[output,10n**25n,3n*10n**25n],{output,reserveIn:10n**25n,reserveOut:3n*10n**25n},String(output));
 for(const exempt of [false,true])await compare(quoteModel,quotes.address,'conditionalFee','conditionalFee(uint256,bool)',[amount,exempt?1n:0n],{amount,exempt},String(exempt));
 check('mechanism-runtime-complete',true);console.log('MECHANISM_RUNTIME_PASS',checks.length);
}finally{anvil.kill('SIGTERM');await once(anvil,'exit');}
