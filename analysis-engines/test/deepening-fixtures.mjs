import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {hash} from '../model.mjs';
const require=createRequire(import.meta.url),solc=require('solc-0.8.30');
export const deepeningSource=`// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
interface IQuotes {function quote(address who,uint256 amount) external view returns(uint256);function list(uint256 amount) external view returns(uint256[] memory);function targets() external view returns(IQuotes[] memory);}
contract Deepening {
 address public owner;bool public open;uint256 public limit=6;
 mapping(address=>bool) public allowed;
 constructor(){owner=msg.sender;}
 function finite(uint256 n,uint256 x) external pure returns(uint256 sum){require(n<=8);for(uint256 i;i<n;i++){sum+=x/3+i;}}
 function finiteWhile(uint256 n,uint256 x) external pure returns(uint256 sum){require(n<7);uint256 i;while(i<n){sum+=x/3;i++;}}
 function finiteDo(uint256 n) external pure returns(uint256 sum){require(n<=5);uint256 i;do{sum+=i;i++;}while(i<n);}
 function boundedBreak(uint256 n) external pure returns(uint256 sum){for(uint256 i;i<n;i++){if(i==5)break;sum+=i;}}
 function dynamic(uint256 n) external pure returns(uint256 sum){for(uint256 i;i<n;i++)sum+=i;}
 function mixedLoop(bool bypass,uint256 n) external pure returns(uint256 sum){if(bypass){}else{n=0;}for(uint256 i;i<n;i++)sum+=i;}
 function decrement(uint256 n,uint256 x) external pure returns(uint256 sum){require(n<=6);while(n>0){sum+=x;n--;}}
 function localBound(uint256 n,uint256 x) external pure returns(uint256 sum){uint256 count=n;if(count>5)count=5;for(uint256 i;i<count;i++)sum+=x;}
 function wordMath(uint256 x,uint256 y,uint256 denominator) external pure returns(uint256 z){assembly {let prod:=mul(x,y) switch denominator case 0 {z:=0} default {z:=div(prod,denominator)}}}
 function wordBranch(uint256 x) external pure returns(uint256 z){assembly {z:=add(x,1) if lt(z,x) {z:=not(0)}}}
 function wordLoop(uint256 x) external pure returns(uint256 z){assembly {function twice(v)->r {r:=add(v,v)} z:=x for {let i:=0} lt(i,4) {i:=add(i,1)} {z:=twice(z)}}}
 function wordTuple(uint256 x) external pure returns(uint256 a,uint256 b){assembly {function split(v)->l,r {l:=div(v,3) r:=mod(v,3)} a,b:=split(x)}}
 function wordSigned(uint256 x,uint256 y) external pure returns(uint256 a,uint256 b){assembly {a:=sdiv(x,y) b:=smod(x,y)}}
 function wordShift(uint256 x,uint256 bits) external pure returns(uint256 a,uint256 b,uint256 c){assembly {a:=shl(bits,x) b:=shr(bits,x) c:=sar(bits,x)}}
 function wordMod(uint256 x,uint256 y,uint256 denominator) external pure returns(uint256 a,uint256 b){assembly {a:=addmod(x,y,denominator) b:=mulmod(x,y,denominator)}}
 function wordSignExtend(uint256 x,uint256 k) external pure returns(uint256 z){assembly {z:=signextend(k,x)}}
 function wordByte(uint256 x,uint256 k) external pure returns(uint256 z){assembly {z:=byte(k,x)}}
 function wordRevert(uint256 x) external pure returns(uint256 z){assembly {if iszero(x) {revert(0,0)} z:=sub(x,1)}}
 function dirty(uint8 x) external pure returns(uint256 z){assembly {z:=x}}
 function memoryValue(uint256 x) external pure returns(uint256 z){assembly {mstore(0,x) z:=mload(0)}}
 function quote(IQuotes target,uint256 amount) external view returns(uint256){return target.quote(msg.sender,amount)/3;}
 function twiceQuote(IQuotes target,uint256 amount) external view returns(uint256){return target.quote(msg.sender,amount)+target.quote(msg.sender,amount+1);}
 function quoteThenRevert(IQuotes target,uint256 amount) external view returns(uint256){uint256 value=target.quote(msg.sender,amount);require(value==0);return value;}
 function dynamicList(IQuotes target,uint256 amount) external view returns(uint256[] memory){return target.list(amount);}
 function contractList(IQuotes target) external view returns(IQuotes[] memory){return target.targets();}
 function guarded() external {require(msg.sender==owner);open=true;}
 function alternative() external {require(msg.sender==owner||open);open=true;}
 function impossible() external {require(msg.sender==owner&&msg.sender!=owner);open=true;}
 function roleGuard() external {require(allowed[msg.sender]);open=true;}
 function roleAlternative() external {require(allowed[msg.sender]||open);open=true;}
 function split(uint8 amount,uint8 fee) external pure returns(uint8 net,uint8 charge){require(fee<=amount);net=amount-fee;charge=fee;}
 function brokenSplit(uint8 amount,uint8 fee) external pure returns(uint8 net,uint8 charge){unchecked{net=amount-fee;}charge=fee;}
}
`;
export function compileDeepeningFixture(optimizer=false) {
 const file='Deepening.sol',std={language:'Solidity',sources:{[file]:{content:deepeningSource}},settings:{optimizer:{enabled:optimizer,runs:200},evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout']}}}};
 const output=JSON.parse(solc.compile(JSON.stringify(std)));assert.deepEqual((output.errors||[]).filter(e=>e.severity==='error'),[]);
 const compiled=output.contracts[file].Deepening,bytecode='0x'+compiled.evm.deployedBytecode.object,address='0x'+'2'.repeat(40);
 return {compiled,bytecode,snapshot:{chain:'ethereum',chain_id:'1',block_number:1,block_hash:'0x'+'1'.repeat(64),code_hash:hash(bytecode),code_address:address,storage_address:address,entry_address:address},engines:['whatsabi','sevm','solc-ast'],
 source:{stdJsonInput:std,compilation:{compilerVersion:solc.version(),fullyQualifiedName:file+':Deepening'},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi}};
}
