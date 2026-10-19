import{id,unique}from'../model.mjs';

const clone=value=>structuredClone(value),same=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
const validType=type=>type?.kind==='struct'&&Number.isSafeInteger(type.declaration_id)&&!type.type?.includes('[');

// Local names hold references. Each heap update creates a new immutable value
// version, so reads captured before a possible write retain their old value.
export class SourceAggregates{
 constructor(result,method,{frameId='entry',budget={},shared=null,references=null,heap=null,onExpression=null}={}){
  this.result=result;this.method=method;this.framePath=frameId;this.frameId=id(method.method_id,'source-aggregate-frame',frameId);this.budget=budget;
  for(const[key,value]of Object.entries({versions:0,records:0,maxVersions:2048,maxPerMethod:256,maxRecords:8192,maxFields:32,maxDepth:4}))budget[key]??=value;
  this.shared=shared||{serial:0,methodVersions:0,versions:new Map(),types:new Map(),frontiers:new Set(),onExpression};this.references=references||new Map();this.heap=heap||{current:new Map()};
  this.shared.evaluationGaps||=new Map();
  result.sections.expressions||=[];result.sections.dataflow||=[];result.sections.frontier||=[];
 }
 fork({newFunction=false,frameId=this.framePath,shareHeap=newFunction}={}){
  return new SourceAggregates(this.result,this.method,{frameId,budget:this.budget,shared:this.shared,references:newFunction?new Map():new Map(this.references),heap:shareHeap?this.heap:{current:new Map(this.heap.current),opaqueMemoryEffects:[...(this.heap.opaqueMemoryEffects||[])]}});
 }
 unknown(type,reason,context={}){
  if(!this.shared.frontiers.has(reason)){
   this.shared.frontiers.add(reason);this.result.sections.frontier.push({frontier_id:id(this.method.method_id,'source-aggregate-frontier',reason),method_id:this.method.method_id,reason,status:'partial',path:context.path||this.framePath,evidence_ids:[...(context.evidence_ids||[])]});
  }
  return{kind:'unresolved_aggregate_reference',type:clone(type||null),unresolved:[reason]};
 }
 get(declaration){return this.references.get(typeof declaration==='object'?declaration.id:declaration)||null;}
 _referenceWithinBudget(reference){
  let count=0;const visit=(r,depth)=>{if(!r||++count>64||depth>16)return false;if(r.kind==='conditional_aggregate_reference')return visit(r.yes,depth+1)&&visit(r.no,depth+1);return['aggregate_reference','unresolved_aggregate_reference'].includes(r.kind);};return visit(reference,0);
 }
 bind(declaration,reference,{context={},kind='assignment'}={}){
  const type=declaration.aggregate_type||reference?.type;
  if(reference&&!this._referenceWithinBudget(reference))reference=this.unknown(type,'SOURCE_AGGREGATE_REFERENCE_BUDGET',context);
  if(this.budget.records>=this.budget.maxRecords)reference=this.unknown(type,'SOURCE_AGGREGATE_RECORD_BUDGET',context);
  else{
   reference=reference?clone(reference):this.unknown(type,'SOURCE_AGGREGATE_REFERENCE_UNAVAILABLE',context);
   const key=id(this.method.method_id,this.frameId,'source-aggregate-binding',declaration.id,this.shared.serial++);
   this.result.sections.dataflow.push({dataflow_id:key,method_id:this.method.method_id,kind:'source_aggregate_reference_binding',binding_kind:kind,declaration_id:declaration.id,reference:clone(reference),frame_id:this.frameId,source_frame_path:this.framePath,path:context.path||this.framePath,conditions:[...(context.conditions||[])],evidence_ids:[...(context.evidence_ids||[])]});this.budget.records++;
  }
  this.references.set(declaration.id,reference);return clone(reference);
 }
 version(object){const state=this.heap.current.get(object);return state?.version_id?this.shared.versions.get(state.version_id)||null:null;}
 _objectDepth(reference,seen=new Set()){
  if(reference?.kind==='conditional_aggregate_reference'){
   const a=this._objectDepth(reference.yes,new Set(seen)),b=this._objectDepth(reference.no,new Set(seen));return a===null||b===null?null:Math.max(a,b);
  }
  if(reference?.kind!=='aggregate_reference')return null;if(seen.has(reference.object_id))return Infinity;seen.add(reference.object_id);
  const value=this.version(reference.object_id);if(!value?.fields)return null;let depth=1;
  for(const field of value.fields)if(field.reference){const nested=this._objectDepth(field.reference,new Set(seen));if(nested===null)return null;depth=Math.max(depth,1+nested);if(depth>this.budget.maxDepth)return depth;}
  return depth;
 }
 _emit(object,type,fields,{context={},unresolved=[],parents=[],evaluationExpressionIds=[],evaluationMayFail=false,mutation=null}={}){
  let reason=null;
  if(this.budget.versions>=this.budget.maxVersions||this.shared.methodVersions>=this.budget.maxPerMethod)reason='SOURCE_AGGREGATE_VERSION_BUDGET';
  else if(this.budget.records+1+(fields?.length||0)+evaluationExpressionIds.length>this.budget.maxRecords)reason='SOURCE_AGGREGATE_RECORD_BUDGET';
  if(reason){this.heap.current.set(object,{version_id:null,unresolved:unique([...unresolved,reason])});this.unknown(type,reason,context);return null;}
  const key=id(this.method.method_id,this.frameId,object,'source-aggregate-version',this.shared.serial++),row={expression_id:key,method_id:this.method.method_id,op:'SourceAggregateVersion',symbolic_format:'source_aggregate_version',type:type.type||null,aggregate_type_declaration_id:type.declaration_id,aggregate_object_id:object,aggregate_frame_id:this.frameId,source_frame_path:this.framePath,parent_aggregate_version_ids:unique(parents),parent_aggregate_expression_ids:unique(parents),fields:fields?clone(fields):null,evaluation_history_expression_ids:unique(evaluationExpressionIds),evaluation_history_may_fail:evaluationMayFail,source_aggregate_mutation:clone(mutation),operands:(fields||[]).flatMap(f=>f.value_expression_id?[f.value_expression_id]:[]),path:context.path||this.framePath,conditions:[...(context.conditions||[])],evidence_ids:[...(context.evidence_ids||[])],status:unresolved.length?'partial':'source_aggregate_value_captured',unresolved_model_reasons:[...unresolved],validation_scope:'compiler_typed_memory_value_not_whole_execution_proof'};
  this.shared.versions.set(key,row);this.result.sections.expressions.push(row);this.shared.onExpression?.(row);this.heap.current.set(object,{version_id:key,unresolved:[...unresolved]});this.budget.versions++;this.shared.methodVersions++;this.budget.records+=1+(fields?.length||0)+evaluationExpressionIds.length;return row;
 }
 allocate(type,fields,{context={},depth=1}={}){
  if(!validType(type))return this.unknown(type,'SOURCE_AGGREGATE_TYPE_UNSUPPORTED',context);
  if(depth>this.budget.maxDepth)return this.unknown(type,'SOURCE_AGGREGATE_NESTING_BUDGET',context);
  if(fields.length>this.budget.maxFields)return this.unknown(type,'SOURCE_AGGREGATE_FIELD_BUDGET',context);
  if(fields.some(f=>!Number.isSafeInteger(f.declaration_id))||new Set(fields.map(f=>f.declaration_id)).size!==fields.length)return this.unknown(type,'SOURCE_AGGREGATE_FIELD_IDENTITY_UNAVAILABLE',context);
  if(fields.some(f=>!f.value_expression_id&&!f.reference&&!f.field_gap))return this.unknown(type,'SOURCE_AGGREGATE_FIELD_VALUE_UNAVAILABLE',context);
  if(fields.some(f=>f.reference&&!this._referenceWithinBudget(f.reference)))return this.unknown(type,'SOURCE_AGGREGATE_REFERENCE_BUDGET',context);
  for(const field of fields)if(field.reference){const nested=this._objectDepth(field.reference);if(nested===null)return this.unknown(type,'SOURCE_AGGREGATE_NESTED_VALUE_UNAVAILABLE',context);if(nested+1>this.budget.maxDepth)return this.unknown(type,'SOURCE_AGGREGATE_NESTING_BUDGET',context);}
  if(this.budget.versions>=this.budget.maxVersions||this.shared.methodVersions>=this.budget.maxPerMethod)return this.unknown(type,'SOURCE_AGGREGATE_VERSION_BUDGET',context);
  if(this.budget.records+fields.length+1>this.budget.maxRecords)return this.unknown(type,'SOURCE_AGGREGATE_RECORD_BUDGET',context);
  const object=id(this.method.method_id,this.frameId,'source-aggregate-object',context.ast_id,this.shared.serial++);this.shared.types.set(object,clone(type));
  const value=this._emit(object,type,fields,{context});return value?{kind:'aggregate_reference',object_id:object,type:clone(type)}:this.unknown(type,'SOURCE_AGGREGATE_ALLOCATION_UNAVAILABLE',context);
 }
 evaluationHistory(value){
  const pending=value?[value]:[],seen=new Set(),expressions=[];let mayFail=false;
  while(pending.length&&seen.size<this.budget.maxPerMethod){const row=pending.pop();if(!row||seen.has(row.expression_id))continue;seen.add(row.expression_id);expressions.push(...(row.evaluation_history_expression_ids||[]));mayFail||=row.evaluation_history_may_fail===true;for(const parent of row.parent_aggregate_version_ids||[])pending.push(this.shared.versions.get(parent));}
  return{expressions:unique(expressions),mayFail,unresolved:pending.length?['SOURCE_AGGREGATE_EVALUATION_HISTORY_BUDGET']:[]};
 }
 writeScalar(reference,field,valueExpression,{context={},evaluationMayFail=false,evaluationExpressionIds=[],evaluationGroup=null}={}){
  const fail=reason=>{this.invalidate(reference,reason,{context});return{status:'partial',reason};};
  if(reference?.kind!=='aggregate_reference')return fail('SOURCE_AGGREGATE_FIELD_WRITE_TARGET_UNAVAILABLE');
  const previous=this.version(reference.object_id),state=this.heap.current.get(reference.object_id),type=this.shared.types.get(reference.object_id);
  if(this.heap.opaqueMemoryEffects?.length||!previous?.fields||state?.unresolved?.length)return fail('SOURCE_AGGREGATE_FIELD_WRITE_PRIOR_HEAP_UNAVAILABLE');
  const old=previous.fields.find(f=>f.declaration_id===field.declaration_id);
  if(!old||old.reference||old.type!==field.type||!valueExpression)return fail('SOURCE_AGGREGATE_FIELD_WRITE_VALUE_UNAVAILABLE');
  const fields=previous.fields.map(f=>f===old?{declaration_id:f.declaration_id,type:f.type,value_expression_id:valueExpression,evaluation_may_fail:evaluationMayFail,...(evaluationGroup?{evaluation_group_id:evaluationGroup}:{})}:clone(f));
  const row=this._emit(reference.object_id,type,fields,{context,parents:[previous.expression_id],evaluationExpressionIds:unique([...(old.value_expression_id?[old.value_expression_id]:[]),...(old.evaluation_group_id?[old.evaluation_group_id]:[]),...evaluationExpressionIds]),evaluationMayFail:previous.fields.some(f=>f.evaluation_may_fail&&(!evaluationGroup||f.evaluation_group_id!==evaluationGroup)),mutation:{kind:'compiler_scalar_field_assignment',field_declaration_id:field.declaration_id,value_expression_id:valueExpression,previous_version_expression_id:previous.expression_id}});
  return row?{status:'source_aggregate_field_written',version_expression_id:row.expression_id}:{status:'partial',reason:'SOURCE_AGGREGATE_VERSION_OR_RECORD_BUDGET'};
 }
 project(reference,field,{context={},depth=1}={}){
  if(depth>this.budget.maxDepth+16)return{kind:'unresolved_aggregate_value',type:field.type,unresolved:['SOURCE_AGGREGATE_PROJECTION_BUDGET'],aggregate_version_ids:[]};
  if(reference?.kind==='conditional_aggregate_reference'){
   const yes=this.project(reference.yes,field,{context,depth:depth+1}),no=this.project(reference.no,field,{context,depth:depth+1});
   return{kind:'conditional_aggregate_value',condition_expression_id:reference.condition_expression_id,yes,no,type:field.type,aggregate_return_expression_ids:unique([...(reference.aggregate_return_expression_ids||[]),...(yes.aggregate_return_expression_ids||[]),...(no.aggregate_return_expression_ids||[])]),aggregate_version_ids:unique([...(reference.aggregate_version_ids||[]),...(yes.aggregate_version_ids||[]),...(no.aggregate_version_ids||[])])};
  }
  const failed=(reasons,versions=[])=>({kind:'unresolved_aggregate_value',type:field.type,unresolved:unique(reasons),aggregate_version_ids:unique([...(reference?.aggregate_version_ids||[]),...versions])});
  if(reference?.kind!=='aggregate_reference')return failed(reference?.unresolved||['SOURCE_AGGREGATE_REFERENCE_UNAVAILABLE']);
  const state=this.heap.current.get(reference.object_id),value=this.version(reference.object_id);
  if(!value||state.unresolved.length)return failed(state?.unresolved?.length?state.unresolved:['SOURCE_AGGREGATE_VERSION_UNAVAILABLE'],value?[value.expression_id]:[]);
  const found=value.fields?.find(f=>f.declaration_id===field.declaration_id);if(!found)return failed(['SOURCE_AGGREGATE_FIELD_UNAVAILABLE']);
  const history=this.evaluationHistory(value),versions=unique([...(reference.aggregate_version_ids||[]),value.expression_id]),evaluation=unique([...(reference.aggregate_evaluation_expression_ids||[]),...history.expressions,...(value.fields||[]).flatMap(f=>[f.value_expression_id,f.evaluation_group_id].filter(Boolean))]),
    unresolved=unique([...(reference.unresolved||[]),...history.unresolved,...(history.mayFail?['SOURCE_AGGREGATE_PRIOR_EVALUATION_REQUIRES_FAILURE_MODEL']:[]),...((value.fields||[]).some(f=>f!==found&&f.evaluation_may_fail&&(!found.evaluation_group_id||f.evaluation_group_id!==found.evaluation_group_id))?['SOURCE_AGGREGATE_SIBLING_EVALUATION_REQUIRES_FAILURE_MODEL']:[])]);
  if(found.reference){const nested=clone(found.reference);nested.aggregate_version_ids=unique([...(nested.aggregate_version_ids||[]),...versions]);nested.aggregate_evaluation_expression_ids=unique([...(nested.aggregate_evaluation_expression_ids||[]),...evaluation]);nested.aggregate_return_expression_ids=unique([...(nested.aggregate_return_expression_ids||[]),...(reference.aggregate_return_expression_ids||[])]);nested.unresolved=unique([...(nested.unresolved||[]),...unresolved]);return{kind:'nested_aggregate_reference',reference:nested,type:found.type,aggregate_version_ids:versions};}
  if(found.field_gap)return failed([found.field_gap]);
  if(!found.value_expression_id)return failed(['SOURCE_AGGREGATE_FIELD_VALUE_UNAVAILABLE']);
  return{kind:'source_aggregate_field_value',value_expression_id:found.value_expression_id,type:found.type,field_declaration_id:found.declaration_id,aggregate_version_ids:versions,aggregate_evaluation_expression_ids:evaluation,aggregate_return_expression_ids:reference.aggregate_return_expression_ids||[],unresolved};
 }
 invalidate(reference,reason,{context={},recursive=true}={}){
  const objects=new Set();let unknownTarget=false;
  const gather=ref=>{
   if(ref?.kind==='conditional_aggregate_reference'){gather(ref.yes);gather(ref.no);return;}
   if(ref?.kind!=='aggregate_reference'){unknownTarget=true;return;}if(objects.has(ref.object_id))return;objects.add(ref.object_id);
   if(recursive)for(const f of this.version(ref.object_id)?.fields||[])if(f.reference)gather(f.reference);
  };gather(reference);
  if(unknownTarget){this.invalidateAll(reason,context);return;}
  for(const object of objects){const previous=this.version(object);this._emit(object,this.shared.types.get(object),null,{context,unresolved:[reason],parents:previous?[previous.expression_id]:[]});}
 }
 invalidateAll(reason,context={}){this.heap.opaqueMemoryEffects=unique([...(this.heap.opaqueMemoryEffects||[]),reason]);for(const object of[...this.heap.current.keys()])this.invalidate({kind:'aggregate_reference',object_id:object},reason,{context,recursive:false});}
 merge(yes,no,{conditionExpression,yesTerminated=false,noTerminated=false,context={},createPhi=null}={}){
  if(yesTerminated&&noTerminated)return;
  if(yesTerminated||noTerminated){const live=yesTerminated?no:yes;this.references=new Map(live.references);this.heap.current=new Map(live.heap.current);this.heap.opaqueMemoryEffects=[...(live.heap.opaqueMemoryEffects||[])];return;}
  this.heap.opaqueMemoryEffects=unique([...(yes.heap.opaqueMemoryEffects||[]),...(no.heap.opaqueMemoryEffects||[])]);
  for(const object of new Set([...yes.heap.current.keys(),...no.heap.current.keys()])){
   const a=yes.heap.current.get(object),b=no.heap.current.get(object);
   if(!a||!b){this.heap.current.set(object,clone(a||b));continue;}
   if(same(a,b)){if(a)this.heap.current.set(object,clone(a));continue;}
   const parents=[a?.version_id,b?.version_id].filter(Boolean),left=this.shared.versions.get(a.version_id),right=this.shared.versions.get(b.version_id),fields=[];
   let joinable=Boolean(conditionExpression&&createPhi&&left?.fields&&right?.fields&&!a.unresolved?.length&&!b.unresolved?.length&&left.fields.length===right.fields.length);
   if(joinable)for(const field of left.fields){
    const other=right.fields.find(f=>f.declaration_id===field.declaration_id);
    if(!other||field.type!==other.type){joinable=false;break;}
    if(same(field,other)){fields.push(clone(field));continue;}
    if(field.reference||other.reference||!field.value_expression_id||!other.value_expression_id){joinable=false;break;}
    fields.push({declaration_id:field.declaration_id,type:field.type,value_expression_id:createPhi(field,[conditionExpression,field.value_expression_id,other.value_expression_id]),evaluation_may_fail:field.evaluation_may_fail||other.evaluation_may_fail});
   }
   this._emit(object,this.shared.types.get(object),joinable?fields:null,{context,parents,unresolved:joinable?[]:['SOURCE_AGGREGATE_BRANCH_WRITE_JOIN'],mutation:joinable?{kind:'compiler_scalar_field_branch_merge',condition_expression_id:conditionExpression}:null});
  }
  for(const declaration of new Set([...yes.references.keys(),...no.references.keys()])){
   const a=yes.references.get(declaration),b=no.references.get(declaration);
   if(same(a,b)){if(a)this.references.set(declaration,clone(a));continue;}
   const type=a?.type||b?.type;
   if(!conditionExpression||!a||!b||!(validType(a.type)&&validType(b.type)&&a.type.declaration_id===b.type.declaration_id)){this.references.set(declaration,this.unknown(type,'SOURCE_AGGREGATE_REFERENCE_JOIN_UNAVAILABLE',context));continue;}
   const joined={kind:'conditional_aggregate_reference',type:clone(type),condition_expression_id:conditionExpression,yes:a,no:b};
   this.references.set(declaration,this._referenceWithinBudget(joined)?clone(joined):this.unknown(type,'SOURCE_AGGREGATE_REFERENCE_BUDGET',context));
  }
 }
}
