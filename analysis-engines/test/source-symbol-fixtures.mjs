export const shadowGuardSource=name=>`pragma solidity ^0.8.24;contract Verification{
 address public owner;constructor(){owner=msg.sender;}
 function ${name}(bool)internal pure{}
 function take(address next)external{${name}(msg.sender==owner);owner=next;}
 function read(uint256 n)external pure returns(uint256){${name}(n==0);return n;}
}`;
export const shadowMessageSource=`pragma solidity ^0.8.24;contract Verification{
 struct Env{address sender;}Env private msg;address public owner;
 constructor(){owner=address(7);msg.sender=address(7);}
 function take(address next)external{require(msg.sender==owner);owner=next;}
}`;
