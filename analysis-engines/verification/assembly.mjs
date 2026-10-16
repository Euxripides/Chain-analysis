import {evmWord,WORD_ARITIES} from '../ir/evm-word.mjs';
import {scalarType,asInt,mod,WORD,MAX_WORD,ARRAY} from './terms.mjs';
import {and,or,number} from './solver.mjs';

const wordType={sort:'Int',type:'uint256',bits:256,signed:false,min:0n,max:MAX_WORD,mod:WORD};

export function executeAssembly(assembly,state,api) {
 const {terms,nodes,clone,fail,gate,location,env,method,entryFn}=api;
 const refs=Array.isArray(assembly.externalReferences)?assembly.externalReferences:Object.entries(assembly.externalReferences||{}).map(([src,r])=>({src,...r}));
 let visits=0,serial=0,depth=0;
 const word=(term,known=null)=>({...wordType,term:terms.define(mod(term)),known:known===null?null:(known%WORD+WORD)%WORD});
 const budget=()=>{if(++visits>2048)fail('YUL_STATE_NODE_BUDGET');};
 const value=(s,key)=>s.locals.get(key)||fail('YUL_LOCAL_UNBOUND');
 function external(n,s,write=false) {
  const ref=refs.find(r=>r.src===n.src),d=nodes.get(ref?.declaration);
  if(!d)fail('YUL_EXTERNAL_REFERENCE_UNAVAILABLE');
  const suffix=ref.suffix||(ref.isSlot?'slot':ref.isOffset?'offset':null);
  if(suffix){if(write)fail('YUL_STORAGE_POINTER_ASSIGNMENT_UNSUPPORTED');const loc=api.locate({...d,nodeType:'Identifier',referencedDeclaration:d.id},s),text=suffix==='slot'?loc.slot:String(loc.offset);return word(text,/^\d+$/.test(text)?BigInt(text):null);}
  const type=scalarType(d);if(type.bits!==256)fail('YUL_EXTERNAL_DIRTY_BITS_UNSUPPORTED');
  if(d.stateVariable)fail('YUL_DIRECT_STATE_REFERENCE_UNSUPPORTED');
  return {declaration:d,read:()=>word(asInt(value(s,d.id))),write:v=>s.locals.set(d.id,{...type,term:terms.define(terms.normalize(v.term,type))})};
 }
 function memory(s) {
  if(!s.yul_state_memory){
   const tag='assembly_'+assembly.id+'_'+terms.serial++;
   s.yul_state_memory=terms.variable(tag+'_entry',ARRAY).term;s.yul_entry_memory=s.yul_state_memory;
   s.yul_entry_msize=terms.variable(tag+'_msize',wordType,'assembly entry memory size').term;s.yul_msize=s.yul_entry_msize;
   terms.constraints.add('(= (mod '+s.yul_entry_msize+' 32) 0)');
  }
 }
 function memRange(s,offset,size) {
  memory(s);if(size==='0')return;gate(s,'(or (= '+size+' 0) (<= (+ '+offset+' '+size+') '+WORD+'))','memory_expansion_overflow');
  const after='(* (div (+ '+offset+' '+size+' 31) 32) 32)';
  s.yul_msize=terms.define('(ite (and (distinct '+size+' 0) (> '+after+' '+s.yul_msize+')) '+after+' '+s.yul_msize+')');
 }
 function memByte(s,address,snapshot=s.yul_state_memory) {
  const original=mod('(select '+s.yul_entry_memory+' '+address+')',256n);
  terms.constraints.add('(=> (>= '+address+' '+s.yul_entry_msize+') (= '+original+' 0))');
  return mod('(select '+snapshot+' '+address+')',256n);
 }
 function memStore(s,offset,bytes) {
  memRange(s,offset,String(bytes.length));
  for(const [i,b]of bytes.entries())s.yul_state_memory=terms.define('(store '+s.yul_state_memory+' (+ '+offset+' '+i+') '+mod(b,256n)+')',ARRAY);
 }
 function loadWord(s,offset){memRange(s,offset,'32');return word('(+ '+Array.from({length:32},(_,i)=>'(* '+memByte(s,'(+ '+offset+' '+i+')')+' '+(1n<<BigInt((31-i)*8))+')').join(' ')+')');}
 function calldata(s) {
  if(s.yul_calldata)return;
  s.yul_calldata=terms.variable('calldata_entry',ARRAY).term;
  s.yul_calldata_size=terms.variable('calldata_size',wordType,'calldata byte length').term;
  const params=entryFn.parameters?.parameters||[],minimum=method.selector?4+params.length*32:0;
  terms.constraints.add('(>= '+s.yul_calldata_size+' '+minimum+')');
  if(!method.selector)return;
  const code=Buffer.from(method.selector.slice(2),'hex');
  for(let i=0;i<4;i++)terms.constraints.add('(= '+mod('(select '+s.yul_calldata+' '+i+')',256n)+' '+code[i]+')');
  for(const [index,p]of params.entries()){
   const type=scalarType(p),raw=type.sort==='Bool'?'(ite arg_'+p.id+' 1 0)':mod('arg_'+p.id),bytes=/^bytes(\d+)$/.exec(type.type);
   const encoded=bytes&&type.bits<256?'(* '+raw+' '+(1n<<BigInt(256-type.bits))+')':raw;
   for(let i=0;i<32;i++)terms.constraints.add('(= '+mod('(select '+s.yul_calldata+' '+(4+index*32+i)+')',256n)+' (mod (div '+encoded+' '+(1n<<BigInt((31-i)*8))+') 256))');
  }
 }
 const dataByte=(s,address)=>'(ite (< '+address+' '+s.yul_calldata_size+') '+mod('(select '+s.yul_calldata+' '+address+')',256n)+' 0)';
 function effect(s,n,kind,extra={}) {
  api.effects.push({kind,sequence:api.effects.length,source_location:location({...assembly,src:n.src||assembly.src}),condition:and(s.conditions),scope:'source_execution_path',...extra});
 }
 function rawStore(s,n,slot,v,transient=false) {
  const prior=s.storage;
  if(transient){s.yul_transient||=terms.variable('transient_0',ARRAY).term;s.yul_transient=terms.define('(store '+s.yul_transient+' '+slot.term+' '+v.term+')',ARRAY);}
  else{
   s.storage=terms.define('(store '+s.storage+' '+slot.term+' '+v.term+')',ARRAY);
   const matched=[];
   for(const item of api.layout.storage||[])if(slot.known!==null&&BigInt(item.slot)===slot.known){
    const type=api.layout.types[item.type];let scalar;try{scalar=scalarType({type:type.label});}catch{continue;}
    if(!scalar)continue;
    const loc={root:item.astId,slot:String(item.slot),offset:item.offset||0,scalar_type:type.label,keys:[],fields:[]};
    const old=api.readLocation(loc,{...s,storage:prior}),after=api.readLocation(loc,s);
    matched.push({root_ast_id:item.astId,variable:item.label,slot:slot.term,offset:item.offset||0,bits:scalar.bits||8,keys:[],fields:[],old_value:asInt(old),new_value:asInt(after),raw_storage_effect:true,source_location:location(assembly),physical_storage_after:s.storage});
   }
   s.writes.push(...(matched.length?matched:[{root_ast_id:null,slot:slot.term,keys:[],fields:[],old_value:api.readWord(prior,slot.term),new_value:v.term,raw_storage_effect:true,source_location:location(assembly),physical_storage_after:s.storage}]));
   if(s.writes.length>64)fail('STATE_WRITE_BUDGET');
  }
  effect(s,n,transient?'tstore':'sstore',{slot:slot.term,value:v.term,storage_after:transient?s.yul_transient:s.storage});
 }
 function builtin(n,args,s) {
  const name=n.functionName.name,[a,b,c]=args;
  if(name==='caller')return word(env.get('msg.sender').term);
  if(name==='origin')return word(env.get('tx.origin').term);
  if(name==='callvalue')return word(env.get('msg.value').term);
  if(name==='timestamp'||name==='number')return word(env.get('block.'+name).term);
  if(name==='address')return word(BigInt(method.storage_address).toString(),BigInt(method.storage_address));
  if(name==='mstore'||name==='mstore8'){
   const width=name==='mstore8'?1n:32n;
   if(a.known===null||!(a.known>=128n||a.known+width<=64n))api.recordGap('YUL_COMPILER_MEMORY_ALIAS_REQUIRES_EXECUTION_WITNESS_OR_VERIFIED_REGION');
   const bytes=name==='mstore8'?[mod(b.term,256n)]:Array.from({length:32},(_,i)=>'(mod (div '+b.term+' '+(1n<<BigInt((31-i)*8))+') 256)');
   memStore(s,a.term,bytes);effect(s,n,name,{offset:a.term,value:b.term});return word('0',0n);
  }
  if(name==='mload'){const out=loadWord(s,a.term);effect(s,n,name,{offset:a.term,result:out.term});return out;}
  if(name==='msize'){memory(s);return word(s.yul_msize);}
  if(name==='sload'){const out=word(api.readWord(s.storage,a.term));effect(s,n,name,{slot:a.term,result:out.term});return out;}
  if(name==='sstore'){rawStore(s,n,a,b);return word('0',0n);}
  if(['tload','tstore','mcopy'].includes(name)&&!/(cancun|prague|osaka)/.test(api.evmRules||''))fail('YUL_OPCODE_FORK_NOT_VERIFIED:'+name);
  if(name==='tload'){s.yul_transient||=terms.variable('transient_0',ARRAY).term;return word('(select '+s.yul_transient+' '+a.term+')');}
  if(name==='tstore'){rawStore(s,n,a,b,true);return word('0',0n);}
  if(name==='calldatasize'){calldata(s);return word(s.yul_calldata_size);}
  if(name==='calldataload'){calldata(s);return word('(+ '+Array.from({length:32},(_,i)=>'(* '+dataByte(s,'(+ '+a.term+' '+i+')')+' '+(1n<<BigInt((31-i)*8))+')').join(' ')+')');}
  if(['mcopy','calldatacopy'].includes(name)){
   if(c.known===null||c.known>256n)fail('YUL_COPY_LENGTH_REQUIRES_BOUNDED_RANGE');
   if(c.known!==0n&&(a.known===null||!(a.known>=128n||a.known+c.known<=64n)))api.recordGap('YUL_COMPILER_MEMORY_ALIAS_REQUIRES_EXECUTION_WITNESS_OR_VERIFIED_REGION');
   if(name==='mcopy')memRange(s,b.term,c.term);else calldata(s);
   const snapshot=s.yul_state_memory,bytes=Array.from({length:Number(c.known)},(_,i)=>name==='mcopy'?memByte(s,'(+ '+b.term+' '+i+')',snapshot):dataByte(s,'(+ '+b.term+' '+i+')'));
   memStore(s,a.term,bytes);effect(s,n,name,{destination:a.term,source:b.term,byte_length:c.term});return word('0',0n);
  }
  if(name==='pop')return word('0',0n);
  if(WORD_ARITIES[name]!==args.length)fail('YUL_STATE_BUILTIN_UNSUPPORTED:'+name);
  let term;const x=a?.term,y=b?.term,z=c?.term;
  if(['add','sub','mul'].includes(name))term='('+{add:'+',sub:'-',mul:'*'}[name]+' '+x+' '+y+')';
  else if(['div','mod'].includes(name))term='(ite (= '+y+' 0) 0 ('+name+' '+x+' '+y+'))';
  else if(['addmod','mulmod'].includes(name))term='(ite (= '+z+' 0) 0 (mod ('+(name==='addmod'?'+':'*')+' '+x+' '+y+') '+z+'))';
  else if(['eq','lt','gt','iszero'].includes(name))term='(ite '+(name==='iszero'?'(= '+x+' 0)':'('+{eq:'=',lt:'<',gt:'>'}[name]+' '+x+' '+y+')')+' 1 0)';
  else {
   const ops={and:'bvand',or:'bvor',xor:'bvxor',not:'bvnot',sdiv:'bvsdiv',smod:'bvsrem',shl:'bvshl',shr:'bvlshr',sar:'bvashr',slt:'bvslt',sgt:'bvsgt'},op=ops[name];
   if(!op)fail('YUL_STATE_WORD_OPERATION_PENDING:'+name);
   const values=['shl','shr','sar'].includes(name)?[b,a]:args;
   const expr='('+op+' '+values.map(v=>'((_ int2bv 256) '+v.term+')').join(' ')+')';
   term=['slt','sgt'].includes(name)?'(ite '+expr+' 1 0)':'(bv2int '+expr+')';
   if(['sdiv','smod'].includes(name))term='(ite (= '+y+' 0) 0 '+term+')';
  }
  let known=null;if(args.every(v=>v.known!==null))known=evmWord(name,args.map(v=>v.known));
  return word(term,known);
 }
 function yExpr(n,s,bindings,functions) {
  budget();
  if(n.nodeType==='YulLiteral'){if(!['number','bool'].includes(n.kind))fail('YUL_STATE_LITERAL_UNSUPPORTED');const v=n.kind==='bool'?(n.value==='true'?1n:0n):BigInt(n.value);return word(number(v),v);}
  if(n.nodeType==='YulIdentifier')return bindings.has(n.name)?value(s,bindings.get(n.name)):external(n,s).read?.()||external(n,s);
  if(n.nodeType!=='YulFunctionCall')fail('YUL_STATE_EXPRESSION_UNSUPPORTED');
  // Yul evaluates arguments from right to left, including memory effects.
  const args=new Array(n.arguments.length);for(let i=n.arguments.length-1;i>=0;i--)args[i]=yExpr(n.arguments[i],s,bindings,functions);
  const fn=functions.get(n.functionName.name);
  if(!fn)return builtin(n,args,s);
  const rows=yCall(fn,args,s,functions);if(rows.length!==1||rows[0].values.length!==1)fail('YUL_VALUE_FUNCTION_BRANCH_OR_TUPLE_PENDING');
  Object.assign(s,rows[0].state);return rows[0].values[0];
 }
 function assign(n,v,s,bindings) {
  if(bindings.has(n.name))s.locals.set(bindings.get(n.name),v);
  else{const ref=external(n,s,true);if(!ref.write)fail('YUL_EXTERNAL_TARGET_UNSUPPORTED');ref.write(v);}
 }
 function yBlock(n,s,outer,outerFunctions,shared=false) {
  const bindings=shared?outer:new Map(outer),functions=new Map(outerFunctions);
  for(const f of n.statements||[])if(f.nodeType==='YulFunctionDefinition')functions.set(f.name,f);
  let rows=[s];for(const child of n.statements||[])if(child.nodeType!=='YulFunctionDefinition')rows=rows.flatMap(s=>{
   if(s.halt||s.yul_leave||s.yul_break||s.yul_continue)return [s];return yStmt(child,s,bindings,functions);
  });
  if(rows.length>32)fail('YUL_STATE_PATH_BUDGET');return rows;
 }
 function yCall(fn,args,s,functions) {
  if(++depth>12)fail('YUL_STATE_FUNCTION_DEPTH');const original=s.locals,bindings=new Map(),state=clone(s);
  for(const [i,p]of(fn.parameters||[]).entries()){const key='yf_'+serial++;bindings.set(p.name,key);state.locals.set(key,args[i]);}
  for(const p of fn.returnVariables||[]){const key='yr_'+serial++;bindings.set(p.name,key);state.locals.set(key,word('0',0n));}
  const rows=yBlock(fn.body,state,bindings,functions).map(state=>{
   const values=(fn.returnVariables||[]).map(p=>value(state,bindings.get(p.name)));delete state.yul_leave;state.locals=new Map(original);return {state,values};
  });depth--;return rows;
 }
 function yStmt(n,s,bindings,functions) {
  budget();
  if(n.nodeType==='YulBlock')return yBlock(n,s,bindings,functions);
  if(n.nodeType==='YulVariableDeclaration'){
   let values;
   if(n.value?.nodeType==='YulFunctionCall'&&functions.has(n.value.functionName.name)){
    const args=n.value.arguments.map(()=>null);for(let i=args.length-1;i>=0;i--)args[i]=yExpr(n.value.arguments[i],s,bindings,functions);
    const rows=yCall(functions.get(n.value.functionName.name),args,s,functions);if(rows.length!==1)fail('YUL_TUPLE_FUNCTION_BRANCH_PENDING');Object.assign(s,rows[0].state);values=rows[0].values;
   }else values=n.value?[yExpr(n.value,s,bindings,functions)]:n.variables.map(()=>word('0',0n));
   if(values.length!==n.variables.length)fail('YUL_LOCAL_TUPLE_ARITY');
   for(const [i,v]of n.variables.entries()){const key='yl_'+serial++;bindings.set(v.name,key);s.locals.set(key,values[i]);}return [s];
  }
  if(n.nodeType==='YulAssignment'){
   if(n.variableNames.length!==1)fail('YUL_ASSIGNMENT_TUPLE_PENDING');assign(n.variableNames[0],yExpr(n.value,s,bindings,functions),s,bindings);return [s];
  }
  if(n.nodeType==='YulIf'){
   const c=yExpr(n.condition,s,bindings,functions);if(c.known!==null)return c.known===0n?[s]:yBlock(n.body,s,bindings,functions);
   const yes=clone(s),no=clone(s);yes.conditions.push('(not (= '+c.term+' 0))');no.conditions.push('(= '+c.term+' 0)');return [...yBlock(n.body,yes,bindings,functions),no];
  }
  if(n.nodeType==='YulSwitch'){
   const value=yExpr(n.expression,s,bindings,functions),rows=[],cases=n.cases.filter(c=>c.value!=='default');
   for(const c of cases){const b=clone(s),v=yExpr(c.value,s,bindings,functions);b.conditions.push('(= '+value.term+' '+v.term+')');rows.push(...yBlock(c.body,b,bindings,functions));}
   const b=clone(s);b.conditions.push(and(cases.map(c=>'(distinct '+value.term+' '+yExpr(c.value,s,bindings,functions).term+')')));const fallback=n.cases.find(c=>c.value==='default');rows.push(...(fallback?yBlock(fallback.body,b,bindings,functions):[b]));return rows;
  }
  if(n.nodeType==='YulLeave'){s.yul_leave=true;return [s];}
  if(n.nodeType==='YulBreak'){s.yul_break=true;return [s];}
  if(n.nodeType==='YulContinue'){s.yul_continue=true;return [s];}
  if(n.nodeType==='YulForLoop'){
   const local=new Map(bindings);let pending=yBlock(n.pre,s,local,functions,true),finished=[];
   for(let iteration=0;pending.length;iteration++){
    const active=[];
    for(const p of pending){const c=yExpr(n.condition,p,local,functions);if(c.known===null)fail('YUL_DYNAMIC_LOOP_INVARIANT_REQUIRED');if(c.known===0n)finished.push(p);else active.push(p);}
    if(iteration>=16&&active.length)fail('YUL_LOOP_INDUCTION_OR_BOUND_REQUIRED');
    pending=[];for(const p of active)for(const b of yBlock(n.body,p,local,functions)){
     if(b.yul_break){delete b.yul_break;finished.push(b);}else if(b.halt||b.yul_leave)finished.push(b);else{delete b.yul_continue;pending.push(...yBlock(n.post,b,local,functions));}
    }
   }return finished;
  }
  if(n.nodeType==='YulExpressionStatement'){
   const call=n.expression,name=call.functionName?.name;
   if(['revert','invalid'].includes(name)){gate(s,'false',name);return [];}
   if(['return','stop'].includes(name)){
    const args=call.arguments.map(()=>null);for(let i=args.length-1;i>=0;i--)args[i]=yExpr(call.arguments[i],s,bindings,functions);
    if(name==='return'){memRange(s,args[0].term,args[1].term);s.yul_raw_return={offset:args[0].term,byte_length:args[1].term,memory:s.yul_state_memory};}
    else s.yul_raw_return={byte_length:'0',data:'0x'};
    s.halt='evm_return';return [s];
   }
   if(functions.has(name)){const args=call.arguments.map(()=>null);for(let i=args.length-1;i>=0;i--)args[i]=yExpr(call.arguments[i],s,bindings,functions);return yCall(functions.get(name),args,s,functions).map(r=>r.state);}
   yExpr(call,s,bindings,functions);return [s];
  }
  fail('YUL_STATE_STATEMENT_UNSUPPORTED:'+n.nodeType);
 }
 if(!assembly.AST)fail('YUL_COMPILER_AST_UNAVAILABLE');
 // Each Solidity-to-assembly boundary has independent symbolic compiler memory.
 // Persistent and transient storage remain shared with the enclosing method.
 const entry=clone(state);delete entry.yul_state_memory;delete entry.yul_entry_memory;
 return yBlock(assembly.AST,entry,new Map(),new Map()).map(s=>{delete s.yul_state_memory;delete s.yul_entry_memory;return s;});
}
