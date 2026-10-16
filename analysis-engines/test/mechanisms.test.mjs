import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileMechanismFixture} from './mechanism-fixtures.mjs';
import {evaluateReturn} from '../../scripts/contract-analysis/expression_model_eval.mjs';
const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');

for(const optimized of [false,true])test('tuple formulas, constant loops and guard modifiers '+optimized,()=>{
 const r=analyze(compileMechanismFixture('Mechanisms',optimized));
 assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast').status,'completed',JSON.stringify(r.sections.engines));
 assert.ok(r.sections.expressions.every(e=>new Set(e.evidence_ids).size===e.evidence_ids.length),'repeated expansion mutated expression evidence');
 const m=name=>r.sections.methods.find(m=>m.name===name),entry=name=>r.sections.dataflow.find(d=>d.dataflow_id===m(name).entry_return_derivation_id);
 assert.equal(entry('quote').status,'represented_source_dependencies_resolved');assert.match(entry('quote').symbolic_expression,/amount\*feeBps\/10000/);
 assert.equal(entry('selected').status,'represented_source_dependencies_resolved');assert.match(entry('selected').symbolic_expression,/feeBps/);
 assert.match(entry('tupleSwap').symbolic_expression,/amount\+3.*amount/);assert.equal(entry('tupleSwap').status,'represented_source_dependencies_resolved');
 for(const name of ['constantLoop','fixedWhile','fixedDo','flowControl','descending']){assert.equal(entry(name).status,'represented_source_dependencies_resolved',name);assert.ok(entry(name).loops.length,name);}
 assert.equal(entry('constantLoop').loops[0].iterations,3);assert.equal(entry('fixedWhile').loops[0].iterations,3);assert.equal(entry('fixedDo').loops[0].iterations,2);
 assert.equal(m('loopLimit').entry_return_model_status,'partial');assert.ok(m('loopLimit').entry_return_model_unresolved.some(x=>x.includes('ITERATION_BUDGET')));
 if(actualSolver){
  assert.equal(m('dynamic').entry_return_model_status,'represented_source_dependencies_resolved');
  assert.deepEqual(evaluateReturn(r,'dynamic',{amount:10000n}),[49995000n]);
 }else{assert.equal(m('dynamic').entry_return_model_status,'partial');assert.ok(m('dynamic').entry_return_model_unresolved.some(x=>x.includes('LOOP_DYNAMIC_BOUND')));}
 const check=entry('modified');assert.equal(check.status,'represented_source_dependencies_resolved');assert.ok(check.successful_return_paths[0].conditions.some(c=>c.includes('amount>0')));
 assert.ok(r.sections.funds.filter(f=>f.method_id===m('guardedPayment').method_id).every(f=>f.conditions.includes('value>0')));
 assert.ok(r.sections.permissions.some(g=>g.method_id===m('guardedPayment').method_id&&g.expanded_condition?.includes('amount')&&g.expanded_condition.includes('>0')));
 const funds=r.sections.funds.filter(f=>f.method_id===m('pay').method_id);assert.equal(funds.length,2);assert.ok(funds.every(f=>f.amount_derivation_status==='represented_source_dependencies_resolved'),JSON.stringify(funds));
 assert.ok(funds.some(f=>f.expanded_amount_expression.includes('feeBps')));assert.ok(funds.every(f=>!f.expanded_amount_expression.includes('tuple[')));
 const guard=r.sections.permissions.find(g=>g.method_id===m('pay').method_id&&g.condition.startsWith('hasRole'));
 assert.ok(guard.state_predicates.some(p=>p.predicate_kind==='boolean_state_lookup'&&p.expected&&p.subject_expressions.includes('msg.sender')),JSON.stringify(guard));
 assert.ok(guard.state_predicates.some(p=>p.lookups.some(l=>l.projection.path.some(k=>k.expanded_key_expression?.includes('17')))));
 const deny=r.sections.permissions.find(g=>g.method_id===m('deny').method_id);assert.ok(deny.state_predicates.some(p=>p.expected===false));
 assert.ok(r.sections.permissions.some(g=>g.method_id===m('pay').method_id&&g.state_predicates.some(p=>p.predicate_kind==='state_numeric_comparison'&&p.operator==='>=')));
 assert.ok(r.sections.control_effects.some(e=>e.method_id===m('grant').method_id&&e.impact_types.includes('caller_authorization')&&e.affected_method_ids.includes(m('pay').method_id)));
});

test('vault and AMM returns preserve state inputs, division order and constraints',()=>{
 const vault=analyze(compileMechanismFixture('Vault',true)),quotes=analyze(compileMechanismFixture('Quotes',true));
 for(const r of [vault,quotes])assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');
 const entry=(r,name)=>{const m=r.sections.methods.find(m=>m.name===name);return r.sections.dataflow.find(d=>d.dataflow_id===m.entry_return_derivation_id)};
 for(const name of ['convertToShares','convertToAssets','previewDeposit','previewRedeem'])assert.equal(entry(vault,name).status,'represented_source_dependencies_resolved',name);
 assert.match(entry(vault,'previewDeposit').symbolic_expression,/feeBps/);assert.match(entry(vault,'previewRedeem').symbolic_expression,/managed/);
 assert.match(entry(quotes,'amountOut').symbolic_expression,/997/);assert.match(entry(quotes,'amountOut').symbolic_expression,/1000/);
 assert.ok(entry(quotes,'amountIn').successful_return_paths[0].conditions.some(c=>c.includes('reserveOut>output')));
 const fee=entry(quotes,'conditionalFee');assert.equal(fee.successful_return_paths.length,2);assert.ok(fee.successful_return_paths.some(p=>p.value==='(amount, 0)'));
 const profile=vault.sections.protocols.find(p=>p.profile==='erc4626');assert.equal(profile.economic_model_status,'code_derived_formulas_with_execution_obligations');
 assert.ok(profile.method_models.some(m=>m.signature==='previewDeposit(uint256)'&&m.return_formulas.some(f=>f.expression.includes('feeBps'))));
 assert.ok(vault.sections.property_obligations.some(p=>p.property==='erc4626_deposit_preview_bound'&&p.status==='requires_execution_with_declared_state'));
 assert.equal(profile.economic_model_verified,false);assert.equal(profile.deployed_protocol_identity_verified,false);
});

test('encoded CALL follows exact selectors and static ABI words without crediting malformed encodings',()=>{
 const r=analyze(compileMechanismFixture('EncodedCalls',true)),method=name=>r.sections.methods.find(m=>m.name===name);
 for(const name of ['encoded','signatureEncoding','typedEncoding']) {
  const funds=r.sections.funds.filter(f=>f.method_id===method(name).method_id);assert.equal(funds.length,1,name);
  assert.equal(funds[0].expanded_amount_expression,'amount');assert.equal(funds[0].expanded_to_expression,'to');assert.equal(funds[0].expanded_asset_expression,'token');
  assert.equal(funds[0].asset_standard_verified,false);assert.equal(funds[0].unit,'asset_quantity_or_token_id');
  assert.ok(r.sections.calls.some(c=>c.method_id===method(name).method_id&&c.selector==='0xa9059cbb'&&c.signature==='transfer(address,uint256)'));
 }
 for(const name of ['wrongSelector','wrongAmount','encodeOnly'])assert.equal(method(name).fund_ids.length,0,name);
});
