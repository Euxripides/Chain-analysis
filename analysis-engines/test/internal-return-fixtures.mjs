import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {hash} from '../model.mjs';
const require=createRequire(import.meta.url),solc=require('solc-0.8.30');
export const source=`pragma solidity ^0.8.24;
contract ReturnPaths {
 address public owner; address payable public treasury; uint16 public feeBps; bool public paused;
 constructor(){owner=msg.sender;treasury=payable(msg.sender);feeBps=125;}
 function configure(uint16 rate,address payable target) external {require(msg.sender==owner);require(rate<=1000);feeBps=rate;treasury=target;}
 function transferOwner(address target) external {require(msg.sender==owner);owner=target;}
 function toggle(bool value) external {require(msg.sender==owner);paused=value;}
 function alternatives(uint16 rate) external {require(msg.sender==owner || feeBps==0);feeBps=rate;}
 function conjunction(uint16 rate) external {require(msg.sender==owner && !paused);feeBps=rate;}
 function calculatedOwner(uint16 rate,uint160 mask) external {require(msg.sender==address(uint160(owner)^mask));feeBps=rate;}
 function blacklist(uint16 rate) external {require(msg.sender!=owner);feeBps=rate;}
 function quote(uint256 amount,bool exempt) external view returns(uint256){return calculate(amount,exempt);}
 function calculate(uint256 amount,bool exempt) internal view returns(uint256 fee){
   if(exempt)return 0;
   uint256 scaled=amount*feeBps; scaled=scaled/10000;
   if(scaled>1000000)return 1000000;
   fee=scaled;
 }
 function withdraw(uint256 amount,bool exempt) external {require(!paused);uint256 fee=calculate(amount,exempt);treasury.transfer(fee);payable(msg.sender).transfer(amount-fee);}
 function local(uint256 amount) external pure returns(uint256){return stages(amount);}
 function stages(uint256 amount) internal pure returns(uint256){uint256 value=amount/3;value+=7;value=value*2;return value;}
 function named(uint256 amount) external pure returns(uint256){return namedStages(amount);}
 function namedStages(uint256 amount) internal pure returns(uint256 output){output=amount/7;if(amount>100)output+=5;}
 function reject(uint256 amount) external pure returns(uint256){return positive(amount);}
 function positive(uint256 amount) internal pure returns(uint256){require(amount>0);if(amount==2)revert();return amount/2;}
 function wrapped(uint256 amount) external pure returns(uint256){return wrap(amount);}
 function wrap(uint256 amount) internal pure returns(uint256){unchecked{uint256 next=amount;next+=1;return next;}}
 function callerUnchecked(uint256 amount) external pure returns(uint256){unchecked{return checked(amount);}}
 function checked(uint256 amount) internal pure returns(uint256){uint256 next=amount;next+=1;return next;}
 function loop(uint256 amount) external pure returns(uint256){return iterate(amount);}
 function iterate(uint256 amount) internal pure returns(uint256){uint256 value;for(uint256 i=0;i<amount;i++)value+=i;return value;}
 function effects(uint256 amount) external returns(uint256){return stateful(amount);}
 function stateful(uint256 amount) internal returns(uint256){feeBps=1;return amount;}
 function two(uint256 amount) external pure returns(uint256){return stages(amount)+stages(amount+3);}
 receive() external payable{}
}
`;
export function compileReturnFixture(optimizer=false) {
 const std={language:'Solidity',sources:{'ReturnPaths.sol':{content:source}},settings:{optimizer:{enabled:optimizer,runs:200},evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout']}}}};
 const output=JSON.parse(solc.compile(JSON.stringify(std)));
 assert.deepEqual((output.errors||[]).filter(e=>e.severity==='error'),[]);
 const compiled=output.contracts['ReturnPaths.sol'].ReturnPaths,bytecode='0x'+compiled.evm.deployedBytecode.object;
 return {compiled,bytecode,snapshot:{chain:'ethereum',chain_id:'1',block_number:1,block_hash:'0x'+'1'.repeat(64),code_hash:hash(bytecode),code_address:'0x'+'2'.repeat(40),storage_address:'0x'+'2'.repeat(40),entry_address:'0x'+'2'.repeat(40)},
  engines:['whatsabi','sevm','solc-ast'],source:{stdJsonInput:std,compilation:{compilerVersion:solc.version(),fullyQualifiedName:'ReturnPaths.sol:ReturnPaths'},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi}};
}
