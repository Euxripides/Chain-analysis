import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileExpressionFixture} from './expression-fixtures.mjs';

for(const legacy of [false,true])for(const optimizer of [false,true])test('local expression effects and ordered return domains '+JSON.stringify({legacy,optimizer}),()=>{
 const result=analyze(compileExpressionFixture(legacy,optimizer));
 assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'completed',JSON.stringify(result.sections.engines));
 const method=name=>result.sections.methods.find(m=>m.name===name),model=name=>result.sections.dataflow.find(d=>d.method_id===method(name).method_id&&d.kind==='entry_return_derivation');
 for(const name of legacy?['add','sub','mul','fee','payout','wrappedPost','sub8','mul8','skipped','unusedDivision']:['add','compound','andUpdate','orUpdate','conditional','post','pre','decrement','uncheckedPost','negative','assignArgument','assignCast','unusedOverflow','overwrittenDivision','discardedCall','protectedDivision','protectedConditional','independentTuple']) {
  assert.equal(model(name)?.status,'represented_source_dependencies_resolved',name+':'+JSON.stringify(method(name)));
  assert.ok(model(name).successful_return_paths.every(p=>Array.isArray(p.execution_steps)),name);
 }
 const add=model('add');assert.match(add.symbolic_expression,/x\s*\+\s*y/);
 assert.ok(add.successful_return_paths.some(p=>p.conditions.some(c=>c.includes('>='))));
 for(const name of legacy?['ambiguous']:['ambiguous','ambiguousArguments','ambiguousTuple','ambiguousCompound']) {
  assert.equal(method(name).entry_return_model_status,'partial',name);
  assert.ok(method(name).entry_return_model_unresolved.includes('INTERNAL_RETURN_EVALUATION_ORDER_UNSPECIFIED'),name+JSON.stringify(method(name)));
 }
 if(legacy) {
  assert.equal(model('mul').successful_return_paths.length,2);
  assert.ok(model('mul').successful_return_paths.some(p=>p.value==='uint256(0)'&&p.conditions.some(c=>c.replace(/\s/g,'').includes('y==0'))));
  assert.ok(result.sections.funds.filter(f=>f.method_id===method('pay').method_id).every(f=>f.amount_derivation_status==='represented_source_dependencies_resolved'));
  assert.ok(result.sections.expressions.some(e=>e.symbolic_format==='identity'&&e.arithmetic==='wrap'));
 }else {
  assert.ok(model('andUpdate').successful_return_paths.some(p=>p.conditions.some(c=>c==='!(flag)')&&p.value==='x'));
  assert.ok(model('orUpdate').successful_return_paths.some(p=>p.conditions.some(c=>c==='(flag)')&&p.value.includes('x')));
  assert.ok(model('unusedOverflow').successful_return_paths[0].execution_steps.some(s=>s.expression.includes('+')));
  assert.ok(model('overwrittenDivision').successful_return_paths[0].execution_steps.some(s=>s.expression.includes('/')));
  assert.equal(method('dynamicBound').entry_return_model_status,'partial');
  assert.equal(method('assemblyValue').entry_return_model_status,'represented_source_dependencies_resolved');
 }
});
