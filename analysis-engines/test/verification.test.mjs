import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture,verificationSource,rolesSource} from './verification-fixtures.mjs';
import {evaluateReturn} from '../../scripts/contract-analysis/expression_model_eval.mjs';
const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');

test('native transfer and send retain unique CALL source locations and their distinct return types',()=>{
 for(const optimized of [false,true]){
  const result=analyze(compileVerificationFixture(optimized,'pragma solidity ^0.8.24;contract Verification{function pay(address payable recipient)external payable{recipient.transfer(msg.value);}function tryPay(address payable recipient)external payable returns(bool){return recipient.send(msg.value);}}'));
  const deps=result.sections.external_returns;
  assert.ok(deps.some(d=>d.signature==='native_transfer'&&d.return_types.length===0));
  assert.ok(deps.some(d=>d.signature==='native_send'&&d.return_types.join(',')==='bool'));
  assert.equal(result.sections.source_pc_maps.length,2);
  assert.ok(result.sections.source_pc_maps.every(m=>m.call_type==='CALL'&&m.status==='unique_source_callsite'&&m.source_dependency_ids.length));
 }
});

test('induction records all obligations and only substitutes discharged loop summaries',()=>{
 const result=analyze(compileVerificationFixture()),method=name=>result.sections.methods.find(m=>m.name===name),
  loops=name=>result.sections.loop_invariants.filter(m=>m.method_id===method(name).method_id);
 assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(result.sections.engines));
 assert.equal(loops('affine')[0].obligations.length,7);
 assert.equal(loops('triangular')[0].polynomial_certificates[0].terms[0].degree,1);
 assert.ok(result.sections.source_pc_maps.some(m=>m.status==='unique_source_callsite'));
 for(const p of [128n,129n,159n,256n,1024n])assert.deepEqual(evaluateReturn(result,'memoryWord',{p,v:(1n<<255n)+7n}),[(1n<<255n)+7n]);
 assert.deepEqual(evaluateReturn(result,'memoryByte',{v:(1n<<256n)-1n}),[(1n<<256n)-85n]);
 for(const name of ['unknownMemory','separateBlocks']) {
  const m=method(name),entry=result.sections.dataflow.find(d=>d.dataflow_id===m.entry_return_derivation_id);
  assert.equal(entry.status,'partial');assert.ok(entry.unresolved.some(s=>s.includes('MEMORY_ENTRY_BYTES_UNKNOWN')));
 }
 assert.equal(result.sections.dataflow.find(d=>d.dataflow_id===method('unsafeMemory').entry_return_derivation_id).status,'partial');
 if(actualSolver) {
  for(const name of ['affine','wrapped','subtract'])assert.equal(loops(name)[0].complete,true,JSON.stringify(loops(name)));
  assert.equal(loops('triangular')[0].complete,true,JSON.stringify(loops('triangular')));
  assert.deepEqual(evaluateReturn(result,'triangular',{n:10000n}),[49995000n]);
  assert.equal(loops('counterOverflow')[0].complete,false);
  assert.equal(loops('counterOverflow')[0].status,'counterexample');
  for(const n of [0n,1n,17n,128n,10000n])assert.deepEqual(evaluateReturn(result,'affine',{n,c:3n}),[n*3n]);
  assert.deepEqual(evaluateReturn(result,'wrapped',{n:10000n,c:(1n<<256n)-1n}),[(1n<<256n)-10000n]);
  assert.throws(()=>evaluateReturn(result,'affine',{n:2n,c:(1n<<256n)-1n}),/panic/);
  assert.deepEqual(evaluateReturn(result,'subtract',{n:128n,c:3n,initial:1000n}),[616n]);
 }
});

test('state models include all mutating entries, raw storage and atomic revert paths',()=>{
 const result=analyze(compileVerificationFixture()),named=name=>result.sections.state_transitions.find(t=>result.sections.methods.find(m=>m.method_id===t.method_id)?.name===name);
 assert.ok(result.sections.state_transitions.every(t=>t.complete),JSON.stringify(result.sections.state_transitions.map(t=>t.unresolved)));
 const write=named('storageWrite');assert.ok(write.effects.some(e=>e.kind==='sstore'));assert.ok(write.effects.some(e=>e.kind==='sload'));
 assert.ok(write.paths.some(p=>p.writes.some(w=>w.root_ast_id===null)));
 assert.ok(write.reverted_paths.every(p=>p.storage_after==='storage_0'));
 assert.ok(named('transfer').paths[0].writes.length===2);
 const ledger=result.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');
 assert.equal(ledger.complete,false);assert.ok(ledger.unresolved.some(s=>s.includes('UNCLASSIFIED_RAW_STORAGE_WRITE')));
 if(actualSolver)assert.equal(result.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity').complete,true,JSON.stringify(result.sections.authorization_models));
});

test('closed ledger conservation proves all entries and finds concrete bypass and issuance defects',()=>{
 const closed=verificationSource.split('\n').filter(line=>!line.includes('function storageWrite')).join('\n');
 const good=analyze(compileVerificationFixture(true,closed));
 for(const proof of good.sections.verification_obligations){
  const artifact=good.sections.artifacts.find(a=>a.artifact_id===proof.proof_artifact_id);
  assert.deepEqual(JSON.parse(artifact.text).property,proof,'published proof must retain the exact assumptions and evidence saved in its artifact');
 }
 assert.equal(good.sections.protocol_invariants.length,2);
 if(actualSolver){
  assert.equal(good.sections.protocol_invariants[0].complete,true,JSON.stringify(good.sections.protocol_invariants));
  assert.equal(good.sections.authorization_models[0].complete,true);
  assert.equal(good.sections.protocol_invariants.find(m=>m.kind==='erc20_source_initialization').complete,true);
  const brokenInit=analyze(compileVerificationFixture(true,closed.replace('owner=msg.sender;','owner=msg.sender;totalSupply=1;')));
  assert.equal(brokenInit.sections.protocol_invariants.find(m=>m.kind==='erc20_source_initialization').status,'counterexample');
  const issuance=analyze(compileVerificationFixture(true,closed.replace('totalSupply+=value;','totalSupply+=value+1;')));
  assert.equal(issuance.sections.protocol_invariants[0].status,'counterexample',JSON.stringify(issuance.sections.protocol_invariants));
  const bypass=analyze(compileVerificationFixture(true,closed.replace('contract Verification {','contract Verification { function seize(address next)external{owner=next;}')));
  assert.equal(bypass.sections.authorization_models[0].status,'counterexample');
  const proof=bypass.sections.verification_obligations.find(p=>p.model_id===bypass.sections.authorization_models[0].model_id&&p.status==='counterexample');
  assert.ok(proof.counterexample.inputs.some(v=>v.kind==='entry_storage_word'));
 }
});

test('Solidity returns run modifier suffixes while an EVM RETURN halts the entire frame',()=>{
 const source=`pragma solidity ^0.8.24;contract Verification {address public owner;modifier suffix(){_;owner=address(0);}function normal()external suffix{return;}function raw()external suffix{assembly{return(0,0)}}}`;
 const result=analyze(compileVerificationFixture(true,source));
 const by=name=>result.sections.state_transitions.find(t=>result.sections.methods.find(m=>m.method_id===t.method_id)?.name===name);
 assert.equal(by('normal').complete,true);assert.equal(by('normal').paths[0].writes.length,1);
 assert.equal(by('raw').complete,true);assert.equal(by('raw').paths[0].writes.length,0);assert.equal(by('raw').paths[0].halt,'evm_return');assert.deepEqual(by('raw').paths[0].return_values,[]);
});

test('explicit base and super calls retain their body while unqualified virtual calls dispatch',()=>{
 const source=`pragma solidity ^0.8.24;contract Base {uint256 public flag;function f()internal virtual{flag=1;}}contract Verification is Base{function f()internal override{flag=2;}function explicitBase()external{Base.f();}function dispatched()external{f();}function viaSuper()external{super.f();}}`;
 const result=analyze(compileVerificationFixture(true,source));
 for(const [name,value]of [['explicitBase','1'],['dispatched','2'],['viaSuper','1']]){
  const row=result.sections.state_transitions.find(t=>result.sections.methods.find(m=>m.method_id===t.method_id)?.name===name);
  assert.equal(row.complete,true,JSON.stringify(row));assert.equal(row.paths[0].writes[0].new_value,value,name);
 }
 const hidden=analyze(compileVerificationFixture(true,'pragma solidity ^0.8.24;contract Base{address public owner;fallback()external virtual{owner=msg.sender;}}contract Verification is Base{fallback()external override{}}'));
 assert.equal(hidden.sections.state_transitions.length,1);assert.equal(hidden.sections.state_transitions[0].paths[0].writes.length,0);
 if(actualSolver)assert.equal(hidden.sections.authorization_models[0].complete,true);
});

test('role administration covers grant, revoke and self renunciation across every mutating entry',()=>{
 const result=analyze(compileVerificationFixture(true,rolesSource));
 assert.ok(result.sections.state_transitions.every(t=>t.complete),JSON.stringify(result.sections.state_transitions));
 const model=result.sections.authorization_models.find(m=>m.kind==='access_control_admin_integrity');assert.ok(model);
 assert.equal(model.entry_coverage.length,3);
 if(actualSolver){
  assert.equal(model.complete,true,JSON.stringify(model));
  const bad=analyze(compileVerificationFixture(true,rolesSource.replace('contract Verification {','contract Verification { function bypass(bytes32 role,address account)external{roles[role].members[account]=true;}')));
  const broken=bad.sections.authorization_models.find(m=>m.kind==='access_control_admin_integrity');
  assert.equal(broken.status,'counterexample',JSON.stringify(broken));
  const raw=analyze(compileVerificationFixture(true,rolesSource.replace('contract Verification {','contract Verification { function poke(uint256 slot)external{assembly{sstore(slot,1)}}')));
  const gap=raw.sections.authorization_models.find(m=>m.kind==='access_control_admin_integrity');
  assert.equal(gap.complete,false);assert.match(gap.unresolved.join(','),/UNCLASSIFIED_RAW_STORAGE_WRITE/);
 }
});
