import test from 'node:test';
import assert from 'node:assert/strict';
import{analyze}from'../analyze.mjs';
import{compileVerificationFixture}from'./verification-fixtures.mjs';
import{exactPowerSum}from'../verification/polynomials.mjs';
import{evaluateReturn}from'../../scripts/contract-analysis/expression_model_eval.mjs';
import{propertyTranslator}from'../properties/smt.mjs';
const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');
const M=1n<<256n;
function run(source,optimized=true){
 const result=analyze(compileVerificationFixture(optimized,'pragma solidity ^0.8.24;contract Verification{'+source+'}'));
 assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(result.sections.engines));
 if(actualSolver)for(const loop of result.sections.loop_invariants)assert.equal(loop.complete,true,JSON.stringify({loop,failures:result.sections.verification_obligations.filter(p=>p.status!=='proved_under_declared_model')}));
 return result;
}
test('discrete polynomial sums agree with independent finite accumulation',()=>{
 for(let degree=0;degree<=3;degree++){
  let expected=0n;
  for(let n=0n;n<=1024n;n++){assert.equal(exactPowerSum(degree,n),expected);expected+=n**BigInt(degree);}
 }
});
test('checked polynomial summaries preserve empty loops, source overflow and subtraction',()=>{
 for(const optimized of [false,true]){
  const result=run('function calc(uint256 n,uint256 c,uint256 initial)external pure returns(uint256 a){a=initial;for(uint256 i;i<n;i++)a+=c*i*i+3*i+7;}'+
   'function subtract(uint256 n,uint256 initial)external pure returns(uint256 a){a=initial;for(uint256 i;i<n;i++)a-=i*i;}',optimized);
  if(!actualSolver)continue;
  for(const n of [0n,1n,17n,128n,10000n]){
   let expected=13n;for(let i=0n;i<n;i++)expected+=3n*i*i+3n*i+7n;
   assert.deepEqual(evaluateReturn(result,'calc',{n,c:3n,initial:13n}),[expected]);
  }
  assert.deepEqual(evaluateReturn(result,'calc',{n:0n,c:M-1n,initial:M-1n}),[M-1n]);
  assert.throws(()=>evaluateReturn(result,'calc',{n:3n,c:M-1n,initial:0n}),/panic_0x11/);
  assert.deepEqual(evaluateReturn(result,'subtract',{n:128n,initial:1000000n}),[309120n]);
  assert.throws(()=>evaluateReturn(result,'subtract',{n:3n,initial:1n}),/panic_0x11/);
 }
});
test('word-ring polynomial summaries certify nested multiplication and negative terms',()=>{
 const result=run('function calc(uint256 n,uint256 c)external pure returns(uint256 a){unchecked{for(uint256 i;i<n;i++)a+=c*i*i*i-i;}}');
 if(!actualSolver)return;
 for(const n of [0n,1n,17n,128n,10000n]){
  let expected=0n;for(let i=0n;i<n;i++)expected=(expected+(M-1n)*i*i*i-i)%M;
  assert.deepEqual(evaluateReturn(result,'calc',{n,c:M-1n}),[(expected+M)%M]);
 }
 const obligations=result.sections.verification_obligations;
 assert.ok(obligations.some(p=>p.property.startsWith('loop_word_product_projection_')));
 assert.ok(obligations.some(p=>p.property.startsWith('polynomial_integrality_preservation_')));
 for(const property of obligations){const saved=JSON.parse(result.sections.artifacts.find(a=>a.artifact_id===property.proof_artifact_id).text);assert.deepEqual(saved.property,property);}
});
test('checked increment guards stay lazy at zero iterations and retain hidden intermediate overflow',()=>{
 const result=run('function calc(uint256 n,uint256 c)external pure returns(uint256 a){for(uint256 i;i<n;i++)a+=(c+i)*i*0;}');
 if(!actualSolver)return;
 assert.deepEqual(evaluateReturn(result,'calc',{n:0n,c:M-1n}),[0n]);
 assert.deepEqual(evaluateReturn(result,'calc',{n:1n,c:M-1n}),[0n]);
 assert.throws(()=>evaluateReturn(result,'calc',{n:2n,c:M-1n}),/panic_0x11/);
 const method=result.sections.methods.find(m=>m.name==='calc'),translated=propertyTranslator(result,method);
 assert.equal(translated.walk(method.entry_return_expression_id).kind,'Int');
});
test('while loops use proved summaries and unsupported recurrence dependencies remain incomplete',()=>{
 const result=run('function calc(uint256 n)external pure returns(uint256 a){uint256 i;while(i<n){a+=i*i;i++;}}');
 if(actualSolver)assert.deepEqual(evaluateReturn(result,'calc',{n:10000n}),[333283335000n]);
 const invalid=analyze(compileVerificationFixture(true,'pragma solidity ^0.8.24;contract Verification{'+
  'function narrow(uint8 n)external pure returns(uint256 a){unchecked{for(uint8 i;i<n;i++)a+=i*i;}}'+
  'function coupled(uint256 n)external pure returns(uint256 a){uint256 b;for(uint256 i;i<n;i++){a+=b;b+=a;}}'+
  'function divide(uint256 n)external pure returns(uint256 a){for(uint256 i;i<n;i++)a+=i/2;}}'));
 for(const loop of invalid.sections.loop_invariants)assert.equal(loop.complete,false);
 assert.ok(invalid.sections.loop_invariants.some(m=>m.unresolved.some(s=>s.includes('NARROW_INTERMEDIATE'))));
 assert.ok(invalid.sections.loop_invariants.some(m=>m.unresolved.some(s=>s.includes('COUPLED_CYCLIC_RECURRENCE'))));
 assert.ok(invalid.sections.loop_invariants.some(m=>m.unresolved.some(s=>s.includes('NOT_POLYNOMIAL'))));
});

test('state transitions consume discharged loop certificates before proving ledger preservation',()=>{
 const source=`pragma solidity ^0.8.24;contract Verification{
 address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;
 constructor(){owner=msg.sender;}
 function balanceOf(address who)external view returns(uint256){return balances[who];}
 function mint(address to,uint256 n)external{require(msg.sender==owner);uint256 sum;for(uint256 i;i<n;i++)sum+=i*i;balances[to]+=sum;totalSupply+=sum;}
 }`;
 const result=analyze(compileVerificationFixture(true,source));
 if(actualSolver){
  assert.equal(result.sections.loop_invariants[0].complete,true);
  assert.equal(result.sections.state_transitions[0].complete,true,JSON.stringify(result.sections.state_transitions));
  assert.ok(result.sections.state_transitions[0].effects.some(e=>e.kind==='inductive_loop'&&e.loop_model_id===result.sections.loop_invariants[0].model_id));
  assert.equal(result.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation').complete,true,JSON.stringify(result.sections.protocol_invariants));
  assert.equal(result.sections.authorization_models[0].complete,true);
  const broken=analyze(compileVerificationFixture(true,source.replace('totalSupply+=sum;','totalSupply+=sum+1;')));
  assert.equal(broken.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation').status,'counterexample');
 }
});
