import test from 'node:test';
import assert from 'node:assert/strict';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture} from './verification-fixtures.mjs';

const source=`pragma solidity ^0.8.24;
contract Verification{
  error OwnableUnauthorizedAccount(address account);
  error AccessControlUnauthorizedAccount(address account,bytes32 neededRole);
  error Unauthorized();
  address private _owner;address public admin;address public treasury;
  mapping(bytes32=>mapping(address=>bool)) private _roles;
  bytes32 public constant OPERATOR_ROLE=keccak256("OPERATOR_ROLE");
  constructor(){_owner=msg.sender;admin=msg.sender;_roles[OPERATOR_ROLE][msg.sender]=true;}
  function _msgSender()internal view returns(address){return msg.sender;}
  function owner()public view returns(address){return _owner;}
  function _checkOwner()internal view{if(owner()!=_msgSender())revert OwnableUnauthorizedAccount(_msgSender());}
  modifier onlyOwner(){_checkOwner();_;}
  function hasRole(bytes32 role,address account)public view returns(bool){return _roles[role][account];}
  function _checkRole(bytes32 role,address account)internal view{if(!hasRole(role,account))revert AccessControlUnauthorizedAccount(account,role);}
  modifier onlyRole(bytes32 role){_checkRole(role,_msgSender());_;}
  function ownerSet(address t)external onlyOwner{treasury=t;}
  function roleSet(address t)external onlyRole(OPERATOR_ROLE){treasury=t;}
  function inlineSet(address t)external{if(msg.sender!=admin)revert Unauthorized();treasury=t;}
  function elseSet(address t)external{if(msg.sender==admin){treasury=t;}else{revert Unauthorized();}}
  function legacySet(address t)external{if(msg.sender!=admin)revert("not admin");treasury=t;}
  function openSet(address t)external{if(t==address(0))revert Unauthorized();treasury=t;}
}`;

const result=analyze(compileVerificationFixture(true,source));
const guards=name=>{const m=result.sections.methods.find(x=>x.name===name);return {m,guards:result.sections.permissions.filter(p=>p.method_id===m.method_id)};};
const callers=gs=>gs.flatMap(g=>g.caller_constraints||[]);

test('an internal if-revert owner check (OpenZeppelin v5 onlyOwner) is a caller guard on the entry',()=>{
  const {m,guards:gs}=guards('ownerSet'),cs=callers(gs);
  assert.equal(gs.length,1);assert.equal(gs[0].guard_form,'if_revert');assert.equal(gs[0].condition,'!(owner()!=_msgSender())');
  assert.equal(result.sections.expressions.find(e=>e.expression_id===gs[0].expression_id).op,'!');
  assert.deepEqual(cs.map(c=>[c.principal,c.relation,c.target_expression,c.target_value_resolution]),[['msg.sender','equals','_owner','direct_state_address']]);
  assert.ok(m.behaviors.includes('调用者条件'));
});
test('a negated role lookup (OpenZeppelin v5 onlyRole) becomes a caller role predicate',()=>{
  const {m,guards:gs}=guards('roleSet'),ps=gs.flatMap(g=>g.state_predicates||[]);
  assert.ok(ps.some(p=>p.predicate_kind==='boolean_state_lookup'&&p.expected===true&&p.subject_expressions.some(t=>/msg\.sender/.test(t))),JSON.stringify(ps));
  assert.ok(m.behaviors.includes('调用者条件'));
});
for(const name of ['inlineSet','elseSet','legacySet'])test(name+' records msg.sender == admin',()=>{
  const cs=callers(guards(name).guards);
  assert.deepEqual(cs.map(c=>[c.principal,c.target_expression]),[['msg.sender','admin']]);
});
test('an argument check written as if-revert is a guard without any caller constraint',()=>{
  const {m,guards:gs}=guards('openSet');
  assert.equal(gs.length,1);assert.equal(callers(gs).length,0);assert.ok(!m.behaviors.includes('调用者条件'));
});
