import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {analyze} from '../analyze.mjs';
import {hash} from '../model.mjs';
const require=createRequire(import.meta.url),solc=require('solc-0.4.18');
export const source=`pragma solidity 0.4.18;
library Arithmetic {
 function add(uint256 a,uint256 b) internal pure returns(uint256){uint256 c=a+b;assert(c>=a);return c;}
 function sub(uint256 a,uint256 b) internal pure returns(uint256){assert(b<=a);return a-b;}
}
contract LegacyLedger {
 using Arithmetic for uint256;
 address public owner;uint256 public feeRate;uint256 public maximumFee;
 mapping(address=>uint256) private ledger;
 event Transfer(address indexed from,address indexed to,uint256 amount);
 function LegacyLedger(){owner=msg.sender;maximumFee=1000000;}
 function balanceOf(address account) external constant returns(uint256){return ledger[account];}
 function configure(uint256 rate) external {require(msg.sender==owner);feeRate=rate;}
 function issue(address account,uint256 amount) external {require(msg.sender==owner);ledger[account]=ledger[account].add(amount);Transfer(address(0),account,amount);}
 function transfer(address target,uint256 amount) external {
  uint256 fee=amount*feeRate/10000;if(fee>maximumFee)fee=maximumFee;
  uint256 net=amount.sub(fee);
  ledger[msg.sender]=ledger[msg.sender].sub(amount);
  ledger[target]=ledger[target].add(net);ledger[owner]=ledger[owner].add(fee);
  Transfer(msg.sender,owner,fee);Transfer(msg.sender,target,net);
 }
 function counterfeit(address target,uint256 amount) external {Transfer(msg.sender,target,amount);}
 function replace(address target,uint256 amount) external {ledger[target]=amount;Transfer(msg.sender,target,amount);}
}
contract NFTLedger {
 mapping(uint256=>address) public ownerOf;mapping(address=>uint256) public balanceOf;
 event Transfer(address indexed from,address indexed to,uint256 tokenId);
 function mintOne(address to) external {ownerOf[1]=to;balanceOf[to]+=1;Transfer(address(0),to,1);}
}
`;
function fixture(name='LegacyLedger'){const standard={language:'Solidity',sources:{'Legacy.sol':{content:source}},settings:{optimizer:{enabled:true,runs:200},outputSelection:{'*':{'*':['abi','evm.deployedBytecode','evm.bytecode']}}}};
 const output=JSON.parse(solc.compileStandardWrapper(JSON.stringify(standard)));assert.deepEqual((output.errors||[]).filter(e=>e.severity==='error'),[]);
 const compiled=output.contracts['Legacy.sol'][name],bytecode='0x'+compiled.evm.deployedBytecode.object;
 return {bytecode,engines:['solc-ast','whatsabi','sevm'],snapshot:{chain:'ethereum',chain_id:'1',block_number:1,block_hash:'0x'+'1'.repeat(64),code_hash:hash(bytecode),code_address:'0x'+'2'.repeat(40),storage_address:'0x'+'2'.repeat(40),entry_address:'0x'+'2'.repeat(40)},source:{stdJsonInput:standard,compilation:{compilerVersion:solc.version(),fullyQualifiedName:'Legacy.sol:'+name},match:'exact_match',runtimeMatch:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode}}};
}
test('legacy SafeMath ledger assignments connect real fee writes without crediting unrelated events',()=>{
 const result=analyze(fixture());assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'completed',JSON.stringify(result.sections.engines));
 const m=name=>result.sections.methods.find(m=>m.name===name),funds=name=>result.sections.funds.filter(f=>f.method_id===m(name).method_id);
 assert.equal(funds('transfer').length,2);assert.ok(funds('transfer').some(f=>f.to_expression==='owner'&&f.amount_expression==='fee'));
 assert.ok(funds('transfer').every(f=>f.ledger_effects.some(e=>e.delta_operation==='+')));assert.equal(funds('issue')[0].kind,'mint');
 assert.equal(funds('counterfeit').length,0);assert.equal(funds('replace').length,0);
 const effect=result.sections.control_effects.find(e=>e.method_id===m('configure').method_id&&e.configuration_key==='feeRate');
 assert.ok(effect?.impact_types.includes('amount_formula'),JSON.stringify(effect));
 assert.ok(effect.affected_operations.some(o=>o.dependency_status==='represented_expression_dependency'));
 const fee=funds('transfer').find(f=>f.to_expression==='owner'),evaluate=new Function('amount','feeRate','maximumFee','return '+fee.expanded_amount_expression.replace(/\b\d+\b/g,'$&n'));
 assert.equal(fee.amount_derivation_status,'represented_source_dependencies_resolved');
 for(const amount of[0n,7n,9007199254740993n])for(const feeRate of[0n,137n])for(const maximumFee of[0n,1000000n]){
  const raw=amount*feeRate/10000n;assert.equal(evaluate(amount,feeRate,maximumFee),raw>maximumFee?maximumFee:raw);
 }
 assert.ok(effect.caller_constraints.some(c=>c.target_value_resolution==='direct_state_address'));
});
test('NFT token ID one and its balance counter never become an ERC20 raw amount',()=>{
 const result=analyze(fixture('NFTLedger'));assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');
 assert.ok(!result.sections.funds.some(f=>f.unit==='token_raw'));
});
