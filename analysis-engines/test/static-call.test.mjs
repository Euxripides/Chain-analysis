import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture} from './verification-fixtures.mjs';
import {RULE_VERSION} from '../model.mjs';

const source=`pragma solidity ^0.8.30;
interface IOracle { function quote(uint256 x) external view returns (uint256); function pair(uint256 x) external view returns (uint256,uint256); function blob() external view returns (bytes memory); }
interface ISink { function poke(uint256 x) external; }
contract Verification {
 uint256 public total;uint256 public left;uint256 public right;IOracle public oracle;
 constructor(IOracle o){oracle=o;}
 function settle(IOracle source,uint256 x) external { total = source.quote(x); }
 function settlePair(uint256 x) external { (uint256 a,uint256 b) = oracle.pair(x); total = a + b; }
 function settleBlob() external { bytes memory value = oracle.blob(); total = value.length; }
 function mutate(ISink sink,uint256 x) external { sink.poke(x); }
}`;
const cache=new Map();
function inspect(optimized){if(cache.has(optimized))return cache.get(optimized);const result=analyze(compileVerificationFixture(optimized,source));assert.equal(result.rule_version,RULE_VERSION);assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');cache.set(optimized,result);return result;}
const method=(result,name)=>{const row=result.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(row,name);return row;};
const transition=(result,name)=>{const row=result.sections.state_transitions.find(t=>t.method_id===method(result,name).method_id);assert.ok(row,name);return row;};

for(const optimized of[false,true]){
 test(`readonly external calls model as STATICCALL host reads optimizer=${optimized}`,()=>{
  const result=inspect(optimized),state=transition(result,'settle');
  assert.equal(state.complete,true,JSON.stringify(state.unresolved));
  assert.equal(state.paths[0].writes.length,1);
  assert.match(state.paths[0].writes[0].new_value,/^static_return_/);
  const effect=state.effects.find(e=>e.kind==='static_call');
  assert.ok(effect);
  assert.equal(effect.call_type,'STATICCALL');assert.equal(effect.signature,'quote(uint256)');assert.equal(effect.return_binding,'requires_target_identity_and_execution');
  assert.equal(effect.state_effect,'none_by_execution_rules');assert.equal(effect.failure,'reverts_on_failure');assert.equal(effect.return_count,1);
 });
 test(`readonly tuple returns bind local slots and stay read-only optimizer=${optimized}`,()=>{
  const result=inspect(optimized),state=transition(result,'settlePair');
  assert.equal(state.complete,true,JSON.stringify(state.unresolved));
  assert.equal(state.effects.filter(e=>e.kind==='static_call').length,1);
  assert.equal(state.effects.find(e=>e.kind==='static_call').return_count,2);
  assert.equal(state.paths[0].writes.length,1);
 });
 test(`bytes returns and state-mutating external calls stay explicit gaps optimizer=${optimized}`,()=>{
  const result=inspect(optimized),blob=transition(result,'settleBlob');
  assert.equal(blob.complete,false);assert.ok(blob.unresolved.some(v=>v.startsWith('STATE_STATIC_CALL_RETURN_TYPE_UNSUPPORTED:bytes')));
  const mutate=transition(result,'mutate');
  assert.equal(mutate.complete,false);assert.ok(mutate.unresolved.includes('STATE_EXTERNAL_CALL_REQUIRES_MODEL'));
 });
}
