import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {mkdirSync,writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {compileVerificationFixture} from '../../analysis-engines/test/verification-fixtures.mjs';
import {permitSource} from '../../analysis-engines/test/signature-fixtures.mjs';
import {appendPermitEntry,namespacePermitSource,unrelatedNonceSource} from '../../analysis-engines/test/permit-nonce-fixtures.mjs';
import {erc7201Root} from '../../analysis-engines/namespace-layout.mjs';
import {localGenesisEVM} from './local_genesis_evm.mjs';

const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url));
const {keccak_256}=require('@noble/hashes/sha3.js'),{secp256k1}=require('@noble/curves/secp256k1.js');
const hash=b=>'0x'+Buffer.from(keccak_256(b)).toString('hex'),word=v=>BigInt.asUintN(256,BigInt(v)).toString(16).padStart(64,'0');
const selector=s=>hash(Buffer.from(s)).slice(0,10),encode=(s,args=[])=>selector(s)+args.map(word).join('');
const json=v=>JSON.stringify(v,(_,x)=>typeof x==='bigint'?x.toString():x,2);
const out=(process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-permit-nonces/frozen-validation')+'/permit-runtime';mkdirSync(out,{recursive:true});
const image=process.env.CONTRACT_TEST_IMAGE;if(!/^sha256:[0-9a-f]{64}$/.test(image||''))throw Error('FROZEN_CANDIDATE_DIGEST_REQUIRED');
const port=18785,checks=[],runs=[],executions=[],nonceKind='permit_nonce_monotonicity',permitKind='erc2612_permit_integrity';
const admin='0x'+'1'.repeat(40),relayer='0x'+'3'.repeat(40),spender='0x'+'4'.repeat(40),owner='0x7e5f4552091a69125d5dfcb7b8c2659029395bdf';
const secret=new Uint8Array(32);secret[31]=1;
const permit='permit(address,address,uint256,uint256,uint8,bytes32,bytes32)',gas={gas:'0x989680',gasPrice:'0x77359400'},max=(1n<<256n)-1n;
function save(){writeFileSync(out+'/report.json',json({checks,runs,executions,image,execution_mode:'frozen_candidate_image',network:'local_anvil',complete:checks.at(-1)?.scenario==='permit-runtime-complete'&&checks.at(-1).status==='PASS',whole_goal_complete:false}));}
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});save();if(!ok){console.error(json(evidence)?.slice(0,6000));throw Error(scenario);}if(checks.length%20===0||scenario==='permit-runtime-complete')console.log('PERMIT_RUNTIME_CHECKS',checks.length,scenario);}
let sequence=0;async function rpc(method,params=[]){const response=await fetch('http://127.0.0.1:'+port,{method:'POST',headers:{'content-type':'application/json'},body:json({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(20000)}),body=await response.json();if(body.error)throw Error(method+':'+body.error.message);return body.result;}
function engine(input){const r=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:json(input),encoding:'utf8',timeout:180000,maxBuffer:64<<20});if(r.status!==0)throw Error('FROZEN_ENGINE_FAILED:'+r.stderr.slice(-2000));return JSON.parse(r.stdout);}
const policy=(result,kind)=>result.sections.authorization_models.find(m=>m.kind===kind);
const view=(item,signature,args=[])=>rpc('eth_call',[{to:item.address,data:encode(signature,args)},'latest']).then(BigInt);
const nonce=item=>view(item,'nonces(address)',[owner]);
async function deploy(source,label,options,expected='proved_under_declared_model'){
 const fixture=compileVerificationFixture(options.optimized,source,'shanghai',{viaIR:options.viaIR});
 const tx=await rpc('eth_sendTransaction',[{from:admin,data:'0x'+fixture.compiled.evm.bytecode.object,...gas}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]);check(label+'-deployed',receipt.status==='0x1',{tx_hash:tx});
 const address=receipt.contractAddress,code=await rpc('eth_getCode',[address,'latest']),block=await rpc('eth_getBlockByNumber',['latest',false]);
 const input={...fixture,compiled:undefined,output:undefined,bytecode:code,source:{...fixture.source,runtimeBytecode:{...fixture.source.runtimeBytecode,onchainBytecode:code}},snapshot:{...fixture.snapshot,chain_id:'1',entry_address:address,code_address:address,storage_address:address,code_hash:hash(Buffer.from(code.slice(2),'hex')),block_number:Number(BigInt(block.number)),block_hash:block.hash,evm_rules:'shanghai'}};
 const result=engine(input),n=policy(result,nonceKind),p=policy(result,permitKind);
 check(label+'-candidate-identity',result.rule_version===(process.env.CONTRACT_EXPECTED_RULE_VERSION||'funds-semantics-13')&&result.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed'));
 check(label+'-nonce-result',expected==='incomplete'?n&&!n.complete&&n.unresolved.some(s=>s.includes('NONCE_UNCLASSIFIED_RAW_STORAGE_WRITE')):n?.status===expected,{model:n});
 if(expected!=='incomplete')check(label+'-permit-entry-result',p?.complete===true,{model:p});
 check(label+'-entry-coverage',n?.entry_coverage.length===result.sections.state_transitions.length);
 const closure=result.sections.verification_closure[0];check(label+'-proof-scope',closure.whole_authorization_proved===false&&closure.whole_protocol_economics_proved===false);
 if(expected==='counterexample')check(label+'-counterexample-artifact',result.sections.verification_obligations.some(o=>o.model_id===n.model_id&&o.status==='counterexample'&&result.sections.artifacts.some(a=>a.artifact_id===o.proof_artifact_id&&JSON.parse(a.text).property.counterexample)),{model_id:n.model_id});
 writeFileSync(out+'/'+label+'-input.json',json(input));writeFileSync(out+'/'+label+'-analysis.json',json(result));
 runs.push({label,source_group:options.group,optimizer:options.optimized,via_ir:options.viaIR,address,code_hash:input.snapshot.code_hash,tx_hash:tx,nonce_model_id:n.model_id,nonce_status:n.status});save();return{label,address,input,result};
}
function signed(item,{amount=123n,n=0n,deadline=2000000000n}={}){
 const domain=hash(Buffer.from([hash(Buffer.from('EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)')),hash(Buffer.from('Verification')),hash(Buffer.from('1')),1n,item.address].map(word).join(''),'hex'));
 const struct=hash(Buffer.from([hash(Buffer.from('Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)')),owner,spender,amount,n,deadline].map(word).join(''),'hex'));
 const digest=hash(Buffer.from('1901'+domain.slice(2)+struct.slice(2),'hex')),signature=secp256k1.sign(Buffer.from(digest.slice(2),'hex'),secret,{prehash:false,format:'recovered'});
 return{args:[owner,spender,amount,deadline,27n+BigInt(signature[0]),BigInt('0x'+Buffer.from(signature.subarray(1,33)).toString('hex')),BigInt('0x'+Buffer.from(signature.subarray(33)).toString('hex'))],digest};
}
async function send(item,signature,args,label,{status='0x1',from=relayer,replay=false}={}){
 const before=await nonce(item),tx=await rpc('eth_sendTransaction',[{from,to:item.address,data:encode(signature,args),...gas}]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[tx]),after=await nonce(item);
 const raw_transaction=await rpc('eth_getTransactionByHash',[tx]),raw_block=await rpc('eth_getBlockByHash',[receipt.blockHash,false]);
 const witness={raw_transaction,raw_receipt:receipt,raw_block,parent_block:await rpc('eth_getBlockByHash',[raw_block.parentHash,false]),prestate:await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer',tracerConfig:{includeEmpty:true}}]),state_diff:await rpc('debug_traceTransaction',[tx,{tracer:'prestateTracer',tracerConfig:{diffMode:true}}]),call_trace:await rpc('debug_traceTransaction',[tx,{tracer:'callTracer'}]),hardfork:'shanghai',simulation:true};
 writeFileSync(out+'/'+label+'-witness.json',json(witness));executions.push({label,tx_hash:tx,block_hash:receipt.blockHash,status:receipt.status,nonce_before:before.toString(),nonce_after:after.toString()});
 check(label+'-receipt',receipt.status===status,{tx_hash:tx,status:receipt.status,before,after});
 if(replay){const input={operation:'replay',snapshot:{...item.input.snapshot,block_number:Number(BigInt(raw_block.number)),block_hash:raw_block.hash,state_granularity:'transaction_prestate'},witness},result=engine(input),row=result.sections.replays[0];writeFileSync(out+'/'+label+'-replay-input.json',json(input));writeFileSync(out+'/'+label+'-replay.json',json(result));check(label+'-isolated-replay',row?.complete&&row.original_call_trace_matches&&row.state_diff_complete&&row.differences.length===0,{status:row?.status,gaps:row?.witness_gaps,differences:row?.differences,error:row?.error});}
 return{before,after,tx};
}
async function setNonce(item,value,namespace){const root=namespace?erc7201Root('example.permit'):0n,slot=hash(Buffer.from(word(owner)+word(root),'hex'));await rpc('anvil_setStorageAt',[item.address,slot,'0x'+word(value)]);check(item.label+'-controlled-nonce-prestate',await nonce(item)===value);return slot;}
const sandbox=await localGenesisEVM({binary:process.env.ANVIL_BINARY,port,sourcePort:18784}),child=sandbox.child;
writeFileSync(out+'/local-genesis-environment.json',json(sandbox.info));
try{
 let ready=false;for(let i=0;i<100;i++){if(child.exitCode!==null)throw Error('OWNED_ANVIL_EXITED:'+child.exitCode);try{await rpc('eth_chainId');ready=true;break;}catch{await new Promise(r=>setTimeout(r,50));}}check('owned-local-anvil',ready);
 for(const address of[admin,relayer,owner])await rpc('anvil_setBalance',[address,'0x'+(10n**30n).toString(16)]);
 for(const optimized of[false,true])for(const viaIR of[false,true]){
  const suffix=optimized+'-'+viaIR;
  for(const[group,source]of[['ordinary',permitSource],['namespace',namespacePermitSource()],['aliases',namespacePermitSource({aliases:true})],['unrelated',unrelatedNonceSource()]]){
   const item=await deploy(source,group+'-'+suffix,{group,optimized,viaIR}),first=signed(item);
   await send(item,permit,first.args,item.label+'-first',{replay:true});check(item.label+'-consumed-once',await nonce(item)===1n&&await view(item,'allowance(address,address)',[owner,spender])===123n);
   await send(item,permit,first.args,item.label+'-old-signature-rejected',{status:'0x0'});check(item.label+'-failed-call-atomic',await nonce(item)===1n);
   if(group==='unrelated'){await send(item,'bumpOther(address)',[owner],item.label+'-other-field');check(item.label+'-own-namespace-preserved',await nonce(item)===1n);}
   await setNonce(item,max,group!=='ordinary');await send(item,permit,signed(item,{n:max}).args,item.label+'-checked-overflow',{status:'0x0',replay:true});check(item.label+'-overflow-rollback',await nonce(item)===max);
  }
  for(const namespace of[false,true]){
   const source=namespace?namespacePermitSource({extra:'function reset(address who)external{Store storage p=store();p.nonces[who]=0;}'}):appendPermitEntry(permitSource,'function reset(address who)external{nonces[who]=0;}');
   const item=await deploy(source,'reset-'+namespace+'-'+suffix,{group:'reset',optimized,viaIR},'counterexample'),first=signed(item);
   await send(item,permit,first.args,item.label+'-first');const reset=await send(item,'reset(address)',[owner],item.label+'-reset',{replay:true});check(item.label+'-concrete-decrease',reset.before===1n&&reset.after===0n,{model_id:policy(item.result,nonceKind).model_id});
   const reused=await send(item,permit,first.args,item.label+'-same-signature-reused',{replay:true});check(item.label+'-concrete-reuse',reused.before===0n&&reused.after===1n,{digest:first.digest,tx_hash:reused.tx});
  }
  const restored=await deploy(namespacePermitSource({extra:'function restore(address who)external{uint before=store().nonces[who];store().nonces[who]=0;store().nonces[who]=before;}function resetThenFail(address who)external{store().nonces[who]=0;revert();}'}),'restored-'+suffix,{group:'restored',optimized,viaIR}),old=signed(restored);
  await send(restored,permit,old.args,restored.label+'-first');await send(restored,'restore(address)',[owner],restored.label+'-restored',{replay:true});check(restored.label+'-final-state-preserved',await nonce(restored)===1n);
  await send(restored,'resetThenFail(address)',[owner],restored.label+'-reverted-reset',{status:'0x0',replay:true});check(restored.label+'-ancestor-rollback',await nonce(restored)===1n);await send(restored,permit,old.args,restored.label+'-still-rejected',{status:'0x0'});
  const invalidated=await deploy(namespacePermitSource({extra:'function invalidate(uint next)external{require(next>store().nonces[msg.sender]);store().nonces[msg.sender]=next;}'}),'invalidate-'+suffix,{group:'invalidate',optimized,viaIR});
  await send(invalidated,permit,signed(invalidated).args,invalidated.label+'-first');await send(invalidated,'invalidate(uint256)',[2n],invalidated.label+'-increase',{from:owner,replay:true});check(invalidated.label+'-increased-value',await nonce(invalidated)===2n);await send(invalidated,permit,signed(invalidated,{n:2n}).args,invalidated.label+'-new-signature');check(invalidated.label+'-new-nonce-consumed',await nonce(invalidated)===3n);
  const wrapped=await deploy(namespacePermitSource({extra:'function wrap(address who)external{unchecked{store().nonces[who]++;}}'}),'wrap-'+suffix,{group:'wrap',optimized,viaIR},'counterexample');await setNonce(wrapped,max,true);const wrap=await send(wrapped,'wrap(address)',[owner],wrapped.label+'-wrapped',{replay:true});check(wrapped.label+'-modular-decrease',wrap.before===max&&wrap.after===0n);
  const raw=await deploy(namespacePermitSource({extra:'function raw(uint slot,uint value)external{assembly{sstore(slot,value)}}'}),'raw-'+suffix,{group:'raw',optimized,viaIR},'incomplete'),rawSigned=signed(raw);await send(raw,permit,rawSigned.args,raw.label+'-first');const slot=hash(Buffer.from(word(owner)+word(erc7201Root('example.permit')),'hex'));await send(raw,'raw(uint256,uint256)',[slot,0n],raw.label+'-raw-reset',{replay:true});await send(raw,permit,rawSigned.args,raw.label+'-raw-reuse',{replay:true});
 }
 check('permit-runtime-complete',true);
}finally{writeFileSync(out+'/local-genesis-environment.json',json(sandbox.info));await sandbox.close();}
