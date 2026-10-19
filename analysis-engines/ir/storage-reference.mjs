import {keccak_256} from '@noble/hashes/sha3.js';
import {id,unique} from '../model.mjs';

const WORD=1n<<256n,cleanLabel=n=>(n?.typeDescriptions?.typeString||n?.type||n?.name||'').replace(/ (?:storage|memory|calldata)(?: (?:ref|pointer))?$/,'');
const copy=value=>structuredClone(value);
export const storageTypeID=node=>(node?.typeDescriptions?.typeIdentifier||'').replace(/_ptr$/,'').replace(/_\$_/g,',').replace(/\$_/g,'(').replace(/_\$/g,')');
const scalarSize=label=>{
 if(label==='bool')return 1;
 if(/^address(?: payable)?$|^contract /.test(label))return 20;
 const integer=/^u?int(\d*)$/.exec(label),bytes=/^bytes(\d+)$/.exec(label);
 const size=integer?Number(integer[1]||256)/8:bytes?Number(bytes[1]):null;
 return Number.isInteger(size)&&size>=1&&size<=32?size:null;
};
export const aggregateStorageType=type=>Boolean(type&&(type.members||type.base||type.encoding==='mapping'||type.encoding==='bytes'));
export function storageArrayInfo(type,types){
 if(!type?.base)return null;
 const element=types[type.base],size=BigInt(element?.numberOfBytes||0);
 if(size<1n)throw Error('STORAGE_ARRAY_ELEMENT_LAYOUT_UNAVAILABLE');
 const packed=element.encoding==='inplace'&&!aggregateStorageType(element)&&size<=32n;
 const length=type.encoding==='dynamic_array'?null:type.length??/\[(\d+)\]$/.exec(type.label)?.[1];
 if(length===undefined)throw Error('STORAGE_FIXED_ARRAY_LENGTH_UNAVAILABLE');
 return{dynamic:type.encoding==='dynamic_array',length:length===null?null:String(length),element_type:type.base,
  element_bytes:String(size),elements_per_slot:packed?String(32n/size):'1',stride_slots:packed?'1':String((size+31n)/32n),packed};
}

// Before solc emitted storageLayout, reconstruct sizes from compiler AST type
// identities. Never advance allocation beyond an unsupported preceding type.
export function deriveLegacyStorageLayout(orderedContracts,typeOf,nodes){
 const types={},storage=[],unresolved=[],building=new Set();let built=0;
 function type(n,depth=0){
  if(!n||depth>24||++built>8192)throw Error('LEGACY_STORAGE_TYPE_BUDGET');
  const key=storageTypeID(n);if(!key)throw Error('LEGACY_STORAGE_TYPE_IDENTITY_UNAVAILABLE');
  if(types[key])return key;
  const label=cleanLabel(n),size=scalarSize(label),reference=n.referencedDeclaration??n.pathNode?.referencedDeclaration,definition=nodes.get(reference);
  if(size){types[key]={encoding:'inplace',label,numberOfBytes:String(size)};return key;}
  if(n.nodeType==='ElementaryTypeName'&&['bytes','string'].includes(label)){
   types[key]={encoding:'bytes',label,numberOfBytes:'32'};return key;
  }
  if(n.nodeType==='Mapping'){
   types[key]={encoding:'mapping',label,numberOfBytes:'32'};
   types[key].key=type(n.keyType,depth+1);types[key].value=type(n.valueType,depth+1);return key;
  }
  if(n.nodeType==='ArrayTypeName'){
   const base=type(n.baseType,depth+1),length=n.length?/\[(\d+)\]$/.exec(label)?.[1]:null;
   if(n.length&&!length)throw Error('LEGACY_STORAGE_ARRAY_LENGTH_UNAVAILABLE');
   const value=types[key]={encoding:n.length?'inplace':'dynamic_array',label,numberOfBytes:'32',base,...(n.length?{length}:{} )};
   if(n.length){const info=storageArrayInfo(value,types),count=BigInt(length),per=BigInt(info.elements_per_slot);
    if(count<1n)throw Error('LEGACY_STORAGE_ARRAY_LENGTH_INVALID');
    value.numberOfBytes=String((info.packed?(count+per-1n)/per:count*BigInt(info.stride_slots))*32n);
   }return key;
  }
  if(definition?.nodeType==='EnumDefinition'){
   const count=definition.members.length;if(!count)throw Error('LEGACY_STORAGE_ENUM_EMPTY');
   const bytes=Math.max(1,Math.ceil(Math.ceil(Math.log2(count))/8));
   types[key]={encoding:'inplace',label,numberOfBytes:String(bytes),enum_ast_id:definition.id};return key;
  }
  if(definition?.nodeType==='UserDefinedValueTypeDefinition'){
   const underlying=type(definition.underlyingType,depth+1);types[key]={...types[underlying],label,underlying};return key;
  }
  if(definition?.nodeType!=='StructDefinition')throw Error('LEGACY_STORAGE_TYPE_UNSUPPORTED:'+label);
  if(building.has(definition.id))throw Error('LEGACY_STORAGE_INLINE_RECURSION');
  building.add(definition.id);
  const value=types[key]={encoding:'inplace',label,numberOfBytes:'0',struct_ast_id:definition.id,members:[]};let slot=0n,offset=0;
  for(const field of definition.members){
   const child=type(field.typeName,depth+1),entry=types[child],bytes=BigInt(entry.numberOfBytes),whole=entry.encoding!=='inplace'||aggregateStorageType(entry)||bytes>32n;
   if(bytes<1n)throw Error('LEGACY_STORAGE_INLINE_RECURSION');
   if(whole||BigInt(offset)+bytes>32n){if(offset)slot++;offset=0;}
   value.members.push({astId:field.id,label:field.name,slot:String(slot),offset,type:child});
   if(whole){slot+=(bytes+31n)/32n;offset=0;}else{offset+=Number(bytes);if(offset===32){slot++;offset=0;}}
  }
  value.numberOfBytes=String((slot+(offset?1n:0n))*32n);building.delete(definition.id);return key;
 }
 let slot=0n,offset=0,blocked=false;
 for(const contract of [...orderedContracts].reverse())for(const variable of contract.nodes||[]){
  if(variable.nodeType!=='VariableDeclaration'||!variable.stateVariable||variable.constant||variable.mutability==='immutable')continue;
  const label=typeOf(variable);
  if(blocked){unresolved.push({ast_id:variable.id,type:label,reason:'unsupported_preceding_legacy_aggregate'});continue;}
  try{
   const key=type(variable.typeName),entry=types[key],bytes=BigInt(entry.numberOfBytes),whole=entry.encoding!=='inplace'||aggregateStorageType(entry)||bytes>32n;
   if(bytes<1n)throw Error('LEGACY_STORAGE_SIZE_UNAVAILABLE');
   if(whole||BigInt(offset)+bytes>32n){if(offset)slot++;offset=0;}
   storage.push({astId:variable.id,label:variable.name,slot:String(slot),offset,type:key,layout_basis:'derived_solidity_ast_storage_rules'});
   if(whole){slot+=(bytes+31n)/32n;offset=0;}else{offset+=Number(bytes);if(offset===32){slot++;offset=0;}}
  }catch(error){blocked=true;unresolved.push({ast_id:variable.id,type:label,reason:String(error.message)});}
 }
 return{storage,types,basis:'derived_solidity_ast_storage_rules',unresolved};
}

export function storageTypeRegistry(layout,nodes){
 const byStruct=new Map();
 for(const[type_id,type]of Object.entries(layout.types||{}))if(type.members){
  const candidates=[...nodes.values()].filter(n=>n.nodeType==='StructDefinition'&&n.members.length===type.members.length&&n.members.every((m,i)=>m.id===type.members[i].astId));
  if(candidates.length===1){type.struct_ast_id=candidates[0].id;byStruct.set(candidates[0].id,type_id);}
 }
 function forNode(node){
  const name=node?.typeName||node,key=storageTypeID(name),type=layout.types?.[key];
  if(type)return{type_id:key,...type};
  const reference=name?.referencedDeclaration??name?.pathNode?.referencedDeclaration??Number(/^t_struct\([^)]*\)(\d+)_storage$/.exec(key)?.[1]),found=byStruct.get(reference);
  return found?{type_id:found,...layout.types[found]}:null;
 }
 return{layout,byStruct,forNode,pointerType(declaration){
  if(declaration?.storageLocation!=='storage'&&!(declaration&&!declaration.stateVariable&&['default',undefined].includes(declaration.storageLocation)&&/_storage_ptr$/.test(declaration.typeDescriptions?.typeIdentifier||'')))return null;
  const type=forNode(declaration);return type&&aggregateStorageType(type)?{sort:'StorageReference',type:'storage:'+type.type_id,type_id:type.type_id,struct_ast_id:type.struct_ast_id??null}:null;
 }};
}

const constant=value=>({op:'constant',value:String(value)}),literal=e=>e?.op==='constant'?BigInt(e.value):null;
export function storageSlotTerm(op,...args){
 const values=args.map(literal);
 if(values.every(v=>v!==null)){
  const value=op==='add'?values[0]+values[1]:op==='mul'?values[0]*values[1]:op==='div'?values[0]/values[1]:op==='mod'?values[0]%values[1]:null;
  if(value!==null)return constant(op==='add'||op==='mul'?(value%WORD+WORD)%WORD:value);
 }
 if(op==='add'&&values[1]===0n||op==='mul'&&values[1]===1n)return args[0];
 return{op,args};
}
export function evaluateStorageSlot(term,expressions={}){
 if(term.op==='unknown')throw Error(term.reason||'STORAGE_SLOT_UNAVAILABLE');
 if(term.op==='constant'||term.op==='expression'){
  const value=term.op==='constant'?term.value:expressions[term.expression_id];if(value===undefined)throw Error('STORAGE_KEY_VALUE_UNAVAILABLE');
  let word=BigInt(value);const bytes=/^bytes(\d+)$/.exec(term.encoding_type||'');
  if(bytes){const bits=BigInt(Number(bytes[1])*8);if(word<0n||word>=1n<<bits)throw Error('STORAGE_MAPPING_KEY_OUT_OF_TYPE_RANGE');word<<=256n-bits;}
  return word;
 }
 if(term.op==='keccak_words'){
  const raw=Buffer.concat(term.args.map(a=>Buffer.from(((evaluateStorageSlot(a,expressions)%WORD+WORD)%WORD).toString(16).padStart(64,'0'),'hex')));
  return BigInt('0x'+Buffer.from(keccak_256(raw)).toString('hex'));
 }
 if(!['add','mul','div','mod'].includes(term.op))throw Error('STORAGE_SLOT_OPERATION_UNSUPPORTED');
 const a=evaluateStorageSlot(term.args[0],expressions),b=evaluateStorageSlot(term.args[1],expressions);
 if(term.op==='add')return(a+b)%WORD;if(term.op==='mul')return a*b%WORD;if(term.op==='div')return a/b;if(term.op==='mod')return a%b;
 throw Error('STORAGE_SLOT_OPERATION_UNSUPPORTED');
}
export function storageRoot(item,types){
 const type=types?.[item?.type];if(!item||!type)return null;
 return{root_state_ast_id:item.astId,base_slot:String(item.slot),type_id:item.type,scalar_type:type.label,offset:item.offset||0,path:[],static_slot:String(item.slot),
  slot_expression:constant(item.slot),offset_expression:constant(item.offset||0),bounds:[],layout_basis:item.layout_basis||'verified_compiler_layout'};
}
export function storageMember(parent,field,types){
 const child=types[field?.type];if(!child)return null;
 const slot=storageSlotTerm('add',parent.slot_expression,constant(field.slot));
 return{...copy(parent),root_state_ast_id:parent.namespace_root?field.astId:parent.root_state_ast_id,namespace_root:false,type_id:field.type,scalar_type:child.label,offset:field.offset||0,
  static_slot:literal(slot)===null?null:slot.value,slot_expression:slot,offset_expression:constant(field.offset||0),path:[...parent.path,{kind:'member',name:field.label,field_ast_id:field.astId,relative_slot:String(field.slot),byte_offset:field.offset||0}]};
}
export function storageIndex(parent,key,types){
 const type=types[parent.type_id];if(!type)return null;
 const term=key.key_literal!==null&&key.key_literal!==undefined?constant(key.key_literal):{op:'expression',expression_id:key.key_expression_id};
 if(type.encoding==='mapping'){
  const keyType=types[type.key],child=types[type.value];
  if(!child||!keyType)return null;
  // Dynamic mapping keys require their unpadded bytes, not an ABI word.
  if(['bytes','string'].includes(keyType.label))return{...copy(parent),type_id:type.value,scalar_type:child.label,offset:0,static_slot:null,
   slot_expression:{op:'unknown',reason:'STORAGE_DYNAMIC_MAPPING_KEY_BYTES_UNAVAILABLE'},offset_expression:constant(0),
   path:[...parent.path,{kind:'mapping',key_type:keyType.label,...key}],unresolved:unique([...(parent.unresolved||[]),'STORAGE_DYNAMIC_MAPPING_KEY_BYTES_UNAVAILABLE'])};
  const slot={op:'keccak_words',args:[{...term,encoding_type:keyType.label},parent.slot_expression]};
  return{...copy(parent),type_id:type.value,scalar_type:child.label,offset:0,static_slot:null,slot_expression:slot,offset_expression:constant(0),
   path:[...parent.path,{kind:'mapping',key_type:keyType.label,...key}]};
 }
 const info=storageArrayInfo(type,types);if(!info)return null;
 const base=info.dynamic?{op:'keccak_words',args:[parent.slot_expression]}:parent.slot_expression;
 const delta=info.packed?storageSlotTerm('div',term,constant(info.elements_per_slot)):storageSlotTerm('mul',term,constant(info.stride_slots));
 const slot=storageSlotTerm('add',base,delta),offset=info.packed?storageSlotTerm('mul',storageSlotTerm('mod',term,constant(info.elements_per_slot)),constant(info.element_bytes)):constant(0);
 return{...copy(parent),type_id:info.element_type,scalar_type:types[info.element_type].label,offset:literal(offset)===null?null:Number(offset.value),static_slot:literal(slot)===null?null:slot.value,
  slot_expression:slot,offset_expression:offset,bounds:[...(parent.bounds||[]),{kind:info.dynamic?'dynamic_array':'fixed_array',index_expression_id:key.key_expression_id,index_literal:key.key_literal,
   length:info.length,length_slot_expression:info.dynamic?copy(parent.slot_expression):null,status:'requires_index_in_bounds'}],
  path:[...parent.path,{kind:info.dynamic?'dynamic_array':'fixed_array',...key,...info}]};
}

// Bindings are immutable descriptions of locations, not copies of their values.
// Captured key expression IDs retain the construction program point.
export class StorageReferences{
 constructor(result,method,{shared=null,bindings=null,returns=null,budget={count:0,max:4096}}={}){
  this.result=result;this.method=method;this.budget=budget;this.shared=shared||{serial:0,records:new Map(),callReturns:new Map(),expressionBindings:new Map()};this.bindings=bindings||new Map();this.returns=returns||[];
  for(const name of['storage_references','storage_reference_phis','storage_access_bindings'])result.sections[name]||=[];
 }
 fork({newFunction=false}={}){return new StorageReferences(this.result,this.method,{shared:this.shared,bindings:new Map(this.bindings),returns:newFunction?[]:this.returns,budget:this.budget});}
 get(declaration){return copy(this.bindings.get(declaration)||{cases:[],unresolved:['STORAGE_REFERENCE_BINDING_UNAVAILABLE']});}
 bind(declaration,value){this.bindings.set(declaration,copy(value||{cases:[],unresolved:['STORAGE_REFERENCE_BINDING_UNAVAILABLE']}));}
 capture(projection,{expression_id=null,context={},evidence_ids=[]}={}){
  if(this.shared.records.size>=512||this.budget.count>=this.budget.max)return{cases:[],unresolved:['STORAGE_REFERENCE_BUDGET']};
  const storage_reference_id=id(this.method.method_id,'storage-reference',expression_id,context.path||'',this.shared.serial++);
  const row={storage_reference_id,method_id:this.method.method_id,code_hash:this.method.code_hash,expression_id,projection:copy(projection),
   root_state_ast_id:projection.root_state_ast_id,type_id:projection.type_id,conditions:[...(context.conditions||[])],path:context.path||'',construction_order:context.order??null,
   status:projection.unresolved?.length?'partial':'location_captured',unresolved:projection.unresolved||[],evidence_ids:[...evidence_ids]};
  this.budget.count++;this.shared.records.set(storage_reference_id,row);this.result.sections.storage_references.push(row);
  return{cases:[{storage_reference_id,conditions:[...(context.conditions||[])]}],unresolved:[...(projection.unresolved||[])]};
 }
 resolve(value){return(value?.cases||[]).map(c=>({case:c,record:this.shared.records.get(c.storage_reference_id)})).filter(c=>c.record);}
 invalidate(reason){for(const key of this.bindings.keys())this.bind(key,{cases:[],unresolved:[reason]});}
 returned(component,{allReturningPathsCovered=false}={}){
  const rows=this.returns.filter(r=>r.component===component),cases=rows.flatMap(r=>(r.value?.cases||[]).map(c=>({...c,conditions:unique([...r.conditions,...c.conditions])}))),
   unresolved=unique([...rows.flatMap(r=>r.value?.unresolved||[]),...(!allReturningPathsCovered?['STORAGE_REFERENCE_RETURN_PATHS_UNVERIFIED']:[]),...(!rows.length?['STORAGE_REFERENCE_RETURN_UNAVAILABLE']:[])]);
  if(cases.length>16)return{cases:[],unresolved:unique([...unresolved,'STORAGE_REFERENCE_BRANCH_BUDGET'])};
  return{cases,unresolved};
 }
 merge(yes,no,{yesConditions=[],noConditions=[],yesTerminated=false,noTerminated=false,context={}}={}){
  for(const key of new Set([...this.bindings.keys(),...yes.bindings.keys(),...no.bindings.keys()])){
   const a=yes.get(key),b=no.get(key);if(!yesTerminated&&!noTerminated&&JSON.stringify(a)===JSON.stringify(b)){this.bind(key,a);continue;}
   const selected=[...(!yesTerminated?[{value:a,conditions:yesConditions}]:[]),...(!noTerminated?[{value:b,conditions:noConditions}]:[])];
   if(!selected.length)continue;
   const cases=selected.flatMap(({value,conditions})=>value.cases.length?value.cases.map(c=>({...c,conditions:unique([...c.conditions,...conditions])})):[{storage_reference_id:null,conditions}]);
   const unresolved=unique(selected.flatMap(v=>v.value.unresolved));
   if(cases.length>16){this.bind(key,{cases:[],unresolved:['STORAGE_REFERENCE_BRANCH_BUDGET']});continue;}
   const storage_reference_phi_id=id(this.method.method_id,'storage-reference-phi',key,context, this.shared.serial++);
   this.result.sections.storage_reference_phis.push({storage_reference_phi_id,method_id:this.method.method_id,declaration_id:key,cases:copy(cases),unresolved,
    status:unresolved.length||cases.some(c=>!c.storage_reference_id)?'partial':'conditional_locations',evidence_ids:unique(cases.flatMap(c=>this.shared.records.get(c.storage_reference_id)?.evidence_ids||[]))});
   this.bind(key,{cases,unresolved,storage_reference_phi_id});
  }
 }
}
