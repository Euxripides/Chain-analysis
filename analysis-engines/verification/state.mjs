import {id,unique} from '../model.mjs';
import {numericLiteral} from '../literals.mjs';
import {Terms,scalarType,asBool,asInt,mod,WORD,MAX_WORD,ARRAY} from './terms.mjs';
import {and,or,number,obligation,solveObligations,closeModels} from './solver.mjs';
import {createRequire} from 'node:module';
const require=createRequire(import.meta.url),{keccak_256}=require('@noble/hashes/sha3.js');

const walk=(node,fn)=>{if(!node)return;fn(node);for(const [key,value]of Object.entries(node))if(!['typeDescriptions','documentation'].includes(key)){
 if(Array.isArray(value)){for(const x of value)if(x?.nodeType)walk(x,fn);}else if(value?.nodeType)walk(value,fn);
}};
const uint256={sort:'Int',type:'uint256',bits:256,signed:false,min:0n,max:MAX_WORD,mod:WORD};
const bool={sort:'Bool',type:'bool'};

export function buildStateTransition(fn,method,ctx) {
 const {nodes,functions,layout,location,sourceText}=ctx,terms=new Terms(),failure=[],unresolved=[],effects=[],staticSlots=unique((layout.storage||[]).map(s=>s.slot));
 const layoutByID=new Map((layout.storage||[]).map(s=>[s.astId,s])),env=new Map();
 const caller=terms.variable('caller',{...uint256,bits:160,max:(1n<<160n)-1n,mod:1n<<160n},'msg.sender');env.set('msg.sender',caller);
 for(const [name,type]of [['tx.origin',{...uint256,bits:160,max:(1n<<160n)-1n,mod:1n<<160n}],['msg.value',uint256],['block.timestamp',uint256],['block.number',uint256]])env.set(name,terms.variable(name.replace('.','_'),type,name));
 const initial=ctx.zeroInitialStorage?'((as const '+ARRAY+') 0)':terms.variable('storage_0',ARRAY).term;
 let visits=0,frameSerial=0;
 const clone=s=>({...s,locals:new Map(s.locals),conditions:[...s.conditions],writes:[...s.writes],returns:[...s.returns]});
 const fail=message=>{throw new Error(message);};
 const gate=(s,condition,reason='revert')=>{const rejected=clone(s);rejected.conditions.push('(not '+condition+')');rejected.storage=initial;if(rejected.yul_transient)rejected.yul_transient='transient_0';rejected.halt=reason;failure.push(rejected);s.conditions.push(condition);};
 const bounded=rows=>{if(rows.length>32)fail('STATE_PATH_BUDGET');return rows;};
 const expressionEffects=n=>{
  const reads=new Set(),writes=new Set();walk(n,node=>{
   if(node.nodeType==='Identifier')reads.add(node.referencedDeclaration);
   const target=node.nodeType==='Assignment'?node.leftHandSide:node.nodeType==='UnaryOperation'&&['++','--','delete'].includes(node.operator)?node.subExpression:null;
   if(target)walk(target,t=>{if(t.nodeType==='Identifier')writes.add(t.referencedDeclaration);});
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
  const type=scalarType({type:loc.scalar_type}),bits=type.bits||8,base=1n<<BigInt(loc.offset*8),above=base*(1n<<BigInt(bits)),old=readLocation(loc,s),word=readWord(s.storage,loc.slot);
  const encoded=type.sort==='Bool'?'(ite '+asBool(value)+' 1 0)':!type.signed&&!value.signed&&value.bits<=type.bits?asInt(value):terms.normalize(asInt(value),{...type,signed:false});
  const updated='(+ (mod '+word+' '+base+') (* '+encoded+' '+base+') (* (div '+word+' '+above+') '+above+'))';
  s.storage=terms.define('(store '+s.storage+' '+loc.slot+' '+(bits===256&&loc.offset===0?encoded:updated)+')',ARRAY);
  s.writes.push({root_ast_id:loc.root,variable:nodes.get(loc.root)?.name,slot:loc.slot,offset:loc.offset,bits,keys:loc.keys,
   old_value:asInt(old),new_value:asInt(value),fields:loc.fields||[],source_location:location(node),physical_storage_after:s.storage});
  if(s.writes.length>64)fail('STATE_WRITE_BUDGET');
 }
 function locate(node,s) {
  if(node.nodeType==='Identifier') {
   const d=nodes.get(node.referencedDeclaration),item=layoutByID.get(d?.id),type=layout.types?.[item?.type];
   if(!item||!type)fail('STATE_STORAGE_LAYOUT_UNAVAILABLE');
   return {root:d.id,slot:String(item.slot),offset:item.offset||0,type_id:item.type,scalar_type:type.label,keys:[]};
  }
  const parent=locate(node.baseExpression||node.expression,s),type=layout.types?.[parent.type_id];
  if(node.nodeType==='IndexAccess'&&type.encoding==='mapping') {
   const key=expr(node.indexExpression,s),child=layout.types[type.value];
   return {...parent,slot:terms.hash([key.signed?mod(asInt(key)):asInt(key),parent.slot],staticSlots),offset:0,type_id:type.value,scalar_type:child.label,keys:[...parent.keys,asInt(key)]};
  }
  if(node.nodeType==='MemberAccess'&&type.members) {
   const field=type.members.find(f=>f.astId===node.referencedDeclaration||f.label===node.memberName),child=layout.types?.[field?.type];
   if(!field||!child)fail('STATE_STRUCT_FIELD_UNAVAILABLE');
   const slot=field.slot==='0'?parent.slot:terms.define(mod('(+ '+parent.slot+' '+field.slot+')'));
   if(field.slot!=='0')terms.reserveDerivedField(slot);
   return {...parent,slot,offset:field.offset||0,type_id:field.type,scalar_type:child.label,fields:[...(parent.fields||[]),field.astId]};
  }
  fail('STATE_STORAGE_PATH_UNSUPPORTED');
 }
 function target(node) {
  const callee=node.expression;let d=nodes.get(callee?.referencedDeclaration);
  if(d?.nodeType!=='FunctionDefinition')return null;
  const parent=nodes.get(d.scope),internal=callee.nodeType==='Identifier'||callee.nodeType==='MemberAccess'&&['super',parent?.name].includes(callee.expression?.name);
  if(!internal||parent?.contractKind==='library'&&!['internal','private'].includes(d.visibility))return null;
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
   fail('STATE_NONSCALAR_LITERAL');
  }
  if(n.nodeType==='Identifier') {
   const d=nodes.get(n.referencedDeclaration);if(s.locals.has(n.referencedDeclaration))return s.locals.get(n.referencedDeclaration);
   if(d?.constant&&d.value)return expr(d.value,s);
   if(d?.stateVariable)return readLocation(locate(n,s),s);
   fail('STATE_IDENTIFIER_UNBOUND:'+n.name);
  }
  if(n.nodeType==='MemberAccess') {
   const name=sourceText(n).replace(/\s/g,'');if(env.has(name))return env.get(name);
   if(['min','max'].includes(n.memberName)&&n.expression?.nodeType==='FunctionCall'&&n.expression.expression?.name==='type'){
    const t=scalarType(n);return {term:number(n.memberName==='max'?t.max:t.min),...t};
   }return readLocation(locate(n,s),s);
  }
  if(n.nodeType==='IndexAccess')return readLocation(locate(n,s),s);
  if(n.nodeType==='TupleExpression'&&n.components?.length===1)return expr(n.components[0],s);
  if(n.nodeType==='Conditional') {
   if(expressionEffects(n.trueExpression).writes.size||expressionEffects(n.falseExpression).writes.size)fail('STATE_CONDITIONAL_LOCAL_EFFECT_PENDING');
   const c=expr(n.condition,s),yes=clone(s),no=clone(s);yes.conditions.push(asBool(c));no.conditions.push('(not '+asBool(c)+')');
   const a=expr(n.trueExpression,yes),b=expr(n.falseExpression,no);
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
   const a=expr(n.leftExpression,s),op=n.operator;
   if(['&&','||'].includes(op)){
    if(expressionEffects(n.rightExpression).writes.size)fail('STATE_SHORT_CIRCUIT_LOCAL_EFFECT_PENDING');
    const branch=clone(s);branch.conditions.push(op==='&&'?asBool(a):'(not '+asBool(a)+')');const b=expr(n.rightExpression,branch);
    if(branch.storage!==s.storage)fail('STATE_SHORT_CIRCUIT_SIDE_EFFECT');
    s.conditions.push('(=> '+(op==='&&'?asBool(a):'(not '+asBool(a)+')')+' '+and(branch.conditions)+')');
    return terms.value('('+ (op==='&&'?'and':'or')+' '+asBool(a)+' '+asBool(b)+')',bool);
   }
   independent([n.leftExpression,n.rightExpression]);const b=expr(n.rightExpression,s);
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
   if(n.expression?.name==='keccak256'&&n.arguments?.length===1&&n.arguments[0].nodeType==='Literal'&&['string','hexString','unicodeString'].includes(n.arguments[0].kind)){
    const literal=n.arguments[0],bytes=literal.hexValue?Buffer.from(literal.hexValue,'hex'):Buffer.from(literal.value);return {term:BigInt('0x'+Buffer.from(keccak_256(bytes)).toString('hex')).toString(),...uint256};
   }
   if(n.kind==='typeConversion'){const v=expr(n.arguments[0],s);return typed(n,asInt(v),s,{wrap:true});}
   const rows=invoke(n,s);if(!rows.length)fail('STATE_VALUE_CALL_ALWAYS_REVERTS');
   if(rows.some(r=>r.state.storage!==s.storage||r.state.yul_transient!==s.yul_transient||r.state.halt==='evm_return'||r.values.length!==1))fail('STATE_VALUE_CALL_SIDE_EFFECT_OR_TUPLE');
   s.conditions.push(or(rows.map(r=>and(r.state.conditions))));let value=rows.at(-1).values[0].term;
   for(const r of rows.slice(0,-1).reverse())value='(ite '+and(r.state.conditions)+' '+r.values[0].term+' '+value+')';
   return terms.value(value,scalarType(n));
  }
  fail('STATE_EXPRESSION_UNSUPPORTED:'+n.nodeType);
 }
 function assign(n,v,s,node) {
  const d=nodes.get(n.referencedDeclaration);
  if(n.nodeType==='Identifier'&&d&&!d.stateVariable){s.locals.set(d.id,v);return;}
  storeLocation(locate(n,s),v,s,node);
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
  if(n.nodeType==='VariableDeclarationStatement') {
   if(n.declarations.length!==1||!n.declarations[0])fail('STATE_LOCAL_TUPLE_UNSUPPORTED');
   const d=n.declarations[0],type=scalarType(d),value=n.initialValue?expr(n.initialValue,s):{term:type.sort==='Bool'?'false':'0',...type};s.locals.set(d.id,value);return [s];
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
   if(e?.nodeType==='FunctionCall'&&['require','assert'].includes(e.expression?.name)) {
    if(e.arguments.slice(1).some(a=>a.nodeType!=='Literal'))fail('STATE_GUARD_MESSAGE_EFFECT_UNSUPPORTED');
    gate(s,asBool(expr(e.arguments[0],s)),e.expression.name==='assert'?'panic_0x01':'revert');return [s];
   }
   if(e?.nodeType==='FunctionCall'&&e.kind!=='typeConversion')return invoke(e,s).map(r=>r.state);
   expr(e,s);return [s];
  }
  fail('STATE_CONTROL_UNSUPPORTED:'+n.nodeType);
 }
 function call(d,args,s) {
  if((s.stack||[]).length>=12||(s.stack||[]).includes(d.id))fail('STATE_RECURSION_OR_DEPTH');
  const saved=s.locals,stack=s.stack,entry=clone(s);entry.stack=[...(stack||[]),d.id];
  for(const [i,p]of(d.parameters?.parameters||[]).entries()){if(!args[i])fail('STATE_CALL_ARGUMENT_MISSING');entry.locals.set(p.id,args[i]);}
  for(const r of d.returnParameters?.parameters||[])if(r.name){const type=scalarType(r);entry.locals.set(r.id,{term:type.sort==='Bool'?'false':'0',...type});}
  function execute(index,state) {
   if(index===(d.modifiers||[]).length)return statement(d.body,state);
   const invocation=d.modifiers[index],modifier=nodes.get(invocation.modifierName?.referencedDeclaration);
   if(modifier?.nodeType!=='ModifierDefinition'||!modifier.body)fail('STATE_MODIFIER_UNAVAILABLE');
   let placeholders=0;walk(modifier.body,n=>{if(n.nodeType==='PlaceholderStatement')placeholders++;});
   if(placeholders!==1)fail('STATE_MODIFIER_PLACEHOLDER_COUNT_UNSUPPORTED');
   for(const [i,p]of(modifier.parameters?.parameters||[]).entries())state.locals.set(p.id,expr(invocation.arguments[i],state));
   return statement(modifier.body,state,p=>execute(index+1,p));
  }
  return execute(0,entry).map(state=>{
   const values=state.halt==='evm_return'?[]:state.returns.length?state.returns:(d.returnParameters?.parameters||[]).map(r=>state.locals.get(r.id));
   if(values.some(v=>!v))fail('STATE_RETURN_VALUE_UNAVAILABLE');
   if(state.halt!=='evm_return')state.halt=null;state.returns=[];state.locals=new Map(saved);state.stack=stack;return {state,values};
  });
 }
 const model={model_id:id(method.method_id,'state-transition'),method_id:method.method_id,code_hash:method.code_hash,code_address:method.code_address,
  storage_address:method.storage_address,scope:'successful_and_reverted_source_state_transitions',
  assumptions:['typed_entry_arguments_and_environment','keccak_storage_paths_collision_free_in_declared_layout','gas_feasibility_not_proved',ctx.zeroInitialStorage?'creation_storage_starts_zero':'entry_storage_is_symbolic'],
  evidence_ids:method.evidence_ids,source_location:location(fn),unresolved,complete:false,paths:[],effects};
 try {
  const args=(fn.parameters?.parameters||[]).map(p=>terms.variable('arg_'+p.id,scalarType(p),p.name));
  const state={storage:initial,locals:new Map(),conditions:[],writes:[],returns:[],stack:[],unchecked:ctx.legacyArithmetic||false,halt:null};
  if(method.state_mutability!=='payable')gate(state,'(= '+env.get('msg.value').term+' 0)');
  for(const d of ctx.initializers||[])if(d.value&&!d.constant){
   if(d.mutability==='immutable')fail('CREATION_IMMUTABLE_INITIALIZER_REQUIRES_MODEL');
   storeLocation(locate({...d,nodeType:'Identifier',referencedDeclaration:d.id},state),expr(d.value,state),state,d);
  }
  const rows=call(fn,args,state);
  model.paths=rows.map((r,index)=>({path_id:id(model.model_id,index),condition:and(r.state.conditions),storage_before:initial,storage_after:r.state.storage,transient_after:r.state.yul_transient,halt:r.state.halt||'return',writes:r.state.writes,return_values:r.values.map(v=>v.term),raw_return:r.state.yul_raw_return}));
  model.complete=unresolved.length===0;model.status=model.complete?'represented_source_transition':'incomplete';
 }catch(error){unresolved.push(String(error.message));model.status='incomplete';}
 model.reverted_paths=failure.map((s,index)=>({path_id:id(model.model_id,'revert',index),condition:and(s.conditions),storage_after:initial,transient_after:s.yul_transient,attempted_writes:s.writes,halt:s.halt}));
 model.successful_path_count=model.paths.length;model.reverted_path_count=model.reverted_paths.length;
 model.input_variables=terms.inputs;
 return {model,terms,layoutByID,initial,caller,readLocation,locate,expr,call,clone,
  initialState:()=>({storage:initial,locals:new Map(),conditions:[],writes:[],returns:[],stack:[],unchecked:ctx.legacyArithmetic||false,halt:null})};
}

// This proves the source constructor from zero storage. Runtime source identity
// alone does not establish the historical deployment or a proxy's initial state.
export function buildCreationTransition(result,ctx) {
 const contract=ctx.mainContract;
 if(!contract||contract.baseContracts?.length)return {reason:'CREATION_INHERITANCE_AND_BASE_ARGUMENTS_REQUIRE_MODEL'};
 const fn=ctx.functions.find(f=>f.scope===contract.id&&(f.kind==='constructor'||f.isConstructor))||
  {id:contract.id,src:contract.src,nodeType:'FunctionDefinition',kind:'constructor',parameters:{parameters:[]},returnParameters:{parameters:[]},modifiers:[],body:{nodeType:'Block',statements:[]}};
 const existing=ctx.methodByAST.get(fn.id),method_id=existing?.method_id||id(result.snapshot.code_hash,result.snapshot.storage_address,'source-creation');
 const method={...(existing||{}),method_id,
  code_hash:result.snapshot.code_hash,code_address:result.snapshot.code_address,storage_address:result.snapshot.storage_address,state_mutability:fn.stateMutability||'nonpayable',
  evidence_ids:[]};
 method.evidence_ids=[ctx.getEvidence(method,fn)];
 if(!existing)result.sections.methods.push({...method,name:'constructor',signature:'constructor()',visibility:'public',entry_kind:'constructor',code_phase:'creation_source_not_verified_against_deployment',
  dispatch_role:'implicit_source_definition',parameters:[],outputs:[],source_location:ctx.location(fn),completeness:{complete:false,basis:'source',unresolved:['DEPLOYMENT_CREATION_IDENTITY_NOT_VERIFIED']}});
 const transition=buildStateTransition(fn,method,{...ctx,zeroInitialStorage:true,initializers:ctx.stateVars.filter(d=>d.scope===contract.id)});
 transition.model.scope='source_creation_from_zero_storage';transition.model.deployment_identity_verified=false;
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
