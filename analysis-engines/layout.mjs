// Solidity versions before storageLayout output: derive only documented simple
// layouts. Unsupported aggregates stop allocation for all following variables.
export function legacyLayout(orderedContracts,typeOf) {
  let slot=0n,offset=0,blocked=false;const storage=[],unresolved=[];
  const width=type=>{
    if(type==='bool')return 1;
    if(/^address(?: payable)?$/.test(type)||/^contract /.test(type))return 20;
    const integer=/^(?:u?int)(\d*)$/.exec(type);if(integer)return Number(integer[1]||256)/8;
    const bytes=/^bytes(\d+)$/.exec(type);if(bytes)return Number(bytes[1]);
    return null;
  };
  for(const contract of [...orderedContracts].reverse())for(const variable of contract.nodes||[]) {
    if(variable.nodeType!=='VariableDeclaration'||!variable.stateVariable||variable.constant||variable.mutability==='immutable')continue;
    const type=typeOf(variable).replace(/\s+(?:storage|memory|calldata)(?:\s+(?:ref|pointer))?$/, '');let size=width(type),whole=false,slots=1n;
    if(/^mapping\s*\(|^(string|bytes)$|\[\]$/.test(type)){size=32;whole=true}
    const array=/^(.+)\[(\d+)\]$/.exec(type);
    if(array){const item=width(array[1]);if(item){size=32;whole=true;const perSlot=BigInt(Math.floor(32/item));slots=(BigInt(array[2])+perSlot-1n)/perSlot}}
    if(blocked||!size||size<1||size>32||!Number.isInteger(size)) {
      blocked=true;unresolved.push({ast_id:variable.id,type,reason:'unsupported_legacy_layout_type_or_preceding_aggregate'});continue;
    }
    if(whole||offset+size>32){if(offset)slot++;offset=0}
    storage.push({astId:variable.id,label:variable.name,slot:slot.toString(),offset,type,layout_basis:'derived_solidity_storage_rules'});
    if(whole){slot+=slots;offset=0}else{offset+=size;if(offset===32){slot++;offset=0}}
  }
  return {storage,types:{},basis:'derived_solidity_storage_rules',unresolved};
}
