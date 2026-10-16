import test from'node:test';import assert from'node:assert/strict';
import{analyze}from'../analyze.mjs';import{compileVerificationFixture}from'./verification-fixtures.mjs';
import{evaluateReturn}from'../../scripts/contract-analysis/expression_model_eval.mjs';import{propertyTranslator}from'../properties/smt.mjs';
const solver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1'),M=1n<<256n;
function run(body){
 const result=analyze(compileVerificationFixture(true,'pragma solidity ^0.8.24;contract Verification{'+body+'}'));
 assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(result.sections.engines));
 if(solver)for(const m of result.sections.loop_invariants)assert.equal(m.complete,true,JSON.stringify({model:m,failures:result.sections.verification_obligations.filter(p=>p.status!=='proved_under_declared_model').map(p=>({property:p.property,status:p.status,reason:p.reason}))}));
 return result;
}
test('source coupled loops prove ordered reads and preserve nonzero initial values',()=>{
 const result=run('function forward(uint n,uint c,uint x,uint y)external pure returns(uint a,uint b){a=x;b=y;for(uint i;i<n;i++){a+=c;b+=a;}}'+
  'function reverse(uint n,uint c,uint x,uint y)external pure returns(uint a,uint b){a=x;b=y;for(uint i;i<n;i++){b+=a;a+=c;}}');
 assert.equal(result.sections.loop_invariants.length,2);assert.ok(result.sections.loop_invariants.every(m=>m.coupled_certificates.length===2));
 if(!solver)return;
 for(const n of[0n,1n,17n,128n,10000n,1000000000000000n]){
  assert.deepEqual(evaluateReturn(result,'forward',{n,c:3n,x:7n,y:11n}),[7n+3n*n,11n+7n*n+3n*n*(n+1n)/2n]);
  assert.deepEqual(evaluateReturn(result,'reverse',{n,c:3n,x:7n,y:11n}),[7n+3n*n,11n+7n*n+3n*n*(n-1n)/2n]);
 }
 for(const name of['forward','reverse']){
  assert.deepEqual(evaluateReturn(result,name,{n:0n,c:M-1n,x:M-1n,y:M-1n}),[M-1n,M-1n]);
  assert.throws(()=>evaluateReturn(result,name,{n:1n,c:1n,x:M-1n,y:0n}),/panic_0x11/);
  const method=result.sections.methods.find(m=>m.name===name),translated=propertyTranslator(result,method),values=result.sections.expressions.filter(e=>e.method_id===method.method_id&&e.symbolic_format==='coupled_polynomial_value'&&e.empty_loop_is_entry);
  assert.ok(values.length>=2);for(const value of values)assert.equal(translated.walk(value.expression_id).kind,'Int');
 }
});
test('three and four coupled accumulators use exact rational closed forms',()=>{
 const result=run('function calc(uint n,uint c)external pure returns(uint a,uint b,uint d,uint e){a=2;b=3;d=5;e=7;for(uint i;i<n;i++){a+=c;b+=a;d+=b;e+=d;}}');
 if(!solver)return;
 for(const n of[0n,1n,17n,128n,10000n]){
  let a=2n,b=3n,d=5n,e=7n;for(let i=0n;i<n;i++){a+=11n;b+=a;d+=b;e+=d;}
  assert.deepEqual(evaluateReturn(result,'calc',{n,c:11n}),[a,b,d,e]);
 }
});
test('coupled summary retains hidden intermediate overflow and all unused updates',()=>{
 const result=run('function hidden(uint n,uint c,uint initial)external pure returns(uint b){uint a=initial;for(uint i;i<n;i++){a+=c;b+=(a+i)*0;}}'+
  'function unused(uint n,uint c,uint initial)external pure returns(uint b){uint a=initial;for(uint i;i<n;i++){b+=a;a+=c;}}');
 if(!solver)return;
 assert.deepEqual(evaluateReturn(result,'hidden',{n:0n,c:M-1n,initial:M-1n}),[0n]);
 assert.deepEqual(evaluateReturn(result,'hidden',{n:1n,c:0n,initial:M-1n}),[0n]);
 assert.throws(()=>evaluateReturn(result,'hidden',{n:2n,c:0n,initial:M-1n}),/panic_0x11/);
 assert.throws(()=>evaluateReturn(result,'unused',{n:1n,c:1n,initial:M-1n}),/panic_0x11/);
});
test('checked nonlinear coupled increments retain the exact source multiplication',()=>{
 const result=run('function calc(uint n,uint c,uint initial)external pure returns(uint b){uint a=initial;for(uint i;i<n;i++){a+=c;b+=a*a;}}');
 if(!solver)return;
 for(const n of[0n,1n,17n,128n,10000n]){let a=2n,b=0n;for(let i=0n;i<n;i++){a+=3n;b+=a*a;}assert.deepEqual(evaluateReturn(result,'calc',{n,c:3n,initial:2n}),[b]);}
 assert.throws(()=>evaluateReturn(result,'calc',{n:1n,c:0n,initial:1n<<128n}),/panic_0x11/);
});
test('coupled summaries reach state transitions and ledger proofs only after all obligations',()=>{
 const source='address public owner;mapping(address=>uint)public balances;uint public totalSupply;constructor(){owner=msg.sender;}'+
  'function balanceOf(address user)external view returns(uint){return balances[user];}'+
  'function mint(address to,uint n,uint c)external{require(msg.sender==owner);uint a;uint b;for(uint i;i<n;i++){a+=c;b+=a;}balances[to]+=b;totalSupply+=b;}';
 const result=run(source);if(!solver)return;
 assert.ok(result.sections.state_transitions.every(m=>m.complete),JSON.stringify(result.sections.state_transitions));
 assert.ok(result.sections.state_transitions.some(m=>m.effects.some(e=>e.kind==='inductive_coupled_loop')));
 assert.equal(result.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation').complete,true);
 const bad=run(source.replace('totalSupply+=b;','totalSupply+=b+1;'));assert.equal(bad.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation').status,'counterexample');
});
test('unchecked coupled refinements and feedback remain explicit until proved',()=>{
 const result=analyze(compileVerificationFixture(true,'pragma solidity ^0.8.24;contract Verification{'+
  'function feedback(uint n)external pure returns(uint a){uint b;for(uint i;i<n;i++){a+=b;b+=a;}}'+
  'function wrapping(uint n,uint c)external pure returns(uint a,uint b){unchecked{for(uint i;i<n;i++){a+=c;b+=a;}}}}'));
 for(const m of result.sections.loop_invariants)assert.equal(m.complete,false);
 assert.ok(result.sections.loop_invariants.some(m=>m.unresolved.some(s=>s.includes('COUPLED_CYCLIC'))));
 assert.ok(result.sections.loop_invariants.some(m=>m.unresolved.some(s=>s.includes('WORD_RING_REFINEMENT_PENDING'))));
});
test('coupled while loops preserve their counter update inside the body',()=>{
 const result=run('function calc(uint n,uint c)external pure returns(uint a,uint b){uint i;while(i<n){a+=c;b+=a;i++;}}');
 if(!solver)return;
 for(const n of[0n,1n,17n,128n,10000n])assert.deepEqual(evaluateReturn(result,'calc',{n,c:3n}),[3n*n,3n*n*(n+1n)/2n]);
});
test('narrow checked counter multiplication is preserved within coupled wide accumulators',()=>{
 const result=run('function calc(uint8 n)external pure returns(uint a,uint b){for(uint8 i;i<n;i++){a+=i*i;b+=a;}}');
 if(!solver)return;
 for(const n of[0n,1n,15n,16n]){let a=0n,b=0n;for(let i=0n;i<n;i++){a+=i*i;b+=a;}assert.deepEqual(evaluateReturn(result,'calc',{n}),[a,b]);}
 for(const n of[17n,255n])assert.throws(()=>evaluateReturn(result,'calc',{n}),/panic_0x11/);
});
