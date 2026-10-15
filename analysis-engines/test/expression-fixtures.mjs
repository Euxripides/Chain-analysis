import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {readFileSync} from 'node:fs';
import {hash} from '../model.mjs';
const require=createRequire(import.meta.url);
export const modernSource=`// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
contract ExpressionPaths {
 function add(uint256 x,uint256 y) external pure returns(uint256 z){require((z=x+y)>=x);}
 function compound(uint256 x,uint256 y) external pure returns(uint256 z){z=x;require((z+=y)>=x);}
 function andUpdate(uint256 x,uint256 y,bool flag) external pure returns(uint256 z){z=x;if(flag && (z=y)>0)z+=1;}
 function orUpdate(uint256 x,uint256 y,bool flag) external pure returns(uint256 z){z=x;if(flag || (z=y)>0)z+=1;}
 function conditional(uint256 x,uint256 y,bool flag) external pure returns(uint256 z){z=x;uint256 selected=flag?(z+=y):(z-=y);return selected+z;}
 function post(uint256 x) external pure returns(uint256 oldValue,uint256 current){oldValue=x++;current=x;}
 function pre(uint256 x) external pure returns(uint256 current,uint256 afterValue){current=++x;afterValue=x;}
 function decrement(uint256 x) external pure returns(uint256 oldValue,uint256 current){oldValue=x--;current=x;}
 function uncheckedPost(uint8 x) external pure returns(uint8 oldValue,uint8 current){unchecked{oldValue=x++;current=x;}}
 function negative(int8 x) external pure returns(int8 oldValue,int8 current){oldValue=x--;current=x;}
 function assignArgument(uint256 x,uint256 y) external pure returns(uint256 z){z=x;uint256 a=twice(z=y);return a+z;}
 function twice(uint256 value) internal pure returns(uint256){return value*2;}
 function assignCast(uint256 x) external pure returns(uint8 z){uint256 a;z=uint8(a=x);}
 function unusedOverflow(uint256 x) external pure returns(uint256){uint256 z=x;z++;return 1;}
 function overwrittenDivision(uint256 x) external pure returns(uint256 z){z=10/x;z=1;}
 function discardedCall(uint256 x) external pure returns(uint256){checked(x);return 1;}
 function checked(uint256 x) internal pure returns(uint256){return x+1;}
 function protectedDivision(uint256 x,uint256 y,bool flag) external pure returns(uint256 z){if(flag && (z=x/y)>0)return z;return 7;}
 function protectedConditional(uint256 x,uint256 y,bool flag) external pure returns(uint256 z){uint256 a=flag?(z=x/y):(z=7);return a;}
 function ambiguous(uint256 x) external pure returns(uint256){return (x=1)+x;}
 function ambiguousArguments(uint256 x) external pure returns(uint256){return pair(x++,x);}
 function pair(uint256 x,uint256 y) internal pure returns(uint256){return x+y;}
 function ambiguousTuple(uint256 x) external pure returns(uint256,uint256){return (x++,x);}
 function ambiguousCompound(uint256 x) external pure returns(uint256){x+=(x=7);return x;}
 function independentTuple(uint256 x,uint256 y) external pure returns(uint256,uint256){return (x++,y++);}
 function dynamicBound(uint256 x) external pure returns(uint256 z){while(x-->0)z++;}
 function assemblyValue(uint256 x) external pure returns(uint256 z){assembly{z:=x}}
}
`;
export const legacyWrapper=`pragma solidity =0.5.16;
import './SafeMath.sol';
contract LegacyExpressions {
 using SafeMath for uint256;
 function add(uint256 x,uint256 y) external pure returns(uint256){return x.add(y);}
 function sub(uint256 x,uint256 y) external pure returns(uint256){return x.sub(y);}
 function mul(uint256 x,uint256 y) external pure returns(uint256){return x.mul(y);}
 function fee(uint256 amount,uint256 rate) external pure returns(uint256){return amount.mul(rate)/10000;}
 function payout(uint256 amount,uint256 rate) external pure returns(uint256){return amount.sub(amount.mul(rate)/10000);}
 function pay(uint256 amount,uint256 rate) external {uint256 charge=amount.mul(rate)/10000;msg.sender.transfer(amount.sub(charge));}
 function wrappedPost(uint8 x) external pure returns(uint8 oldValue,uint8 current){oldValue=x++;current=x;}
 function sub8(uint8 x,uint8 y) external pure returns(uint8 z){require((z=x-y)<=x);}
 function mul8(uint8 x,uint8 y) external pure returns(uint8 z){require(y==0||(z=x*y)/y==x);}
 function skipped(uint256 x,uint256 y,bool flag) external pure returns(uint256 z){z=x;if(flag && (z=x/y)>0)return z;return 7;}
 function unusedDivision(uint256 x) external pure returns(uint256 z){z=10/x;z=1;}
 function ambiguous(uint256 x) external pure returns(uint256){return (x=1)+x;}
 function() external payable{}
}
`;
export function compileExpressionFixture(legacy=false,optimizer=false) {
 const solc=require(legacy?'solc-0.5.16':'solc-0.8.30'),name=legacy?'LegacyExpressions':'ExpressionPaths',file=name+'.sol';
 const sources={[file]:{content:legacy?legacyWrapper:modernSource}};
 if(legacy)sources['SafeMath.sol']={content:readFileSync(new URL('./vendor/uniswap-v2/SafeMath.sol',import.meta.url),'utf8')};
 const std={language:'Solidity',sources,settings:{optimizer:{enabled:optimizer,runs:200},evmVersion:legacy?'istanbul':'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout']}}}};
 const compiledOutput=JSON.parse(solc.compile(JSON.stringify(std)));
 assert.deepEqual((compiledOutput.errors||[]).filter(e=>e.severity==='error'),[]);
 const compiled=compiledOutput.contracts[file][name],bytecode='0x'+compiled.evm.deployedBytecode.object;
 return {compiled,bytecode,snapshot:{chain:'ethereum',chain_id:'1',block_number:1,block_hash:'0x'+'1'.repeat(64),code_hash:hash(bytecode),code_address:'0x'+'2'.repeat(40),storage_address:'0x'+'2'.repeat(40),entry_address:'0x'+'2'.repeat(40)},engines:['whatsabi','sevm','solc-ast'],
 source:{stdJsonInput:std,compilation:{compilerVersion:solc.version(),fullyQualifiedName:file+':'+name},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi}};
}
