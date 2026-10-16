import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture,rolesSource} from './verification-fixtures.mjs';
import {Terms,MAX_WORD} from '../verification/terms.mjs';

const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');
import {ADDRESS_MASK,assemblyLedgerSource} from './assembly-storage-fixtures.mjs';
const run=(source,optimized=true,evm='shanghai')=>{
 const fixture=compileVerificationFixture(optimized,source,evm);fixture.snapshot.evm_rules=evm;const result=analyze(fixture);
 assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(result.sections.engines));return result;
};
const ledger=r=>r.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');
const transition=(r,name)=>r.sections.state_transitions.find(t=>r.sections.methods.find(m=>m.method_id===t.method_id)?.name===name);

test('raw digest congruence does not invent storage collision assumptions',()=>{
 const terms=new Terms(),parts=['key','slot'];
 assert.equal(terms.digestWords(parts),terms.hash(parts,['0']));
 const generic=terms.digestWords(['other','unknown']);
 assert.ok(![...terms.constraints].some(c=>c.includes('(distinct '+generic+' ')));
 assert.equal(terms.hashes.length,1);
 assert.ok([...terms.constraints].some(c=>c.includes(String(MAX_WORD))));
});

test('masked mapping assembly writes join source getters and discharge ledger preservation',()=>{
 for(const optimized of [false,true]){
  const r=run(assemblyLedgerSource(),optimized);
  for(const name of ['mint','transfer']){
   const t=transition(r,name);assert.equal(t.complete,true,JSON.stringify(t));
   assert.ok(t.paths.every(p=>p.writes.every(w=>w.root_ast_id!==null)));
   assert.ok(t.effects.some(e=>e.kind==='keccak256'&&e.storage_path_status==='declared_mapping_path'));
  }
  if(actualSolver){
   assert.equal(ledger(r).complete,true,JSON.stringify(ledger(r)));
   assert.equal(r.sections.authorization_models[0].complete,true,JSON.stringify(r.sections.authorization_models));
  }
 }
 if(actualSolver)assert.equal(ledger(run(assemblyLedgerSource({broken:true}))).status,'counterexample');
});

test('dirty address words and overwritten hash preimages cannot acquire declared balance writes',()=>{
 for(const settings of [{key:'to'},{between:'mstore8(63,127)'},{between:'mstore8(31,127)'}]){
  const r=run(assemblyLedgerSource(settings)),mint=transition(r,'mint');
  assert.ok(mint.paths.some(p=>p.writes.some(w=>w.root_ast_id===null)),JSON.stringify(mint));
  assert.equal(ledger(r).complete,false);
  assert.match(ledger(r).unresolved.join(','),/UNCLASSIFIED_RAW_STORAGE_WRITE/);
 }
});

test('overlapping MCOPY uses the pre-write memory snapshot and retains only exact full words',()=>{
 const r=run(assemblyLedgerSource({copied:true}),true,'cancun'),mint=transition(r,'mint');
 assert.equal(mint.complete,true,JSON.stringify(mint));
 assert.ok(mint.paths.every(p=>p.writes.every(w=>w.root_ast_id!==null)));
 if(actualSolver)assert.equal(ledger(r).complete,true,JSON.stringify(ledger(r)));
});

test('symbolic writes and sibling branch memory cannot reuse invalidated mapping provenance',()=>{
 const extra=`function corrupt(address to,uint256 p,uint256 overwrite)external{
  assembly{mstore(0,and(to,${ADDRESS_MASK}))mstore(32,balances.slot)
   if overwrite{mstore(p,127)}
   let key:=keccak256(0,64)sstore(key,1)}
 }`;
 const r=run(assemblyLedgerSource({extra})),t=transition(r,'corrupt');
 assert.equal(t.complete,false);assert.match(t.unresolved.join(','),/COMPILER_MEMORY_ALIAS/);
 assert.ok(t.paths.some(p=>p.writes.some(w=>w.root_ast_id===null)));
 assert.ok(t.paths.some(p=>p.writes.every(w=>w.root_ast_id!==null)));
 assert.equal(ledger(r).complete,false);
});

test('a scalar slot reference classifies every packed variable affected by the physical SSTORE',()=>{
 const r=run(`pragma solidity ^0.8.24;contract Verification{address public owner;uint96 public count;
 function overwrite(uint256 v)external{assembly{sstore(owner.slot,v)}}}`),t=transition(r,'overwrite');
 assert.equal(t.complete,true);assert.equal(t.paths[0].writes.length,2);
 assert.deepEqual(t.paths[0].writes.map(w=>[w.variable,w.offset,w.bits]),[['owner',0,160],['count',20,96]]);
 if(actualSolver)assert.equal(r.sections.authorization_models[0].status,'counterexample');
});

test('nested mapping hashes retain struct member identities for role administration proofs',()=>{
 const body=`function rawGrant(bytes32 role,address account)external{
  require(hasRole(getRoleAdmin(role),msg.sender));assembly{
   mstore(0,role)mstore(32,roles.slot)let parent:=keccak256(0,64)
   mstore(0,and(account,${ADDRESS_MASK}))mstore(32,parent)sstore(keccak256(0,64),1)
  }}
 function rawAdmin(bytes32 role,bytes32 next)external{
  require(hasRole(0,msg.sender));assembly{
   mstore(0,role)mstore(32,roles.slot)sstore(add(keccak256(0,64),1),next)
  }}
 `;
 const source=rolesSource.replace('contract Verification {','contract Verification {'+body),r=run(source);
 for(const name of ['rawGrant','rawAdmin'])assert.equal(transition(r,name).complete,true,JSON.stringify(transition(r,name)));
 const write=transition(r,'rawGrant').paths[0].writes[0];assert.equal(write.keys.length,2);assert.equal(write.fields.length,1);
 if(actualSolver){
  assert.equal(r.sections.authorization_models[0].complete,true,JSON.stringify(r.sections.authorization_models));
  const bad=run(source.replace('require(hasRole(getRoleAdmin(role),msg.sender));assembly{','assembly{'));
  assert.equal(bad.sections.authorization_models[0].status,'counterexample');
 }
});
