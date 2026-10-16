import{spawn,spawnSync}from'node:child_process';import{once}from'node:events';import{readFileSync,mkdirSync,writeFileSync}from'node:fs';
import{compileVerificationFixture}from'../../analysis-engines/test/verification-fixtures.mjs';import{deriveCreate2Address}from'../../analysis-engines/opcode-witness.mjs';import{createRequire}from'node:module';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js'),hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),sel=s=>hash(Buffer.from(s)).slice(0,10),word=n=>BigInt(n).toString(16).padStart(64,'0');
const out='acceptance/contract-analysis-transaction-history/geth-prestate';mkdirSync(out,{recursive:true});
const pinned=JSON.parse(readFileSync('acceptance/contract-analysis-transaction-history/geth-runtime/geth-image.json','utf8')),name='contract-history-geth-qa-'+process.pid,checks=[],rows=[];
if(pinned.image!=='sha256:2dd7ef210a1a3fb87887676f7ead1b98161af83810928cdf6fc5b84bcdcc8ef4'||pinned.dev_chain_hardfork!=='osaka')throw Error('GETH_PIN_OR_RULES_CHANGED');
const source=`pragma solidity ^0.8.24;contract Verification{uint public seenBalance;uint public height;uint public clock;uint public fee;uint public randomness;address public miner;uint public gasLimit;uint public blobFee;uint public chain;bytes32 public parent;constructor()payable{seenBalance=address(this).balance;height=block.number;clock=block.timestamp;fee=block.basefee;randomness=block.prevrandao;miner=block.coinbase;gasLimit=block.gaslimit;blobFee=block.blobbasefee;chain=block.chainid;parent=blockhash(block.number-1);}function mutate()external{seenBalance=0;height=0;assembly{sstore(11,caller())}}function clear()external{assembly{sstore(11,0)}}}contract Factory{function create(uint salt)external returns(address){return address(new Verification{salt:bytes32(salt)}());}}`,fixture=compileVerificationFixture(true,source,'cancun');
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(out+'/report.json',JSON.stringify({checks,rows,pinned,network:'isolated_geth_dev',complete:scenario==='geth-prestate-complete'&&ok,whole_goal_complete:false},null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw Error(scenario);}
let n=0;async function rpc(method,params=[]){const r=await fetch('http://127.0.0.1:18777',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++n,method,params}),signal:AbortSignal.timeout(30000)}),b=await r.json();if(b.error)throw Error(method+':'+JSON.stringify(b.error));return b.result;}
async function receipt(tx){for(let i=0;i<150;i++){let r=await rpc('eth_getTransactionReceipt',[tx]);if(r)return r;await new Promise(r=>setTimeout(r,200));}throw Error('RECEIPT_TIMEOUT');}
const geth=spawn('docker',['run','--rm','--pull','never','--name',name,'--network','host','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','1g','--memory-swap','1g','--pids-limit','128','--tmpfs','/tmp:rw,nosuid,size=384m',pinned.image,'--dev','--dev.period','5','--http','--http.addr','127.0.0.1','--http.port','18777','--http.api','eth,debug,net,web3','--authrpc.addr','127.0.0.1','--authrpc.port','18778','--maxpeers','0','--nodiscover','--port','0','--ipcdisable','--datadir','/tmp/geth-history','--cache','128'],{stdio:['ignore','ignore','pipe']});let diagnostics='';geth.stderr.on('data',b=>{if(diagnostics.length<65536)diagnostics+=b.toString();});
try{
 let ready=false;for(let i=0;i<150;i++){if(geth.exitCode!==null)throw Error('GETH_EXIT:'+diagnostics);try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,100));}}check('isolated-geth-ready',ready);const[admin]=await rpc('eth_accounts'),gas={gas:'0x989680',gasPrice:'0x77359400'};
 const deployed=await rpc('eth_sendTransaction',[{from:admin,data:'0x'+fixture.output.contracts['Verification.sol'].Factory.evm.bytecode.object,...gas}]),factory=(await receipt(deployed)).contractAddress;
 for(const salt of[7n,8n,9n,10n,11n]){
  const creation=salt<=9n,child=deriveCreate2Address(factory,creation?salt:8n,Buffer.from(fixture.compiled.evm.bytecode.object,'hex'));
  if(salt===8n)await rpc('eth_sendTransaction',[{from:admin,to:child,value:'0x7b',gas:'0x5208',gasPrice:gas.gasPrice}]);
  if(salt===9n)await rpc('eth_sendTransaction',[{from:admin,to:'0x'+'3'.repeat(40),value:'0x1',gas:'0x5208',gasPrice:gas.gasPrice}]);
  const tx=await rpc('eth_sendTransaction',[{from:admin,to:creation?factory:child,data:creation?sel('create(uint256)')+word(salt):sel(salt===10n?'mutate()':'clear()'),...gas}]),r=await receipt(tx),block=await rpc('eth_getBlockByHash',[r.blockHash,false]),parent=await rpc('eth_getBlockByHash',[block.parentHash,false]);check('salt-'+salt+'-actual-success',r.status==='0x1');
  const standard=await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer',tracerConfig:{diffMode:false}}]),include=await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer',tracerConfig:{diffMode:false,includeEmpty:true}}]),actual=await rpc('eth_call',[{to:child,data:sel('seenBalance()')},r.blockNumber]),parentBalance=await rpc('eth_getBalance',[child,{blockHash:parent.hash,requireCanonical:true}]);
  if(creation)check('salt-'+salt+'-include-empty-has-exact-transaction-state',include[child]!==undefined&&BigInt(include[child].balance)===BigInt(actual),{entry:include[child],actual});
  if(creation&&salt!==8n)check('salt-'+salt+'-default-tracer-omits-new-empty-account',standard[child]===undefined,{standard:standard[child]});
  if(creation&&salt!==7n)check('salt-'+salt+'-same-block-preceding-transaction',r.transactionIndex==='0x1',r.transactionIndex);
  if(!creation){
   const slots={};for(const key of[0n,1n,11n])slots[word(key)]=await rpc('eth_getStorageAt',[child,'0x'+word(key),{blockHash:block.hash,requireCanonical:true}]);
   check('salt-'+salt+'-actual-storage-clearing',BigInt(slots[word(0)])===0n&&BigInt(slots[word(1)])===0n&&BigInt(slots[word(11)])===(salt===10n?BigInt(admin):0n),slots);
  }
  if(salt===8n)check('prefunded-account-proves-parent-state-is-different',BigInt(parentBalance)===0n&&BigInt(include[child].balance)===123n&&BigInt(actual)===123n,{parentBalance,transactionPrestate:include[child].balance,actual});
  const witness={raw_transaction:await rpc('eth_getTransactionByHash',[tx]),raw_receipt:r,raw_block:block,parent_block:parent,prestate:include,state_diff:await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),call_trace:await rpc('debug_traceTransaction',[tx,{tracer:'callTracer'}]),hardfork:'osaka',simulation:true};
  // These are actual parent-state observations for block-system execution,
  // kept separate from the transaction tracer accounts in the saved witness.
  const system={},ref={blockHash:parent.hash,requireCanonical:true},slot=n=>'0x'+n.toString(16).padStart(64,'0'),clock=BigInt(block.timestamp)%8191n;
  for(const[address,slots]of[
   ['0x0000f90827f1c53a10cb7a02335b175320002935',[(BigInt(block.number)-1n)%8191n]],
   ['0x000f3df6d732807ef1319fb7b8bb8522d0beac02',[clock,clock+8191n]],
   ['0x00000961ef480eb55e80d19ad83579a64c007002',[0n,1n,2n,3n]],
   ['0x0000bbddc7ce488642fb579f8b00f3a590007251',[0n,1n,2n,3n]],
   ['0xfffffffffffffffffffffffffffffffffffffffe',[]]
  ]){
   const account={balance:await rpc('eth_getBalance',[address,ref]),nonce:await rpc('eth_getTransactionCount',[address,ref]),code:await rpc('eth_getCode',[address,ref]),storage:{}};
   for(const key of slots)account.storage[slot(key)]=await rpc('eth_getStorageAt',[address,slot(key),ref]);system[address]=account;
  }
  witness.block_system_prestate={scope:'observed_parent_state_for_block_system_calls',parent_block_hash:parent.hash,accounts:system};
  writeFileSync(out+'/salt-'+salt+'-opcode.json',JSON.stringify(await rpc('debug_traceTransaction',[tx,{disableStorage:true,enableMemory:false,disableStack:false,enableReturnData:false}])));
  const file=process.cwd()+'/'+out+'/salt-'+salt+'-witness.json';writeFileSync(file,JSON.stringify(witness));writeFileSync(out+'/salt-'+salt+'-default-prestate.json',JSON.stringify(standard));
  const collected=spawnSync('/usr/local/go/bin/go',['test','./internal/api','-run','^TestContractBlockSystemStateLiveGeth$','-count=1','-v'],{cwd:new URL('../../backend/',import.meta.url),env:{...process.env,GOMAXPROCS:'2',CONTRACT_GETH_LIVE_WITNESS:file},encoding:'utf8',timeout:120000,maxBuffer:8<<20});
  writeFileSync(out+'/salt-'+salt+'-production-collector.log',(collected.stdout||'')+(collected.stderr||''));
  check('salt-'+salt+'-production-collector-uses-actual-node',collected.status===0&&collected.stdout.includes('--- PASS: TestContractBlockSystemStateLiveGeth'),{status:collected.status,output:collected.status?collected.stdout?.slice(-3000):undefined});
  const production=JSON.parse(readFileSync(out+'/salt-'+salt+'-production-collector.json','utf8'));witness.prestate=production.prestate;witness.block_system_prestate=production.block_system_prestate;writeFileSync(file,JSON.stringify(witness));
  rows.push({tx_hash:tx,child,salt:String(salt),scenario:creation?'create2':'assembly_storage_clear',transaction_index:r.transactionIndex,parent_balance:parentBalance,transaction_prestate_balance:include[child].balance,actual,production_collector_local_rpc_requests:production.local_rpc_requests});
 }
 check('geth-prestate-complete',true);
}finally{spawnSync('docker',['stop','--time','10',name],{stdio:'ignore',timeout:20000});if(geth.exitCode===null)await once(geth,'exit');writeFileSync(out+'/geth.log',diagnostics);}
