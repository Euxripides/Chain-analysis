import {id,unique} from '../model.mjs';
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

const walk=(node,fn)=>{if(!node)return;fn(node);for(const [key,value]of Object.entries(node))if(!['typeDescriptions','documentation'].includes(key)){
 if(Array.isArray(value)){for(const x of value)if(x?.nodeType)walk(x,fn);}else if(value?.nodeType)walk(value,fn);
}};
const uint256={sort:'Int',type:'uint256',bits:256,signed:false,min:0n,max:MAX_WORD,mod:WORD};
const bool={sort:'Bool',type:'bool'};

export function buildStateTransition(fn,method,ctx) {
 const {nodes,functions,layout,location,sourceText}=ctx,terms=new Terms(),failure=[],unresolved=[],effects=[],staticSlots=unique([...(layout.storage||[]).map(s=>s.slot),...(layout.namespace_static_slots||[])]);
 const localType=d=>(ctx.namespace_registry&&namespacePointerType(d,ctx.namespace_registry))||scalarLocalType(d);
 const layoutByID=new Map((layout.storage||[]).map(s=>[s.astId,s])),env=new Map();
 const caller=terms.variable('caller',{...uint256,bits:160,max:(1n<<160n)-1n,mod:1n<<160n},'msg.sender');env.set('msg.sender',caller);
 for(const [name,type]of [['tx.origin',{...uint256,bits:160,max:(1n<<160n)-1n,mod:1n<<160n}],['msg.value',uint256],['block.timestamp',uint256],['block.number',uint256]])env.set(name,terms.variable(name.replace('.','_'),type,name));
 const initial=ctx.zeroInitialStorage?'((as const '+ARRAY+') 0)':terms.variable('storage_0',ARRAY).term;
 let visits=0,frameSerial=0;
 const clone=s=>({...s,locals:new Map(s.locals),immutables:new Map(s.immutables),conditions:[...s.conditions],writes:[...s.writes],returns:[...s.returns],signature_recoveries:[...(s.signature_recoveries||[])]});
 const fail=message=>{throw new Error(message);};
 const gate=(s,condition,reason='revert')=>{const rejected=clone(s);rejected.conditions.push('(not '+condition+')');rejected.storage=initial;if(rejected.yul_transient)rejected.yul_transient='transient_0';rejected.halt=reason;failure.push(rejected);s.conditions.push(condition);};
 const bounded=rows=>{if(rows.length>32)fail('STATE_PATH_BUDGET');return rows;};
 const expressionEffects=n=>{
  const reads=new Set(),writes=new Set();walk(n,node=>{
   if(node.nodeType==='Identifier')reads.add(node.referencedDeclaration);
   const target=node.nodeType==='Assignment'?node.leftHandSide:node.nodeType==='UnaryOperation'&&['++','--','delete'].includes(node.operator)?node.subExpression:null;
   if(target){let root=target;while(['IndexAccess','MemberAccess'].includes(root.nodeType))root=root.baseExpression||root.expression;
    if(root.nodeType==='Identifier')writes.add(root.referencedDeclaration);
   }
  });return {reads,writes};
 };
 const independent=list=>{
  const all=list.map(expressionEffects);for(let i=0;i<all.length;i++)for(let j=i+1;j<all.length;j++){
   if([...all[i].writes].some(k=>all[j].reads.has(k)||all[j].writes.has(k))||[...all[j].writes].some(k=>all[i].reads.has(k)))fail('STATE_UNSPECIFIED_SIBLING_EVALUATION_ORDER');
  }
 };
 const typed=(node,term,s,{wrap=false}={})=>{
  const type=scalarType(node);if(type.sort==='Bool')return {term, ...type};
  if(!wrap&&!s.unchecked)gate(s,and(['(<= '+number(type.min)+' '+term+')','(<= '+term+' '+number(type.max)+')']),'panic_0x11');
  return terms.value(wrap||s.unchecked?terms.normalize(term,type):term,type);
 };
 const coerce=(v,type)=>{
  if(v.sort==='StorageReference'||type.sort==='StorageReference'){
   if(v.sort!==type.sort||v.struct_ast_id!==type.struct_ast_id||!v.storage_location)fail('STATE_STORAGE_REFERENCE_TYPE_OR_ORIGIN_MISMATCH');return {...v,...type};
  }
  return v.type===type.type?v:coerceBytes(v,type,terms)||terms.value(type.sort==='Bool'?asBool(v):terms.normalize(fixedBytesTerm(asInt(v),v.type,type.type),type),type);
 };
 const readWord=(storage,slot)=>{
  const key=terms.define(slot),entry=terms.define('(select '+initial+' '+key+')');
  terms.constraints.add('(and (<= 0 '+entry+') (<= '+entry+' '+MAX_WORD+'))');
  terms.observe(key,'persistent storage slot',{kind:'storage_slot'});terms.observe(entry,'persistent storage word at '+key,{kind:'entry_storage_word',slot_symbol:key});
  return terms.define('(select '+storage+' '+key+')');
 };
 function readLocation(loc,s) {
  const type=scalarType({type:loc.scalar_type}),raw=readWord(s.storage,loc.slot),low='(mod (div '+raw+' '+(1n<<BigInt(loc.offset*8))+') '+(type.mod||256n)+')';
  return terms.value(type.bits===256&&!type.signed&&loc.offset===0?raw:type.sort==='Bool'?'(not (= '+low+' 0))':type.signed?'(ite (>= '+low+' '+(type.mod>>1n)+') (- '+low+' '+type.mod+') '+low+')':low,type);
 }
 function storeLocation(loc,value,s,node) {
  const type=scalarType({type:loc.scalar_type});value=coerce(value,type);
  const bits=type.bits||8,base=1n<<BigInt(loc.offset*8),above=base*(1n<<BigInt(bits)),old=readLocation(loc,s),word=readWord(s.storage,loc.slot);
  const encoded=type.sort==='Bool'?'(ite '+asBool(value)+' 1 0)':!type.signed&&!value.signed&&value.bits<=type.bits?asInt(value):terms.normalize(asInt(value),{...type,signed:false});
  const updated='(+ (mod '+word+' '+base+') (* '+encoded+' '+base+') (* (div '+word+' '+above+') '+above+'))';
  s.storage=terms.define('(store '+s.storage+' '+loc.slot+' '+(bits===256&&loc.offset===0?encoded:updated)+')',ARRAY);
  s.writes.push({root_ast_id:loc.root,variable:nodes.get(loc.root)?.name,slot:loc.slot,offset:loc.offset,bits,keys:loc.keys,
   old_value:asInt(old),new_value:asInt(value),fields:loc.fields||[],source_location:location(node),physical_storage_after:s.storage,
   ...(loc.namespace_id?{namespace_id:loc.namespace_id,namespace_struct_ast_id:loc.namespace_struct_ast_id}:{})});
  if(s.writes.length>64)fail('STATE_WRITE_BUDGET');
 }
 function locate(node,s) {
  if(node.nodeType==='FunctionCall'){
   const value=expr(node,s);if(value.sort!=='StorageReference'||!value.storage_location)fail('STATE_STORAGE_REFERENCE_CALL_UNAVAILABLE');return value.storage_location;
  }
  if(node.nodeType==='Identifier') {
   const value=s.locals.get(node.referencedDeclaration);if(value?.sort==='StorageReference'){
    if(!value.storage_location)fail('STATE_STORAGE_REFERENCE_UNBOUND');return value.storage_location;
   }
   const d=nodes.get(node.referencedDeclaration),item=layoutByID.get(d?.id),type=layout.types?.[item?.type];
   if(!item||!type)fail('STATE_STORAGE_LAYOUT_UNAVAILABLE');
   return {root:d.id,slot:String(item.slot),offset:item.offset||0,type_id:item.type,scalar_type:type.label,keys:[]};
  }
  const parent=locate(node.baseExpression||node.expression,s),type=layout.types?.[parent.type_id];
  if(node.nodeType==='IndexAccess'&&type.encoding==='mapping') {
   const key=coerce(expr(node.indexExpression,s),scalarType({type:layout.types[type.key].label})),child=layout.types[type.value];
   const encoded=fixedBytesTerm(key.signed?mod(asInt(key)):asInt(key),key.type,'bytes32');
   return {...parent,slot:terms.hash([encoded,parent.slot],staticSlots),offset:0,type_id:type.value,scalar_type:child.label,keys:[...parent.keys,asInt(key)]};
  }
  if(node.nodeType==='MemberAccess'&&type.members) {
   const field=type.members.find(f=>f.astId===node.referencedDeclaration||f.label===node.memberName),child=layout.types?.[field?.type];
   if(!field||!child)fail('STATE_STRUCT_FIELD_UNAVAILABLE');
   const slot=field.slot==='0'?parent.slot:terms.define(mod('(+ '+parent.slot+' '+field.slot+')'));
   if(field.slot!=='0')terms.reserveDerivedField(slot);
   return {...parent,root:parent.namespace_root?field.astId:parent.root,slot,offset:field.offset||0,type_id:field.type,scalar_type:child.label,
    fields:parent.namespace_root?[]:[...(parent.fields||[]),field.astId],namespace_root:false};
  }
  fail('STATE_STORAGE_PATH_UNSUPPORTED');
 }
 function locationValue(loc,s){
  const type=layout.types[loc.type_id],reference=ctx.namespace_registry?.byStruct.get(type?.struct_ast_id);
  return reference?{...reference,storage_location:loc}:readLocation(loc,s);
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
  independent(node.arguments);const args=node.arguments.map(a=>expr(a,s));return call(d,args,s);
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
   if(builtin(n,'this',nodes))return {term:BigInt(method.storage_address).toString(),...scalarType({type:'address'})};
   if(d?.constant&&d.value)return expr(d.value,s);
   if(d?.mutability==='immutable'){
    if(ctx.zeroInitialStorage){const type=scalarType(d);return s.immutables.get(d.id)||{term:type.sort==='Bool'?'false':'0',...type};}
    const value=ctx.runtimeValue?.(d);if(value===null||value===undefined)fail('STATE_IMMUTABLE_RUNTIME_IDENTITY_UNAVAILABLE');
    return {...scalarType(d),term:value==='true'||value==='false'?value:BigInt(value).toString()};
   }
   if(d?.stateVariable)return locationValue(locate(n,s),s);
   fail('STATE_IDENTIFIER_UNBOUND:'+n.name);
  }
  if(n.nodeType==='MemberAccess') {
   const name=builtinMember(n,nodes);if(env.has(name))return env.get(name);
   if(name==='block.chainid'&&/^\d+$/.test(String(ctx.chainID||'')))return {...uint256,term:String(ctx.chainID)};
   if(['min','max'].includes(n.memberName)&&n.expression?.nodeType==='FunctionCall'&&n.expression.expression?.name==='type'){
    const t=scalarType(n);return {term:number(n.memberName==='max'?t.max:t.min),...t};
   }return locationValue(locate(n,s),s);
  }
  if(n.nodeType==='IndexAccess')return locationValue(locate(n,s),s);
  if(n.nodeType==='TupleExpression'&&n.components?.length===1)return expr(n.components[0],s);
  if(n.nodeType==='Conditional') {
   if(expressionEffects(n.trueExpression).writes.size||expressionEffects(n.falseExpression).writes.size)fail('STATE_CONDITIONAL_LOCAL_EFFECT_PENDING');
   const c=expr(n.condition,s),yes=clone(s),no=clone(s);yes.conditions.push(asBool(c));no.conditions.push('(not '+asBool(c)+')');
   const type=scalarType(n),a=coerce(expr(n.trueExpression,yes),type),b=coerce(expr(n.falseExpression,no),type);
   if(yes.storage!==s.storage||no.storage!==s.storage)fail('STATE_CONDITIONAL_SIDE_EFFECT');
   s.conditions.push(or([and(yes.conditions),and(no.conditions)]));return terms.value('(ite '+asBool(c)+' '+a.term+' '+b.term+')',scalarType(n));
  }
  if(n.nodeType==='UnaryOperation') {
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
    if(expressionEffects(n.rightExpression).writes.size)fail('STATE_SHORT_CIRCUIT_LOCAL_EFFECT_PENDING');
    const branch=clone(s);branch.conditions.push(op==='&&'?asBool(a):'(not '+asBool(a)+')');const b=expr(n.rightExpression,branch);
    if(branch.storage!==s.storage)fail('STATE_SHORT_CIRCUIT_SIDE_EFFECT');
    s.conditions.push('(=> '+(op==='&&'?asBool(a):'(not '+asBool(a)+')')+' '+and(branch.conditions)+')');
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
   independent([n.leftHandSide,n.rightHandSide]);
   const right=expr(n.rightHandSide,s);let value=right;
   if(n.operator!=='=') {const left=expr(n.leftHandSide,s);if(!['+=','-=','*='].includes(n.operator))fail('STATE_ASSIGNMENT_UNSUPPORTED');value=typed(n.leftHandSide,'('+n.operator[0]+' '+asInt(left)+' '+asInt(right)+')',s);}
   assign(n.leftHandSide,value,s,n);return value;
  }
  if(n.nodeType==='FunctionCall') {
   const callee=n.expression;
   const accessor=ctx.namespace_registry?.accessors.get(target(n)?.id);
   if(accessor){if(n.arguments.length)fail('STATE_NAMESPACE_ACCESSOR_ARGUMENTS');return namespaceRootReference(accessor);}
   if(builtin(callee?.expression,'abi',nodes)&&['encode','encodePacked'].includes(callee?.memberName)){
    independent(n.arguments);return encodeABI(n.arguments.map(a=>expr(a,s)),{packed:callee.memberName==='encodePacked'});
   }
   if(builtin(callee,'keccak256',nodes)&&n.arguments?.length===1)return hashBytes(expr(n.arguments[0],s),terms);
   if(builtin(callee,'ecrecover',nodes)){
    independent(n.arguments);const value=recoverSigner(n.arguments.map(a=>expr(a,s)),terms),record={...value.recovery,source_ast_id:n.id,source_location:location(n),recovered_address:value.term,condition:and(s.conditions)};
    s.signature_recoveries||=[];s.signature_recoveries.push(record);return value;
   }
   if(n.kind==='typeConversion'){
    const v=expr(n.arguments[0],s),type=localType(n),bytes=coerceBytes(v,type,terms);if(bytes)return bytes;
    return coerce(v,type);
   }
   const rows=invoke(n,s);if(!rows.length)fail('STATE_VALUE_CALL_ALWAYS_REVERTS');
   if(rows.some(r=>r.state.storage!==s.storage||r.state.yul_transient!==s.yul_transient||r.state.halt==='evm_return'||r.values.length!==1))fail('STATE_VALUE_CALL_SIDE_EFFECT_OR_TUPLE');
   if(rows.length>1&&rows.some(r=>r.values[0].sort==='StorageReference'))fail('STATE_STORAGE_REFERENCE_BRANCH_JOIN');
   s.conditions.push(or(rows.map(r=>and(r.state.conditions))));let value=rows.at(-1).values[0].term;
   for(const r of rows.slice(0,-1).reverse())value='(ite '+and(r.state.conditions)+' '+r.values[0].term+' '+value+')';
   for(const row of rows)for(const recovery of row.state.signature_recoveries||[])if(!(s.signature_recoveries||[]).includes(recovery)){s.signature_recoveries||=[];s.signature_recoveries.push(recovery);}
   if(rows.length===1)return rows[0].values[0];
   return terms.value(value,scalarType(n));
  }
  fail('STATE_EXPRESSION_UNSUPPORTED:'+n.nodeType);
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
   const c=expr(n.condition,s),yes=clone(s),no=clone(s);yes.conditions.push(asBool(c));no.conditions.push('(not '+asBool(c)+')');
   return bounded([...statement(n.trueBody,yes,placeholder),...(n.falseBody?statement(n.falseBody,no,placeholder):[no])]);
  }
  if(['ForStatement','WhileStatement'].includes(n.nodeType))return loopStatement(n,s,placeholder);
  if(n.nodeType==='VariableDeclarationStatement') {
   if(n.declarations.length!==1||!n.declarations[0])fail('STATE_LOCAL_TUPLE_UNSUPPORTED');
   const d=n.declarations[0],type=localType(d);if(type.sort==='StorageReference'&&!n.initialValue)fail('STATE_STORAGE_REFERENCE_UNBOUND');
   const value=n.initialValue?coerce(expr(n.initialValue,s),type):type.sort==='Bytes'?{...byteSequence([]),...type}:{term:type.sort==='Bool'?'false':'0',...type};s.locals.set(d.id,value);return [s];
  }
  if(n.nodeType==='Return'){s.returns=n.expression?[expr(n.expression,s)]:[];s.halt='return';return [s];}
  if(n.nodeType==='RevertStatement'){const failed=clone(s);failed.storage=initial;failed.halt='revert';failure.push(failed);return [];}
  if(n.nodeType==='EmitStatement'){independent(n.eventCall.arguments||[]);for(const a of n.eventCall.arguments||[])expr(a,s);return [s];}
  if(n.nodeType==='InlineAssembly'){
   if(!ctx.assembly)fail('STATE_ASSEMBLY_MODEL_PENDING');return ctx.assembly(n,s,{terms,env,expr,locate,readLocation,storeLocation,readWord,gate,clone,fail,effects,initial,nodes,location,sourceText,staticSlots,layout,method,entryFn:fn,evmRules:ctx.evmRules,
    recordGap:reason=>{if(!unresolved.includes(reason))unresolved.push(reason);}});
  }
  if(n.nodeType==='ExpressionStatement') {
   const e=n.expression;
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
  const saved=s.locals,stack=s.stack,entry=clone(s);entry.stack=[...(stack||[]),d.id];
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
   if(state.halt!=='evm_return')state.halt=null;state.returns=[];state.locals=new Map(saved);state.stack=stack;return {state,values};
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
 if(layout.namespace_storage)model.namespace_storage={scope:layout.namespace_storage.scope,complete:layout.namespace_storage.complete,
  namespaces:layout.namespace_storage.namespaces.map(n=>({namespace_id:n.namespace_id,struct_ast_id:n.struct_ast_id,root_slot:n.root_slot,
   layout_complete:n.layout_complete,pointer_assignment_verified:n.pointer_assignment_verified,evidence_ids:n.evidence_ids})),
  evidence_ids:unique(layout.namespace_storage.accessors.flatMap(a=>a.evidence_ids||[])),historical_initialization_verified:false};
 try {
  const args=(fn.parameters?.parameters||[]).map(p=>{const type=localType(p);if(type.sort==='Bytes')fail('STATE_UNBOUNDED_BYTES_ENTRY_INPUT');if(type.sort==='StorageReference')fail('STATE_STORAGE_REFERENCE_ENTRY_REQUIRES_CALLER_CONTEXT');return terms.variable('arg_'+p.id,type,p.name);});
  const state={storage:initial,locals:new Map(),immutables:new Map(),conditions:[],writes:[],returns:[],stack:[],unchecked:ctx.legacyArithmetic||false,halt:null};
  if(method.state_mutability!=='payable')gate(state,'(= '+env.get('msg.value').term+' 0)');
  if(!ctx.creationPlan)initialize(ctx.initializers||[],state);
  const rows=ctx.creationPlan?creation(args,state):call(fn,args,state);
  if(rows.some(r=>r.values.some(v=>v.sort==='Bytes')))fail('STATE_DYNAMIC_ENTRY_RETURN_ENCODING_PENDING');
  model.paths=rows.map((r,index)=>({path_id:id(model.model_id,index),condition:and(r.state.conditions),storage_before:initial,storage_after:r.state.storage,transient_after:r.state.yul_transient,halt:r.state.halt||'return',writes:r.state.writes,return_values:r.values.map(v=>v.term),signature_recoveries:r.state.signature_recoveries||[],immutable_values:[...r.state.immutables].map(([state_ast_id,v])=>({state_ast_id,type:v.type,value:v.term})),raw_return:r.state.yul_raw_return}));
  if(rows.some(r=>r.state.signature_recoveries?.length))model.assumptions.push('symbolic_ecrecover_is_a_conservative_function_without_unforgeability_or_hash_collision_axioms');
  model.complete=unresolved.length===0;model.status=model.complete?'represented_source_transition':'incomplete';
 }catch(error){unresolved.push(String(error.message));model.status='incomplete';}
 model.reverted_paths=failure.map((s,index)=>({path_id:id(model.model_id,'revert',index),condition:and(s.conditions),storage_after:initial,transient_after:s.yul_transient,attempted_writes:s.writes,halt:s.halt}));
 model.successful_path_count=model.paths.length;model.reverted_path_count=model.reverted_paths.length;
 model.input_variables=terms.inputs;
 return {model,terms,layoutByID,initial,caller,readLocation,locate,expr,call,clone,
  initialState:()=>({storage:initial,locals:new Map(),immutables:new Map(),conditions:[],writes:[],returns:[],stack:[],unchecked:ctx.legacyArithmetic||false,halt:null})};
}

// This proves the source constructor from zero storage. Runtime source identity
// alone does not establish the historical deployment or a proxy's initial state.
export function buildCreationTransition(result,ctx) {
 const contract=ctx.mainContract;
 if(!contract)return {reason:'CREATION_CONTRACT_AST_UNAVAILABLE'};
 const fn=ctx.functions.find(f=>f.scope===contract.id&&(f.kind==='constructor'||f.isConstructor))||
  {id:contract.id,src:contract.src,nodeType:'FunctionDefinition',kind:'constructor',parameters:{parameters:[]},returnParameters:{parameters:[]},modifiers:[],body:{nodeType:'Block',statements:[]}};
 const existing=ctx.methodByAST.get(fn.id),method_id=existing?.method_id||id(result.snapshot.code_hash,result.snapshot.storage_address,'source-creation');
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
 if(transitions.length)result.sections.coverage.push({dimension:'source_state_transitions',scope:'all_mutating_runtime_entries',complete:transitions.every(t=>t.model.complete),entries:transitions.length,represented:transitions.filter(t=>t.model.complete).length});
 return transitions;
}
