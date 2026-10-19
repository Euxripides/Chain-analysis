import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture} from './verification-fixtures.mjs';
import {inheritedInitializationSource,diamondCreationSource,immutableCreationSource,stateArgumentCreationSource} from './creation-fixtures.mjs';
const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');
function run(source,optimized=true,viaIR=false){
 const fixture=compileVerificationFixture(optimized,source,'shanghai',{viaIR});fixture.snapshot.evm_rules='shanghai';
 const r=analyze(fixture);assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(r.sections.engines));return r;
}
const initialization=r=>r.sections.protocol_invariants.find(m=>m.kind==='erc20_source_initialization');
function proveCreationValue(r,slot,expected){
 if(!actualSolver)return;
 const creation=r.sections.initial_state_models[0],proof=r.sections.verification_obligations.find(p=>p.property==='constructor_establishes_ledger_invariant'),saved=JSON.parse(r.sections.artifacts.find(a=>a.artifact_id===proof.proof_artifact_id).text);
 const violation='(or '+creation.paths.map(p=>'(and '+p.condition+' (distinct (select '+p.storage_after+' '+slot+') '+expected+'))').join(' ')+')';
 const query={...saved.query,property_id:'independent-creation-value',violation,variables:[]};
 const solve=spawnSync(process.env.CONTRACT_PYTHON||'/opt/contract-python/bin/python',[fileURLToPath(new URL('../properties/solver.py',import.meta.url))],{input:JSON.stringify({queries:[query]}),encoding:'utf8',timeout:20000});
 assert.equal(solve.status,0,solve.stderr);const answer=JSON.parse(solve.stdout)[0];assert.equal(answer.domain,'sat',JSON.stringify(answer));assert.equal(answer.decision,'unsat',JSON.stringify(answer));
}
test('creation respects the different inherited initializer orders of legacy and IR code generation',()=>{
 for(const optimized of [false,true])for(const viaIR of [false,true]){
  const r=run(inheritedInitializationSource,optimized,viaIR),creation=r.sections.initial_state_models[0];assert.equal(creation.complete,true,JSON.stringify(creation.unresolved));
  assert.deepEqual(creation.constructor_sequence.map(c=>c.contract),['Base','Verification']);
  assert.equal(creation.deployment_identity_verified,false);
  if(actualSolver)assert.equal(initialization(r).complete,true,JSON.stringify(initialization(r)));
  proveCreationValue(r,0,'42');proveCreationValue(r,1,viaIR?'42':'1');
 }
});
test('C3 diamonds call each constructor once and bind transitive base arguments',()=>{
 for(const viaIR of [false,true]){
  const r=run(diamondCreationSource,true,viaIR),creation=r.sections.initial_state_models[0];assert.equal(creation.complete,true,JSON.stringify(creation.unresolved));
  assert.deepEqual(creation.constructor_sequence.map(c=>c.contract),['Root','B','C','Verification']);
  if(actualSolver)assert.equal(initialization(r).complete,true,JSON.stringify(initialization(r)));
  proveCreationValue(r,3,'1234');
 }
});
test('base arguments observe the state at their compiler-specific evaluation point',()=>{
 for(const viaIR of [false,true]){const r=run(stateArgumentCreationSource,true,viaIR);assert.equal(r.sections.initial_state_models[0].complete,true);proveCreationValue(r,0,viaIR?'0':'7');}
});
test('constructor polynomial invariants discharge before establishing the source ledger',()=>{
 const r=run(`pragma solidity ^0.8.24;contract Root{address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;
 constructor(uint256 n){owner=msg.sender;uint256 sum;for(uint256 i;i<n;i++)sum+=i*i;balances[msg.sender]=sum;totalSupply=sum;}
 function balanceOf(address a)external view returns(uint256){return balances[a];}function mint(address a,uint256 v)external{require(msg.sender==owner);balances[a]+=v;totalSupply+=v;}}
 contract Verification is Root{constructor(uint256 n)Root(n){}}`);
 if(actualSolver){assert.equal(r.sections.loop_invariants[0].complete,true);assert.equal(r.sections.initial_state_models[0].complete,true,JSON.stringify(r.sections.initial_state_models));assert.equal(initialization(r).complete,true,JSON.stringify(initialization(r)));}
});
test('immutable construction values stay separate from persistent storage and bind to verified runtime words',()=>{
 const r=run(immutableCreationSource),creation=r.sections.initial_state_models[0];assert.equal(creation.complete,true,JSON.stringify(creation.unresolved));
 assert.equal(creation.paths[0].immutable_values.length,2);assert.ok(creation.paths[0].writes.every(w=>!['owner','deploymentChain'].includes(w.variable)));
 assert.ok(creation.effects.some(e=>e.kind==='immutable_assignment'&&e.variable==='owner'));
 assert.equal(r.sections.state_transitions[0].complete,true,JSON.stringify(r.sections.state_transitions[0].unresolved));
 if(actualSolver){assert.equal(initialization(r).complete,true);assert.equal(r.sections.authorization_models[0].complete,true,JSON.stringify(r.sections.authorization_models));}
});
test('creation gaps and invalid initial issuance remain visible to the policy result',()=>{
 if(!actualSolver)return;
 const wrong=run(inheritedInitializationSource.replace('totalSupply=y;','totalSupply=y+1;'));assert.equal(initialization(wrong).status,'counterexample');
 const unknown=run(inheritedInitializationSource.replace('constructor(){x=42;}','constructor(){assembly{let p:=mload(64) sstore(p,1)}}'));
 assert.equal(initialization(unknown).complete,false);assert.ok(unknown.sections.initial_state_models[0].paths.some(p=>p.writes.some(w=>w.root_ast_id===null)));
});

import {createHash as creationHash}from'node:crypto';
import {id as creationRecordID}from'../model.mjs';

function creationMaterial(value,{explicit=false}={}){
 const body=explicit?`uint256 public value;constructor(){value=${value};}`:`uint256 public value=${value};`;
 const source=`pragma solidity ^0.8.30;contract Verification{${body}function set(uint256 n)external{value=n;}}`;
 return {kind:'source',source:{language:'Solidity',files:{'Verification.sol':source},compiler_version:'0.8.30+commit.73712a01',target:'Verification.sol:Verification',settings:{evmVersion:'shanghai',metadata:{bytecodeHash:'none'}}}};
}
function creationFromMaterial(material){
 const raw=JSON.stringify(material),input_sha256=creationHash('sha256').update(raw).digest('hex');
 const model=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256,state_granularity:'symbolic_state',evm_rules:'shanghai'},engines:['solc-ast']});
 assert.equal(model.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');
 const initial=model.sections.initial_state_models[0],method=model.sections.methods.find(m=>m.method_id===initial.method_id);assert.ok(method);
 return {model,initial,method,input_sha256};
}
test('different input and initcode with equal runtime retain different implicit creation and initial model identities',()=>{
 const left=creationFromMaterial(creationMaterial(7)),right=creationFromMaterial(creationMaterial(9));
 assert.equal(left.model.snapshot.code_hash,right.model.snapshot.code_hash);assert.notEqual(left.input_sha256,right.input_sha256);
 assert.notEqual(left.initial.creation_identity.initcode,right.initial.creation_identity.initcode);
 assert.notEqual(left.method.method_id,right.method.method_id);assert.notEqual(left.initial.model_id,right.initial.model_id);
 assert.equal(left.method.dispatch_role,'implicit_source_definition');assert.equal(right.method.dispatch_role,'implicit_source_definition');
 for(const result of[left,right]){
  assert.equal(result.initial.deployment_identity_verified,false);assert.ok(result.method.completeness.unresolved.includes('DEPLOYMENT_CREATION_IDENTITY_NOT_VERIFIED'));
  const evidence=new Map(result.model.sections.evidence.map(e=>[e.evidence_id,e]));assert.ok(result.method.evidence_ids.length);for(const eid of result.method.evidence_ids)assert.equal(evidence.get(eid)?.method_id,result.method.method_id);
 }
});
test('the same submitted source material retains stable creation and model identities across analysis runs',()=>{
 const material=creationMaterial(7),left=creationFromMaterial(material),right=creationFromMaterial(material);
 assert.equal(left.method.method_id,right.method.method_id);assert.equal(left.initial.model_id,right.initial.model_id);assert.deepEqual(left.method.evidence_ids,right.method.evidence_ids);
});
test('observed templates without submitted input keep the existing implicit creation identity',()=>{
 const fixture=compileVerificationFixture(true,'pragma solidity ^0.8.30;contract Verification{uint256 public value=7;function set(uint256 n)external{value=n;}}','shanghai');
 assert.equal(fixture.snapshot.input_sha256,undefined);const model=analyze(fixture),initial=model.sections.initial_state_models[0],method=model.sections.methods.find(m=>m.method_id===initial.method_id);
 assert.equal(method.dispatch_role,'implicit_source_definition');assert.equal(method.method_id,creationRecordID(fixture.snapshot.code_hash,fixture.snapshot.storage_address,'source-creation'));assert.equal(initial.deployment_identity_verified,false);
});
test('explicit constructors retain their original declaration binding and submitted material separation',()=>{
 const left=creationFromMaterial(creationMaterial(7,{explicit:true})),right=creationFromMaterial(creationMaterial(9,{explicit:true}));
 assert.equal(left.model.snapshot.code_hash,right.model.snapshot.code_hash);assert.notEqual(left.method.method_id,right.method.method_id);
 for(const result of[left,right]){assert.notEqual(result.method.dispatch_role,'implicit_source_definition');assert.equal(result.model.sections.methods.filter(m=>m.method_id===result.initial.method_id).length,1);assert.equal(result.initial.deployment_identity_verified,false);}
});
