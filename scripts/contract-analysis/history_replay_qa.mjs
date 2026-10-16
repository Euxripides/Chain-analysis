import{spawnSync}from'node:child_process';import{readFileSync,writeFileSync,mkdirSync}from'node:fs';
const base=process.env.CONTRACT_HISTORY_QA_BASE||'acceptance/contract-analysis-transaction-history/geth-prestate',out=process.env.CONTRACT_HISTORY_QA_OUT||'acceptance/contract-analysis-transaction-history/frozen-geth-replay';mkdirSync(out,{recursive:true});
const image=process.env.CONTRACT_TEST_IMAGE||readFileSync('acceptance/contract-analysis-transaction-history/frozen-engine/engine-image.txt','utf8').trim(),checks=[];if(!/^sha256:[0-9a-f]{64}$/.test(image))throw Error('EXACT_FROZEN_IMAGE_REQUIRED');
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(out+'/report.json',JSON.stringify({checks,image,source:'actual_pinned_geth_dev_transactions',execution_mode:'frozen_candidate_replay_of_production_capture',complete:scenario==='geth-isolated-replay-complete'&&ok,whole_goal_complete:false},null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok){console.log(JSON.stringify(evidence));throw Error(scenario);}}
function execute(witness,label){const tx=witness.raw_transaction,b=witness.raw_block,snapshot={vm_family:'evm',chain:'eth',chain_id:BigInt(tx.chainId).toString(),entry_address:tx.to,code_address:tx.to,storage_address:tx.to,block_number:Number(BigInt(b.number)),block_hash:b.hash,state_granularity:'transaction_prestate',evm_rules:'osaka'};
 const p=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify({operation:'replay',snapshot,witness}),encoding:'utf8',timeout:200000,maxBuffer:64<<20});
 if(p.status!==0){writeFileSync(out+'/'+label+'-error.log',p.stderr||String(p.error));return{complete:false,status:'engine_exit',error:p.stderr?.slice(-1500)};}
 const result=JSON.parse(p.stdout);writeFileSync(out+'/'+label+'-result.json',JSON.stringify(result));return result.sections.replays[0];
}
for(const salt of['7','8','9','10','11']){
 const witness=JSON.parse(readFileSync(base+'/salt-'+salt+'-witness.json','utf8')),row=execute(witness,'salt-'+salt);
 check('salt-'+salt+'-full-isolated-replay',row.complete&&row.original_call_trace_matches&&row.state_diff_complete&&row.differences.length===0,{status:row.status,differences:row.differences,gaps:row.witness_gaps,error:row.error});
 if(salt==='7'||salt==='9'){
  const missing=structuredClone(witness);missing.prestate=JSON.parse(readFileSync(base+'/salt-'+salt+'-default-prestate.json','utf8'));const bad=execute(missing,'salt-'+salt+'-missing-account');
  check('salt-'+salt+'-omitted-created-account-does-not-prove-replay',!bad.complete,{status:bad.status,gaps:bad.witness_gaps,error:bad.error});
 }else if(salt==='8'){
  const report=JSON.parse(readFileSync(base+'/report.json','utf8')),child=report.rows.find(r=>r.salt===salt).child,bad=structuredClone(witness);bad.prestate[child].balance='0x0';const wrong=execute(bad,'salt-8-parent-balance-substitution');
  check('parent-state-substitution-cannot-prove-same-block-prefund',!wrong.complete,{status:wrong.status,differences:wrong.differences,gaps:wrong.witness_gaps,error:wrong.error});
 }
 if(salt==='7'){
  const report=JSON.parse(readFileSync(base+'/report.json','utf8')),child=report.rows.find(r=>r.salt===salt).child;
  const omitted=structuredClone(witness),key=Object.keys(omitted.state_diff.post[child].storage)[0];delete omitted.state_diff.post[child].storage[key];
  const extra=execute(omitted,'salt-7-unlisted-actual-storage-write');
  check('unlisted-actual-storage-write-prevents-callsite-proof',!extra.complete&&extra.state_diff_complete&&extra.differences.some(d=>d.startsWith('storage:'+child+':'))&&extra.opcode_witness_status==='complete_replay_required',{status:extra.status,differences:extra.differences});
  const account=structuredClone(witness),creator=Object.keys(account.state_diff.post).find(a=>a!==child&&account.state_diff.post[a].nonce!==undefined);
  if(!creator)throw Error('ACTUAL_CHANGED_CREATOR_ACCOUNT_REQUIRED');delete account.state_diff.pre[creator];delete account.state_diff.post[creator];
  const extraAccount=execute(account,'salt-7-unlisted-actual-account-change');
  check('unlisted-actual-account-change-prevents-callsite-proof',!extraAccount.complete&&extraAccount.state_diff_complete&&extraAccount.differences.some(d=>d.endsWith(':'+creator))&&extraAccount.opcode_witness_status==='complete_replay_required',{status:extraAccount.status,differences:extraAccount.differences});
 }
}
check('geth-isolated-replay-complete',true);
