import {createRequire} from 'node:module';
import {hash} from '../model.mjs';
const require=createRequire(import.meta.url),solc=require('solc-0.8.30');
export const verificationSource=`// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
interface IOracle {function quote(uint256 x) external view returns(uint256);}
contract Verification {
 address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;uint256 public treasury;
 constructor(){owner=msg.sender;}
 function balanceOf(address who)external view returns(uint256){return balances[who];}
 function affine(uint256 n,uint256 c) external pure returns(uint256 sum){for(uint256 i;i<n;i++)sum+=c;}
 function wrapped(uint256 n,uint256 c) external pure returns(uint256 sum){unchecked{for(uint256 i;i<n;i++)sum+=c;}}
 function subtract(uint256 n,uint256 c,uint256 initial)external pure returns(uint256 sum){sum=initial;for(uint256 i;i<n;i++)sum-=c;}
 function counterOverflow(uint256 n)external pure returns(uint256){uint256 sum;unchecked{for(uint8 i;i<n;i++)sum+=1;}return sum;}
 function triangular(uint256 n)external pure returns(uint256 sum){for(uint256 i;i<n;i++)sum+=i;}
 function memoryWord(uint256 p,uint256 v)external pure returns(uint256 out){require(p>=128&&p<=1024);assembly{mstore(p,v)out:=mload(p)}}
 function unsafeMemory(uint256 p,uint256 v)external pure returns(uint256 out){assembly{mstore(p,v)out:=mload(p)}}
 function memoryByte(uint256 v)external pure returns(uint256 out){assembly{mstore(0x80,v)mstore8(0x9f,0xab)out:=mload(0x80)}}
 function unknownMemory()external pure returns(uint256 out){assembly{out:=mload(0x40)}}
 function separateBlocks(uint256 v)external pure returns(uint256 out){assembly{mstore(0x80,v)}assembly{out:=mload(0x80)}}
 function storageWrite(uint256 slot,uint256 v)external returns(uint256 out){require(msg.sender==owner);assembly{sstore(slot,v)out:=sload(slot)}}
 function transferOwnership(address next)external{require(msg.sender==owner);owner=next;}
 function mint(address to,uint256 value)external{require(msg.sender==owner);balances[to]+=value;totalSupply+=value;}
 function transfer(address to,uint256 value)external{balances[msg.sender]-=value;balances[to]+=value;}
 function twoSites(IOracle oracle,uint256 x)external view returns(uint256){return oracle.quote(x)+oracle.quote(x+1);}
 function repeated(IOracle oracle,uint256 n)external view returns(uint256 sum){for(uint256 i;i<n;i++)sum+=oracle.quote(i);}
}`;

export const rolesSource=`pragma solidity ^0.8.24;
contract Verification {
 struct Role {mapping(address=>bool) members;bytes32 admin;}
 mapping(bytes32=>Role) private roles;
 function hasRole(bytes32 role,address account)public view returns(bool){return roles[role].members[account];}
 function getRoleAdmin(bytes32 role)public view returns(bytes32){return roles[role].admin;}
 function grantRole(bytes32 role,address account)external{require(hasRole(getRoleAdmin(role),msg.sender));roles[role].members[account]=true;}
 function revokeRole(bytes32 role,address account)external{require(hasRole(getRoleAdmin(role),msg.sender));roles[role].members[account]=false;}
 function renounceRole(bytes32 role,address account)external{require(account==msg.sender);roles[role].members[account]=false;}
}`;

export function compileVerificationFixture(optimized=true,source=verificationSource,evmVersion='shanghai') {
 const input={language:'Solidity',sources:{'Verification.sol':{content:source}},settings:{optimizer:{enabled:optimized,runs:200},evmVersion,outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers'],'':['ast']}}}};
 const output=JSON.parse(solc.compile(JSON.stringify(input))),errors=(output.errors||[]).filter(e=>e.severity==='error');
 if(errors.length)throw new Error(JSON.stringify(errors));const compiled=output.contracts['Verification.sol'].Verification,bytecode='0x'+compiled.evm.deployedBytecode.object;
 return {compiled,output,engines:['whatsabi','sevm','solc-ast'],bytecode,snapshot:{vm_family:'evm',chain:'ethereum',chain_id:'1',entry_address:'0x'+'1'.repeat(40),code_address:'0x'+'1'.repeat(40),storage_address:'0x'+'1'.repeat(40),code_hash:'0x'+hash(bytecode),block_number:1,block_hash:'0x'+'2'.repeat(64)},
  source:{stdJsonInput:input,compilation:{compilerVersion:solc.version(),fullyQualifiedName:'Verification.sol:Verification'},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi}};
}
