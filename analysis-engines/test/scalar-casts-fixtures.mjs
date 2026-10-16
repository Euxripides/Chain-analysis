export const scalarCastsSource=`pragma solidity ^0.8.24;contract Verification{
 address public owner;mapping(bytes16=>uint256)public buckets;
 constructor(){owner=msg.sender;}
 function widen(bytes16 x)external pure returns(bytes32){return x;}
 function narrow(bytes32 x)external pure returns(bytes16){return bytes16(x);}
 function local(bytes16 x)external pure returns(bytes32){bytes32 y=x;return y;}
 function assigned(bytes16 x)external pure returns(bytes32){bytes32 y;y=x;return y;}
 function passed(bytes16 x)external pure returns(bytes32){return pass(x);}
 function pass(bytes32 y)internal pure returns(bytes32){return y;}
 function tupleValue(bytes16 x)external pure returns(bytes32,bytes16){return(x,bytes16(bytes32(x)));}
 function conditional(bool c,bytes16 a,bytes32 b)external pure returns(bytes32){return c?a:b;}
 function equal(bytes16 a,bytes32 b)external pure returns(bool){return a==b;}
 function lazy(bool c,uint256 denominator)external pure returns(uint256){return c?1:1/denominator;}
 function set(bytes16 key,uint256 value)external{buckets[key]=value;}
 function stateLocal(bytes16 x)external{bytes32 y=x;buckets[bytes16(y)]=1;}
 function seize(address next)external{require(msg.sender==address(uint160(uint256(bytes32(bytes20(owner))))));owner=next;}
}`;
