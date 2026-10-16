export const inheritedInitializationSource=`pragma solidity ^0.8.24;
contract Base{
 uint256 public x=1;
 constructor(){x=42;}
}
contract Verification is Base{
 uint256 public y=x;address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;
 constructor(){owner=msg.sender;balances[msg.sender]=y;totalSupply=y;}
 function balanceOf(address who)external view returns(uint256){return balances[who];}
 function mint(address to,uint256 value)external{require(msg.sender==owner);balances[to]+=value;totalSupply+=value;}
}`;
export const diamondCreationSource=`pragma solidity ^0.8.24;
contract Root{
 address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;uint256 public marker;
 constructor(uint256 n){owner=msg.sender;balances[msg.sender]=n;totalSupply=n;marker=marker*10+1;}
 function balanceOf(address who)external view returns(uint256){return balances[who];}
 function mint(address to,uint256 value)external{require(msg.sender==owner);balances[to]+=value;totalSupply+=value;}
}
contract B is Root{constructor(uint256 x)Root(x+3){marker=marker*10+2;}}
abstract contract C is Root{constructor(){marker=marker*10+3;}}
contract Verification is B,C{constructor(uint256 seed)B(seed*2){marker=marker*10+4;}}
`;
export const immutableCreationSource=`pragma solidity ^0.8.24;
contract Verification{
 address public immutable owner;uint256 public immutable deploymentChain;mapping(address=>uint256)public balances;uint256 public totalSupply;
 constructor(){owner=msg.sender;deploymentChain=block.chainid;balances[msg.sender]=7;totalSupply=7;}
 function balanceOf(address who)external view returns(uint256){return balances[who];}
 function mint(address to,uint256 value)external{require(msg.sender==owner);balances[to]+=value;totalSupply+=value;}
}`;
export const stateArgumentCreationSource=`pragma solidity ^0.8.24;
contract BaseArgument{uint256 public argument;constructor(uint256 n){argument=n;}}
contract Verification is BaseArgument{
 uint256 public sourceValue=7;address public owner;mapping(address=>uint256)public balances;uint256 public totalSupply;
 constructor()BaseArgument(sourceValue){owner=msg.sender;balances[msg.sender]=argument;totalSupply=argument;}
 function balanceOf(address a)external view returns(uint256){return balances[a];}
 function mint(address a,uint256 v)external{require(msg.sender==owner);balances[a]+=v;totalSupply+=v;}
}`;
