export const factorySource=`// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
contract Verification {
 address public owner; uint256 public totalSupply; mapping(address=>uint256) public balances;
 constructor(uint256 seed){owner=msg.sender;totalSupply=seed;balances[msg.sender]=seed;}
 function balanceOf(address who)external view returns(uint256){return balances[who];}
 function set(uint256 next)external{require(msg.sender==owner);balances[owner]=next;totalSupply=next;}
}
contract Factory {
 address public last;
 function create(uint256 seed)external returns(address){last=address(new Verification(seed));return last;}
 function create2(uint256 seed,bytes32 salt)external returns(address){last=address(new Verification{salt:salt}(seed));return last;}
 function createAndMutate(uint256 seed)external returns(address){Verification child=new Verification(seed);child.set(seed+10);last=address(child);return last;}
 function aborted(uint256 seed)external returns(address){new Verification(seed);revert("ancestor reverts");}
 function caught(uint256 seed)external returns(address){try this.aborted(seed) returns(address){}catch{}last=address(new Verification(seed+1));return last;}
}
contract ConstructorFactory {address public child;constructor(uint256 seed){child=address(new Verification(seed));}}
contract DelegateFactory {
 address immutable implementation;
 constructor(address target){implementation=target;}
 fallback()external{address target=implementation;assembly{calldatacopy(0,0,calldatasize())let ok:=delegatecall(gas(),target,0,calldatasize(),0,0)returndatacopy(0,0,returndatasize())switch ok case 0{revert(0,returndatasize())}default{return(0,returndatasize())}}}
}`;
