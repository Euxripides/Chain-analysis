import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileDeepeningFixture} from './deepening-fixtures.mjs';
import {evaluateReturn} from '../../scripts/contract-analysis/expression_model_eval.mjs';

for(const optimizer of [false,true])test('symbolic loop domains and structured Yul '+optimizer,()=>{
 const r=analyze(compileDeepeningFixture(optimizer));
 assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast').status,'completed',JSON.stringify(r.sections.engines));
 const m=name=>r.sections.methods.find(m=>m.name===name),entry=name=>r.sections.dataflow.find(d=>d.dataflow_id===m(name).entry_return_derivation_id);
 for(const name of ['finite','finiteWhile','finiteDo','boundedBreak','decrement','localBound','wordMath','wordBranch','wordLoop','wordTuple','wordSigned','wordShift','wordMod','wordSignExtend','wordByte','wordRevert'])assert.equal(entry(name)?.status,'represented_source_dependencies_resolved',name+':'+JSON.stringify(m(name)));
 assert.equal(entry('dynamic').status,'partial');assert.ok(entry('dynamic').successful_return_paths.length>=16);
 assert.ok(r.sections.frontier.some(f=>f.method_id===m('dynamic').method_id&&f.kind==='dynamic_loop_remainder'));
 assert.equal(entry('mixedLoop').status,'partial');assert.ok(entry('mixedLoop').loops.some(l=>l.complete===false&&l.remaining_paths>0));
 for(let n=0n;n<=8n;n++)assert.deepEqual(evaluateReturn(r,'finite',{n,x:10n}),[n*3n+n*(n-1n)/2n]);
 for(let n=0n;n<=5n;n++)assert.deepEqual(evaluateReturn(r,'finiteDo',{n}),[n*(n-1n)/2n]);
 for(const x of [0n,1n,9n,(1n<<256n)-1n])assert.deepEqual(evaluateReturn(r,'wordLoop',{x}),[x*16n% (1n<<256n)]);
 assert.deepEqual(evaluateReturn(r,'wordMath',{x:7n,y:10n,denominator:0n}),[0n]);
 assert.deepEqual(evaluateReturn(r,'wordTuple',{x:10n}),[3n,1n]);
 assert.deepEqual(evaluateReturn(r,'wordBranch',{x:(1n<<256n)-1n}),[(1n<<256n)-1n]);
 assert.match(m('dirty').entry_return_model_unresolved.join(','),/DIRTY_BITS/);
 assert.equal(entry('memoryValue').status,'represented_source_dependencies_resolved');
 for(const x of [0n,7n,(1n<<256n)-1n])assert.deepEqual(evaluateReturn(r,'memoryValue',{x}),[x]);
 assert.ok(r.sections.external_returns.some(d=>d.method_id===m('quote').method_id&&d.signature==='quote(address,uint256)'&&d.callee_msg_sender===r.snapshot.storage_address));
 assert.ok(r.sections.external_returns.some(d=>d.return_types.includes('uint256[]')));
 assert.ok(r.sections.external_returns.some(d=>d.return_types.includes('address[]')));
 assert.equal(m('contractList').entry_return_model_status,'partial');
 assert.equal(entry('quote').status,'partial');
 const ids=new Set(r.sections.evidence.map(e=>e.evidence_id));assert.ok(r.sections.external_returns.every(d=>d.evidence_ids.every(k=>ids.has(k))));
 const property=(name,kind)=>r.sections.property_obligations.find(p=>p.method_id===m(name).method_id&&p.property===kind);
 for(const name of ['guarded','alternative'])assert.ok(property(name,'successful_guard_requires_caller_equality'),name);
 for(const name of ['split','brokenSplit'])assert.ok(property(name,'returned_components_conserve_input'),name);
 if(process.env.CONTRACT_TEST_IMAGE){
  assert.equal(property('guarded','successful_guard_requires_caller_equality').status,'proved_under_declared_model');
  assert.equal(property('alternative','successful_guard_requires_caller_equality').status,'counterexample');
  assert.equal(property('impossible','successful_guard_requires_caller_equality').status,'infeasible_assumptions');
  assert.equal(property('roleGuard','successful_guard_requires_state_predicate').status,'proved_under_declared_model');
  assert.equal(property('roleAlternative','successful_guard_requires_state_predicate').status,'counterexample');
  assert.equal(property('split','returned_components_conserve_input').status,'proved_under_declared_model');
  assert.equal(property('brokenSplit','returned_components_conserve_input').status,'counterexample');
 }
});
