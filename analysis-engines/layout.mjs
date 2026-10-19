import{deriveLegacyStorageLayout}from'./ir/storage-reference.mjs';

// Unsupported types still stop all following allocations. Compiler AST identity
// also lets old compilers expose members and element strides without names.
export function legacyLayout(orderedContracts,typeOf,nodes=null){
 if(orderedContracts.some(c=>(c.nodes||[]).some(v=>v.stateVariable&&!v.typeName)))return simpleLegacyLayout(orderedContracts,typeOf);
 if(!nodes){nodes=new Map();const visit=n=>{if(!n||typeof n!=='object')return;if(n.nodeType)nodes.set(n.id,n);for(const v of Object.values(n))if(Array.isArray(v))v.forEach(visit);else if(v&&typeof v==='object')visit(v);};orderedContracts.forEach(visit);}
 return deriveLegacyStorageLayout(orderedContracts,typeOf,nodes);
}

// Keep the earlier type-label-only API for callers without compiler type nodes.
// Such callers receive no aggregate type registry or reference identity.
function simpleLegacyLayout(orderedContracts,typeOf){
 let slot=0n,offset=0,blocked=false;const storage=[],unresolved=[];
 const width=type=>type==='bool'?1:/^address(?: payable)?$|^contract /.test(type)?20:/^u?int\d*$/.test(type)?Number(type.replace(/^u?int/,'')||256)/8:/^bytes\d+$/.test(type)?Number(type.slice(5)):null;
 for(const contract of [...orderedContracts].reverse())for(const v of contract.nodes||[]){
  if(v.nodeType!=='VariableDeclaration'||!v.stateVariable||v.constant||v.mutability==='immutable')continue;
  const type=typeOf(v).replace(/\s+(?:storage|memory|calldata)(?:\s+(?:ref|pointer))?$/,'');let size=width(type),whole=false,slots=1n;
  if(/^mapping\s*\(|^(?:string|bytes)$|\[\]$/.test(type)){size=32;whole=true;}
  const array=/^(.+)\[(\d+)\]$/.exec(type);
  if(array&&width(array[1])){size=32;whole=true;const per=BigInt(Math.floor(32/width(array[1])));slots=(BigInt(array[2])+per-1n)/per;}
  if(blocked||!Number.isInteger(size)||size<1||size>32){blocked=true;unresolved.push({ast_id:v.id,type,reason:'unsupported_legacy_layout_type_or_preceding_aggregate'});continue;}
  if(whole||offset+size>32){if(offset)slot++;offset=0;}
  storage.push({astId:v.id,label:v.name,slot:String(slot),offset,type,layout_basis:'derived_solidity_storage_rules'});
  if(whole){slot+=slots;offset=0;}else{offset+=size;if(offset===32){slot++;offset=0;}}
 }
 return{storage,types:{},basis:'derived_solidity_storage_rules',unresolved};
}
