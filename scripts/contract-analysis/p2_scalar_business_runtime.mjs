// Independent concrete instances of sealed runtimes. This is not a fork of a deployed state.
import assert from'node:assert/strict';import{createHash}from'node:crypto';import{createRequire}from'node:module';
import{readFileSync,writeFileSync,mkdirSync}from'node:fs';import{createServer}from'node:net';import{once}from'node:events';
import{localGenesisEVM}from'./local_genesis_evm.mjs';
const root=new URL('../../',import.meta.url),base=new URL('acceptance/contract-analysis-scalars/',root),materials=new URL('acceptance/contract-analysis-general/corpus/',root),
 sha=b=>createHash('sha256').update(b).digest('hex'),read=u=>JSON.parse(readFileSync(u)),image=readFileSync(new URL('engine-image.txt',base),'utf8').trim(),
 out=new URL('real-business-execution/'+image.slice(7,23)+'/'+new Date().toISOString().replace(/\D/g,'')+'Z/',base);
mkdirSync(out,{recursive:true});const write=(n,v)=>writeFileSync(new URL(n,out),typeof v==='string'||Buffer.isBuffer(v)?v:JSON.stringify(v,(_,x)=>typeof x==='bigint'?String(x):x));
const require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js'),keccak=b=>Buffer.from(keccak_256(b)),
 word=n=>BigInt(n).toString(16).padStart(64,'0'),hex=n=>'0x'+word(n),selector=s=>keccak(Buffer.from(s)).toString('hex').slice(0,8),
 hashWords=(...ns)=>BigInt('0x'+keccak(Buffer.from(ns.map(word).join(''),'hex')).toString('hex')),mapSlot=(key,slot)=>hashWords(key,slot),
 MAX=(1n<<256n)-1n,HUGE=9007199254740993n,sender='0x'+'1'.repeat(40),target='0x'+'2'.repeat(40),token='0x'+'3'.repeat(40),receiver='0x'+'4'.repeat(40),
 other='0x'+'5'.repeat(40),otherToken='0x'+'6'.repeat(40),corpus=new URL('corpus/candidate-18-'+image.slice(7,23)+'/',base),runs=read(new URL('runs.json',corpus));
assert.equal(runs.length,54);assert.match(image,/^sha256:[a-f0-9]{64}$/);
const seal=read(new URL('seal.json',materials)),manifestRaw=readFileSync(new URL('corpus-manifest.json',materials));assert.equal(sha(manifestRaw),seal.manifest_sha256);
const manifest=JSON.parse(manifestRaw),identities=[];
function material(key){const context=manifest.samples.flatMap(s=>s.contexts).find(c=>c.key===key);assert.ok(context);const dir=new URL('materials/'+key+'/',materials);
 for(const[n,h]of Object.entries(context.files))assert.equal(sha(readFileSync(new URL(n,dir))),h);
 const runtime=readFileSync(new URL('runtime.hex',dir),'utf8').trim();identities.push({context:key,identity:'frozen_runtime_in_owned_empty_genesis',runtime_sha256:sha(runtime),material_files:context.files,original_snapshot_is_not_the_owned_execution_state:true});return runtime;}
const runtimeByKey={'sushi-masterchef':material('sushi-masterchef'),'uniswap-v3-staker':material('uniswap-v3-staker')};
function savedFund(key,method,channel){const name=key+'-'+channel+'-output.json',raw=readFileSync(new URL(name,corpus)),run=runs.find(r=>r.key===key+'-'+channel);assert.equal(sha(raw),run.files[name]);
 const r=JSON.parse(raw);assert.equal(r.snapshot.engine_digest,image);const s=r.sections,m=s.methods.find(m=>m.name===method),funds=s.funds.filter(f=>f.method_id===m.method_id&&f.kind==='token_transfer_intent');assert.equal(funds.length,1);
 const f=funds[0];assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');return{result_sha256:sha(raw),method_id:m.method_id,fund_id:f.fund_id,expression:f.expanded_amount_expression,
  asset:f.expanded_asset_expression,to:f.expanded_to_expression,evidence_ids:f.evidence_ids,amount_derivation_id:f.amount_derivation_id};}
const formulas={};for(const key of Object.keys(runtimeByKey)){
 formulas[key]=['source','submitted_source'].map(channel=>({channel,...savedFund(key,key==='sushi-masterchef'?'emergencyWithdraw':'claimReward',channel)}));
 for(const f of formulas[key]){const compact=t=>t.replace(/\baddress\b|[()\s]/g,'');
  assert.equal(compact(f.asset),key==='sushi-masterchef'?'poolInfo[_pid].lpToken':'rewardToken');assert.equal(compact(f.to),key==='sushi-masterchef'?'msg.sender':'to');}
}
// Restrict evaluation to the exact independent business inputs. No arbitrary Solidity code is evaluated.
function quantity(f,key,due,requested,pid){let text=f.expression;
 if(key==='sushi-masterchef'){let previous;do{previous=text;text=text.replace(/\(\s*(userInfo\[_pid\]\[msg\.sender\])\s*\)/g,'$1');}while(text!==previous);}
 for(const[a,b]of(key==='sushi-masterchef'?[['userInfo[_pid][msg.sender].amount','due']]:[['rewards[rewardToken][msg.sender]','due']]))text=text.split(a).join(b);
 assert.match(text,/^[A-Za-z0-9_\s()+*/?:<>=!%&|.\-]+$/);for(const name of text.match(/[A-Za-z_]\w*/g)||[])assert.ok(['due','amountRequested','_pid','uint256','true','false'].includes(name),'UNSUPPORTED_SAVED_AMOUNT:'+f.expression);
 return new Function('due','amountRequested','_pid','uint256','return '+text.replace(/\b[0-9]+\b/g,'$&n'))(due,requested,pid,v=>BigInt.asUintN(256,v));}
const witnessSource=`pragma solidity 0.8.30;contract ControlledToken{mapping(address=>uint256)public balances;uint256 public mode;event Transfer(address indexed from,address indexed to,uint256 value);
function transfer(address to,uint256 amount)external returns(bool){require(balances[msg.sender]>=amount);balances[msg.sender]-=amount;balances[to]+=amount;emit Transfer(msg.sender,to,amount);if(mode==1){assembly{return(0,0)}}if(mode==2)return false;if(mode==3)revert("CONTROLLED_TOKEN_REVERT");return true;}}`;
const solc=require('solc-0.8.30'),compileInput={language:'Solidity',sources:{'ControlledToken.sol':{content:witnessSource}},settings:{evmVersion:'shanghai',optimizer:{enabled:false},outputSelection:{'*':{'*':['abi','storageLayout','evm.deployedBytecode']}}}},
 compiled=JSON.parse(solc.compile(JSON.stringify(compileInput)));assert.deepEqual((compiled.errors||[]).filter(e=>e.severity==='error'),[]);
const witness=compiled.contracts['ControlledToken.sol'].ControlledToken;assert.equal(witness.storageLayout.storage.find(v=>v.label==='balances').slot,'0');assert.equal(witness.storageLayout.storage.find(v=>v.label==='mode').slot,'1');
write('witness-source.sol',witnessSource);write('witness-compiler-input.json',compileInput);write('witness-compiler-output.json',compiled);write('frozen-identities.json',identities);write('saved-formulas.json',formulas);
async function port(){const s=createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const p=s.address().port;s.close();await once(s,'close');return p;}
const portNumber=await port(),sourcePort=await port(),binary=process.env.ANVIL_BINARY||new URL('../contract-verification-20261015/analysis-engines/bin/anvil',root).pathname;
assert.equal(sha(readFileSync(binary)),'00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604');let sequence=0,vm,currentCase;const records=[];
const rpc=async(method,params=[])=>{const body=await(await fetch('http://127.0.0.1:'+portNumber,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(15000)})).json();if(body.error)throw Error(method+':'+body.error.message);return body.result;},
 set=async(account,slot,value)=>rpc('anvil_setStorageAt',[account,hex(slot),hex(value)]),get=async(account,slot)=>BigInt(await rpc('eth_getStorageAt',[account,hex(slot),'latest']));
const cases=[];for(const pid of[0n,2n])for(const due of[0n,7n,HUGE,MAX])for(let mode=0;mode<4;mode++)cases.push({key:'sushi-masterchef',pid,due,requested:0n,mode});
for(const[due,requested]of[[7n,0n],[7n,3n],[7n,7n],[7n,99n],[0n,0n],[HUGE,3n],[HUGE,HUGE+1n],[MAX,MAX]])for(let mode=0;mode<4;mode++)cases.push({key:'uniswap-v3-staker',pid:0n,due,requested,mode});assert.equal(cases.length,64);
function calls(trace){return trace.structLogs.filter(l=>l.depth===1&&l.op==='CALL').map(l=>{const n=i=>BigInt('0x'+l.stack.at(i).replace(/^0x/,'')),offset=Number(n(-4)),length=Number(n(-5));assert.ok(offset<1<<20&&length<1<<20);
 const memory=l.memory.map(x=>x.replace(/^0x/,'')).join('');return{pc:l.pc,to:'0x'+word(n(-2)).slice(-40),value:n(-3),input:'0x'+memory.slice(offset*2,(offset+length)*2)};});}
try{vm=await localGenesisEVM({binary,port:portNumber,sourcePort});await rpc('anvil_setBalance',[sender,'0x'+(10n**30n).toString(16)]);await rpc('anvil_setCode',[token,'0x'+witness.evm.deployedBytecode.object]);
 for(const c of cases){currentCase=c;const snapshot=await rpc('evm_snapshot'),{key,due,requested,pid,mode}=c,chef=key==='sushi-masterchef',paid=chef?due:requested!==0n&&requested<due?requested:due,reverted=mode>=2,
  userSlot=chef?mapSlot(sender,mapSlot(pid,7n)):mapSlot(sender,mapSlot(token,3n)),debtSlot=userSlot+1n,poolBase=hashWords(6n)+pid*4n,
  untouched=chef?[[mapSlot(other,mapSlot(pid,7n)),123n],[mapSlot(other,mapSlot(pid,7n))+1n,456n],[mapSlot(sender,mapSlot(pid+1n,7n)),789n],[poolBase+1n,321n],[poolBase+2n,654n],[poolBase+3n,987n]]:
   [[mapSlot(other,mapSlot(token,3n)),123n],[mapSlot(sender,mapSlot(otherToken,3n)),456n]],to=chef?sender:receiver;
  await rpc('anvil_setCode',[target,runtimeByKey[key]]);assert.equal(await rpc('eth_getCode',[target,'latest']),runtimeByKey[key]);await set(target,userSlot,due);
  if(chef){await set(target,6n,4n);await set(target,poolBase,BigInt(token));await set(target,debtSlot,17n);}
  for(const[slot,value]of untouched)await set(target,slot,value);await set(token,mapSlot(target,0n),MAX);await set(token,mapSlot(to,0n),0n);await set(token,mapSlot(other,0n),33n);await set(token,1n,mode);
  for(const f of formulas[key])assert.equal(quantity(f,key,due,requested,pid),paid,'saved source/submitted formula vs independent expected payment');
  const data=chef?'0x'+selector('emergencyWithdraw(uint256)')+word(pid):'0x'+selector('claimReward(address,address,uint256)')+word(token)+word(receiver)+word(requested),
   before={user:await get(target,userSlot),debt:chef?await get(target,debtSlot):null,payer_token:await get(token,mapSlot(target,0n)),recipient_token:await get(token,mapSlot(to,0n)),sentinels:untouched.map(([slot,value])=>({slot:hex(slot),value}))},
   tx=await rpc('eth_sendTransaction',[{from:sender,to:target,data,gas:'0x1c9c380',gasPrice:'0x77359400'}]);await rpc('evm_mine');
  const receipt=await rpc('eth_getTransactionReceipt',[tx]),trace=await rpc('debug_traceTransaction',[tx,{enableMemory:true}]),label='case-'+records.length;
  write(label+'-receipt.json',receipt);write(label+'-trace.json',trace);assert.equal(receipt.status==='0x0',reverted);assert.equal(trace.failed,reverted);
  const actual=calls(trace);assert.equal(actual.length,1);assert.equal(actual[0].to,token);assert.equal(actual[0].value,0n);assert.equal(actual[0].input,'0xa9059cbb'+word(to)+word(paid));
  const after={user:await get(target,userSlot),debt:chef?await get(target,debtSlot):null,payer_token:await get(token,mapSlot(target,0n)),recipient_token:await get(token,mapSlot(to,0n))};
  assert.equal(after.user,reverted?due:chef?0n:due-paid);if(chef)assert.equal(after.debt,reverted?17n:0n);assert.equal(after.payer_token,MAX-(reverted?0n:paid));assert.equal(after.recipient_token,reverted?0n:paid);
  for(const[slot,value]of untouched)assert.equal(await get(target,slot),value);assert.equal(await get(token,mapSlot(other,0n)),33n);assert.equal(receipt.logs.length,reverted?0:2);
  const tokenExit=trace.structLogs.filter(l=>l.depth===2&&['RETURN','REVERT'].includes(l.op)).at(-1);assert.ok(tokenExit);
  const exitOffset=Number(BigInt('0x'+tokenExit.stack.at(-1).replace(/^0x/,''))),exitLength=Number(BigInt('0x'+tokenExit.stack.at(-2).replace(/^0x/,''))),
   tokenReturn='0x'+tokenExit.memory.map(x=>x.replace(/^0x/,'')).join('').slice(exitOffset*2,(exitOffset+exitLength)*2);
  assert.equal(tokenExit.op,mode===3?'REVERT':'RETURN');if(mode===0)assert.equal(tokenReturn,hex(1n));if(mode===1)assert.equal(tokenReturn,'0x');if(mode===2)assert.equal(tokenReturn,hex(0n));if(mode===3)assert.ok(tokenReturn.startsWith('0x08c379a0'));
  if(!reverted){const event=receipt.logs[0];assert.equal(event.address,token);assert.deepEqual(event.topics,['0x'+keccak(Buffer.from('Transfer(address,address,uint256)')).toString('hex'),hex(target),hex(to)]);assert.equal(event.data,hex(paid));}
  if(!chef&&!reverted)assert.equal(BigInt('0x'+trace.returnValue.replace(/^0x/,'')),paid);
  const row={...c,status:'PASS',transaction_hash:tx,calldata:data,expected_quantity:paid,reverted,before,after,actual_calls:actual,token_exit:{opcode:tokenExit.op,return_data:tokenReturn},source_formulas:formulas[key],
   receipt_sha256:sha(JSON.stringify(receipt)),trace_sha256:sha(JSON.stringify(trace)),storage_seed_scope:'explicit independent mapping/dynamic-array slots in owned state; deployed historical state not used'};
  records.push(row);write(label+'-comparison.json',row);write('progress.json',{status:'RUNNING',image,cases:records.length,last:row});assert.equal(await rpc('evm_revert',[snapshot]),true);
 }
 write('results.json',{status:'PASS',image,cases:records.length,records,genesis:vm.info,frozen_runtime_identities:identities,witness_compiler:solc.version(),witness_runtime_sha256:sha(witness.evm.deployedBytecode.object),
  verifier_sha256:sha(readFileSync(new URL(import.meta.url))),new_provider_requests:0,whole_authorization_proved:false,whole_protocol_economics_proved:false,
  scope:'64 concrete owned token instances: exact transfer input, bounded source formula, captured pre-write reward, principal/reward/debt storage, true/empty/false/revert return, two token balances and rollback; arbitrary token behavior, reentry, historical state and all paths unproved'});
 console.log('P2_SCALAR_REAL_BUSINESS_EVM_PASS',records.length,out.pathname);
}catch(error){write('failure.json',{image,current_case:currentCase,completed_cases:records.length,error:error.message,stack:error.stack});throw error;}finally{if(vm)await vm.close();}
