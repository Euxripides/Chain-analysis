import test from'node:test';import assert from'node:assert/strict';
import{analyze}from'../analyze.mjs';import{compileVerificationFixture,rolesSource}from'./verification-fixtures.mjs';import{erc7201Root}from'../namespace-layout.mjs';
const root='0x'+erc7201Root('example.main').toString(16).padStart(64,'0');
const solver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');
const source=`pragma solidity ^0.8.24;contract Verification{
/// @custom:storage-location erc7201:example.main
struct Store{address owner;uint8 initialized;mapping(address=>uint)balances;uint total;}
bytes32 constant ROOT=${root};
function store()private pure returns(Store storage $){assembly{$.slot:=ROOT}}
constructor(){Store storage $=store();$.owner=msg.sender;$.initialized=1;}
function owner()external view returns(address){return store().owner;}
function balanceOf(address user)external view returns(uint){return store().balances[user];}
function totalSupply()external view returns(uint){return store().total;}
function transferOwnership(address next)external{Store storage $=store();require(msg.sender==$.owner);$.owner=next;}
function setFlag(uint8 value)external{store().initialized=value;}
function mint(address to,uint amount)external{Store storage $=store();require(msg.sender==$.owner);$.balances[to]+=amount;$.total+=amount;}
function helper(Store storage $,address to,uint amount)internal{$.balances[to]+=amount;$.total+=amount;}
function helperMint(address to,uint amount)external{require(msg.sender==store().owner);helper(store(),to,amount);}
}`;
function run(text=source){const result=analyze(compileVerificationFixture(true,text));assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(result.sections.engines));return result;}
test('verified namespace pointers reach packed fields and mapping state transitions',()=>{
 const r=run();assert.equal(r.sections.state_transitions.length,4);
 for(const t of r.sections.state_transitions)assert.equal(t.complete,true,JSON.stringify(t));
 const layout=JSON.parse(r.sections.artifacts.find(a=>a.kind==='storage_layout').text),fields=layout.storage.filter(f=>f.namespace_id==='example.main'),byname=Object.fromEntries(fields.map(f=>[f.label,f]));
 assert.equal(fields.length,4);assert.equal(byname.owner.slot,byname.initialized.slot);assert.equal(byname.initialized.offset,20);
 const transitions=Object.fromEntries(r.sections.state_transitions.map(t=>[r.sections.methods.find(m=>m.method_id===t.method_id).name,t]));
 for(const name of['mint','helperMint']){
  const writes=transitions[name].paths.flatMap(p=>p.writes);assert.ok(writes.some(w=>w.root_ast_id===byname.balances.astId&&w.keys.length===1));
  assert.ok(writes.some(w=>w.root_ast_id===byname.total.astId&&w.keys.length===0));
 }
 assert.ok(transitions.transferOwnership.paths.flatMap(p=>p.writes).every(w=>w.root_ast_id===byname.owner.astId));
 assert.ok(transitions.setFlag.paths.flatMap(p=>p.writes).every(w=>w.root_ast_id===byname.initialized.astId&&w.offset===20));
 for(const name of['mint','helperMint']){
  const method=r.sections.methods.find(m=>m.name===name),writes=r.sections.storage.filter(s=>s.method_id===method.method_id&&s.access==='write');
  const balance=writes.find(w=>w.state_ast_id===byname.balances.astId),supply=writes.find(w=>w.state_ast_id===byname.total.astId);
  assert.ok(balance,JSON.stringify(writes));assert.ok(supply,JSON.stringify(writes));
  assert.equal(balance.slot_role,'layout_base');assert.equal(balance.storage_projection.base_slot,BigInt(root).toString());
  const key=balance.storage_projection.path.find(p=>p.kind==='mapping');assert.equal(key.key_referenced_declaration,method.parameters[0].source_ast_id);
  assert.equal(supply.slot_role,'direct_slot');assert.equal(supply.slot,byname.total.slot);assert.equal(supply.storage_projection.static_slot,supply.slot);
 }
 const flagMethod=r.sections.methods.find(m=>m.name==='setFlag'),flagWrite=r.sections.storage.find(s=>s.method_id===flagMethod.method_id&&s.access==='write');
 assert.equal(flagWrite.offset,20);assert.equal(flagWrite.slot,byname.initialized.slot);assert.equal(flagWrite.slot_role,'direct_slot');
 assert.equal(layout.namespace_storage.complete,true);assert.ok(layout.namespace_storage.namespaces[0].evidence_ids.length>=4);
 for(const evidence of layout.namespace_storage.namespaces[0].evidence_ids)assert.ok(r.sections.evidence.some(e=>e.evidence_id===evidence));
 assert.equal(r.sections.coverage.find(c=>c.dimension==='namespaced_storage').complete,true);
 assert.equal(transitions.helperMint.namespace_storage.historical_initialization_verified,false);
 const owner=r.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity'),ledger=r.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');
 assert.equal(owner.owner_state_ast_id,byname.owner.astId);assert.equal(ledger.balance_state_ast_id,byname.balances.astId);assert.equal(ledger.supply_state_ast_id,byname.total.astId);
 if(solver){assert.equal(owner.complete,true,JSON.stringify(owner));assert.equal(ledger.complete,true,JSON.stringify(ledger));}
 assert.equal(r.sections.verification_closure[0].whole_authorization_proved,false);assert.equal(r.sections.verification_closure[0].whole_protocol_economics_proved,false);
});

test('local storage aliases in namespace getters preserve owner and ledger proof identities',()=>{
 const r=run(source.replace('return store().owner;','Store storage ptr=store();return ptr.owner;').replace('return store().balances[user];','Store storage ptr=store();return ptr.balances[user];').replace('return store().total;','Store storage ptr=store();return ptr.total;'));
 assert.ok(r.sections.state_transitions.every(t=>t.complete));
 const owner=r.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity'),ledger=r.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation');
 assert.ok(owner);assert.ok(ledger);if(solver){assert.equal(owner.complete,true,JSON.stringify(owner));assert.equal(ledger.complete,true,JSON.stringify(ledger));}
});

test('role membership and administrator fields use the same verified namespace mapping',()=>{
 const definition=`/// @custom:storage-location erc7201:example.main
 struct Store{mapping(bytes32=>Role) roles;}
 bytes32 constant ROOT=${root};
 function store()private pure returns(Store storage $){assembly{$.slot:=ROOT}}`;
 const text=rolesSource.replace('mapping(bytes32=>Role) private roles;',definition).replaceAll('return roles[','return store().roles[').replaceAll(';roles[',';store().roles[');
 const good=run(text),bad=run(text.replace('require(hasRole(getRoleAdmin(role),msg.sender));',''));
 assert.ok(good.sections.state_transitions.every(t=>t.complete),JSON.stringify(good.sections.state_transitions));
 const model=good.sections.authorization_models.find(m=>m.kind==='access_control_admin_integrity');assert.ok(model);
 if(solver){assert.equal(model.complete,true,JSON.stringify(model));assert.equal(bad.sections.authorization_models.find(m=>m.kind==='access_control_admin_integrity').status,'counterexample');}
});
test('namespace ownership bypass and inflation retain concrete proof counterexamples',()=>{
 const bypass=run(source.replace('require(msg.sender==$.owner);$.owner=next;','$.owner=next;'));
 const inflation=run(source.replace('$.total+=amount;}','$.total+=amount+1;}'));
 if(solver){assert.equal(bypass.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity').status,'counterexample');assert.equal(inflation.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation').status,'counterexample');}
});
test('assembly overwrite of a packed namespace word records both physical field effects',()=>{
 const r=run(source.replace(/\}\s*$/,'function pollute(uint value)external{assembly{sstore(ROOT,value)}}}')),
  layout=JSON.parse(r.sections.artifacts.find(a=>a.kind==='storage_layout').text),fields=layout.storage.filter(f=>f.namespace_id==='example.main'),byname=Object.fromEntries(fields.map(f=>[f.label,f])),
  method=r.sections.methods.find(m=>m.name==='pollute'),transition=r.sections.state_transitions.find(t=>t.method_id===method.method_id);
 assert.equal(transition.complete,true,JSON.stringify(transition));const writes=transition.paths.flatMap(p=>p.writes);
 assert.ok(writes.some(w=>w.raw_storage_effect&&w.root_ast_id===byname.owner.astId&&w.offset===0));
 assert.ok(writes.some(w=>w.raw_storage_effect&&w.root_ast_id===byname.initialized.astId&&w.offset===20));
 assert.equal(writes.length,2);if(solver)assert.equal(r.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity').status,'counterexample');
});

test('namespace mapping hashes and checked assembly writes share the typed ledger',()=>{
 const raw=`function rawMint(address to,uint amount)external{Store storage $=store();require(msg.sender==$.owner);uint next=$.balances[to]+amount;uint total=$.total+amount;
 assembly{mstore(0,and(to,0xffffffffffffffffffffffffffffffffffffffff))mstore(32,add(ROOT,1))sstore(keccak256(0,64),next)sstore(add(ROOT,2),total)}}`;
 const r=run(source.replace(/\}\s*$/,raw+'}')),method=r.sections.methods.find(m=>m.name==='rawMint'),transition=r.sections.state_transitions.find(t=>t.method_id===method.method_id);
 assert.equal(transition.complete,true,JSON.stringify(transition));const writes=transition.paths.flatMap(p=>p.writes);
 assert.equal(writes.length,2);assert.ok(writes.every(w=>w.raw_storage_effect&&w.root_ast_id!==null));assert.equal(writes[0].keys.length,1);
 if(solver)assert.equal(r.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation').complete,true,JSON.stringify(r.sections.protocol_invariants));
});

test('unbounded coupled loop summaries preserve namespace owner and ledger identities',()=>{
 const loop=`function loopMint(address to,uint n,uint c)external{Store storage $=store();require(msg.sender==$.owner);uint a;uint b;for(uint i;i<n;i++){a+=c;b+=a;}$.balances[to]+=b;$.total+=b;}`;
 const r=run(source.replace(/\}\s*$/,loop+'}')),method=r.sections.methods.find(m=>m.name==='loopMint');
 if(solver){
  assert.equal(r.sections.state_transitions.find(t=>t.method_id===method.method_id).complete,true);
  assert.ok(r.sections.loop_invariants.every(m=>m.complete));
  assert.equal(r.sections.authorization_models.find(m=>m.kind==='owner_identity_integrity').complete,true);
  assert.equal(r.sections.protocol_invariants.find(m=>m.kind==='erc20_ledger_conservation').complete,true);
 }
});

test('a bad virtual override cannot inherit a validated base namespace accessor',()=>{
 const text=source.replace('contract Verification{','contract Base{').replace('function store()private pure','function store()internal pure virtual')+
 'contract Verification is Base{function store()internal pure override returns(Store storage $){assembly{$.slot:=0x'+'2'.repeat(64)+'}}}';
 const r=run(text);assert.ok(r.sections.state_transitions.every(t=>!t.complete));assert.equal(r.sections.coverage.find(c=>c.dimension==='namespaced_storage').complete,false);
 const layout=JSON.parse(r.sections.artifacts.find(a=>a.kind==='storage_layout').text);assert.equal(layout.namespace_storage.accessors.filter(a=>a.complete).length,1);
 assert.ok(layout.namespace_storage.accessors.some(a=>a.unresolved.includes('NAMESPACE_ACTUAL_ROOT_MISMATCH')));
});

test('conditional and loop pointer rebindings do not publish an assumed physical field',()=>{
 const body=`pragma solidity ^0.8.24;contract Verification{
 /// @custom:storage-location erc7201:example.main
 struct Store{Config first;Config second;}struct Config{uint value;}bytes32 constant ROOT=${root};
 function store()private pure returns(Store storage $){assembly{$.slot:=ROOT}}
 function branch(bool choose,uint value)external{Config storage p=store().first;if(choose)p=store().second;p.value=value;}
 function loop(uint n,uint value)external{Config storage p=store().first;for(uint i;i<n;p=store().second){i++;}p.value=value;}
 }`;
 const r=run(body);for(const name of['branch','loop']){
  const method=r.sections.methods.find(m=>m.name===name);assert.ok(method);
  assert.equal(r.sections.storage.filter(s=>s.method_id===method.method_id&&s.access==='write').length,0);
  const transition=r.sections.state_transitions.find(t=>t.method_id===method.method_id);
  if(name==='branch'){assert.equal(transition.complete,true,JSON.stringify(transition));assert.equal(transition.paths.length,2);}
  else assert.equal(transition.complete,false);
 }
});
test('wrong actual namespace root cannot acquire typed storage transitions',()=>{
 const r=run(source.replace('bytes32 constant ROOT='+root,'bytes32 constant ROOT=0x'+'2'.repeat(64)));
 assert.ok(r.sections.state_transitions.every(t=>!t.complete));
 const layout=JSON.parse(r.sections.artifacts.find(a=>a.kind==='storage_layout').text);assert.equal(layout.namespace_storage.namespaces[0].pointer_assignment_verified,false);
 assert.ok(layout.namespace_storage.accessors.some(a=>a.unresolved.includes('NAMESPACE_ACTUAL_ROOT_MISMATCH')));
});
