import {id,unique,hash} from '../model.mjs';
import {numericLiteral} from '../literals.mjs';
import {Terms,scalarType,asBool,asInt,mod,WORD,MAX_WORD,ARRAY} from './terms.mjs';
import {and,or,number,obligation,solveObligations,closeModels} from './solver.mjs';
import {detectLoopRecurrences} from './loops.mjs';
import {coupledPolynomialSMT} from './coupled-polynomials.mjs';
import {polynomialSMT} from './polynomials.mjs';
import {fixedBytesTerm} from '../ir/scalar-casts.mjs';
import {localType as scalarLocalType,literalBytes,coerceBytes,encodeABI,hashBytes,recoverSigner,byteSequence} from './bytes.mjs';
import {builtin,builtinMember,internalDispatch} from '../ir/source-symbols.mjs';
import {namespacePointerType,namespaceRootReference} from '../namespace-layout.mjs';
import{storageTypeRegistry,aggregateStorageType,storageArrayInfo}from'../ir/storage-reference.mjs';

const walk=(node,fn)=>{if(!node)return;fn(node);for(const [key,value]of Object.entries(node))if(!['typeDescriptions','documentation'].includes(key)){
 if(Array.isArray(value)){for(const x of value)if(x?.nodeType)walk(x,fn);}else if(value?.nodeType)walk(value,fn);
}};
const uint256={sort:'Int',type:'uint256',bits:256,signed:false,min:0n,max:MAX_WORD,mod:WORD};
const bool={sort:'Bool',type:'bool'};

export function buildStateTransition(fn,method,ctx) {
 const {nodes,functions,layout,location,sourceText}=ctx,terms=new Terms(),failure=[],unresolved=[],effects=[],staticSlots=unique([...(layout.storage||[]).map(s=>s.slot),...(layout.namespace_static_slots||[])]);
 const storageTypes=ctx.storage_types||storageTypeRegistry(layout,nodes);
 const localType=d=>(ctx.namespace_registry&&namespacePointerType(d,ctx.namespace_registry))||storageTypes.pointerType(d)||scalarLocalType(d);
 const layoutByID=new Map((layout.storage||[]).map(s=>[s.astId,s])),env=new Map();
 const caller=terms.variable('caller',{...uint256,bits:160,max:(1n<<160n)-1n,mod:1n<<160n},'msg.sender');env.set('msg.sender',caller);
 for(const [name,type]of [['tx.origin',{...uint256,bits:160,max:(1n<<160n)-1n,mod:1n<<160n}],['msg.value',uint256],['block.timestamp',uint256],['block.number',uint256]])env.set(name,terms.variable(name.replace('.','_'),type,name));
 const executionAddress=ctx.executionAddress||method.storage_address;
 const selfAddress=/^0x[0-9a-f]{40}$/i.test(executionAddress||'')?{term:BigInt(executionAddress).toString(),...scalarType({type:'address'})}:terms.variable('execution_address',scalarType({type:'address'}),'symbolic execution address');
 env.set('address(this)',selfAddress);
 if(!/^\d+$/.test(String(ctx.chainID||'')))env.set('block.chainid',terms.variable('chain_id',uint256,'symbolic chain identity'));
 const initial=ctx.zeroInitialStorage?'((as const '+ARRAY+') 0)':terms.variable('storage_0',ARRAY).term;
 let visits=0,frameSerial=0,effectSerial=0;
 const rootCases=()=>[{case_id:'effect_0',ancestors:['effect_0'],conditions:[]}];
 const clone=s=>({...s,locals:new Map(s.locals),immutables:new Map(s.immutables),conditions:[...s.conditions],writes:[...s.writes],returns:[...s.returns],signature_recoveries:[...(s.signature_recoveries||[])],symbolic_cases:[...(s.symbolic_cases||rootCases())]});
 const fail=message=>{throw new Error(message);};
 const gate=(s,condition,reason='revert')=>{const rejected=clone(s);rejected.conditions.push('(not '+condition+')');rejected.storage=initial;if(rejected.yul_transient)rejected.yul_transient='transient_0';rejected.halt=reason;failure.push(rejected);s.conditions.push(condition);};
 const revert=s=>{const failed=clone(s);failed.storage=initial;if(failed.yul_transient)failed.yul_transient='transient_0';failed.halt='revert';failure.push(failed);return [];};
 const bounded=rows=>{if(rows.length>32)fail('STATE_PATH_BUDGET');return rows;};
 const expressionEffects=n=>{
  const reads=new Set(),writes=new Set(),seen=new Set();
  function scan(part,depth=0){walk(part,node=>{
   if(node.nodeType==='Identifier'){reads.add(node.referencedDeclaration);if(storageTypes.pointerType(nodes.get(node.referencedDeclaration)))reads.add('*');}
   const left=node.nodeType==='Assignment'?node.leftHandSide:node.nodeType==='UnaryOperation'&&['++','--','delete'].includes(node.operator)?node.subExpression:null;
   if(left){let root=left;while(['IndexAccess','MemberAccess'].includes(root.nodeType))root=root.baseExpression||root.expression;
    if(root.nodeType==='Identifier'){writes.add(root.referencedDeclaration);if(left!==root&&storageTypes.pointerType(nodes.get(root.referencedDeclaration)))writes.add('*');}
    else writes.add('*');
   }
   if(node.nodeType==='FunctionCall'){
    if(/^t_function_array(?:push|pop)_/.test(node.expression?.typeDescriptions?.typeIdentifier||''))writes.add('*');
    const d=target(node);if(!d||ctx.namespace_registry?.accessors.has(d.id))return;
    if(depth>=12){writes.add('*');return;}if(seen.has(d.id))return;seen.add(d.id);scan(d.body,depth+1);
    for(const invocation of d.modifiers||[]){const modifier=nodes.get(invocation.modifierName?.referencedDeclaration);if(modifier?.body)scan(modifier.body,depth+1);else writes.add('*');}
   }
   if(node.nodeType==='InlineAssembly')writes.add('*');
  });}
  scan(n);return {reads,writes};
 };
 const persistent=id=>{const d=nodes.get(id);return d?.stateVariable&&!d.constant&&d.mutability!=='immutable';};
 const overlaps=(writes,other)=>[...writes].some(k=>other.has(k)||k==='*'&&[...other].some(id=>id==='*'||persistent(id))||other.has('*')&&persistent(k));
 const independent=list=>{
  const all=list.map(expressionEffects);for(let i=0;i<all.length;i++)for(let j=i+1;j<all.length;j++){
   if(overlaps(all[i].writes,new Set([...all[j].reads,...all[j].writes]))||overlaps(all[j].writes,all[i].reads))fail('STATE_UNSPECIFIED_SIBLING_EVALUATION_ORDER');
  }
 };
 const typed=(node,term,s,{wrap=false}={})=>{
  const type=scalarType(node);if(type.sort==='Bool')return {term, ...type};
  if(!wrap&&!s.unchecked)gate(s,and(['(<= '+number(type.min)+' '+term+')','(<= '+term+' '+number(type.max)+')']),'panic_0x11');
  return terms.value(wrap||s.unchecked?terms.normalize(term,type):term,type);
 };
 const coerce=(v,type)=>{
  if(v.sort==='StorageReference'||type.sort==='StorageReference'){
   if(v.sort!==type.sort||!v.storage_location||(type.type_id?v.storage_location.type_id!==type.type_id:v.struct_ast_id!==type.struct_ast_id))fail('STATE_STORAGE_REFERENCE_TYPE_OR_ORIGIN_MISMATCH');return {...v,...type};
  }
  return v.type===type.type?v:coerceBytes(v,type,terms)||terms.value(type.sort==='Bool'?asBool(v):terms.normalize(fixedBytesTerm(asInt(v),v.type,type.type),type),type);
 };
 function choice(s,condition){
  const branch=clone(s);branch.conditions.push(condition);
  branch.symbolic_cases=(s.symbolic_cases||rootCases()).map(c=>{const case_id='effect_'+(++effectSerial);return{case_id,ancestors:[...c.ancestors,case_id],conditions:[...c.conditions,condition]};});
  return branch;
 }
 function chooseTerm(rows,value,sort='Int'){
  const values=rows.map(r=>String(value(r)));if(values.every(v=>v===values[0]))return values[0];
  let term=values.at(-1);for(let i=rows.length-2;i>=0;i--)term='(ite '+and(rows[i].state.conditions)+' '+values[i]+' '+term+')';return terms.define(term,sort);
 }
 function joinValue(rows,value){
  const values=rows.map(value),first=values[0];if(!first||values.some(v=>!v||v.sort!==first.sort))fail('STATE_JOIN_VALUE_TYPE_UNAVAILABLE');
  if(first.sort==='StorageReference'){
   const locations=values.map(v=>v.storage_location);if(locations.some(l=>!l||l.type_id!==locations[0].type_id))fail('STATE_STORAGE_REFERENCE_JOIN_TYPE_MISMATCH');
   const common=key=>locations.every(l=>JSON.stringify(l[key])===JSON.stringify(locations[0][key])),sameKeys=locations.every(l=>l.keys.length===locations[0].keys.length),
    offset=common('offset')?locations[0].offset:chooseTerm(rows,r=>value(r).storage_location.offset);
   return{...first,storage_location:{...locations[0],slot:chooseTerm(rows,r=>value(r).storage_location.slot),offset,root:common('root')&&sameKeys?locations[0].root:null,
    keys:sameKeys?locations[0].keys.map((_,i)=>chooseTerm(rows,r=>value(r).storage_location.keys[i])):[],fields:common('fields')?locations[0].fields:[],
    namespace_id:common('namespace_id')?locations[0].namespace_id:undefined,namespace_struct_ast_id:common('namespace_struct_ast_id')?locations[0].namespace_struct_ast_id:undefined,
    conditional_location_cases:rows.map(r=>({condition:and(r.state.conditions),location:value(r).storage_location}))}};
  }
  if(first.sort==='Bytes'){
   if(!values.every(v=>JSON.stringify(v)===JSON.stringify(first)))fail('STATE_CONDITIONAL_BYTE_VALUE_PENDING');return first;
  }
  if(values.some(v=>v.type!==first.type))fail('STATE_JOIN_SCALAR_TYPE_MISMATCH');
  return{...first,term:chooseTerm(rows,r=>value(r).term,first.sort)};
 }
 function joinStates(s,rows,{collapsePureCases=false}={}){
  if(!rows.length)fail('STATE_VALUE_CALL_ALWAYS_REVERTS');
  if(rows.some(r=>r.state.halt==='evm_return'))fail('STATE_VALUE_CALL_FRAME_TERMINATION_PENDING');
  if(rows.length===1){Object.assign(s,rows[0].state);return;}
  const first=rows[0].state;
  for(const key of['yul_transient','yul_state_memory','yul_msize','yul_known_words','yul_extent'])if(rows.some(r=>r.state[key]!==first[key]))fail('STATE_CONDITIONAL_YUL_STATE_PENDING');
  const sameMap=(a,b)=>a.size===b.size&&[...a].every(([k,v])=>b.get(k)===v),incomingCases=s.symbolic_cases||rootCases(),
   pure=collapsePureCases&&rows.every(r=>r.state.storage===s.storage&&sameMap(r.state.locals,s.locals)&&sameMap(r.state.immutables,s.immutables)&&
    (r.state.signature_recoveries||[]).length===(s.signature_recoveries||[]).length&&r.state.writes.length===s.writes.length),
   cases=pure?incomingCases:rows.flatMap(r=>r.state.symbolic_cases||rootCases());if(cases.length>32)fail('STATE_PATH_BUDGET');
  const writes=[...new Set(rows.flatMap(r=>r.state.writes))],rawCases=new Map();
  for(const r of rows)for(const w of r.state.writes)if(!w.execution_case_ids){const ids=rawCases.get(w)||new Set();for(const c of s.writes.includes(w)?incomingCases:r.state.symbolic_cases||rootCases())ids.add(c.case_id);rawCases.set(w,ids);}
  for(const[w,ids]of rawCases)w.execution_case_ids=[...ids];
  const locals=new Map();for(const key of s.locals.keys())locals.set(key,joinValue(rows,r=>r.state.locals.get(key)));
  const immutables=new Map();for(const key of new Set(rows.flatMap(r=>[...r.state.immutables.keys()]))){
   const type=scalarType(nodes.get(key));immutables.set(key,joinValue(rows,r=>r.state.immutables.get(key)||{...type,term:type.sort==='Bool'?'false':'0'}));
  }
  Object.assign(s,{storage:chooseTerm(rows,r=>r.state.storage,ARRAY),locals,immutables,conditions:[or(rows.map(r=>and(r.state.conditions)))],writes,symbolic_cases:cases,
   signature_recoveries:[...new Set(rows.flatMap(r=>r.state.signature_recoveries||[]))]});
 }
 const casesOf=s=>(s.symbolic_cases||rootCases()).map(c=>({case:c,condition:and([...s.conditions,...c.conditions]),
  writes:s.writes.filter(w=>!w.execution_case_ids||w.execution_case_ids.some(k=>c.ancestors.includes(k))),
  recoveries:(s.signature_recoveries||[]).filter(r=>!r.execution_case_ids||r.execution_case_ids.some(k=>c.ancestors.includes(k)))}));
 const readWord=(storage,slot)=>{
  const key=terms.define(slot),entry=terms.define('(select '+initial+' '+key+')');
  terms.constraints.add('(and (<= 0 '+entry+') (<= '+entry+' '+MAX_WORD+'))');
  terms.observe(key,'persistent storage slot',{kind:'storage_slot'});terms.observe(entry,'persistent storage word at '+key,{kind:'entry_storage_word',slot_symbol:key});
  return terms.define('(select '+storage+' '+key+')');
 };
 function readLocation(loc,s) {
  const type=scalarType({type:loc.scalar_type}),raw=readWord(s.storage,loc.slot),dynamic=typeof loc.offset==='string',
   low=dynamic?'(bv2int (bvand (bvlshr ((_ int2bv 256) '+raw+') ((_ int2bv 256) (* 8 '+loc.offset+'))) (_ bv'+((type.mod||256n)-1n)+' 256)))':
    '(mod (div '+raw+' '+(1n<<BigInt(loc.offset*8))+') '+(type.mod||256n)+')';
  return terms.value(type.bits===256&&!type.signed&&loc.offset===0?raw:type.sort==='Bool'?'(not (= '+low+' 0))':type.signed?'(ite (>= '+low+' '+(type.mod>>1n)+') (- '+low+' '+type.mod+') '+low+')':low,type);
 }
 function storeLocation(loc,value,s,node) {
  const type=scalarType({type:loc.scalar_type});value=coerce(value,type);
  const bits=type.bits||8,dynamic=typeof loc.offset==='string',base=dynamic?null:1n<<BigInt(loc.offset*8),above=dynamic?null:base*(1n<<BigInt(bits)),old=readLocation(loc,s),word=readWord(s.storage,loc.slot);
  const encoded=type.sort==='Bool'?'(ite '+asBool(value)+' 1 0)':!type.signed&&!value.signed&&value.bits<=type.bits?asInt(value):terms.normalize(asInt(value),{...type,signed:false});
  const shift='((_ int2bv 256) (* 8 '+loc.offset+'))',mask='(bvshl (_ bv'+((1n<<BigInt(bits))-1n)+' 256) '+shift+')',
   updated=dynamic?'(bv2int (bvor (bvand ((_ int2bv 256) '+word+') (bvnot '+mask+')) (bvshl ((_ int2bv 256) '+encoded+') '+shift+')))':
    '(+ (mod '+word+' '+base+') (* '+encoded+' '+base+') (* (div '+word+' '+above+') '+above+'))';
  s.storage=terms.define('(store '+s.storage+' '+loc.slot+' '+(bits===256&&loc.offset===0?encoded:updated)+')',ARRAY);
  s.writes.push({root_ast_id:loc.root,variable:nodes.get(loc.root)?.name,slot:loc.slot,offset:loc.offset,bits,keys:loc.keys,
   execution_case_ids:(s.symbolic_cases||rootCases()).map(c=>c.case_id),
   old_value:asInt(old),new_value:asInt(value),fields:loc.fields||[],source_location:location(node),physical_storage_after:s.storage,
   ...(loc.namespace_id?{namespace_id:loc.namespace_id,namespace_struct_ast_id:loc.namespace_struct_ast_id}:{})});
  if(s.writes.length>64)fail('STATE_WRITE_BUDGET');
 }
 function clearLocation(loc,s,node,depth=0){
  if(depth>16)fail('STATE_DELETE_DEPTH_BUDGET');
  const type=layout.types[loc.type_id];
  if(type.encoding==='mapping')return; // Solidity preserves mapping contents.
  if(type.members){
   for(const field of type.members){
    const child=layout.types[field.type],slot=field.slot==='0'?loc.slot:terms.define(mod('(+ '+loc.slot+' '+field.slot+')'));
    clearLocation({...loc,slot,offset:field.offset||0,type_id:field.type,scalar_type:child.label,root:loc.namespace_root?field.astId:loc.root,
      fields:loc.namespace_root?[]:[...(loc.fields||[]),field.astId],namespace_root:false},s,node,depth+1);
   }return;
  }
  if(type.base){
   const info=storageArrayInfo(type,layout.types);if(info.dynamic)fail('STATE_DELETE_DYNAMIC_ARRAY_REQUIRES_BOUNDED_LENGTH');
   const length=BigInt(info.length);if(length>64n)fail('STATE_DELETE_ARRAY_WRITE_BUDGET');
   for(let index=0n;index<length;index++){
    const per=BigInt(info.elements_per_slot),delta=info.packed?index/per:index*BigInt(info.stride_slots),child=layout.types[info.element_type];
    clearLocation({...loc,slot:delta?terms.define(mod('(+ '+loc.slot+' '+delta+')')):loc.slot,offset:info.packed?Number(index%per)*Number(info.element_bytes):0,
      type_id:info.element_type,scalar_type:child.label,keys:[...loc.keys,String(index)]},s,node,depth+1);
   }return;
  }
  if(type.encoding!=='inplace')fail('STATE_DELETE_BYTES_LAYOUT_PENDING');
  const scalar=scalarType({type:type.label});storeLocation(loc,{...scalar,term:scalar.sort==='Bool'?'false':'0'},s,node);
 }
 function locate(node,s,{conservative=false}={}) {
  if(node.nodeType==='FunctionCall'){
   const value=expr(node,s);if(value.sort!=='StorageReference'||!value.storage_location)fail('STATE_STORAGE_REFERENCE_CALL_UNAVAILABLE');return value.storage_location;
  }
  if(node.nodeType==='Identifier') {
   const value=s.locals.get(node.referencedDeclaration);if(value?.sort==='StorageReference'){
    if(!value.storage_location)fail('STATE_STORAGE_REFERENCE_UNBOUND');return value.storage_location;
   }
   const d=nodes.get(node.referencedDeclaration),item=layoutByID.get(d?.id),type=layout.types?.[item?.type];
   if(!item||!type)fail('STATE_STORAGE_LAYOUT_UNAVAILABLE');
   return {root:d.id,slot:String(item.slot),offset:item.offset||0,type_id:item.type,scalar_type:type.label,keys:[],...(conservative?{hash_semantics:'congruence_without_collision_axioms'}:{})};
  }
  const parent=locate(node.baseExpression||node.expression,s,{conservative}),type=layout.types?.[parent.type_id];
  if(node.nodeType==='IndexAccess'&&type.encoding==='mapping') {
   const key=coerce(expr(node.indexExpression,s),scalarType({type:layout.types[type.key].label})),child=layout.types[type.value];
   const encoded=fixedBytesTerm(key.signed?mod(asInt(key)):asInt(key),key.type,'bytes32');
   const hash=parent.hash_semantics==='congruence_without_collision_axioms'?terms.digestWords([encoded,parent.slot]):terms.hash([encoded,parent.slot],staticSlots);
   return {...parent,slot:hash,offset:0,type_id:type.value,scalar_type:child.label,keys:[...parent.keys,asInt(key)]};
  }
  if(node.nodeType==='IndexAccess'&&type?.base){
   const info=storageArrayInfo(type,layout.types),index=coerce(expr(node.indexExpression,s),uint256),value=asInt(index),length=info.dynamic?readWord(s.storage,parent.slot):info.length;
   gate(s,'(< '+value+' '+length+')',Number(String(ctx.compilerVersion||'0.8').split('.')[1])<8?'legacy_invalid_opcode_array_bounds':'panic_0x32');
   const base=info.dynamic?terms.digestWords([parent.slot]):parent.slot,delta=info.packed?'(div '+value+' '+info.elements_per_slot+')':'(* '+value+' '+info.stride_slots+')',
    slot=terms.define(mod('(+ '+base+' '+delta+')')),offset=info.packed&&info.elements_per_slot!=='1'?terms.define('(* (mod '+value+' '+info.elements_per_slot+') '+info.element_bytes+')'):0,child=layout.types[info.element_type];
   return{...parent,slot,offset,type_id:info.element_type,scalar_type:child.label,keys:[...parent.keys,value],
    hash_semantics:'congruence_without_collision_axioms',array_indices:[...(parent.array_indices||[]),{index:value,length,packed:info.packed,elements_per_slot:info.elements_per_slot,stride_slots:info.stride_slots}]};
  }
  if(node.nodeType==='MemberAccess'&&type.members) {
   const field=type.members.find(f=>f.astId===node.referencedDeclaration||f.label===node.memberName),child=layout.types?.[field?.type];
   if(!field||!child)fail('STATE_STRUCT_FIELD_UNAVAILABLE');
   const slot=field.slot==='0'?parent.slot:terms.define(mod('(+ '+parent.slot+' '+field.slot+')'));
   if(field.slot!=='0'&&parent.hash_semantics!=='congruence_without_collision_axioms')terms.reserveDerivedField(slot);
   return {...parent,root:parent.namespace_root?field.astId:parent.root,slot,offset:field.offset||0,type_id:field.type,scalar_type:child.label,
    fields:parent.namespace_root?[]:[...(parent.fields||[]),field.astId],namespace_root:false};
  }
  fail('STATE_STORAGE_PATH_UNSUPPORTED');
 }
 function locationValue(loc,s){
  const type=layout.types[loc.type_id],reference=ctx.namespace_registry?.byStruct.get(type?.struct_ast_id);
  return reference?{...reference,storage_location:loc}:aggregateStorageType(type)?{sort:'StorageReference',type:'storage:'+loc.type_id,type_id:loc.type_id,struct_ast_id:type.struct_ast_id??null,storage_location:loc}:readLocation(loc,s);
 }
 function target(node) {
  const callee=node.expression;let d=nodes.get(callee?.referencedDeclaration);
  if(d?.nodeType!=='FunctionDefinition')return null;
  if(!internalDispatch(d,callee,nodes))return null;
  if(callee.nodeType==='Identifier')for(let i=0;i<12;i++){
   const override=functions.find(f=>f.baseFunctions?.includes(d.id)&&f.body);if(!override)break;d=override;
  }return d;
 }
 function invoke(node,s) {
  const d=target(node);if(!d)fail('STATE_EXTERNAL_CALL_REQUIRES_MODEL');
  const parent=nodes.get(d.scope),callee=node.expression,receiver=parent?.contractKind==='library'&&callee?.nodeType==='MemberAccess'&&callee.expression?.referencedDeclaration!==parent.id,
   arguments_=receiver?[callee.expression,...node.arguments]:node.arguments;
  independent(arguments_);const args=arguments_.map(a=>expr(a,s));return call(d,args,s);
 }
 function expr(n,s) {
  if(!n||++visits>6000)fail('STATE_EXPRESSION_BUDGET');
  if(n.nodeType==='Literal') {
   if(n.kind==='bool')return {term:n.value, ...bool};
   if(n.kind==='number'){const value=numericLiteral(n.value,n.subdenomination).value;if(value===null)fail('STATE_LITERAL_UNSUPPORTED');return {term:number(value),...uint256};}
   if(['string','hexString','unicodeString'].includes(n.kind))return literalBytes(n);
   fail('STATE_NONSCALAR_LITERAL');
  }
  if(n.nodeType==='Identifier') {
   const d=nodes.get(n.referencedDeclaration);if(s.locals.has(n.referencedDeclaration)){
    const value=s.locals.get(n.referencedDeclaration);if(value.sort==='StorageReference'&&!value.storage_location)fail('STATE_STORAGE_REFERENCE_UNBOUND');return value;
   }
   if(builtin(n,'this',nodes))return selfAddress;
   if(d?.constant&&d.value)return expr(d.value,s);
   if(d?.mutability==='immutable'){
    if(ctx.zeroInitialStorage){const type=scalarType(d);return s.immutables.get(d.id)||{term:type.sort==='Bool'?'false':'0',...type};}
    const value=ctx.runtimeValue?.(d);if(value===null||value===undefined)fail('STATE_IMMUTABLE_RUNTIME_IDENTITY_UNAVAILABLE');
    return {...scalarType(d),term:value==='true'||value==='false'?value:BigInt(value).toString()};
   }
   if(d?.stateVariable)return locationValue(locate(n,s,{conservative:aggregateStorageType(storageTypes.forNode(d))}),s);
   fail('STATE_IDENTIFIER_UNBOUND:'+n.name);
  }
  if(n.nodeType==='MemberAccess') {
   const name=builtinMember(n,nodes);if(env.has(name))return env.get(name);
   if(name==='block.chainid'&&/^\d+$/.test(String(ctx.chainID||'')))return {...uint256,term:String(ctx.chainID)};
   if(['min','max'].includes(n.memberName)&&n.expression?.nodeType==='FunctionCall'&&n.expression.expression?.name==='type'){
    const t=scalarType(n);return {term:number(n.memberName==='max'?t.max:t.min),...t};
   }if(n.memberName==='length'){
    const loc=locate(n.expression,s),type=layout.types[loc.type_id],info=type?.base?storageArrayInfo(type,layout.types):null;
    if(info)return terms.value(info.dynamic?readWord(s.storage,loc.slot):info.length,uint256);
   }return locationValue(locate(n,s,{conservative:aggregateStorageType(storageTypes.forNode(n))}),s);
  }
  if(n.nodeType==='IndexAccess')return locationValue(locate(n,s,{conservative:aggregateStorageType(storageTypes.forNode(n))}),s);
  if(n.nodeType==='TupleExpression'&&n.components?.length===1)return expr(n.components[0],s);
  if(n.nodeType==='Conditional') {
   const c=expr(n.condition,s),yes=choice(s,asBool(c)),no=choice(s,'(not '+asBool(c)+')');
   const a=expr(n.trueExpression,yes),b=expr(n.falseExpression,no),reference=a.sort==='StorageReference'&&b.sort==='StorageReference',type=reference?null:scalarLocalType(n),
    rows=[{state:yes,values:[reference?a:coerce(a,type)]},{state:no,values:[reference?b:coerce(b,type)]}],value=joinValue(rows,r=>r.values[0]);
   joinStates(s,rows,{collapsePureCases:!reference});return value;
  }
  if(n.nodeType==='UnaryOperation') {
   if(n.operator==='delete'){clearLocation(locate(n.subExpression,s,{conservative:true}),s,n);return{...uint256,term:'0'};}
   const a=expr(n.subExpression,s);
   if(n.operator==='!')return terms.value('(not '+asBool(a)+')',bool);
   if(n.operator==='-')return typed(n,'(- '+a.term+')',s);
   if(n.operator==='~')return typed(n,'(bv2int (bvnot ((_ int2bv 256) '+asInt(a)+')))',s,{wrap:true});
   if(['++','--'].includes(n.operator)) {
    const value=typed(n,'('+ (n.operator==='++'?'+':'-')+' '+asInt(a)+' 1)',s);assign(n.subExpression,value,s,n);return n.prefix?value:a;
   }fail('STATE_UNARY_UNSUPPORTED');
  }
  if(n.nodeType==='BinaryOperation') {
   let a=expr(n.leftExpression,s);const op=n.operator;
   if(['&&','||'].includes(op)){
    const condition=op==='&&'?asBool(a):'(not '+asBool(a)+')',branch=choice(s,condition),skipped=choice(s,'(not '+condition+')'),b=expr(n.rightExpression,branch);
    joinStates(s,[{state:branch},{state:skipped}],{collapsePureCases:true});
    return terms.value('('+ (op==='&&'?'and':'or')+' '+asBool(a)+' '+asBool(b)+')',bool);
   }
   independent([n.leftExpression,n.rightExpression]);let b=expr(n.rightExpression,s);
   if(/^bytes\d+$/.test(n.commonType?.typeString||'')){const common=scalarType({type:n.commonType.typeString});a=coerce(a,common);b=coerce(b,common);}
   if(['==','!=','<','<=','>','>='].includes(op))return terms.value(op==='!='?'(not (= '+a.term+' '+b.term+'))':'('+(op==='=='?'=':op)+' '+a.term+' '+b.term+')',bool);
   if(['+','-','*'].includes(op))return typed(n,'('+op+' '+asInt(a)+' '+asInt(b)+')',s);
   if(['/','%'].includes(op)) {
    gate(s,'(not (= '+asInt(b)+' 0))','panic_0x12');
    const q='(ite (< (* '+a.term+' '+b.term+') 0) (- (div (abs '+a.term+') (abs '+b.term+'))) (div (abs '+a.term+') (abs '+b.term+')))';
    return typed(n,op==='/'?q:'(- '+a.term+' (* '+q+' '+b.term+'))',s);
   }
   const bv={'&':'bvand','|':'bvor','^':'bvxor','<<':'bvshl','>>':scalarType(n).signed?'bvashr':'bvlshr'}[op];
   if(bv)return typed(n,'(bv2int ('+bv+' ((_ int2bv 256) '+asInt(a)+') ((_ int2bv 256) '+asInt(b)+')))',s,{wrap:true});
   fail('STATE_BINARY_UNSUPPORTED:'+op);
  }
  if(n.nodeType==='Assignment') {
   if(n.leftHandSide.nodeType==='TupleExpression'&&n.operator==='='){
    const targets=n.leftHandSide.components||[],bound=targets.filter(Boolean);
    if(bound.some(t=>t.nodeType!=='Identifier'||nodes.get(t.referencedDeclaration)?.stateVariable))fail('STATE_TUPLE_STORAGE_ALIAS_ORDER_UNVERIFIED');
    const ids=bound.map(t=>t.referencedDeclaration);if(new Set(ids).size!==ids.length)fail('STATE_TUPLE_ASSIGNMENT_ALIAS_ORDER_UNVERIFIED');
    const values=tupleValues(n.rightHandSide,s);if(values.length!==targets.length)fail('STATE_TUPLE_ASSIGNMENT_ARITY');
    targets.forEach((target,i)=>{if(target)assign(target,values[i],s,n);});return{sort:'Tuple',values};
   }
   if(n.operator!=='='||n.leftHandSide.nodeType!=='Identifier')independent([n.leftHandSide,n.rightHandSide]);
   const right=expr(n.rightHandSide,s);let value=right;
   if(n.operator!=='=') {const left=expr(n.leftHandSide,s);if(!['+=','-=','*='].includes(n.operator))fail('STATE_ASSIGNMENT_UNSUPPORTED');value=typed(n.leftHandSide,'('+n.operator[0]+' '+asInt(left)+' '+asInt(right)+')',s);}
   assign(n.leftHandSide,value,s,n);return value;
  }
  if(n.nodeType==='FunctionCall') {
   const callee=n.expression;
   if(/^t_function_array(?:push|pop)_/.test(callee?.typeDescriptions?.typeIdentifier||''))fail('STATE_ARRAY_MUTATION_REQUIRES_LENGTH_AND_ELEMENT_VERSION');
   const accessor=ctx.namespace_registry?.accessors.get(target(n)?.id);
   if(accessor){if(n.arguments.length)fail('STATE_NAMESPACE_ACCESSOR_ARGUMENTS');return namespaceRootReference(accessor);}
   if(builtin(callee?.expression,'abi',nodes)&&['encode','encodePacked'].includes(callee?.memberName)){
    independent(n.arguments);return encodeABI(n.arguments.map(a=>expr(a,s)),{packed:callee.memberName==='encodePacked'});
   }
   if(builtin(callee,'keccak256',nodes)&&n.arguments?.length===1)return hashBytes(expr(n.arguments[0],s),terms);
   if(builtin(callee,'ecrecover',nodes)){
    independent(n.arguments);const value=recoverSigner(n.arguments.map(a=>expr(a,s)),terms),record={...value.recovery,source_ast_id:n.id,source_location:location(n),recovered_address:value.term,condition:and(s.conditions),execution_case_ids:(s.symbolic_cases||rootCases()).map(c=>c.case_id)};
    s.signature_recoveries||=[];s.signature_recoveries.push(record);return value;
   }
   if(n.kind==='typeConversion'){
    const v=expr(n.arguments[0],s),type=localType(n),bytes=coerceBytes(v,type,terms);if(bytes)return bytes;
    return coerce(v,type);
   }
   const rows=invoke(n,s);if(!rows.length)fail('STATE_VALUE_CALL_ALWAYS_REVERTS');
   if(rows.some(r=>r.values.length!==1))fail('STATE_VALUE_CALL_TUPLE_REQUIRES_DESTRUCTURING');
   const value=joinValue(rows,r=>r.values[0]);joinStates(s,rows);return value;
  }
  fail('STATE_EXPRESSION_UNSUPPORTED:'+n.nodeType);
 }
 function tupleValues(n,s){
  if(n?.nodeType==='TupleExpression'){
   independent(n.components.filter(Boolean));if(n.components.some(c=>!c))fail('STATE_TUPLE_VALUE_COMPONENT_UNAVAILABLE');return n.components.map(c=>expr(c,s));
  }
  if(n?.nodeType==='FunctionCall'&&n.kind!=='typeConversion'&&target(n)&&(target(n).returnParameters?.parameters||[]).length!==1){
   const rows=invoke(n,s);if(!rows.length)fail('STATE_VALUE_CALL_ALWAYS_REVERTS');
   const count=rows[0].values.length;if(rows.some(r=>r.values.length!==count))fail('STATE_TUPLE_RETURN_ARITY');
   const values=Array.from({length:count},(_,i)=>joinValue(rows,r=>r.values[i]));joinStates(s,rows);return values;
  }
  return[expr(n,s)];
 }
 function assign(n,v,s,node) {
  const d=nodes.get(n.referencedDeclaration);
  if(n.nodeType==='Identifier'&&d&&!d.stateVariable){s.locals.set(d.id,coerce(v,localType(d)));return;}
  if(n.nodeType==='Identifier'&&d?.mutability==='immutable'){
   if(!ctx.zeroInitialStorage)fail('STATE_RUNTIME_IMMUTABLE_WRITE');
   const type=scalarType(d);v=coerce(v,type);const term=type.sort==='Bool'?v.term:terms.define(terms.normalize(asInt(v),type));s.immutables.set(d.id,{...type,term});
   effects.push({kind:'immutable_assignment',state_ast_id:d.id,variable:d.name,value:term,condition:and(s.conditions),source_location:location(node)});return;
  }
  storeLocation(locate(n,s),v,s,node);
 }
 function loopStatement(n,s,placeholder){
  const proof=ctx.loopSummaries?.get(n.id);if(!proof?.complete)fail('STATE_LOOP_INVARIANT_NOT_PROVED');
  const bindings=new Map([...s.locals].filter(([,v])=>/^-?\d+$/.test(v.term)).map(([key,v])=>[key,BigInt(v.term)]));
  const descriptor=detectLoopRecurrences(n,nodes,{unchecked:s.unchecked,bindings});
  if(descriptor.recurrences.some(r=>!r.total_increment&&!r.polynomial_summary))fail('STATE_LOOP_SUMMARY_UNSUPPORTED_INCREMENT');
  const rows=n.initializationExpression?statement(n.initializationExpression,s,placeholder):[s];
  return rows.map(state=>{
   const bound=expr(descriptor.bound,state),count=asInt(bound),counter=nodes.get(descriptor.counter);
   if(descriptor.coupled){
    const entry=clone(state),at=(r,index)=>coupledPolynomialSMT(r.coupled_state_polynomial,index,key=>{
     const leaf=r.coupled_state_polynomial.leaves.find(l=>l.key===key),value=leaf.entry_declaration?entry.locals.get(leaf.entry_declaration):expr(leaf.node,entry);
     if(!value)fail('STATE_COUPLED_LOOP_ENTRY_UNBOUND');return asInt(value);
    });
    const last=clone(entry);last.conditions.push('(> '+count+' 0)');last.locals.set(counter.id,terms.value('(- '+count+' 1)',scalarType(counter)));
    for(const r of descriptor.recurrences)last.locals.set(r.declaration,typed(nodes.get(r.declaration),at(r,'(- '+count+' 1)'),last));
    let final=statement(n.body,last,placeholder);if(n.loopExpression)final=final.flatMap(row=>statement(n.loopExpression,row,placeholder));
    if(final.length!==1||final[0].storage!==state.storage||final[0].halt)fail('STATE_COUPLED_LOOP_LAST_ITERATION_EFFECT');
    state.conditions.push('(or (= '+count+' 0) '+and(final[0].conditions)+')');
    for(const r of descriptor.recurrences)state.locals.set(r.declaration,typed(nodes.get(r.declaration),at(r,count),state));
    state.locals.set(counter.id,terms.value(count,scalarType(counter)));
    effects.push({kind:'inductive_coupled_loop',ast_id:n.id,loop_model_id:proof.model_id,proof_obligation_ids:proof.obligations,scope:'all_successful_loop_iterations',source_update_order:descriptor.recurrences.map(r=>r.declaration)});
    return state;
   }
   for(const r of descriptor.recurrences){
    const d=nodes.get(r.declaration),before=state.locals.get(d.id);if(!before)fail('STATE_LOOP_ACCUMULATOR_UNINITIALIZED');
    let delta;
    if(r.polynomial_summary){
     const factors=new Map(r.polynomial.leaves.map(l=>[l.key,asInt(expr(l.node,state))]));
     delta='(div '+polynomialSMT(r.polynomial,count,key=>factors.get(key),{sum:true})+' '+r.polynomial.scale+')';
     if(!r.unchecked){
      const last=clone(state);last.unchecked=false;last.conditions.push('(> '+count+' 0)');
      last.locals.set(counter.id,terms.value('(- '+count+' 1)',scalarType(counter)));expr(r.increment,last);
      if(last.storage!==state.storage)fail('STATE_LOOP_INCREMENT_HAS_STORAGE_EFFECT');
      state.conditions.push('(or (= '+count+' 0) '+and(last.conditions)+')');
     }
    }else delta='(* '+count+' '+asInt(expr(r.increment,state))+')';
    const arithmetic=clone(state);arithmetic.unchecked=r.unchecked;
    const updated=typed(d,'('+r.operator+' '+asInt(before)+' '+delta+')',arithmetic);
    state.conditions=arithmetic.conditions;state.locals.set(d.id,updated);
   }
   state.locals.set(counter.id,terms.value(count,scalarType(counter)));
   effects.push({kind:'inductive_loop',ast_id:n.id,loop_model_id:proof.model_id,proof_obligation_ids:proof.obligations,scope:'all_successful_loop_iterations'});
   return state;
  });
 }
 function statement(n,s,placeholder) {
  if(++visits>6000)fail('STATE_STATEMENT_BUDGET');if(s.halt)return [s];
  if(['Block','UncheckedBlock'].includes(n.nodeType)) {
   const prior=s.unchecked;s.unchecked=prior||n.nodeType==='UncheckedBlock';let rows=[s];
   for(const child of n.statements||[])rows=bounded(rows.flatMap(p=>statement(child,p,placeholder)));
   return rows.map(p=>({...p,unchecked:prior}));
  }
  if(n.nodeType==='PlaceholderStatement'){if(!placeholder)fail('STATE_MODIFIER_PLACEHOLDER_UNBOUND');return placeholder(s).map(p=>p.halt==='evm_return'?p:{...p,halt:null});}
  if(n.nodeType==='IfStatement') {
   const c=expr(n.condition,s),yes=choice(s,asBool(c)),no=choice(s,'(not '+asBool(c)+')');
   return bounded([...statement(n.trueBody,yes,placeholder),...(n.falseBody?statement(n.falseBody,no,placeholder):[no])]);
  }
  if(['ForStatement','WhileStatement'].includes(n.nodeType))return loopStatement(n,s,placeholder);
  if(n.nodeType==='VariableDeclarationStatement') {
   const values=n.initialValue?tupleValues(n.initialValue,s):null;if(values&&values.length!==n.declarations.length)fail('STATE_LOCAL_TUPLE_ARITY');
   for(const[i,d]of n.declarations.entries())if(d){const type=localType(d);if(type.sort==='StorageReference'&&!values)fail('STATE_STORAGE_REFERENCE_UNBOUND');
    const value=values?coerce(values[i],type):type.sort==='Bytes'?{...byteSequence([]),...type}:{term:type.sort==='Bool'?'false':'0',...type};s.locals.set(d.id,value);
   }return[s];
  }
  if(n.nodeType==='Return'){s.returns=n.expression?tupleValues(n.expression,s):[];s.halt='return';return [s];}
  if(n.nodeType==='RevertStatement')return revert(s);
  if(n.nodeType==='EmitStatement'){independent(n.eventCall.arguments||[]);for(const a of n.eventCall.arguments||[])expr(a,s);return [s];}
  if(n.nodeType==='InlineAssembly'){
   if(!ctx.assembly)fail('STATE_ASSEMBLY_MODEL_PENDING');return ctx.assembly(n,s,{terms,env,expr,locate,readLocation,storeLocation,readWord,gate,clone,fail,effects,initial,nodes,location,sourceText,staticSlots,layout,method,entryFn:fn,evmRules:ctx.evmRules,
    recordGap:reason=>{if(!unresolved.includes(reason))unresolved.push(reason);}});
  }
  if(n.nodeType==='ExpressionStatement') {
   const e=n.expression;
   if(e?.nodeType==='FunctionCall'&&builtin(e.expression,'revert',nodes)){
    if(e.arguments.some(a=>a.nodeType!=='Literal'))fail('STATE_REVERT_MESSAGE_EFFECT_UNSUPPORTED');
    return revert(s);
   }
   if(e?.nodeType==='FunctionCall'&&['require','assert'].some(name=>builtin(e.expression,name,nodes))) {
    if(e.arguments.slice(1).some(a=>a.nodeType!=='Literal'))fail('STATE_GUARD_MESSAGE_EFFECT_UNSUPPORTED');
    gate(s,asBool(expr(e.arguments[0],s)),e.expression.name==='assert'?'panic_0x01':'revert');return [s];
   }
   if(e?.nodeType==='FunctionCall'&&e.kind!=='typeConversion'&&target(e))return invoke(e,s).map(r=>r.state);
   expr(e,s);return [s];
  }
  fail('STATE_CONTROL_UNSUPPORTED:'+n.nodeType);
 }
 function call(d,args,s) {
  if((s.stack||[]).length>=12||(s.stack||[]).includes(d.id))fail('STATE_RECURSION_OR_DEPTH');
  const saved=s.locals,stack=s.stack,callerUnchecked=s.unchecked,entry=clone(s);entry.stack=[...(stack||[]),d.id];entry.unchecked=ctx.legacyArithmetic||false;
  for(const [i,p]of(d.parameters?.parameters||[]).entries()){if(!args[i])fail('STATE_CALL_ARGUMENT_MISSING');entry.locals.set(p.id,coerce(args[i],localType(p)));}
  for(const r of d.returnParameters?.parameters||[])if(r.name){const type=localType(r);entry.locals.set(r.id,type.sort==='StorageReference'?{...type,storage_location:null}:type.sort==='Bytes'?{...byteSequence([]),...type}:{term:type.sort==='Bool'?'false':'0',...type});}
  const modifiers=(d.modifiers||[]).filter(m=>!(ctx.zeroInitialStorage&&nodes.get(m.modifierName?.referencedDeclaration)?.nodeType==='ContractDefinition'));
  function execute(index,state) {
   if(index===modifiers.length)return statement(d.body,state);
   const invocation=modifiers[index],modifier=nodes.get(invocation.modifierName?.referencedDeclaration);
   if(modifier?.nodeType!=='ModifierDefinition'||!modifier.body)fail('STATE_MODIFIER_UNAVAILABLE');
   let placeholders=0;walk(modifier.body,n=>{if(n.nodeType==='PlaceholderStatement')placeholders++;});
   if(placeholders!==1)fail('STATE_MODIFIER_PLACEHOLDER_COUNT_UNSUPPORTED');
   for(const [i,p]of(modifier.parameters?.parameters||[]).entries())state.locals.set(p.id,expr(invocation.arguments[i],state));
   return statement(modifier.body,state,p=>execute(index+1,p));
  }
  return execute(0,entry).map(state=>{
   let values=state.halt==='evm_return'?[]:state.returns.length?state.returns:(d.returnParameters?.parameters||[]).map(r=>state.locals.get(r.id));
   if(values.some(v=>!v))fail('STATE_RETURN_VALUE_UNAVAILABLE');
   values=values.map((v,i)=>coerce(v,localType(d.returnParameters.parameters[i])));
   if(state.halt!=='evm_return')state.halt=null;state.returns=[];state.locals=new Map(saved);state.stack=stack;state.unchecked=callerUnchecked;return {state,values};
  });
 }
 function initialize(declarations,state){
  for(const d of declarations)if(d.value&&!d.constant){assign({...d,nodeType:'Identifier',referencedDeclaration:d.id},expr(d.value,state),state,d);}
 }
 function creation(args,state){
  const plan=ctx.creationPlan;
  if(plan.error)fail(plan.error);
  for(const [i,p]of(fn.parameters?.parameters||[]).entries())state.locals.set(p.id,args[i]);
  if(!ctx.viaIR)initialize(plan.initializers,state);
  // Base arguments are read-only here. Their parameter dependencies follow
  // most-derived to base order; constructor bodies run in the reverse order.
  for(const item of plan.contracts){
   for(const invocation of item.invocations){
    independent(invocation.arguments);
    if(invocation.arguments.some(n=>expressionEffects(n).writes.size))fail('CREATION_BASE_ARGUMENT_EFFECT_ORDER_UNSUPPORTED');
    for(const [i,p]of invocation.parameters.entries()){
     const value=expr(invocation.arguments[i],state),type=scalarType(p);
     state.locals.set(p.id,coerce(value,type));
    }
   }
  }
  let rows=[{state,values:[]}];
  for(const item of [...plan.contracts].reverse())rows=bounded(rows.flatMap(row=>{
   if(row.state.halt==='evm_return')return [row];
   if(ctx.viaIR)initialize(item.initializers,row.state);
   if(!item.constructor)return [row];
   const values=(item.constructor.parameters?.parameters||[]).map(p=>row.state.locals.get(p.id)||fail('CREATION_BASE_ARGUMENT_UNBOUND'));
   return call(item.constructor,values,row.state);
  }));
  return rows;
 }
 const model={model_id:id(method.method_id,'state-transition'),method_id:method.method_id,code_hash:method.code_hash,code_address:method.code_address,
  storage_address:method.storage_address,scope:'successful_and_reverted_source_state_transitions',
  assumptions:['typed_entry_arguments_and_environment','keccak_storage_paths_collision_free_in_declared_layout','gas_feasibility_not_proved',ctx.zeroInitialStorage?'creation_storage_starts_zero':'entry_storage_is_symbolic'],
  evidence_ids:method.evidence_ids,source_location:location(fn),unresolved,complete:false,paths:[],effects};
 if(ctx.environmentBasis==='declared'){
  if(ctx.chainID)model.assumptions.push('declared_chain_id:'+ctx.chainID);
  if(ctx.executionAddress)model.assumptions.push('declared_execution_address:'+ctx.executionAddress);
 }
 if(layout.namespace_storage)model.namespace_storage={scope:layout.namespace_storage.scope,complete:layout.namespace_storage.complete,
  namespaces:layout.namespace_storage.namespaces.map(n=>({namespace_id:n.namespace_id,struct_ast_id:n.struct_ast_id,root_slot:n.root_slot,
   layout_complete:n.layout_complete,pointer_assignment_verified:n.pointer_assignment_verified,evidence_ids:n.evidence_ids})),
  evidence_ids:unique(layout.namespace_storage.accessors.flatMap(a=>a.evidence_ids||[])),historical_initialization_verified:false};
 try {
  const args=(fn.parameters?.parameters||[]).map(p=>{const type=localType(p);if(type.sort==='Bytes')fail('STATE_UNBOUNDED_BYTES_ENTRY_INPUT');if(type.sort==='StorageReference')fail('STATE_STORAGE_REFERENCE_ENTRY_REQUIRES_CALLER_CONTEXT');return terms.variable('arg_'+p.id,type,p.name);});
  const state={storage:initial,locals:new Map(),immutables:new Map(),conditions:[],writes:[],returns:[],stack:[],unchecked:ctx.legacyArithmetic||false,halt:null,symbolic_cases:rootCases()};
  if(method.state_mutability!=='payable')gate(state,'(= '+env.get('msg.value').term+' 0)');
  if(!ctx.creationPlan)initialize(ctx.initializers||[],state);
  const rows=ctx.creationPlan?creation(args,state):call(fn,args,state);
  if(rows.some(r=>r.values.some(v=>v.sort==='Bytes')))fail('STATE_DYNAMIC_ENTRY_RETURN_ENCODING_PENDING');
  const expanded=rows.flatMap((r,index)=>casesOf(r.state).map(c=>({r,index,...c})));if(expanded.length>32)fail('STATE_PATH_BUDGET');
  model.paths=expanded.map(({r,index,case:branch,condition,writes,recoveries})=>({path_id:id(model.model_id,index,branch.case_id),condition,storage_before:initial,storage_after:r.state.storage,transient_after:r.state.yul_transient,halt:r.state.halt||'return',writes,return_values:r.values.map(v=>v.term),signature_recoveries:recoveries,immutable_values:[...r.state.immutables].map(([state_ast_id,v])=>({state_ast_id,type:v.type,value:v.term})),raw_return:r.state.yul_raw_return}));
  if(rows.some(r=>r.state.signature_recoveries?.length))model.assumptions.push('symbolic_ecrecover_is_a_conservative_function_without_unforgeability_or_hash_collision_axioms');
  model.complete=unresolved.length===0;model.status=model.complete?'represented_source_transition':'incomplete';
 }catch(error){unresolved.push(String(error.message));model.status='incomplete';}
 const reverted=failure.flatMap((s,index)=>casesOf(s).map(c=>({path_id:id(model.model_id,'revert',index,c.case.case_id),condition:c.condition,storage_after:initial,transient_after:s.yul_transient,attempted_writes:c.writes,halt:s.halt})));
 if(reverted.length>256){unresolved.push('STATE_REVERT_PATH_BUDGET');model.complete=false;model.status='incomplete';}model.reverted_paths=reverted.slice(0,256);
 model.successful_path_count=model.paths.length;model.reverted_path_count=model.reverted_paths.length;
 model.input_variables=terms.inputs;
 const semantics_id=id(model.model_id,'state-transition-semantics'),semantics=JSON.stringify({schema_version:'source-state-transition-v1',model_id:model.model_id,method_id:method.method_id,
  code_hash:method.code_hash,assumptions:model.assumptions,complete:model.complete,unresolved:model.unresolved,initial_storage:initial,
  declarations:terms.smt(),domain:terms.domain(),input_variables:terms.inputs,paths:model.paths,reverted_paths:model.reverted_paths});
 const semanticsArtifact={artifact_id:semantics_id,kind:'state_transition_semantics',filename:'state-transition-'+model.model_id+'.json',media_type:'application/json',text:semantics,sha256:hash(semantics)};
 model.semantics_artifact_id=semantics_id;
 return {model,terms,layoutByID,initial,caller,readLocation,locate,expr,call,clone,semanticsArtifact,
  initialState:()=>({storage:initial,locals:new Map(),immutables:new Map(),conditions:[],writes:[],returns:[],stack:[],unchecked:ctx.legacyArithmetic||false,halt:null,symbolic_cases:rootCases()})};
}

// This proves the source constructor from zero storage. Runtime source identity
// alone does not establish the historical deployment or a proxy's initial state.
export function buildCreationTransition(result,ctx) {
 const contract=ctx.mainContract;
 if(!contract)return {reason:'CREATION_CONTRACT_AST_UNAVAILABLE'};
 const fn=ctx.functions.find(f=>f.scope===contract.id&&(f.kind==='constructor'||f.isConstructor))||
  {id:contract.id,src:contract.src,nodeType:'FunctionDefinition',kind:'constructor',parameters:{parameters:[]},returnParameters:{parameters:[]},modifiers:[],body:{nodeType:'Block',statements:[]}};
 const existing=ctx.methodByAST.get(fn.id),identity=[result.snapshot.code_hash,result.snapshot.storage_address,'source-creation'];
 if(result.snapshot.input_sha256)identity.push(result.snapshot.input_sha256);
 const method_id=existing?.method_id||id(...identity);
 const method={...(existing||{}),method_id,
  code_hash:result.snapshot.code_hash,code_address:result.snapshot.code_address,storage_address:result.snapshot.storage_address,state_mutability:fn.stateMutability||'nonpayable',
  evidence_ids:[]};
 method.evidence_ids=[ctx.getEvidence(method,fn)];
 if(!existing)result.sections.methods.push({...method,name:'constructor',signature:'constructor()',visibility:'public',entry_kind:'constructor',code_phase:'creation_source_not_verified_against_deployment',
  dispatch_role:'implicit_source_definition',parameters:[],outputs:[],source_location:ctx.location(fn),completeness:{complete:false,basis:'source',unresolved:['DEPLOYMENT_CREATION_IDENTITY_NOT_VERIFIED']}});
 const contracts=(contract.linearizedBaseContracts||[contract.id]).map(key=>ctx.nodes.get(key)),creationPlan={contracts:[],initializers:[]};
 if(contract.baseContracts?.length&&!String(ctx.compilerVersion||'').startsWith('0.8.30+'))creationPlan.error='CREATION_INHERITANCE_COMPILER_SEMANTICS_NOT_VERIFIED';
 if(contracts.some(c=>!c))creationPlan.error='CREATION_LINEARIZATION_AST_UNAVAILABLE';
 for(const c of contracts.filter(Boolean)){
  const constructor=ctx.functions.find(f=>f.scope===c.id&&(f.kind==='constructor'||f.isConstructor)),initializers=(c.nodes||[]).filter(n=>n.nodeType==='VariableDeclaration'&&n.stateVariable),invocations=[];
  for(const node of [...(c.baseContracts||[]),...(constructor?.modifiers||[])]){
   const target=ctx.nodes.get(node.baseName?.referencedDeclaration||node.modifierName?.referencedDeclaration);
   if(target?.nodeType!=='ContractDefinition'||!Array.isArray(node.arguments))continue;
   const base=ctx.functions.find(f=>f.scope===target.id&&(f.kind==='constructor'||f.isConstructor)),parameters=base?.parameters?.parameters||[];
   if(parameters.length!==node.arguments.length)creationPlan.error='CREATION_BASE_ARGUMENT_ARITY_MISMATCH';
   invocations.push({target:target.id,parameters,arguments:node.arguments});
  }
  creationPlan.contracts.push({contract:c,constructor,initializers,invocations});
 }
 creationPlan.initializers=[...creationPlan.contracts].reverse().flatMap(c=>c.initializers);
 const transition=buildStateTransition(fn,method,{...ctx,zeroInitialStorage:true,creationPlan});
 result.sections.artifacts.push(transition.semanticsArtifact);
 transition.model.scope='source_creation_from_zero_storage';transition.model.deployment_identity_verified=false;
 transition.model.initialization_order=ctx.viaIR?'base_initializer_then_base_constructor':'all_base_to_derived_initializers_then_base_to_derived_constructors';
 transition.model.constructor_sequence=[...creationPlan.contracts].reverse().map(c=>({contract_ast_id:c.contract.id,contract:c.contract.name,constructor_ast_id:c.constructor?.id??null,initializer_ast_ids:c.initializers.filter(d=>d.value&&!d.constant).map(d=>d.id),base_arguments:c.invocations.map(i=>({target_contract_ast_id:i.target,argument_ast_ids:i.arguments.map(n=>n.id)}))}));
 result.sections.initial_state_models||=[];result.sections.initial_state_models.push(transition.model);
 return transition;
}

export function analyzeStateModels(result,ctx) {
 const transitions=[],dispatchedKinds=new Set();
 for(const fn of ctx.functions) {
  const method=ctx.methodByAST.get(fn.id);
  if(!method||method.code_phase!=='runtime_source'||method.dispatch_role==='overridden_source_definition'||!['public','external'].includes(method.visibility)||['view','pure'].includes(method.state_mutability))continue;
  if(['fallback','receive'].includes(method.entry_kind)){
   if(dispatchedKinds.has(method.entry_kind))continue;dispatchedKinds.add(method.entry_kind);
  }
  transitions.push(buildStateTransition(fn,method,ctx));
 }
 result.sections.state_transitions||=[];result.sections.state_transitions.push(...transitions.map(t=>t.model));
 result.sections.artifacts.push(...transitions.map(t=>t.semanticsArtifact));
 if(transitions.length)result.sections.coverage.push({dimension:'source_state_transitions',scope:'all_mutating_runtime_entries',complete:transitions.every(t=>t.model.complete),entries:transitions.length,represented:transitions.filter(t=>t.model.complete).length});
 return transitions;
}
