export const ADDRESS_MASK='0xffffffffffffffffffffffffffffffffffffffff';
export function assemblyLedgerSource({key='and(to,'+ADDRESS_MASK+')',between='',copied=false,broken=false,extra=''}={}){
 return `pragma solidity ^0.8.24;contract Verification{
 address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;
 constructor(){owner=msg.sender;}
 function balanceOf(address who)external view returns(uint256){return balances[who];}
 function mint(address to,uint256 value)external{
  require(msg.sender==owner);require(value<=type(uint256).max-balances[to]);require(value<=type(uint256).max-totalSupply);
  assembly{
   mstore(0,${key}) mstore(32,balances.slot) ${between}
   ${copied?'mcopy(128,0,64) mcopy(160,128,64)':''}
   let p:=keccak256(${copied?'160':'0'},64)
   sstore(p,add(sload(p),value))
   sstore(totalSupply.slot,add(sload(totalSupply.slot),${broken?'add(value,1)':'value'}))
  }
 }
 function transfer(address to,uint256 value)external{
  require(balances[msg.sender]>=value);require(value<=type(uint256).max-balances[to]);
  assembly{
   mstore(0,caller()) mstore(32,balances.slot) let from:=keccak256(0,64)
   sstore(from,sub(sload(from),value))
   mstore(0,and(to,${ADDRESS_MASK})) let target:=keccak256(0,64)
   sstore(target,add(sload(target),value))
  }
 }
 ${extra}
 }`;
}
