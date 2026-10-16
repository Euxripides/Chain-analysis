import{keccak_256}from'@noble/hashes/sha3.js';
import{internalDispatch}from'./ir/source-symbols.mjs';
const WORD=1n<<256n,hash=b=>Buffer.from(keccak_256(b)),text=n=>n?.typeDescriptions?.typeString||n?.name||'';
const hex=n=>'0x'+n.toString(16).padStart(64,'0');
export function erc7201Root(name){
 if(typeof name!=='string'||!name||/\s/u.test(name)||Buffer.byteLength(name)>256)throw Error('NAMESPACE_IDENTIFIER_UNSUPPORTED');
 const first=BigInt('0x'+hash(Buffer.from(name)).toString('hex'));if(first===0n)throw Error('NAMESPACE_ROOT_PREIMAGE_UNDERFLOW');
 return BigInt('0x'+hash(Buffer.from((first-1n).toString(16).padStart(64,'0'),'hex')).toString('hex'))&~255n;
}
function annotations(n){
 const docs=typeof n.documentation==='string'?n.documentation:n.documentation?.text||'';
 return[...docs.matchAll(/(?:^|\n)\s*@custom:storage-location\s+([^\s]+)/gu)].map(m=>m[1]);
}
function scalarBytes(label){
 if(label==='bool')return 1;if(/^address(?: payable)?$|^contract /.test(label))return 20;
 const integer=/^(?:u?int)(\d*)$/.exec(label),bytes=/^bytes(\d+)$/.exec(label);
 const size=integer?Number(integer[1]||256)/8:bytes?Number(bytes[1]):null;
 return size&&Number.isInteger(size)&&size>=1&&size<=32?size:null;
}

export function deriveNamespaceLayout(struct,nodes){
 const types={},building=new Set();let fields=0;
 function type(n,depth=0){
  if(depth>8)throw Error('NAMESPACE_TYPE_DEPTH_BUDGET');
  const label=text(n),reference=n.referencedDeclaration??n.pathNode?.referencedDeclaration,definition=nodes.get(reference);
  const id='namespace:'+String(n.typeDescriptions?.typeIdentifier||n.nodeType+':'+label+':'+reference);
  if(types[id])return id;
  const bytes=scalarBytes(label);if(bytes){types[id]={encoding:'inplace',label,numberOfBytes:String(bytes),model_supported:true};return id;}
  if(n.nodeType==='ElementaryTypeName'&&['bytes','string'].includes(label)){
   types[id]={encoding:'bytes',label,numberOfBytes:'32',model_supported:false};return id;
  }
  if(n.nodeType==='Mapping'){
   types[id]={encoding:'mapping',label,numberOfBytes:'32'};
   const key=type(n.keyType,depth+1),value=type(n.valueType,depth+1);types[id].key=key;types[id].value=value;
   types[id].model_supported=types[key].model_supported&&types[value].model_supported;return id;
  }
  if(n.nodeType==='ArrayTypeName'){
   const base=type(n.baseType,depth+1);if(!n.length){types[id]={encoding:'dynamic_array',label,numberOfBytes:'32',base,model_supported:false};return id;}
   const match=/\[(\d+)\]$/.exec(label);if(!match)throw Error('NAMESPACE_ARRAY_LENGTH_UNAVAILABLE');const length=BigInt(match[1]);if(length<1n)throw Error('NAMESPACE_ARRAY_LENGTH_INVALID');
   const element=types[base],size=BigInt(element.numberOfBytes),packable=element.encoding==='inplace'&&!element.members&&!element.base&&size<=32n;
   const slots=packable?(length+32n/size-1n)/(32n/size):length*((size+31n)/32n);if(slots>256n)throw Error('NAMESPACE_STATIC_SLOT_BUDGET');
   types[id]={encoding:'inplace',label,numberOfBytes:String(slots*32n),base,length:String(length),model_supported:false};return id;
  }
  if(definition?.nodeType==='EnumDefinition'){
   if(definition.members.length>256)throw Error('NAMESPACE_ENUM_WIDTH_UNSUPPORTED');types[id]={encoding:'inplace',label,numberOfBytes:'1',model_supported:false};return id;
  }
  if(definition?.nodeType==='UserDefinedValueTypeDefinition'){
   const underlying=type(definition.underlyingType,depth+1),item=types[underlying];types[id]={encoding:'inplace',label,numberOfBytes:item.numberOfBytes,underlying,model_supported:false};return id;
  }
  if(definition?.nodeType!=='StructDefinition')throw Error('NAMESPACE_STORAGE_TYPE_UNSUPPORTED:'+label);
  if(building.has(definition.id))throw Error('NAMESPACE_RECURSIVE_INLINE_STRUCT');building.add(definition.id);
  const value=types[id]={encoding:'inplace',label:'struct '+definition.canonicalName,struct_ast_id:definition.id,numberOfBytes:'0',members:[],model_supported:true};let slot=0n,offset=0;
  for(const member of definition.members){
   if(++fields>64)throw Error('NAMESPACE_FIELD_BUDGET');
   const child=type(member.typeName,depth+1),item=types[child],size=BigInt(item.numberOfBytes),whole=item.encoding!=='inplace'||Boolean(item.members||item.base)||size>32n;
   if(whole||offset+Number(size)>32){if(offset)slot++;offset=0;}
   value.members.push({astId:member.id,label:member.name,slot:String(slot),offset,type:child});
   value.model_supported&&=item.model_supported===true;
   if(whole){slot+=(size+31n)/32n;offset=0;}else{offset+=Number(size);if(offset===32){slot++;offset=0;}}
   if(slot+(offset?1n:0n)>256n)throw Error('NAMESPACE_STATIC_SLOT_BUDGET');
  }
  const slots=slot+(offset?1n:0n);if(slots<1n)throw Error('NAMESPACE_EMPTY_STRUCT');value.numberOfBytes=String(slots*32n);building.delete(definition.id);return id;
 }
 const rootType=type({nodeType:'UserDefinedTypeName',referencedDeclaration:struct.id,typeDescriptions:{typeIdentifier:'struct:'+struct.id,typeString:'struct '+struct.canonicalName}});
 return{type_id:rootType,types,slot_count:String(BigInt(types[rootType].numberOfBytes)/32n),members:types[rootType].members};
}

// Layout derivation is separate from pointer identity. Callers must activate
// fields only after a real source pointer assignment has been validated.
export function collectNamespaces({nodes,orderedContracts,layout={storage:[],types:{}}}){
 const parents=new Set(orderedContracts.map(c=>c.id)),records=[],byStruct=new Map();
 for(const n of nodes.values())if(n.nodeType==='StructDefinition'&&parents.has(n.scope)&&annotations(n).length){
  const tags=annotations(n),row={struct_ast_id:n.id,struct_name:n.canonicalName,scope_contract_ast_id:n.scope,layout_complete:false,pointer_assignment_verified:false,complete:false,unresolved:[]};records.push(row);
  try{
   if(tags.length!==1)throw Error('NAMESPACE_ANNOTATION_AMBIGUOUS');const tag=/^erc7201:(.+)$/u.exec(tags[0]);if(!tag)throw Error('NAMESPACE_FORMULA_UNSUPPORTED');
   const root=erc7201Root(tag[1]),derived=deriveNamespaceLayout(n,nodes),end=root+BigInt(derived.slot_count);if(end>WORD)throw Error('NAMESPACE_SLOT_OVERFLOW');
   Object.assign(row,{namespace_id:tag[1],formula:'erc7201',root_slot:String(root),root_hex:hex(root),...derived});
   for(const ordinary of layout.storage||[]){
    const entry=layout.types?.[ordinary.type],size=entry?.numberOfBytes;if(size===undefined)throw Error('NAMESPACE_STANDARD_LAYOUT_EXTENT_UNAVAILABLE');
    const from=BigInt(ordinary.slot),to=from+(BigInt(size)+31n)/32n;if(root<to&&from<end)throw Error('NAMESPACE_STANDARD_STORAGE_OVERLAP');
   }
   row.layout_complete=true;byStruct.set(n.id,row);
  }catch(e){row.unresolved.push(String(e.message));}
 }
 for(let i=0;i<records.length;i++)for(let j=i+1;j<records.length;j++){
  const a=records[i],b=records[j];if(a.root_slot===undefined||b.root_slot===undefined)continue;
  const x=BigInt(a.root_slot),y=BigInt(b.root_slot);if(x<y+BigInt(b.slot_count)&&y<x+BigInt(a.slot_count))for(const r of[a,b]){
   r.layout_complete=false;r.unresolved.push('NAMESPACE_DECLARED_REGION_CONFLICT');byStruct.delete(r.struct_ast_id);
  }
 }
 return{records,byStruct};
}

function literalRoot(node,nodes,refs){
 if(node?.nodeType==='YulLiteral'&&node.kind==='number')return{value:BigInt(node.value),constant_ast_id:null};
 if(node?.nodeType==='YulIdentifier'){
  const ref=refs.find(r=>r.src===node.src),d=nodes.get(ref?.declaration),value=d?.value;
  if(!d?.constant||!['uint256','bytes32'].includes(text(d))||value?.nodeType!=='Literal'||value.kind!=='number'||value.subdenomination)throw Error('NAMESPACE_ROOT_CONSTANT_UNSUPPORTED');
  return{value:BigInt(value.value),constant_ast_id:d.id};
 }
 throw Error('NAMESPACE_ROOT_EXPRESSION_UNSUPPORTED');
}
export function bindNamespaceAccessors({functions,nodes,namespaces}){
 const byFunction=new Map(),records=[];
 for(const fn of functions){
  const returns=fn.returnParameters?.parameters||[],r=returns[0],structID=r?.typeName?.referencedDeclaration??r?.typeName?.pathNode?.referencedDeclaration,ns=namespaces.byStruct.get(structID);
  if(returns.length!==1||r.storageLocation!=='storage'||!ns)continue;
  const row={function_ast_id:fn.id,namespace_struct_ast_id:structID,root_assignment_verified:false,complete:false,unresolved:[]};records.push(row);
  try{
   if(!['internal','private'].includes(fn.visibility)||fn.stateMutability!=='pure'||fn.parameters.parameters.length||fn.modifiers.length||fn.body?.statements?.length!==1)throw Error('NAMESPACE_ACCESSOR_EFFECTS_UNSUPPORTED');
   const asm=fn.body.statements[0],ast=asm.AST;if(asm.nodeType!=='InlineAssembly'||ast?.nodeType!=='YulBlock'||ast.statements.length!==1)throw Error('NAMESPACE_ACCESSOR_ASSEMBLY_UNSUPPORTED');
   const assign=ast.statements[0];if(assign.nodeType!=='YulAssignment'||assign.variableNames.length!==1)throw Error('NAMESPACE_POINTER_ASSIGNMENT_UNSUPPORTED');
   const refs=Array.isArray(asm.externalReferences)?asm.externalReferences:Object.entries(asm.externalReferences||{}).map(([src,r])=>({src,...r})),left=refs.find(ref=>ref.src===assign.variableNames[0].src);
   if(left?.declaration!==r.id||!(left.isSlot||left.suffix==='slot'))throw Error('NAMESPACE_POINTER_DECLARATION_MISMATCH');
   const root=literalRoot(assign.value,nodes,refs);if(root.value<0n||root.value>=WORD||String(root.value)!==ns.root_slot)throw Error('NAMESPACE_ACTUAL_ROOT_MISMATCH');
   Object.assign(row,{root_assignment_verified:true,complete:true,namespace_id:ns.namespace_id,root_slot:ns.root_slot,root_hex:ns.root_hex,return_declaration_ast_id:r.id,constant_ast_id:root.constant_ast_id,assembly_ast_id:asm.id,assignment_src:assign.src});
   byFunction.set(fn.id,{...row,namespace:ns});ns.pointer_assignment_verified=true;ns.complete=ns.layout_complete;
  }catch(e){row.unresolved.push(String(e.message));}
 }
 return{byFunction,records};
}

export function activateNamespaceLayout(base,namespaces,accessors){
 if(!namespaces.records.length)return{layout:structuredClone(base),byField:new Map(),byStruct:new Map(),accessors:accessors.byFunction,namespaces:namespaces.byStruct};
 const layout={...structuredClone(base),storage:structuredClone(base.storage||[]),types:structuredClone(base.types||{}),namespace_static_slots:[],namespace_static_locations:[]},byField=new Map(),byStruct=new Map();
 for(const ns of namespaces.records){
  if(!ns.layout_complete||!ns.pointer_assignment_verified)continue;
  for(const[id,type]of Object.entries(ns.types)){
   if(layout.types[id]&&JSON.stringify(layout.types[id])!==JSON.stringify(type))throw Error('NAMESPACE_TYPE_IDENTITY_CONFLICT');layout.types[id]=type;
   if(type.struct_ast_id!==undefined)byStruct.set(type.struct_ast_id,{type:'namespace-struct:'+type.struct_ast_id,sort:'StorageReference',struct_ast_id:type.struct_ast_id});
  }
  for(let i=0n;i<BigInt(ns.slot_count);i++)layout.namespace_static_slots.push(String(BigInt(ns.root_slot)+i));
  for(const field of ns.members){
   const entry={astId:field.astId,label:field.label,slot:String(BigInt(ns.root_slot)+BigInt(field.slot)),offset:field.offset,type:field.type,
    namespace_id:ns.namespace_id,namespace_struct_ast_id:ns.struct_ast_id,namespace_root_slot:ns.root_slot,layout_basis:'erc7201_ast_layout_with_verified_source_pointer_assignment'};
   if(byField.has(field.astId))throw Error('NAMESPACE_FIELD_IDENTITY_CONFLICT');layout.storage.push(entry);byField.set(field.astId,entry);
   function cells(typeID,slot,offset,fields,depth=0){
    if(depth>8)throw Error('NAMESPACE_STATIC_LOCATION_DEPTH');const type=layout.types[typeID];
    if(type.members){for(const child of type.members)cells(child.type,slot+BigInt(child.slot),child.offset,[...fields,child.astId],depth+1);return;}
    if(type.encoding==='inplace'&&!type.base&&type.model_supported)layout.namespace_static_locations.push({root:field.astId,slot:String(slot),offset,type_id:typeID,scalar_type:type.label,keys:[],fields,namespace_id:ns.namespace_id});
   }cells(field.type,BigInt(entry.slot),field.offset,[]);
  }
 }
 layout.namespace_storage={scope:'verified_source_pointer_roots_and_derived_typed_layout',namespaces:namespaces.records.map(ns=>({namespace_id:ns.namespace_id,struct_ast_id:ns.struct_ast_id,
  root_slot:ns.root_slot,root_hex:ns.root_hex,slot_count:ns.slot_count,type_id:ns.type_id,layout_complete:ns.layout_complete,pointer_assignment_verified:ns.pointer_assignment_verified,complete:ns.complete,unresolved:ns.unresolved})),accessors:accessors.records};
 return{layout,byField,byStruct,accessors:accessors.byFunction,namespaces:namespaces.byStruct};
}
export function namespacePointerType(declaration,registry){
 const id=declaration?.typeName?.referencedDeclaration??declaration?.typeName?.pathNode?.referencedDeclaration;
 return declaration?.storageLocation==='storage'?registry.byStruct.get(id)||null:null;
}
export function namespaceRootReference(binding){
 const ns=binding.namespace;return{sort:'StorageReference',type:'namespace-struct:'+ns.struct_ast_id,struct_ast_id:ns.struct_ast_id,
  storage_location:{root:ns.struct_ast_id,slot:ns.root_slot,offset:0,type_id:ns.type_id,scalar_type:ns.types[ns.type_id].label,keys:[],fields:[],namespace_root:true,
   namespace_id:ns.namespace_id,namespace_struct_ast_id:ns.struct_ast_id}};
}
export function namespaceCalleeID(node,nodes,functions){
 const callee=node?.expression;let d=nodes.get(callee?.referencedDeclaration);
 if(d?.nodeType!=='FunctionDefinition'||!internalDispatch(d,callee,nodes))return null;
 if(callee.nodeType==='Identifier')for(let i=0;i<12;i++){const next=functions.find(f=>f.baseFunctions?.includes(d.id)&&f.body);if(!next)break;d=next;}
 return d.id;
}
export function namespaceAccessPath(node,registry,{aliases=new Map(),resolveFunction}={}){
 if(!registry)return null;const seen=new Set();
 function path(n){
  if(!n||seen.has(n))return null;seen.add(n);
  if(n.nodeType==='Identifier')return aliases.has(n.referencedDeclaration)?path(aliases.get(n.referencedDeclaration)):null;
  if(n.nodeType==='FunctionCall'){
   const id=resolveFunction?resolveFunction(n):n.expression?.referencedDeclaration,binding=registry.accessors.get(id);
   if(!binding||n.arguments.length)return null;const loc=namespaceRootReference(binding).storage_location;
   return{...loc,base_slot:loc.slot,path:[],static_slot:loc.slot};
  }
  const parent=path(n.baseExpression||n.expression);if(!parent)return null;const type=registry.layout.types[parent.type_id];
  if(n.nodeType==='MemberAccess'&&type?.members){
   const field=type.members.find(f=>f.astId===n.referencedDeclaration),child=registry.layout.types[field?.type];if(!field||!child)return null;
   const slot=parent.static_slot===null?null:String(BigInt(parent.static_slot)+BigInt(field.slot));
   return{...parent,root:parent.namespace_root?field.astId:parent.root,type_id:field.type,scalar_type:child.label,offset:field.offset||0,namespace_root:false,
    fields:parent.namespace_root?[]:[...parent.fields,field.astId],static_slot:slot,path:[...parent.path,{kind:'member',name:field.label,relative_slot:field.slot,byte_offset:field.offset,field_ast_id:field.astId}]};
  }
  if(n.nodeType==='IndexAccess'&&type?.encoding==='mapping'){
   const child=registry.layout.types[type.value];return{...parent,type_id:type.value,scalar_type:child.label,offset:0,namespace_root:false,static_slot:null,
    keys:[...parent.keys,n.indexExpression],path:[...parent.path,{kind:'mapping',key_type:registry.layout.types[type.key].label,key_node:n.indexExpression}]};
  }
  return null;
 }
 return path(node);
}
export function namespaceGetterPath(fn,registry,nodes,functions){
 if(!registry||!fn.body?.statements?.length||fn.body.statements.length>17||(fn.modifiers||[]).length)return null;
 const aliases=new Map(),resolveFunction=n=>namespaceCalleeID(n,nodes,functions),statements=fn.body.statements;
 for(const statement of statements.slice(0,-1)){
  if(statement.nodeType!=='VariableDeclarationStatement'||statement.declarations.length!==1||!statement.initialValue)return null;
  const d=statement.declarations[0],type=namespacePointerType(d,registry),value=namespaceAccessPath(statement.initialValue,registry,{aliases,resolveFunction});
  if(!type||!value||registry.layout.types[value.type_id]?.struct_ast_id!==type.struct_ast_id)return null;aliases.set(d.id,statement.initialValue);
 }
 const last=statements.at(-1);if(last.nodeType!=='Return'||!last.expression)return null;
 const value=namespaceAccessPath(last.expression,registry,{aliases,resolveFunction});return value?{node:last.expression,namespace_path:value,namespace_aliases:aliases}:null;
}
