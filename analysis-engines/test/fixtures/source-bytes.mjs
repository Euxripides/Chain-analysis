import {createRequire}from'node:module';
const require=createRequire(import.meta.url),solc=require('solc-0.8.30'),{keccak_256}=require('@noble/hashes/sha3.js');
export const source=`pragma solidity ^0.8.24;
interface Asset {function transfer(address to,uint256 value) external returns(bool);}
interface Envelope {function call(bytes calldata data) external returns(bool);}
contract BytesFlow {
 function direct(address token,address to,uint256 amount) external {
  bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));bytes memory aliasData=data;amount=999;(bool ok,)=token.call(aliasData);require(ok);
 }
 function helper(address token,address to,uint256 amount) external {
  uint256 net=amount/3;bytes memory data=abi.encodeWithSelector(Asset.transfer.selector,to,net);net=777;_send(token,data);
 }
 function _send(address token,bytes memory data) internal {(bool ok,)=token.call(data);require(ok);}
 function _make(address to,uint256 amount) internal pure returns(bytes memory){return abi.encodeWithSignature("transfer(address,uint256)",to,amount);}
 function returned(address token,address to,uint256 amount) external {bytes memory data=_make(to,amount);_send(token,data);}
 function _named(address to,uint256 amount) internal pure returns(bytes memory data){data=abi.encodeCall(Asset.transfer,(to,amount));}
 function named(address token,address to,uint256 amount) external {_send(token,_named(to,amount));}
 function _two(address a,address b,uint256 amount) internal pure returns(bytes memory,bytes memory){return (abi.encodeCall(Asset.transfer,(a,amount)),abi.encodeCall(Asset.transfer,(b,amount+1)));}
 function tupled(address token,address a,address b,uint256 amount) external {(bytes memory left,bytes memory right)=_two(a,b,amount);_send(token,left);_send(token,right);}
 function tupleAssigned(address token,address a,address b,uint256 amount) external {bytes memory left;bytes memory right;(left,right)=_two(a,b,amount);_send(token,left);_send(token,right);}
 function _replace(bytes memory data,address to,uint256 amount) internal pure {data=abi.encodeCall(Asset.transfer,(to,amount+10));}
 function rebound(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));_replace(data,to,amount);_send(token,data);}
 function _mutate(bytes memory data) internal pure {data[0]=0x00;}
 function mutated(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));bytes memory aliasData=data;_mutate(aliasData);_send(token,data);}
 function unrelated(address token,address a,address b,uint256 amount) external {bytes memory left=abi.encodeCall(Asset.transfer,(a,amount));bytes memory right=abi.encodeCall(Asset.transfer,(b,amount+1));right[0]=0x00;_send(token,left);}
 function forked(address token,address a,address b,uint256 amount,bool flag) external {bytes memory data;if(flag)data=abi.encodeCall(Asset.transfer,(a,amount));else data=abi.encodeCall(Asset.transfer,(b,amount+1));_send(token,data);}
 function missing(address token,address to,uint256 amount,bool flag) external {bytes memory data;if(flag)data=abi.encodeCall(Asset.transfer,(to,amount));_send(token,data);}
 function surviving(address token,address to,uint256 amount,bool flag) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));if(flag){data[0]=0x00;return;}_send(token,data);}
 function repeated(address token,address to,uint256 amount,uint256 n) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));for(uint256 i;i<n;i++)data=abi.encodeCall(Asset.transfer,(to,amount+i));_send(token,data);}
 function heapClobbered(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));assembly{mstore(0x40,0)}data=abi.encodeCall(Asset.transfer,(to,amount+1));_send(token,data);}
 function raw(address token,address to,uint256 amount) external {bytes memory data=abi.encodeWithSelector(Asset.transfer.selector,to,amount);bytes memory aliasData=data;assembly{let ok:=call(gas(),token,0,add(aliasData,32),mload(aliasData),0,0) if iszero(ok){revert(0,0)}}}
 function scratch(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));assembly{mstore(0,123)}_send(token,data);}
 function assemblyPointer(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));assembly{data:=0x80}_send(token,data);}
 function _recursive(bytes memory data,uint256 n) internal pure {if(n>0)_recursive(data,n-1);else data[0]=0x00;}
 function recursive(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));_recursive(data,amount);_send(token,data);}
 function shadow(address token,address a,address b,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(a,amount));{bytes memory data=abi.encodeCall(Asset.transfer,(b,amount+1));_mutate(data);}_send(token,data);}
 function ternary(address token,address a,address b,uint256 amount,bool flag) external {bytes memory data=flag?abi.encodeCall(Asset.transfer,(a,amount)):abi.encodeCall(Asset.transfer,(b,amount+1));_send(token,data);}
 struct Record {uint256 number;bytes extra;}
 function nested(address token,uint256 amount) external {Record memory r=Record(amount,hex"1234");bytes memory data=abi.encode(r);(bool ok,)=token.call(data);require(ok);}
 function decoded(address token,address to,uint256 amount) external {uint256 net=amount/7;bytes memory data=abi.encode(to,net);net=999;(address recipient,uint256 value)=abi.decode(data,(address,uint256));_send(token,abi.encodeCall(Asset.transfer,(recipient,value)));}
 function decodedUnknown(address token,bytes memory data) external {(address recipient,uint256 value)=abi.decode(data,(address,uint256));_send(token,abi.encodeCall(Asset.transfer,(recipient,value)));}
 function _dispatch(bytes memory data,function(bytes memory) internal pure effect) internal pure {effect(data);}
 function dynamic(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));_dispatch(data,_mutate);_send(token,data);}
 function compoundMutated(address token,address to,uint256 amount) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));data[0]^=0xff;_send(token,data);}
 function _mutateBool(bytes memory data) internal pure returns(bool){data[0]=0;return true;}
 function shortAnd(address token,address to,uint256 amount,bool flag) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));bool success=flag&&_mutateBool(data);_send(token,data);require(success||!flag);}
 function shortOr(address token,address to,uint256 amount,bool flag) external {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));bool success=flag||_mutateBool(data);_send(token,data);require(success);}
 modifier afterBody(){_;assembly{mstore(0x40,0)}}
 function _post(address to,uint256 amount) internal pure afterBody returns(bytes memory){return abi.encodeCall(Asset.transfer,(to,amount));}
 function afterModifier(address token,address to,uint256 amount) external {_send(token,_post(to,amount));}
 function loopIndex(address token,address to,uint256 amount) external {for(uint256 i=0;i<amount;i++)_send(token,abi.encodeCall(Asset.transfer,(to,i)));}
 function _valueSend(address token,bytes memory data,uint256 weiValue) internal {(bool ok,)=token.call{value:weiValue}(data);require(ok);}
 function zeroValue(address token,address to,uint256 amount) external {_valueSend(token,abi.encodeCall(Asset.transfer,(to,amount)),0);}
 function valueAndTokens(address token,address to,uint256 amount) external payable {bytes memory data=abi.encodeCall(Asset.transfer,(to,amount));(bool ok,)=token.call{value:msg.value}(data);require(ok);}
 function zeroCallOnly(address token) external {(bool ok,)=token.call{value:0}("");require(ok);}
 function wrappedCall(Envelope target,address to,uint256 amount) external {require(target.call(abi.encodeCall(Asset.transfer,(to,amount))));}
}
`;
export function sourceBytesFixture(){
 const standard={language:'Solidity',sources:{'BytesFlow.sol':{content:source}},settings:{evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}}},output=JSON.parse(solc.compile(JSON.stringify(standard)));
 const errors=(output.errors||[]).filter(e=>e.severity==='error');if(errors.length)throw Error(errors.map(e=>e.formattedMessage).join('\n'));
 const compiled=output.contracts['BytesFlow.sol'].BytesFlow,bytecode='0x'+compiled.evm.deployedBytecode.object;
 return{snapshot:{identity_kind:'code_only',code_hash:'0x'+Buffer.from(keccak_256(Buffer.from(bytecode.slice(2),'hex'))).toString('hex'),state_granularity:'symbolic_state',evm_rules:'shanghai'},bytecode,
  source:{stdJsonInput:standard,compilation:{compilerVersion:solc.version(),fullyQualifiedName:'BytesFlow.sol:BytesFlow'},runtimeMatch:'exact_match',match:'exact_match',
   runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi,storageLayout:compiled.storageLayout},compiled,output};
}
