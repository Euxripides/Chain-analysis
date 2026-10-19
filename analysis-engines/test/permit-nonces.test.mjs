import test from'node:test';import assert from'node:assert/strict';import{analyze}from'../analyze.mjs';import{compileVerificationFixture}from'./verification-fixtures.mjs';import{permitSource}from'./signature-fixtures.mjs';import{appendPermitEntry,namespacePermitSource,unrelatedNonceSource}from'./permit-nonce-fixtures.mjs';
const solver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1'),kind='permit_nonce_monotonicity';
function run(source){const result=analyze(compileVerificationFixture(true,source));assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(result.sections.engines));return result;}
const policy=(r,k=kind)=>r.sections.authorization_models.find(m=>m.kind===k);
test('ordinary and namespace permit accessors retain nonce and allowance field identities',()=>{
 for(const source of[permitSource,namespacePermitSource(),namespacePermitSource({aliases:true})]){
  const r=run(source),permit=policy(r,'erc2612_permit_integrity'),nonce=policy(r);assert.ok(permit);assert.ok(nonce);assert.deepEqual(permit.unresolved,[]);assert.deepEqual(nonce.unresolved,[]);
  assert.ok(r.sections.state_transitions.every(t=>t.complete));assert.equal(nonce.nonce_state_ast_id,permit.nonce_state_ast_id);assert.equal(nonce.entry_coverage.length,r.sections.state_transitions.length);
  assert.equal(nonce.comparison_scope,'final_successful_entry_states_with_atomic_reverts');assert.equal(nonce.whole_authorization_proved,false);
  if(solver){assert.equal(permit.complete,true,JSON.stringify(permit));assert.equal(nonce.complete,true,JSON.stringify(nonce));}
 }
});
test('inherited interface declarations cannot shadow the implemented public nonce and allowance getters',()=>{
 const source=permitSource.replace('contract Verification {','interface IPermitState{function nonces(address who)external view returns(uint256);function allowance(address who,address spender)external view returns(uint256);}\ncontract Verification is IPermitState {')
  .replace('public nonces','public override nonces').replace('public allowance','public override allowance');
 const r=run(source),permit=policy(r,'erc2612_permit_integrity'),nonce=policy(r);assert.ok(nonce);assert.deepEqual(permit.unresolved,[]);assert.deepEqual(nonce.unresolved,[]);
 if(solver){assert.equal(permit.complete,true,JSON.stringify(permit));assert.equal(nonce.complete,true,JSON.stringify(nonce));}
});
test('a second entry that resets a nonce remains a defect even when the permit entry is proved',()=>{
 for(const [source,entry]of[[permitSource,'function reset(address who)external{nonces[who]=0;}'],[namespacePermitSource(),'function reset(address who)external{store().nonces[who]=0;}']]){
  const r=run(appendPermitEntry(source,entry)),m=policy(r);assert.ok(m);assert.equal(m.entry_coverage.length,2);
  if(solver){assert.equal(policy(r,'erc2612_permit_integrity').complete,true);assert.equal(m.status,'counterexample',JSON.stringify(m));
   const proofs=r.sections.verification_obligations.filter(p=>p.model_id===m.model_id);assert.ok(proofs.some(p=>p.status==='counterexample'));}
 }
});
test('nonce protection compares final states and permits guarded invalidation increases',()=>{
 for(const extra of['function invalidate(uint next)external{require(next>store().nonces[msg.sender]);store().nonces[msg.sender]=next;}',
  'function restore(address who)external{uint before=store().nonces[who];store().nonces[who]=0;store().nonces[who]=before;}']){
  const r=run(namespacePermitSource({extra}));assert.ok(r.sections.state_transitions.every(t=>t.complete));
  if(solver)assert.equal(policy(r).complete,true,JSON.stringify(policy(r)));
 }
});
test('unchecked nonce wrap and arbitrary raw storage remain represented counterexamples or gaps',()=>{
 const wrap=run(namespacePermitSource({extra:'function wrap()external{unchecked{store().nonces[msg.sender]++;}}'}));
 if(solver)assert.equal(policy(wrap).status,'counterexample',JSON.stringify(policy(wrap)));
 const raw=run(namespacePermitSource({extra:'function raw(uint slot,uint value)external{assembly{sstore(slot,value)}}'}));
 assert.equal(policy(raw).complete,false);assert.ok(policy(raw).unresolved.some(s=>s.includes('NONCE_UNCLASSIFIED_RAW_STORAGE_WRITE')));
});
test('getters bound to a different account cannot establish the permit storage policy',()=>{
 const r=run(namespacePermitSource().replace('return store().nonces[owner];','return store().nonces[address(0)];'));
 assert.equal(policy(r,'erc2612_permit_integrity').complete,false);assert.ok(policy(r,'erc2612_permit_integrity').unresolved.includes('PERMIT_NONCE_OR_ALLOWANCE_STORAGE_NOT_IDENTIFIED'));
 assert.equal(policy(r),undefined);
});
test('nondecreasing nonces do not replace the permit nonce-consumption obligation',()=>{
 const r=run(namespacePermitSource().replace('store().nonces[owner]++','store().nonces[owner]'));
 if(solver){assert.equal(policy(r).complete,true,JSON.stringify(policy(r)));assert.equal(policy(r,'erc2612_permit_integrity').status,'counterexample');}
});
test('allowance getters must bind both keys to the corresponding declarations',()=>{
 for(const expression of ['store().allowances[spender][owner]','store().allowances[address(0)][spender]']){
  const r=run(namespacePermitSource().replace('return store().allowances[owner][spender];','return '+expression+';'));
  const m=policy(r,'erc2612_permit_integrity');assert.equal(m.complete,false);assert.ok(m.unresolved.includes('PERMIT_NONCE_OR_ALLOWANCE_STORAGE_NOT_IDENTIFIED'));
 }
});
test('an unrelated namespace with the same field name does not alter the permit nonce policy',()=>{
 const r=run(unrelatedNonceSource()),m=policy(r);assert.ok(r.sections.state_transitions.every(t=>t.complete));assert.equal(m.entry_coverage.length,2);assert.deepEqual(m.unresolved,[]);
 if(solver)assert.equal(m.complete,true,JSON.stringify(m));
});
test('aliased resets are detected while reverted resets preserve the successful-entry invariant',()=>{
 const reset=run(namespacePermitSource({extra:'function reset(address who)external{Store storage p=store();p.nonces[who]=0;}'}));
 const revert=run(namespacePermitSource({extra:'function resetThenFail(address who)external{store().nonces[who]=0;revert();}'}));
 assert.ok(revert.sections.state_transitions.every(t=>t.complete));
 if(solver){assert.equal(policy(reset).status,'counterexample');assert.equal(policy(revert).complete,true,JSON.stringify(policy(revert)));}
});
test('builtin revert with a literal message rolls back while an unmodeled message stays explicit',()=>{
 const r=run(namespacePermitSource({extra:'function resetThenFail(address who)external{store().nonces[who]=0;revert("stop");}'}));
 assert.ok(r.sections.state_transitions.every(t=>t.complete));assert.ok(r.sections.state_transitions.some(t=>t.reverted_path_count>0&&t.successful_path_count===0));
 if(solver)assert.equal(policy(r).complete,true,JSON.stringify(policy(r)));
 const dynamic=run(namespacePermitSource({extra:'function resetThenFail(address who,string calldata reason)external{store().nonces[who]=0;revert(reason);}'}));
 assert.equal(policy(dynamic).complete,false);
});
test('incomplete entries and exhausted obligations cannot close the contract-wide nonce model',()=>{
 const r=run(namespacePermitSource({extra:'function unknown(address target)external{(bool ok,)=target.call("");require(ok);}'}));
 assert.equal(policy(r).complete,false);assert.ok(policy(r).unresolved.some(s=>s.startsWith('ENTRY_TRANSITION_INCOMPLETE:')));
 if(solver){
  const extra=Array.from({length:49},(_,i)=>'function bump'+i+'()external{store().nonces[msg.sender]++;}').join('');
  const budget=policy(run(namespacePermitSource({extra})));assert.equal(budget.entry_coverage.length,50);assert.equal(budget.complete,false);assert.ok(budget.proof_counts.budget_exhausted>0,JSON.stringify(budget.proof_counts));
 }
});
