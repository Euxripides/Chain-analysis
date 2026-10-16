import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture} from './verification-fixtures.mjs';
import {scalarCastsSource} from './scalar-casts-fixtures.mjs';
import {evaluateReturn} from '../../scripts/contract-analysis/expression_model_eval.mjs';
import {propertyTranslator} from '../properties/smt.mjs';
const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');
for(const optimized of [false,true])test('fixed bytes casts preserve right padding in locals, calls, tuples and comparisons '+optimized,()=>{
 const r=analyze(compileVerificationFixture(optimized,scalarCastsSource));assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(r.sections.engines));
 for(const x of [0n,1n,0x123456789abcdef0n,(1n<<128n)-1n]){
  for(const name of ['widen','local','assigned','passed'])assert.deepEqual(evaluateReturn(r,name,{x}),[x<<128n],name);
  assert.deepEqual(evaluateReturn(r,'narrow',{x:(x<<128n)+99n}),[x]);
  assert.deepEqual(evaluateReturn(r,'tupleValue',{x}),[x<<128n,x]);
  assert.deepEqual(evaluateReturn(r,'conditional',{c:true,a:x,b:7n}),[x<<128n]);
  assert.deepEqual(evaluateReturn(r,'conditional',{c:false,a:x,b:7n}),[7n]);
  assert.deepEqual(evaluateReturn(r,'equal',{a:x,b:x<<128n}),[true]);
  if(x)assert.deepEqual(evaluateReturn(r,'equal',{a:x,b:x}),[false]);
 }
 assert.deepEqual(evaluateReturn(r,'lazy',{c:true,denominator:0n}),[1n]);
 assert.throws(()=>evaluateReturn(r,'lazy',{c:false,denominator:0n}),/panic_0x12/);
 for(const name of ['widen','narrow','local','assigned','passed','conditional','equal']){
  const m=r.sections.methods.find(m=>m.name===name),translated=propertyTranslator(r,m);assert.ok(translated.walk(m.entry_return_expression_id).term,name);
  assert.equal(m.entry_return_model_status,'represented_source_dependencies_resolved',name+':'+JSON.stringify(m.entry_return_model_unresolved));
 }
});
test('fixed bytes storage keys are ABI padded and a cast-based owner bypass is found',()=>{
 const r=analyze(compileVerificationFixture(true,scalarCastsSource)),by=name=>r.sections.state_transitions.find(t=>r.sections.methods.find(m=>m.method_id===t.method_id)?.name===name);
 for(const name of ['set','stateLocal','seize'])assert.equal(by(name).complete,true,JSON.stringify(by(name).unresolved));
 if(actualSolver)assert.equal(r.sections.authorization_models[0].status,'counterexample',JSON.stringify(r.sections.authorization_models));
 assert.ok(r.sections.verification_obligations.some(p=>p.status==='counterexample')||!actualSolver);
});
