export const permitSource=`pragma solidity ^0.8.24;
contract Verification {
 mapping(address=>uint256)public nonces;
 mapping(address=>mapping(address=>uint256))public allowance;
 bytes32 constant DOMAIN_TYPEHASH=keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)");
 bytes32 constant PERMIT_TYPEHASH=keccak256("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)");
 function DOMAIN_SEPARATOR()public view returns(bytes32){return keccak256(abi.encode(DOMAIN_TYPEHASH,keccak256("Verification"),keccak256("1"),block.chainid,address(this)));}
 function permit(address owner,address spender,uint256 value,uint256 deadline,uint8 v,bytes32 r,bytes32 s)external {
  require(owner!=address(0));require(block.timestamp<=deadline);
  bytes32 structHash=keccak256(abi.encode(PERMIT_TYPEHASH,owner,spender,value,nonces[owner]++,deadline));
  bytes memory encoded=abi.encodePacked(hex"1901",DOMAIN_SEPARATOR(),structHash);
  address signer=ecrecover(keccak256(encoded),v,r,s);require(signer==owner);
  allowance[owner][spender]=value;
 }
}`;

export const byteEncodingSource=`pragma solidity ^0.8.24;contract Verification {
 bytes32 public encoded;address public signer;
 function encode(address a,int16 b,bytes3 c,bool d,uint8 e)external{encoded=keccak256(abi.encode(a,b,c,d,e));}
 function packed(address a,int16 b,bytes3 c,bool d,uint8 e)external{encoded=keccak256(abi.encodePacked(a,b,c,d,e));}
 function dynamicKnown()external{bytes memory a=hex"010203";encoded=keccak256(abi.encode(a,unicode"你好",uint8(9)));}
 function recover(bytes32 h,uint8 v,bytes32 r,bytes32 s)external{signer=ecrecover(h,v,r,s);}
}`;
