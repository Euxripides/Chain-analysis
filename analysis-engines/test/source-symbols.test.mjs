import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture} from './verification-fixtures.mjs';
import {shadowGuardSource,shadowMessageSource} from './source-symbol-fixtures.mjs';
import {evaluateReturn} from '../../scripts/contract-analysis/expression_model_eval.mjs';
const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');
for(const name of ['require','assert'])test('user-defined '+name+' is executed as an internal function and cannot invent a guard',()=>{
 const result=analyze(compileVerificationFixture(true,shadowGuardSource(name))),take=result.sections.methods.find(m=>m.name==='take'),read=result.sections.methods.find(m=>m.name==='read');
 assert.ok(result.sections.state_transitions.every(t=>t.complete));
 assert.equal(result.sections.permissions.filter(p=>[take.method_id,read.method_id].includes(p.method_id)).length,0);
 assert.equal(read.entry_return_model_status,'represented_source_dependencies_resolved',JSON.stringify(read.entry_return_model_unresolved));
 assert.deepEqual(evaluateReturn(result,'read',{n:9n}),[9n]);
 if(actualSolver)assert.equal(result.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity').status,'counterexample');
});
test('a state struct named msg retains its storage identity instead of the EVM caller',()=>{
 const result=analyze(compileVerificationFixture(true,shadowMessageSource)),take=result.sections.methods.find(m=>m.name==='take');
 assert.equal(result.sections.state_transitions.find(t=>t.method_id===take.method_id).complete,true);
 const fields=result.sections.expressions.filter(e=>e.method_id===take.method_id&&e.text==='msg.sender');assert.ok(fields.length);assert.ok(fields.every(e=>e.builtin_global===null));
 assert.ok(result.sections.permissions.every(p=>!(p.caller_constraints||[]).length));
 if(actualSolver)assert.equal(result.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity').status,'counterexample');
});
test('parentheses preserve the actual compiler-provided caller identity',()=>{
 const source='pragma solidity ^0.8.24;contract Verification{address public owner;constructor(){owner=(msg).sender;}function take(address next)external{require((msg).sender==owner);owner=next;}}';
 const result=analyze(compileVerificationFixture(true,source));assert.ok(result.sections.state_transitions.every(t=>t.complete));
 assert.ok(result.sections.permissions.some(p=>p.caller_constraints.some(c=>c.principal==='msg.sender')));
 if(actualSolver)assert.equal(result.sections.authorization_models[0].complete,true,JSON.stringify(result.sections.authorization_models));
});
