import {evmWord,WORD_ARITIES} from '../ir/evm-word.mjs';
import {scalarType,asInt,mod,WORD,MAX_WORD,ARRAY} from './terms.mjs';
import {and,or,number} from './solver.mjs';

const wordType={sort:'Int',type:'uint256',bits:256,signed:false,min:0n,max:MAX_WORD,mod:WORD};

export function executeAssembly(assembly,state,api) {
 const {terms,nodes,clone,fail,gate,location,env,method,entryFn}=api;
 const refs=Array.isArray(assembly.externalReferences)?assembly.externalReferences:Object.entries(assembly.externalReferences||{}).map(([src,r])=>({src,...r}));
 let visits=0,serial=0,depth=0;
 const word=(term,known=null,metadata={})=>{
  const normalized=known===null?null:(known%WORD+WORD)%WORD;
  return {...wordType,term:terms.define(mod(term)),known:normalized,range_max:normalized??MAX_WORD,...metadata};
 };
 const budget=()=>{if(++visits>2048)fail('YUL_STATE_NODE_BUDGET');};
 const value=(s,key)=>s.locals.get(key)||fail('YUL_LOCAL_UNBOUND');
 function external(n,s,write=false) {
  const ref=refs.find(r=>r.src===n.src),d=nodes.get(ref?.declaration);
  if(!d)fail('YUL_EXTERNAL_REFERENCE_UNAVAILABLE');
  const suffix=ref.suffix||(ref.isSlot?'slot':ref.isOffset?'offset':null);
  if(suffix){if(write)fail('YUL_STORAGE_POINTER_ASSIGNMENT_UNSUPPORTED');const loc=api.locate({...d,nodeType:'Identifier',referencedDeclaration:d.id},s),text=suffix==='slot'?loc.slot:String(loc.offset);return word(text,/^\d+$/.test(text)?BigInt(text):null,suffix==='slot'?{storage_location:loc}:{});}
  const type=scalarType(d);if(!type.bits||(/^bytes\d+$/.test(type.type)&&type.bits!==256))fail('YUL_EXTERNAL_DIRTY_BITS_UNSUPPORTED');
  if(d.stateVariable)fail('YUL_DIRECT_STATE_REFERENCE_UNSUPPORTED');
  return {declaration:d,read:()=>{
   const typed=value(s,d.id);
   if(type.bits===256)return word(asInt(typed));
   // Solidity only defines the typed low bits here. Unclean high stack bits
   // remain symbolic until an explicit Yul mask or sign extension clears them.
   const key=d.id+':'+asInt(typed),cache=s.yul_external_words||new Map();
   if(cache.has(key))return cache.get(key);
   const dirty=terms.variable('yul_dirty_'+d.id+'_'+terms.serial++,wordType,'raw assembly stack word of '+d.name);
   terms.constraints.add('(= '+mod(dirty.term,type.mod)+' '+mod(asInt(typed),type.mod)+')');
   const out=word(dirty.term);s.yul_external_words=new Map(cache).set(key,out);return out;
  },write:v=>s.locals.set(d.id,{...type,term:terms.define(terms.normalize(v.term,type))})};
 }
 function memory(s) {
  if(!s.yul_state_memory){
   const tag='assembly_'+assembly.id+'_'+terms.serial++;
   s.yul_state_memory=terms.variable(tag+'_entry',ARRAY).term;s.yul_entry_memory=s.yul_state_memory;
   s.yul_entry_msize=terms.variable(tag+'_msize',wordType,'assembly entry memory size').term;s.yul_msize=s.yul_entry_msize;
   s.yul_known_words=new Map();
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
 function memStore(s,offset,bytes,spans=[]) {
  memRange(s,offset.term,String(bytes.length));
  if(!bytes.length)return;
  // Copy-on-write provenance keeps branches and overlapping MCOPY snapshots
  // independent. A symbolic destination may alias every remembered word.
  const known=new Map(s.yul_known_words);
  for(const start of known.keys())if(offset.known===null||BigInt(start)<offset.known+BigInt(bytes.length)&&offset.known<BigInt(start)+32n)known.delete(start);
  if(offset.known!==null)for(const span of spans)known.set(String(offset.known+span.offset),span.value);
  s.yul_known_words=known;
  for(const [i,b]of bytes.entries())s.yul_state_memory=terms.define('(store '+s.yul_state_memory+' (+ '+offset.term+' '+i+') '+mod(b,256n)+')',ARRAY);
 }
 function loadWord(s,offset){
  memRange(s,offset.term,'32');const remembered=offset.known===null?null:s.yul_known_words.get(String(offset.known));
  return remembered||word('(+ '+Array.from({length:32},(_,i)=>'(* '+memByte(s,'(+ '+offset.term+' '+i+')')+' '+(1n<<BigInt((31-i)*8))+')').join(' ')+')');
 }
 const storageType=loc=>api.layout.types[loc?.type_id];
 function fieldLocation(loc,field){const type=api.layout.types[field.type];return {...loc,offset:field.offset||0,type_id:field.type,scalar_type:type.label,fields:[...(loc.fields||[]),field.astId]};}
 function mappingParent(v){
  let loc=v.storage_location;
  if(loc?.word_fields)loc=loc.word_fields.find(l=>storageType(l)?.encoding==='mapping');
  if(!loc&&v.known!==null){const item=(api.layout.storage||[]).find(i=>BigInt(i.slot)===v.known&&api.layout.types[i.type]?.encoding==='mapping');if(item)loc={root:item.astId,slot:v.term,offset:0,type_id:item.type,scalar_type:api.layout.types[item.type].label,keys:[],fields:[]};}
  if(storageType(loc)?.encoding==='mapping')return loc;
  const field=storageType(loc)?.members?.find(f=>f.slot==='0'&&!f.offset&&api.layout.types[f.type]?.encoding==='mapping');
  return field?fieldLocation(loc,field):null;
 }
 function mappingKeyValid(v,parent){
  let type;try{type=scalarType({type:api.layout.types[storageType(parent).key].label});}catch{return false;}
  if(type.bits===256)return true;
  if(type.sort==='Bool')return v.range_max<=1n;
  if(type.signed||/^bytes\d+$/.test(type.type))return false;
  return v.range_max<=type.max;
 }
 function scalarLocations(loc,depth=0){
  if(depth>8)fail('YUL_STORAGE_PROVENANCE_DEPTH');
  const type=storageType(loc);if(!type)return [];
  if(type.members)return type.members.filter(f=>f.slot==='0').flatMap(f=>scalarLocations(fieldLocation(loc,f),depth+1));
  try{scalarType({type:type.label});return [loc];}catch{return [];}
 }
 function shiftedLocation(v,offset){
  const loc=v.storage_location,type=storageType(loc);if(!loc||offset.known===null)return null;
  if(offset.known===0n)return loc;
  const fields=type?.members?.filter(f=>BigInt(f.slot)===offset.known);
  // A packed word can hold multiple fields. Keep its enclosing region and
  // select every affected field at SSTORE rather than silently dropping one.
  return fields?.length?{...loc,type_id:null,scalar_type:null,word_fields:fields.map(f=>fieldLocation(loc,f))}:null;
 }
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
   const matched=[],locations=[];
   if(slot.known!==null){for(const item of api.layout.storage||[])if(BigInt(item.slot)===slot.known)locations.push(...scalarLocations({root:item.astId,slot:slot.term,offset:item.offset||0,type_id:item.type,scalar_type:api.layout.types[item.type].label,keys:[],fields:[]}));}
   else if(slot.storage_location){const loc=slot.storage_location;locations.push(...(loc.word_fields||[loc]).flatMap(l=>scalarLocations(l)));}
   for(const declared of locations){
    const loc={...declared,slot:slot.term},scalar=scalarType({type:loc.scalar_type});
    const old=api.readLocation(loc,{...s,storage:prior}),after=api.readLocation(loc,s);
    matched.push({root_ast_id:loc.root,variable:nodes.get(loc.root)?.name,slot:slot.term,offset:loc.offset||0,bits:scalar.bits||8,keys:loc.keys,fields:loc.fields||[],old_value:asInt(old),new_value:asInt(after),raw_storage_effect:true,source_location:location({...assembly,src:n.src||assembly.src}),physical_storage_after:s.storage});
   }
   s.writes.push(...(matched.length?matched:[{root_ast_id:null,slot:slot.term,keys:[],fields:[],old_value:api.readWord(prior,slot.term),new_value:v.term,raw_storage_effect:true,source_location:location(assembly),physical_storage_after:s.storage}]));
   if(s.writes.length>64)fail('STATE_WRITE_BUDGET');
  }
  effect(s,n,transient?'tstore':'sstore',{slot:slot.term,value:v.term,storage_after:transient?s.yul_transient:s.storage});
 }
 function builtin(n,args,s) {
  const name=n.functionName.name,[a,b,c]=args;
  if(name==='caller')return word(env.get('msg.sender').term,null,{range_max:(1n<<160n)-1n});
  if(name==='origin')return word(env.get('tx.origin').term,null,{range_max:(1n<<160n)-1n});
  if(name==='callvalue')return word(env.get('msg.value').term);
  if(name==='timestamp'||name==='number')return word(env.get('block.'+name).term);
  if(name==='address')return word(BigInt(method.storage_address).toString(),BigInt(method.storage_address));
  if(name==='mstore'||name==='mstore8'){
   const width=name==='mstore8'?1n:32n;
   if(a.known===null||!(a.known>=128n||a.known+width<=64n))api.recordGap('YUL_COMPILER_MEMORY_ALIAS_REQUIRES_EXECUTION_WITNESS_OR_VERIFIED_REGION');
   const bytes=name==='mstore8'?[mod(b.term,256n)]:Array.from({length:32},(_,i)=>'(mod (div '+b.term+' '+(1n<<BigInt((31-i)*8))+') 256)');
   memStore(s,a,bytes,name==='mstore'?[{offset:0n,value:b}]:[]);effect(s,n,name,{offset:a.term,value:b.term});return word('0',0n);
  }
  if(name==='mload'){const out=loadWord(s,a);effect(s,n,name,{offset:a.term,result:out.term});return out;}
  if(name==='keccak256'){
   if(![32n,64n].includes(b.known))fail('YUL_HASH_LENGTH_REQUIRES_VERIFIED_BYTE_ENCODING');
   memRange(s,a.term,b.term);
   const first=loadWord(s,a),parts=[first.term];let loc=null,status='unclassified_digest';
   if(b.known===64n){
    const second=loadWord(s,word('(+ '+a.term+' 32)',a.known===null?null:a.known+32n));parts.push(second.term);
    const parent=mappingParent(second);
    if(parent){
     status='unclassified_mapping_key';
     if(mappingKeyValid(first,parent)){
      const type=storageType(parent),child=api.layout.types[type.value];
      loc={...parent,offset:0,type_id:type.value,scalar_type:child.label,keys:[...parent.keys,first.term]};status='declared_mapping_path';
     }
    }
   }
   const hash=loc?terms.hash(parts,api.staticSlots):terms.digestWords(parts),out=word(hash,null,loc?{storage_location:{...loc,slot:hash}}:{});
   effect(s,n,name,{offset:a.term,byte_length:b.term,preimage_words:parts,result:out.term,storage_path_status:status,...(loc?{root_ast_id:loc.root,keys:loc.keys,fields:loc.fields||[]}:{} )});return out;
  }
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
   const spans=name==='mcopy'&&b.known!==null?[...s.yul_known_words].filter(([start])=>BigInt(start)>=b.known&&BigInt(start)+32n<=b.known+c.known).map(([start,value])=>({offset:BigInt(start)-b.known,value})):[];
   memStore(s,a,bytes,spans);effect(s,n,name,{destination:a.term,source:b.term,byte_length:c.term});return word('0',0n);
  }
  if(name==='pop')return word('0',0n);
  if(WORD_ARITIES[name]!==args.length)fail('YUL_STATE_BUILTIN_UNSUPPORTED:'+name);
  let term,metadata={};const x=a?.term,y=b?.term,z=c?.term;
  if(['add','sub','mul'].includes(name))term='('+{add:'+',sub:'-',mul:'*'}[name]+' '+x+' '+y+')';
  else if(['div','mod'].includes(name))term='(ite (= '+y+' 0) 0 ('+name+' '+x+' '+y+'))';
  else if(['addmod','mulmod'].includes(name))term='(ite (= '+z+' 0) 0 (mod ('+(name==='addmod'?'+':'*')+' '+x+' '+y+') '+z+'))';
  else if(['eq','lt','gt','iszero'].includes(name))term='(ite '+(name==='iszero'?'(= '+x+' 0)':'('+{eq:'=',lt:'<',gt:'>'}[name]+' '+x+' '+y+')')+' 1 0)';
  else if(name==='and'&&args.some(v=>v.known!==null&&(v.known&(v.known+1n))===0n)){
   const mask=args.find(v=>v.known!==null&&(v.known&(v.known+1n))===0n),other=mask===a?b:a;
   term=mod(other.term,mask.known+1n);metadata.range_max=mask.known<other.range_max?mask.known:other.range_max;
  }else {
   const ops={and:'bvand',or:'bvor',xor:'bvxor',not:'bvnot',sdiv:'bvsdiv',smod:'bvsrem',shl:'bvshl',shr:'bvlshr',sar:'bvashr',slt:'bvslt',sgt:'bvsgt'},op=ops[name];
   if(!op)fail('YUL_STATE_WORD_OPERATION_PENDING:'+name);
   const values=['shl','shr','sar'].includes(name)?[b,a]:args;
   const expr='('+op+' '+values.map(v=>'((_ int2bv 256) '+v.term+')').join(' ')+')';
   term=['slt','sgt'].includes(name)?'(ite '+expr+' 1 0)':'(bv2int '+expr+')';
   if(['sdiv','smod'].includes(name))term='(ite (= '+y+' 0) 0 '+term+')';
  }
  let known=null;if(args.every(v=>v.known!==null))known=evmWord(name,args.map(v=>v.known));
  if(name==='add'){const loc=shiftedLocation(a,b)||shiftedLocation(b,a);if(loc)metadata.storage_location=loc;}
  return word(term,known,metadata);
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
  // One lexical declaration has one binding across every branch entering the
  // next statement. Allocating it once per path would overwrite the shared
  // name map and leave earlier sibling states looking up the wrong local.
  for(const child of n.statements||[])if(child.nodeType==='YulVariableDeclaration')for(const v of child.variables)bindings.set(v.name,'yl_'+serial++);
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
   for(const [i,v]of n.variables.entries()){const key=bindings.get(v.name);if(!key)fail('YUL_DECLARATION_BINDING_UNAVAILABLE');s.locals.set(key,values[i]);}return [s];
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
 const entry=clone(state);delete entry.yul_state_memory;delete entry.yul_entry_memory;delete entry.yul_known_words;delete entry.yul_external_words;
 return yBlock(assembly.AST,entry,new Map(),new Map()).map(s=>{delete s.yul_state_memory;delete s.yul_entry_memory;delete s.yul_known_words;delete s.yul_external_words;return s;});
}
