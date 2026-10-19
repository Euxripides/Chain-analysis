import{id,hash,unique}from'../model.mjs';
import{ByteMemory}from'./memory.mjs';
import{encodeAbi,AbiFault}from'./abi.mjs';

const clone=value=>structuredClone(value),binding=version=>({cases:[{source_memory_version_id:version,conditions:[]}],unresolved:[]});
const versionsOf=value=>unique((value?.cases||[]).map(c=>c.source_memory_version_id).filter(Boolean));


const falsePredicate=()=>({op:'constant',value:false});
const predicateNot=p=>!p?falsePredicate():p.op==='constant'?(p.value?falsePredicate():null):p.op==='not'?p.value:p.op==='atom'||p.op==='choice'?{...p,expected:!p.expected}:{op:'not',value:p};
const predicateJoin=(op,values)=>{
 const terms=[],seen=new Set();for(const p of values){if(!p){if(op==='or')return null;continue;}if(p.op==='constant'){if(!p.value){if(op==='and')return p;continue;}if(op==='or')return null;continue;}
  for(const item of p.op===op?p.terms:[p]){const key=JSON.stringify(item);if(!seen.has(key)){seen.add(key);terms.push(item);}}}
 return terms.length?terms.length===1?terms[0]:{op,terms}:op==='and'?null:falsePredicate();
};
const predicateAnd=(...terms)=>predicateJoin('and',terms),predicateOr=terms=>predicateJoin('or',terms);
export const sourceBytePredicateAnd=(...terms)=>predicateAnd(...terms);
function predicateStatus(predicate){
 let nodes=0,budget=false;const inspect=(p,depth=0)=>{if(!p)return;if(++nodes>256||depth>32){budget=true;return;}if(p.op==='not')inspect(p.value,depth+1);else for(const t of p.terms||[])inspect(t,depth+1);};inspect(predicate);if(budget)return'budget';
 const known=new Map();let contradiction=false;const key=p=>p.op==='atom'?'expression:'+p.expression_id:'choice:'+p.choice_id;
 const required=p=>{if(!p)return;if(p.op==='constant'){if(!p.value)contradiction=true;return;}if(p.op==='and'){p.terms.forEach(required);return;}if(p.op==='atom'||p.op==='choice'){const k=key(p);if(known.has(k)&&known.get(k)!==p.expected)contradiction=true;known.set(k,p.expected);}};required(predicate);if(contradiction)return'false';
 const evaluate=p=>{if(!p)return true;if(p.op==='constant')return p.value;if(p.op==='atom'||p.op==='choice')return known.has(key(p))?known.get(key(p))===p.expected:null;if(p.op==='not'){const v=evaluate(p.value);return v===null?null:!v;}const values=p.terms.map(evaluate);return p.op==='and'?values.includes(false)?false:values.every(v=>v===true)?true:null:values.includes(true)?true:values.every(v=>v===false)?false:null;};return evaluate(predicate)===false?'false':'possible';
}

// These addresses are relative to one compiler-created bytes object. They are
// not guessed absolute heap pointers and do not reset pre-assembly memory.
export class SourceByteMemory {
 constructor(result,method,{allocatorKnown=false,allocatorInvalidated=false,budget={objects:0,cells:0,maxObjects:1024,maxCells:262144},shared=null,bindings=null,returns=null,heap=null,exits=null,predicate=null,infeasible=false}={}){
  this.result=result;this.method=method;this.allocatorKnown=allocatorKnown;this.allocatorInvalidated=allocatorInvalidated;this.budget=budget;
  this.budget.versions??=0;this.budget.maxVersions??=4096;
  this.shared=shared||{objects:new Map(),counter:0,cells:0,allocations:0,expressionBindings:new Map(),callReturns:new Map(),byteVersionIDs:new Set()};this.bindings=bindings||new Map();this.returns=returns||[];this.heap=heap||new Map();this.exits=exits||[];this.predicate=predicate;this.infeasible=infeasible;
  for(const name of['source_memory_versions','source_memory_phis','memory_versions'])result.sections[name]||=[];
 }
 fork({newFunction=false}={}){return new SourceByteMemory(this.result,this.method,{allocatorKnown:this.allocatorKnown,allocatorInvalidated:this.allocatorInvalidated,budget:this.budget,shared:this.shared,bindings:new Map(this.bindings),returns:newFunction?[]:this.returns,heap:new Map(this.heap),exits:newFunction?[]:this.exits,predicate:this.predicate,infeasible:this.infeasible});}
 get(declaration){return this.current(this.bindings.get(declaration)||{cases:[],unresolved:['SOURCE_BYTES_BINDING_UNAVAILABLE']});}
 bind(declaration,value){this.bindings.set(declaration,value?clone(value):{cases:[],unresolved:['SOURCE_BYTES_BINDING_UNAVAILABLE']});}

 // References carry an object identity. Immutable versions are dereferenced at
 // consumption; recorded CALL inputs and return exits retain their own bytes.
 _object(candidate){return candidate?.source_memory_object_id||this.entry(candidate?.source_memory_version_id)?.record.source_memory_object_id||null;}

 assume(predicate){this.predicate=predicateAnd(this.predicate,predicate);const status=predicateStatus(this.predicate);this.infeasible||=status==='false';if(status==='budget')this.predicateBudget=true;}
 _bounded(value,reason='SOURCE_BYTES_HEAP_CASE_BUDGET'){
  const seen=new Set(),cases=[];for(const c of value.cases||[]){const status=predicateStatus(c.predicate);if(status==='budget')return{cases:[],unresolved:unique([...(value.unresolved||[]),'SOURCE_BYTES_PREDICATE_BUDGET'])};if(status==='false')continue;
   const row={...c,conditions:unique(c.conditions||[])},key=JSON.stringify(row);if(!seen.has(key)){seen.add(key);cases.push(row);}}
  return cases.length>16?{cases:[],unresolved:unique([...(value.unresolved||[]),reason])}:{...value,cases,unresolved:unique([...(value.unresolved||[]),...(this.predicateBudget?['SOURCE_BYTES_PREDICATE_BUDGET']:[])])};
 }
 current(value){
  if(!value)return{cases:[],unresolved:['SOURCE_BYTES_BINDING_UNAVAILABLE']};if(this.infeasible)return{cases:[],unresolved:[],infeasible:true};
  const cases=[],unresolved=[...(value.unresolved||[])],phis=[];
  for(const c of value.cases||[]){const object=this._object(c);
   if(!object||c.reference_kind==='snapshot'){cases.push(clone(c));continue;}
   const state=this.heap.get(object);if(!state){unresolved.push('SOURCE_BYTES_OBJECT_STATE_UNAVAILABLE');continue;}
   unresolved.push(...(state.unresolved||[]));if(state.source_memory_phi_id)phis.push(state.source_memory_phi_id);
   for(const v of state.cases||[])cases.push({...c,...v,source_memory_object_id:object,reference_kind:'live_object',predicate:predicateAnd(this.predicate,c.predicate,v.predicate),conditions:unique([...(c.conditions||[]),...(v.conditions||[])])});
  }
  return this._bounded({...value,cases,unresolved,source_memory_heap_phi_ids:unique(phis)});
 }
 snapshot(value){const resolved=this.current(value);return{...resolved,cases:resolved.cases.map(c=>({...c,reference_kind:'snapshot'}))};}
 recordExit(conditions=[]){if(this.infeasible)return;if(this.exits.length>=16){this.exits.budgetExceeded=true;return;}this.exits.push({conditions:[...conditions],predicate:this.predicate,heap:new Map([...this.heap].map(([o,v])=>[o,clone(v)]))});}
 _mergeHeaps(branches,context={}){
  const merged=new Map(),objects=unique(branches.flatMap(b=>[...b.heap.keys()]));
  for(const object of objects){const states=branches.map(b=>b.heap.get(object));if(states.every(Boolean)&&states.every(state=>hash(state)===hash(states[0]))){merged.set(object,clone(states[0]));continue;}const cases=[],unresolved=[];
   for(const b of branches){const state=b.heap.get(object);if(!state)continue;unresolved.push(...(state.unresolved||[]));for(const c of state.cases||[])cases.push({...c,predicate:predicateAnd(b.predicate,c.predicate),conditions:unique([...(b.conditions||[]),...(c.conditions||[])])});}
   const state=this._bounded({cases,unresolved});
   if(state.cases.length>1){state.source_memory_phi_id=id(this.method.method_id,'source-bytes-heap-phi',object,context.path||'',state.cases);
    this.result.sections.source_memory_phis.push({source_memory_phi_id:state.source_memory_phi_id,source_memory_object_id:object,method_id:this.method.method_id,merge_kind:'object_current_version',cases:clone(state.cases),unresolved:state.unresolved,status:state.unresolved.length?'partial':'conditional',evidence_ids:[]});}
   merged.set(object,state);
  }return merged;
 }
 entry(version){return this.shared.objects.get(version);}
 saveMemory(memory){for(const row of memory.transition_records)if(!this.shared.byteVersionIDs.has(row.memory_version_id)){
  this.shared.byteVersionIDs.add(row.memory_version_id);this.result.sections.memory_versions.push({...row,method_id:this.method.method_id,code_hash:this.method.code_hash,
   engine:'source',frame_basis:'compiler_object_relative_not_observed_absolute_EVM_frame',source_memory_object_id:memory.frame_id,evidence_ids:row.context?.evidence_ids||[]});}
  return memory;}
 versionFrontier(){const source_memory_version_id=id(this.method.method_id,'source-memory-version-budget');if(!this.shared.objects.has(source_memory_version_id)){
  const record={source_memory_version_id,source_memory_object_id:null,parent_source_memory_version_id:null,method_id:this.method.method_id,
   status:'partial',valid_encoding:false,allocator_known:false,byte_length:null,unresolved:['SOURCE_MEMORY_VERSION_BUDGET'],evidence_ids:[]};
  this.result.sections.source_memory_versions.push(record);this.shared.objects.set(source_memory_version_id,{record,memory:null,argumentNodes:[]});}
  return binding(source_memory_version_id);}
 single(value){value=this.current(value);const versions=versionsOf(value);if(value?.unresolved?.length||versions.length!==1||(value?.cases||[]).some(c=>!c.source_memory_version_id))return null;const entry=this.entry(versions[0]);return entry?.record.valid_encoding&&entry.record.allocator_known?{...entry,cases:clone(value.cases)}:null;}
 conditionalSingle(value){value=this.current(value);const exact=this.single(value);if(exact)return exact;const versions=versionsOf(value);if(value?.unresolved?.length||versions.length!==1||value.cases.some(c=>!c.source_memory_version_id))return null;
  const entry=this.entry(versions[0]),r=entry?.record;return r?.allocator_status==='unbound_internal_entry'&&r.payload_complete&&r.encoding!=='raw_bytes'&&r.unresolved.every(s=>s==='SOURCE_BYTES_ALLOCATOR_UNVERIFIED')?
   {...entry,cases:clone(value.cases),assumptions:['caller_preserves_compiler_memory_allocator']}:null;}
 capture({originExpressionId,encoding,selector=null,selectorPresent=false,selectorExpression=null,types=[],values=[],rawCells=null,argumentExpressionIds=[],argumentTexts=[],argumentNodes=[],context={},evidence_ids=[]}){
  if(this.infeasible)return{cases:[],unresolved:[],infeasible:true};
  if(this.shared.objects.size>=512||this.budget.versions>=this.budget.maxVersions)return this.versionFrontier();
  let encoded;
  try{encoded=rawCells?{status:'encoded',cells:clone(rawCells),head_cells:[],unresolved:[]}:encodeAbi(types,values,{identity:id(this.method.method_id,originExpressionId,context.path||'')});}
  catch(error){if(!(error instanceof AbiFault))throw error;encoded={status:error.status,byte_length:null,cells:null,head_cells:[],unresolved:[error.code]};}
  const prefix=selector&&/^0x[\da-f]{8}$/i.test(selector)?Array.from(Buffer.from(selector.slice(2),'hex'),value=>({kind:'constant',value})):
   selectorPresent?Array.from({length:4},(_,byte_index)=>selectorExpression?{kind:'word_byte',expression:'abi_left_align_4('+selectorExpression.expression+')',byte_index,
    source_expression_id:selectorExpression.expression_id,evidence_ids:selectorExpression.evidence_ids||[]}:{kind:'unknown',reason:'SOURCE_CALL_SELECTOR_UNRESOLVED'}):[];
  const cells=encoded.cells?[...prefix,...encoded.cells]:[...prefix,...encoded.head_cells],length=encoded.cells?cells.length:null;
  const reasons=[...encoded.unresolved,...prefix.filter(b=>b.kind==='unknown').map(b=>b.reason),...(!this.allocatorKnown?['SOURCE_BYTES_ALLOCATOR_UNVERIFIED']:[])];
  const object_id=id(this.method.method_id,'source-bytes-object',originExpressionId,context.path||'',this.shared.counter++);
  const source_memory_version_id=id(object_id,'initial'),record={source_memory_version_id,source_memory_object_id:object_id,parent_source_memory_version_id:null,
   method_id:this.method.method_id,code_hash:this.method.code_hash,origin_expression_id:originExpressionId,encoding,selector,
   argument_types:clone(types),argument_expression_ids:[...argumentExpressionIds],argument_texts:[...argumentTexts],
   byte_length:length===null?null:String(length),address_space:'compiler_object_relative',payload_offset:'32',allocator_known:this.allocatorKnown,
   allocator_status:this.allocatorKnown?'compiler_entry_or_preserved':this.allocatorInvalidated?'invalidated_by_unmodeled_effect':'unbound_internal_entry',
   status:encoded.status==='invalid'?'invalid':reasons.length?'partial':encoded.status,unresolved:unique(reasons),conditions:[...(context.conditions||[])],path:context.path||'',
   construction_order:context.order??null,valid_encoding:!rawCells&&Boolean(encoded.cells)&&!encoded.unresolved.length&&prefix.every(b=>b.kind!=='unknown')&&this.allocatorKnown,evidence_ids:[...evidence_ids]};
  if(this.shared.allocations>=128||this.budget.objects>=this.budget.maxObjects||this.shared.cells+cells.length>65536||this.budget.cells+cells.length>this.budget.maxCells){
   record.status='partial';record.unresolved.push('SOURCE_MEMORY_OBJECT_OR_CELL_BUDGET');record.valid_encoding=false;
   this.budget.versions++;this.result.sections.source_memory_versions.push(record);this.shared.objects.set(source_memory_version_id,{record,memory:null,argumentNodes:[]});this.heap.set(object_id,binding(source_memory_version_id));return this.current(binding(source_memory_version_id));
  }
  this.shared.allocations++;this.budget.objects++;this.shared.cells+=cells.length;this.budget.cells+=cells.length;
  let memory=this.saveMemory(new ByteMemory({frameId:object_id,entry:'solidity_assembly_entry'}));
  memory=this.saveMemory(memory.mstore(0,length===null?{expression:'source_bytes_length('+originExpressionId+')'}:BigInt(length),{evidence_ids}));
  if(cells.length)memory=this.saveMemory(memory.writeBytes(32,cells,{evidence_ids,origin_expression_id:originExpressionId}));
  record.object_memory_version_id=memory.version_id;record.payload_cells=clone(cells);record.payload_complete=Boolean(encoded.cells);
  this.budget.versions++;this.result.sections.source_memory_versions.push(record);this.shared.objects.set(source_memory_version_id,{record,memory,argumentNodes:[...argumentNodes]});this.heap.set(object_id,binding(source_memory_version_id));return this.current(binding(source_memory_version_id));
 }
 invalidate(value,reason,{index=null,byteValue=null,context={},wholeObject=false,evidence_ids=[]}={}){
  if(this.infeasible)return value||{cases:[],unresolved:[],infeasible:true};
  const original=value,possibleObjects=unique((value?.cases||[]).map(c=>this._object(c)).filter(Boolean)),definiteObject=!(value?.unresolved||[]).length&&possibleObjects.length===1&&value.cases.every(c=>this._object(c)===possibleObjects[0]);
  value=this.current(value);if(!definiteObject&&value.cases.some(c=>c.conditions?.length&&!c.predicate)){this.invalidateAll('SOURCE_BYTES_CONDITION_IDENTITY_UNAVAILABLE',context);return{cases:[],unresolved:['SOURCE_BYTES_CONDITION_IDENTITY_UNAVAILABLE']};}if((original?.unresolved||[]).length){this.invalidateAll('SOURCE_BYTES_UNKNOWN_WRITE_ALIAS',context);return this.current(original);}const changes=new Map();
  for(const version of versionsOf(value)){
   const previous=this.entry(version);if(!previous)continue;
   if(this.shared.objects.size>=512||this.budget.versions>=this.budget.maxVersions){changes.set(version,this.versionFrontier().cases[0].source_memory_version_id);continue;}
   const record=previous.record,next_id=id(version,'source-memory-write',reason,context.path||'',context.ast_id||null,this.shared.counter++);
   let memory=previous.memory;
   const knownIndex=typeof index==='bigint'?index:typeof index==='number'&&Number.isSafeInteger(index)?BigInt(index):null;
   if(memory){
    if(wholeObject)memory=memory.invalidate(null,null,reason,{evidence_ids});
    else if(knownIndex!==null&&record.byte_length!==null&&knownIndex>=0n&&knownIndex<BigInt(record.byte_length)&&byteValue!==null)memory=memory.mstore8(32n+knownIndex,byteValue,{evidence_ids});
    else memory=memory.invalidate(32,record.byte_length===null?null:BigInt(record.byte_length),reason,{evidence_ids});
    this.saveMemory(memory);
   }
   const next={...record,source_memory_version_id:next_id,parent_source_memory_version_id:version,valid_encoding:false,
    status:memory?'modified':'partial',unresolved:unique([...record.unresolved,reason]),byte_length:wholeObject?null:record.byte_length,
    object_memory_version_id:memory?.version_id||null,mutation:{reason,index:knownIndex===null?null:String(knownIndex),whole_object:wholeObject,context},evidence_ids:unique([...record.evidence_ids,...evidence_ids])};
   // Prior payload/argument provenance describes an earlier version. It must
   // never be reused as the encoded arguments after an alias was written.
   delete next.payload_cells;next.payload_complete=false;
   this.budget.versions++;this.result.sections.source_memory_versions.push(next);this.shared.objects.set(next_id,{record:next,memory,argumentNodes:[]});changes.set(version,next_id);
  }

  for(const object of unique(value.cases.map(c=>this._object(c)).filter(Boolean))){
   const targets=value.cases.filter(c=>this._object(c)===object),state=this.heap.get(object),cases=targets.map(c=>({...c,source_memory_version_id:changes.get(c.source_memory_version_id)||c.source_memory_version_id}));
   if(!definiteObject){const unconditional=targets.some(c=>!c.predicate&&!(c.conditions||[]).length),guards=targets.map(c=>'('+c.conditions.map(x=>'('+x+')').join(' && ')+')');
    if(!unconditional)for(const c of state?.cases||[])cases.push({...c,predicate:predicateAnd(c.predicate,predicateNot(predicateOr(targets.map(t=>t.predicate)))),conditions:unique([...(c.conditions||[]),'!('+guards.join(' || ')+')'])});}
   this.heap.set(object,this._bounded({cases,unresolved:unique([...(state?.unresolved||[]),...(original?.unresolved||[])])}));
  }
  return this.current(original);

 }
 invalidateAll(reason,context={}){
  if(this.infeasible)return;const versions=unique([...this.heap.values()].flatMap(versionsOf));this.invalidate({cases:versions.map(v=>({source_memory_version_id:v,conditions:[]})),unresolved:[]},reason,{context,wholeObject:true});this.allocatorKnown=false;this.allocatorInvalidated=true;
 }

 propagateCall(child,{allReturningPathsCovered=false}={}){
  const exits=child.exits;
  this.heap=exits.length&&allReturningPathsCovered?this._mergeHeaps(exits,{path:'internal-return'}):new Map(child.heap);
  if(exits.budgetExceeded){for(const[o,state]of this.heap)this.heap.set(o,{cases:[],unresolved:unique([...(state.unresolved||[]),'SOURCE_BYTES_RETURN_STATE_CASE_BUDGET'])});}
  else if(exits.length&&!allReturningPathsCovered){for(const[o,state]of this.heap)this.heap.set(o,{...state,unresolved:unique([...(state.unresolved||[]),'SOURCE_BYTES_RETURN_STATE_PATHS_UNCOVERED'])});}
  // Callee parameter rebinding remains local. Shared object versions propagate.
  for(const declaration of this.bindings.keys())if(child.bindings.has(declaration))this.bindings.set(declaration,clone(child.bindings.get(declaration)));
  this.allocatorKnown&&=child.allocatorKnown;this.allocatorInvalidated||=child.allocatorInvalidated;
 }

 merge(yes,no,{yesConditions=[],noConditions=[],yesTerminated=false,noTerminated=false,context={}}={}){
  if(yes.infeasible&&no.infeasible){this.infeasible=true;return;}
  if(yes.infeasible){yesTerminated=true;noTerminated=false;}if(no.infeasible){noTerminated=true;yesTerminated=false;}
  let yesPredicate=yes.predicate,noPredicate=no.predicate;
  if(JSON.stringify(yesPredicate)===JSON.stringify(noPredicate)&&(yesConditions.length||noConditions.length)){
   const choice=id(this.method.method_id,'source-memory-branch-choice',context.path||'',context.ast_id||null,this.shared.counter++);yesPredicate=predicateAnd(yesPredicate,{op:'choice',choice_id:choice,expected:true});noPredicate=predicateAnd(noPredicate,{op:'choice',choice_id:choice,expected:false});
  }

  if(yesTerminated&&!noTerminated){this.bindings=new Map(no.bindings);this.heap=new Map(no.heap);this.predicate=no.predicate;this.infeasible=no.infeasible;this.allocatorKnown=no.allocatorKnown;this.allocatorInvalidated=no.allocatorInvalidated;return;}
  if(noTerminated&&!yesTerminated){this.bindings=new Map(yes.bindings);this.heap=new Map(yes.heap);this.predicate=yes.predicate;this.infeasible=yes.infeasible;this.allocatorKnown=yes.allocatorKnown;this.allocatorInvalidated=yes.allocatorInvalidated;return;}
  this.heap=this._mergeHeaps([{heap:yes.heap,conditions:yesConditions,predicate:yesPredicate},{heap:no.heap,conditions:noConditions,predicate:noPredicate}],context);
  this.allocatorKnown=yes.allocatorKnown&&no.allocatorKnown;
  this.allocatorInvalidated=yes.allocatorInvalidated||no.allocatorInvalidated;
  for(const declaration of unique([...this.bindings.keys(),...yes.bindings.keys(),...no.bindings.keys()])){
   const a=yes.get(declaration),b=no.get(declaration),left=versionsOf(a),right=versionsOf(b);
   if(hash([a.cases,a.unresolved])===hash([b.cases,b.unresolved])){this.bindings.set(declaration,clone(a));continue;}
   if(!a.unresolved.length&&!b.unresolved.length&&left.length===1&&right.length===1&&left[0]===right[0]){this.bindings.set(declaration,binding(left[0]));continue;}
   const cases=[...a.cases.map(c=>({...c,predicate:predicateAnd(c.predicate,yesPredicate),conditions:unique([...yesConditions,...c.conditions])})),...b.cases.map(c=>({...c,predicate:predicateAnd(c.predicate,noPredicate),conditions:unique([...noConditions,...c.conditions])}))];
   if(!a.cases.length)cases.push({source_memory_version_id:null,conditions:[...yesConditions],unresolved:a.unresolved});
   if(!b.cases.length)cases.push({source_memory_version_id:null,conditions:[...noConditions],unresolved:b.unresolved});
   if(cases.length>16){this.bindings.set(declaration,{cases:[],unresolved:['SOURCE_BYTES_BRANCH_CASE_BUDGET']});continue;}
   const unresolved=unique([...a.unresolved,...b.unresolved]),phi_id=id(this.method.method_id,'source-bytes-phi',declaration,context.path||'',cases);
   this.result.sections.source_memory_phis.push({source_memory_phi_id:phi_id,method_id:this.method.method_id,declaration_id:declaration,cases:clone(cases),unresolved,status:unresolved.length?'partial':'conditional',evidence_ids:[]});
   this.bindings.set(declaration,{cases,unresolved,source_memory_phi_id:phi_id});
  }
 }
 addReturn(value,conditions=[],component=0){if(this.infeasible)return;this.returns.push({value:this.snapshot(value),conditions:[...conditions],component});}
 returned({allReturningPathsCovered=false,component=0}={}){if(this.infeasible)return{cases:[],unresolved:[],infeasible:true};const selected=this.returns.filter(r=>r.component===component),cases=selected.flatMap(r=>r.value.cases.map(c=>({...c,reference_kind:'live_object',conditions:unique([...r.conditions,...c.conditions])})));return{cases:cases.slice(0,16),unresolved:unique([
  ...selected.flatMap(r=>r.value.unresolved),...(!selected.length?['SOURCE_BYTES_RETURN_UNAVAILABLE']:[]),...(!allReturningPathsCovered?['SOURCE_BYTES_RETURN_PATHS_UNCOVERED']:[]),...(cases.length>16?['SOURCE_BYTES_RETURN_CASE_BUDGET']:[])])};}
 read(value){
  value=this.current(value);const cases=[],unresolved=[...(value?.unresolved||[])];
  for(const candidate of value?.cases||[]){const entry=this.entry(candidate.source_memory_version_id);
   if(!entry?.memory||entry.record.byte_length===null){unresolved.push(...(entry?.record.unresolved||['SOURCE_BYTES_VERSION_UNAVAILABLE']));continue;}
   const read=entry.memory.read(32,BigInt(entry.record.byte_length));unresolved.push(...read.unresolved,...(!entry.record.allocator_known?['SOURCE_BYTES_ALLOCATOR_UNVERIFIED']:[]));
   for(const c of read.cases)cases.push({...c,conditions:unique([...candidate.conditions,...c.conditions]),source_memory_version_id:candidate.source_memory_version_id,
    predicate:candidate.predicate||null,encoding_provenance_status:entry.record.valid_encoding?'unmodified_verified_source_encoding':'modified_or_unverified'});
  }
  return{cases,unresolved:unique(unresolved),status:unresolved.length||!cases.length?'partial':cases.length>1?'conditional':'exact'};
 }
}
