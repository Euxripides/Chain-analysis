import{storageFlowFixture}from'./storage-flow.mjs';
export const storageEffectsSource=`pragma solidity >=0.4.26 <0.9.0;
library CellTypes{
struct Cell{uint128 amount;uint64 fee;}
function put(Cell storage cell,uint128 value)internal{cell.amount=value;}
}
contract StorageFlow{
using CellTypes for CellTypes.Cell;
mapping(uint=>CellTypes.Cell) cells;uint public count;
function pointer(uint k)internal view returns(CellTypes.Cell storage out){return cells[k];}
function namedPointer(uint k)internal view returns(CellTypes.Cell storage out){out=cells[k];}
function pick(bool choose,uint k)internal view returns(CellTypes.Cell storage out){if(choose)return cells[k];return cells[k+1];}
function changed(uint k)internal returns(CellTypes.Cell storage out){count++;return cells[k];}
function pair(uint k)internal view returns(CellTypes.Cell storage a,CellTypes.Cell storage b){return(cells[k],cells[k+1]);}
function namedPair(uint k)internal view returns(CellTypes.Cell storage a,CellTypes.Cell storage b){a=cells[k];b=cells[k+1];}
function writeOne(CellTypes.Cell storage cell,uint128 value)internal returns(uint128){cell.amount=value;return cell.amount;}
function readOne(CellTypes.Cell storage cell)internal view returns(uint128){return cell.amount;}
function writeBool(CellTypes.Cell storage cell,uint128 value)internal returns(bool){cell.amount=value;return true;}
function directReturned(uint k,uint128 value)external{pointer(k).amount=value;}
function libraryReceiver(uint k,uint128 value)external{CellTypes.Cell storage cell=cells[k];cell.put(value);}
function joined(bool choose,uint k,uint128 value)external{CellTypes.Cell storage cell=pick(choose,k);cell.amount=value;}
function ternary(bool choose,uint k,uint128 value)external{CellTypes.Cell storage cell=choose?cells[k]:cells[k+1];cell.amount=value;}
function stateful(uint k,uint128 value)external{CellTypes.Cell storage cell=changed(k);cell.amount=value;}
function tupleReferences(uint k,uint128 value)external{(CellTypes.Cell storage a,CellTypes.Cell storage b)=pair(k);a.amount=value;b.amount=value+1;}
function tupleNamed(uint k,uint128 value)external{(CellTypes.Cell storage a,CellTypes.Cell storage b)=namedPair(k);a.amount=value;b.amount=value+1;}
function tupleAssigned(uint k,uint128 value)external{CellTypes.Cell storage a=cells[k+2];CellTypes.Cell storage b=cells[k+3];(a,b)=pair(k);a.amount=value;b.amount=value+1;}
function conditionalEffect(bool choose,uint k,uint128 value)external{CellTypes.Cell storage cell=cells[k];uint128 result=choose?writeOne(cell,value):writeOne(cell,value+1);cell.fee=uint64(result);}
function shortEffect(bool choose,uint k,uint128 value)external{CellTypes.Cell storage cell=cells[k];bool taken=choose&&writeBool(cell,value);cell.fee=taken?1:0;}
function noncommuting(uint k,uint128 value)external{CellTypes.Cell storage cell=cells[k];uint128 result=writeOne(cell,value)+readOne(cell);cell.fee=uint64(result);}
modifier route(bool choose,uint k){CellTypes.Cell storage selected=cells[k];if(choose)selected=cells[k+1];_;selected.fee=9;}
function conditionalModifier(bool choose,uint k,uint128 value)external route(choose,k){cells[k].amount=value;}
function callThenRevert(bool accept,uint k,uint128 value)external{CellTypes.Cell storage cell=cells[k];writeOne(cell,value);require(accept);}
}`;
export const storageEffectsFixture=(version='0.8.30',source=storageEffectsSource)=>storageFlowFixture(version,source);
