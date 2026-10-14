import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createRequire} from 'node:module';
import {compileFixture} from '../../analysis-engines/test/fixtures.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url));
const {keccak_256}=require('@noble/hashes/sha3.js');
const hash=bytes=>'0x'+Buffer.from(keccak_256(bytes)).toString('hex');
const selector=sig=>hash(Buffer.from(sig)).slice(0,10);
const word=v=>BigInt(v).toString(16).padStart(64,'0');
const addressWord=v=>v.slice(2).padStart(64,'0');
const out=new URL('../../'+(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis')+'/runtime-fixtures/',import.meta.url);mkdirSync(out,{recursive:true});
const image=readFileSync(new URL('../engine-image.txt',out),'utf8').trim();const checks=[];
function check(name,ok,evidence){checks.push({scenario:name,status:ok?'PASS':'FAIL',evidence});writeFileSync(new URL('report.json',out),JSON.stringify(checks,null,2));console.log(name,ok?'PASS':'FAIL');if(!ok)throw new Error(name)}
const binary=new URL('../../analysis-engines/bin/anvil',import.meta.url).pathname;
const node=spawn(binary,['--host','127.0.0.1','--port','18745','--accounts','0','--chain-id','1','--hardfork','shanghai','--no-mining','--order','fifo','--auto-impersonate','--silent','--disable-default-create2-deployer'],{stdio:['ignore','ignore','pipe']});
let sequence=0;const rpc=async(method,params=[])=>{const response=await fetch('http://127.0.0.1:18745',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(15000)});const data=await response.json();if(data.error)throw new Error(method+':'+data.error.message);return data.result};
const admin='0x'+'1'.repeat(40),user='0x'+'2'.repeat(40),recipient='0x'+'3'.repeat(40),rows={};
async function send(from,to,data='0x',value='0x0',mine=true){const tx=await rpc('eth_sendTransaction',[{from,...(to?{to}:{}),data,value,gas:'0x4c4b40',gasPrice:'0x77359400'}]);if(mine)await rpc('evm_mine');return tx}
async function deploy(name,args=''){const fixture=compileFixture(name,true),tx=await send(admin,null,'0x'+fixture.compiled.evm.bytecode.object+args),receipt=await rpc('eth_getTransactionReceipt',[tx]);check('deploy-'+name,receipt.status==='0x1');rows[name]={fixture,address:receipt.contractAddress};return receipt.contractAddress}
async function invoke(name,contract,from,signature,args=[],value='0x0',mine=true){return send(from,contract,selector(signature)+args.join(''),value,mine)}
async function balance(token,address){return BigInt(await rpc('eth_call',[{to:token,data:selector('balanceOf(address)')+addressWord(address)},'latest']))}
async function replayTx(name,txHash,contract,negative){
 const tx=await rpc('eth_getTransactionByHash',[txHash]),receipt=await rpc('eth_getTransactionReceipt',[txHash]);
 const block=await rpc('eth_getBlockByHash',[tx.blockHash,false]),parent=await rpc('eth_getBlockByHash',[block.parentHash,false]);
 const prestate=await rpc('debug_traceTransaction',[txHash,{tracer:'prestateTracer'}]);
 const diff=await rpc('debug_traceTransaction',[txHash,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]);
 const trace=await rpc('debug_traceTransaction',[txHash,{tracer:'callTracer'}]);
 const code=await rpc('eth_getCode',[contract,'latest']);
 const witness={raw_transaction:tx,raw_receipt:receipt,raw_block:block,parent_block:parent,prestate,state_diff:diff,call_trace:trace,hardfork:'shanghai',simulation:true};
 if(negative==='missing_slot'){const account=Object.values(prestate).find(account=>Object.keys(account.storage||{}).length);delete account.storage[Object.keys(account.storage)[0]]}
 if(negative==='gas_mismatch')witness.raw_receipt.gasUsed='0x'+(BigInt(receipt.gasUsed)+1n).toString(16);
 const snapshot={chain:'ethereum',chain_id:'1',entry_address:contract,code_address:contract,storage_address:contract,code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash,state_granularity:'transaction_prestate'};
 writeFileSync(new URL(name+'-witness.json',out),JSON.stringify(witness));
 const run=spawnSync('docker',['run','--rm','--pull','never','-i','--name','contract-fixture-'+name,'--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify({operation:'replay',snapshot,witness}),encoding:'utf8',timeout:190000,maxBuffer:32<<20});
 check(name+'-engine-exit',run.status===0,run.stderr?.slice(0,500));const result=JSON.parse(run.stdout);writeFileSync(new URL(name+'-replay.json',out),run.stdout);const replay=result.sections.replays[0];
 check(name+'-exact-replay',negative?replay.complete===false&&replay.status===(negative==='missing_slot'?'witness_gap':'replay_mismatch'):replay.complete===true&&replay.simulation===true,{status:replay.status,gaps:replay.witness_gaps,differences:replay.differences});return {tx,receipt,witness,replay};
}
try{
 let ready=false;for(let n=0;n<100;n++){try{await rpc('eth_chainId');ready=true;break}catch{await new Promise(resolve=>setTimeout(resolve,50))}}check('local-node-ready',ready);
 for(const address of [admin,user,recipient])await rpc('anvil_setBalance',[address,'0x'+(10n**30n).toString(16)]);
 const token=await deploy('StandardToken'),vault=await deploy('AnalystVault',addressWord(token)),nested=await deploy('NestedCalls');
 const amount=10n**24n+1n,deposit=amount*4n;
 await invoke('mint',token,admin,'mint(address,uint256)',[addressWord(user),word(deposit)]);
 await invoke('approve',token,user,'approve(address,uint256)',[addressWord(vault),word((1n<<256n)-1n)]);
 const depositHash=await invoke('deposit',vault,user,'deposit(uint256)',[word(deposit)]);
 check('deposit-exact-ledger',await balance(token,vault)===deposit);await replayTx('deposit',depositHash,vault);
 const zero=await invoke('zero',vault,user,'deposit(uint256)',[word(0)]);check('zero-deposit-reverts',(await rpc('eth_getTransactionReceipt',[zero])).status==='0x0');await replayTx('zero-deposit',zero,vault);
 const unauthorized=await invoke('unauthorized',vault,user,'configure(uint16,address)',[word(125),addressWord(recipient)]);check('unauthorized-config-reverts',(await rpc('eth_getTransactionReceipt',[unauthorized])).status==='0x0');await replayTx('unauthorized-config',unauthorized,vault);
 const feeBefore=await balance(token,recipient),userBefore=await balance(token,user);
 const config=await invoke('configure',vault,admin,'configure(uint16,address)',[word(250),addressWord(recipient)],'0x0',false);
 const redeem=await invoke('redeem',vault,user,'redeem(uint256)',[word(amount)],'0x0',false);await rpc('evm_mine');
 const r1=await rpc('eth_getTransactionReceipt',[config]),r2=await rpc('eth_getTransactionReceipt',[redeem]);check('same-block-config-then-redeem',r1.blockHash===r2.blockHash&&BigInt(r1.transactionIndex)<BigInt(r2.transactionIndex));
 const fee=amount*250n/10000n;check('fee-rounding-and-recipient-ledger',await balance(token,recipient)-feeBefore===fee);check('user-net-redemption',await balance(token,user)-userBefore===amount-fee);
 const replay=await replayTx('same-block-fee',redeem,vault);check('same-block-witness-observes-new-fee',replay.replay.state_slots_checked>0);
 await replayTx('missing-slot',redeem,vault,'missing_slot');await replayTx('receipt-mismatch',redeem,vault,'gas_mismatch');
 const max=await invoke('uintmax',vault,user,'redeem(uint256)',[word((1n<<256n)-1n)]);check('uint256-max-insufficient-shares-reverts',(await rpc('eth_getTransactionReceipt',[max])).status==='0x0');await replayTx('uintmax',max,vault);
 const caught=await invoke('caught',nested,user,'caught(address)',[addressWord(recipient)],'0x2710');const result=await replayTx('caught-revert',caught,nested);
 check('caught-child-revert-retains-successful-sibling',result.receipt.status==='0x1'&&result.witness.call_trace.calls.some(c=>c.error)&&result.witness.call_trace.calls.some(c=>c.to.toLowerCase()===recipient&&!c.error));
 const ledger=await deploy('NativeLedger');
 const proxies=[];proxies.push(['erc1967',await deploy('ERC1967Fixture',addressWord(ledger))]);
 const beacon=await deploy('BeaconFixture',addressWord(ledger));proxies.push(['beacon',await deploy('BeaconProxyFixture',addressWord(beacon))]);
 const cloneHash=await send(admin,null,'0x3d602d80600a3d3981f3363d3d373d3d3d363d73'+ledger.slice(2)+'5af43d82803e903d91602b57fd5bf3');const cloneReceipt=await rpc('eth_getTransactionReceipt',[cloneHash]);check('deploy-clone',cloneReceipt.status==='0x1');proxies.push(['clone',cloneReceipt.contractAddress]);
 // The diamond facet uses mapping slot 0. Keep its routing configuration in
 // a separate slot, as required by delegate-based storage ownership.
 const diamond=await deploy('DiamondFixture',addressWord(ledger));proxies.push(['diamond',diamond]);
 const facets=await rpc('eth_call',[{to:diamond,data:selector('facetAddresses()')},'latest']);check('diamond-loupe-address-array',facets.slice(-40).toLowerCase()===ledger.slice(2));
 const route=await rpc('eth_call',[{to:diamond,data:selector('facetAddress(bytes4)')+selector('put()').slice(2).padEnd(64,'0')},'latest']);check('diamond-selector-route',route.slice(-40).toLowerCase()===ledger.slice(2));
 for(const [kind,proxy]of proxies){
  const tx=await invoke(kind,proxy,user,'put()',[],'0x3e7');const observed=await replayTx(kind+'-delegate',tx,proxy);const serialized=JSON.stringify(observed.witness.call_trace);check(kind+'-uses-implementation-code',serialized.includes('DELEGATECALL')&&serialized.toLowerCase().includes(ledger));
  const take=await invoke(kind,proxy,user,'take(uint256)',[word(500)]);check(kind+'-retains-proxy-owned-ledger',(await rpc('eth_getTransactionReceipt',[take])).status==='0x1');
 }
 const rebased=await deploy('RebasedLedger');
 await invoke('rebase-mint',rebased,admin,'mint(address,uint256)',[addressWord(user),word(amount*4n)]);
 await invoke('rebase-index',rebased,admin,'rebase(uint256)',[word(2n*10n**18n)]);
 check('rebase-computed-balance-exact',await balance(rebased,user)===amount*8n);
 const rebasedBefore=await balance(rebased,user),rebasedRecipient=await balance(rebased,recipient),rebasedAmount=(amount/2n)*2n;
 const rebaseTx=await invoke('rebase-transfer',rebased,user,'transfer(address,uint256)',[addressWord(recipient),word(rebasedAmount)]);
 check('rebase-share-transfer-matches-both-balances',rebasedBefore-await balance(rebased,user)===rebasedAmount&&await balance(rebased,recipient)-rebasedRecipient===rebasedAmount);
 await replayTx('rebase-share-transfer',rebaseTx,rebased);
 const encoded=await deploy('EncodedTokenFlow',addressWord(token)+addressWord(recipient)+word(137));
 await invoke('encoded-fund',token,admin,'mint(address,uint256)',[addressWord(encoded),word(amount)]);
 const encodedBefore=await balance(token,recipient),encodedTx=await invoke('encoded-pay',encoded,user,'pay(uint256)',[word(amount)]);
 check('encoded-library-actual-fee',await balance(token,recipient)-encodedBefore===amount*137n/10000n);
 await replayTx('encoded-library-payment',encodedTx,encoded);
 const code=await rpc('eth_getCode',[encoded,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]),source=structuredClone(rows.EncodedTokenFlow.fixture.source);
 source.runtimeBytecode.onchainBytecode=code;
 const staticInput={source,bytecode:code,snapshot:{chain:'ethereum',chain_id:'1',entry_address:encoded,code_address:encoded,storage_address:encoded,code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash},engines:['whatsabi','sevm','solc-ast','heimdall','slither','gigahorse']};
 const analyzed=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(staticInput),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 check('deployed-immutable-source-analysis-exit',analyzed.status===0,analyzed.stderr?.slice(-500));const model=JSON.parse(analyzed.stdout);writeFileSync(new URL('deployed-immutable-analysis.json',out),analyzed.stdout);
 const pay=model.sections.methods.find(m=>m.signature==='pay(uint256)'),funds=model.sections.funds.filter(f=>f.method_id===pay?.method_id);
 check('deployed-immutable-values-bind-actual-formula',funds.length===1&&funds[0].expanded_asset_expression===token&&funds[0].expanded_to_expression===recipient&&funds[0].expanded_amount_expression==='amount*137/10000',funds);
 const nft=await deploy('OwnershipLedger'),tokenId=9007199254740993123456789n,nftTx=await invoke('ownership-assign',nft,user,'assign(uint256,address)',[word(tokenId),addressWord(recipient)]);
 const observedOwner=await rpc('eth_call',[{to:nft,data:selector('ownerOf(uint256)')+word(tokenId)},'latest']);
 check('actual-nft-owner-slot-matches-recipient',observedOwner.slice(-40)===recipient.slice(2));await replayTx('ownership-assignment',nftTx,nft);
 console.log('RUNTIME_FIXTURE_QA_PASS',checks.length);
}finally{node.kill('SIGKILL');await once(node,'exit').catch(()=>{});}
