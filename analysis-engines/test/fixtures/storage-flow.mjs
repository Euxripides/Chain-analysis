import{createRequire}from'node:module';
import{compileSource}from'../../source.mjs';import{hash}from'../../model.mjs';
const require=createRequire(import.meta.url);
export const storageFlowSource=`pragma solidity >=0.4.26 <0.9.0;
contract StorageFlow{
struct Item{uint128 amount;uint64 rewardDebt;address token;}
mapping(uint=>mapping(address=>Item)) users;Item[] pools;uint24[12][2] packed;mapping(uint=>uint16[]) lists;
function captured(uint k,uint128 amount)external{uint current=k;Item storage user=users[current][msg.sender];current=current+1;user.amount=amount;user.rewardDebt=0;}
function rebound(uint k,uint128 amount)external{Item storage user=users[k][msg.sender];user=users[k+1][msg.sender];user.amount=amount;}
function nested(Item storage user,uint128 amount)internal{user.amount=amount;user.rewardDebt=0;}
function parameter(uint k,uint128 amount)external{Item storage user=users[k][msg.sender];nested(user,amount);user.token=msg.sender;}
function localRebind(Item storage user,uint k,uint128 amount)internal{user=users[k+1][msg.sender];user.amount=amount;}
function isolated(uint k,uint128 amount)external{Item storage user=users[k][msg.sender];localRebind(user,k,amount);user.rewardDebt=9;}
function pointer(uint k)internal view returns(Item storage out){return users[k][msg.sender];}
function namedPointer(uint k)internal view returns(Item storage out){out=users[k][msg.sender];}
function returned(uint k,uint128 amount)external{Item storage user=pointer(k);user.amount=amount;}
function named(uint k,uint128 amount)external{Item storage user=namedPointer(k);user.amount=amount;}
function branch(bool choose,uint k,uint128 amount)external{Item storage user=users[k][msg.sender];if(choose)user=users[k+1][msg.sender];user.amount=amount;}
function arrayItem(uint i,uint128 amount)external{Item storage pool=pools[i];pool.amount=amount;}
function arrayPacked(uint i,uint j,uint24 amount)external{packed[i][j]=amount;}
function arrayMapped(uint k,uint i,uint16 amount)external{lists[k][i]=amount;}
function remove(uint k)external{delete users[k][msg.sender];}
function rollback(uint k,uint128 amount)external{Item storage user=users[k][msg.sender];user.amount=amount;require(amount==0);}
function lengthOf()external view returns(uint){return pools.length;}
}`;
export function storageFlowFixture(version='0.8.30',text=storageFlowSource){
 const compiler=require('solc-'+version),stdJsonInput={language:'Solidity',sources:{'StorageFlow.sol':{content:text}},settings:{optimizer:{enabled:true,runs:200}}},
  compiled=compileSource(stdJsonInput,compiler.version()).contracts['StorageFlow.sol'].StorageFlow,bytecode='0x'+compiled.evm.deployedBytecode.object;
 return{bytecode,engines:['solc-ast'],snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:hash(text+version),code_hash:hash(Buffer.from(bytecode.slice(2),'hex')),
  code_address:'0x'+'2'.repeat(40),storage_address:'0x'+'2'.repeat(40),evm_rules:'cancun',vm_family:'evm',chain_id:'1'},
  source:{stdJsonInput,compilation:{compilerVersion:compiler.version(),fullyQualifiedName:'StorageFlow.sol:StorageFlow'},runtimeMatch:'exact_match',match:'exact_match',
   runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi}};
}
