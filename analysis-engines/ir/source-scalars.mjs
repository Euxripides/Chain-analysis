import{id,unique}from'../model.mjs';

// A binding points at the value captured at one source evaluation. Changing a
// declaration later creates a version; it never changes an older operand.
export class SourceScalars {
 constructor(result,method,{frameId='entry',budget={versions:0,maxVersions:4096,maxPerMethod:512},shared=null,bindings=null}={}){
  this.result=result;this.method=method;this.framePath=frameId;this.frameId=id(method.method_id,'source-scalar-frame',frameId);this.budget=budget;
  budget.versions??=0;budget.maxVersions??=4096;budget.maxPerMethod??=512;
  this.shared=shared||{counter:0,versions:new Map(),methodVersions:0};this.bindings=bindings||new Map();
  this.shared.expressionOperands||=new Map();
  result.sections.source_scalar_versions||=[];result.sections.source_scalar_phis||=[];
 }
 fork({newFunction=false,frameId=this.framePath}={}){
  return new SourceScalars(this.result,this.method,{frameId,budget:this.budget,shared:this.shared,bindings:newFunction?new Map():new Map(this.bindings)});
 }
 get(declaration){const key=typeof declaration==='object'?declaration.id:declaration;return this.shared.versions.get(this.bindings.get(key))||null;}
 version(key){return this.shared.versions.get(key)||null;}
 limit(){
  const key=id(this.method.method_id,'source-scalar-version-budget');
  if(!this.shared.versions.has(key)){
   const row={source_scalar_version_id:key,method_id:this.method.method_id,frame_id:null,declaration_id:null,type:null,
    value_expression_id:null,parent_source_scalar_version_ids:[],status:'partial',unresolved:['SOURCE_SCALAR_VERSION_BUDGET'],evidence_ids:[...(this.method.evidence_ids||[])]};
   this.shared.versions.set(key,row);this.result.sections.source_scalar_versions.push(row);
  }
  return key;
 }
 capture(declaration,valueExpression,{context={},evidence_ids=[],definition_id=null,kind='assignment',parents=null,unresolved=[],extra={}}={}){
  let key;
  if(this.budget.versions>=this.budget.maxVersions||this.shared.methodVersions>=this.budget.maxPerMethod)key=this.limit();
  else{
   key=id(this.method.method_id,this.frameId,declaration.id,'source-scalar-value',this.shared.counter++);
   const previous=this.bindings.get(declaration.id),missing=unique([...unresolved,...(!valueExpression?['SOURCE_SCALAR_VALUE_UNAVAILABLE']:[])]);
   const row={source_scalar_version_id:key,method_id:this.method.method_id,frame_id:this.frameId,source_frame_path:this.framePath,frame_basis:'source_invocation_scope_not_observed_EVM_frame',declaration_id:declaration.id,type:declaration.type,
    value_expression_id:valueExpression||null,parent_source_scalar_version_ids:parents||[],previous_source_scalar_version_id:previous||null,definition_id,kind,
    path:context.path||'',conditions:[...(context.conditions||[])],step_order:context.order??null,
    status:missing.length?'partial':'source_value_captured',unresolved:missing,evidence_ids:[...evidence_ids],
    validation_scope:'compiler_typed_local_value_not_whole_execution_or_arithmetic_proof',...extra};
   this.shared.versions.set(key,row);this.result.sections.source_scalar_versions.push(row);this.budget.versions++;this.shared.methodVersions++;
  }
  this.bindings.set(declaration.id,key);return key;
 }
 invalidate(declarations,reason,context={}){
  for(const declaration of declarations)this.capture(declaration,null,{context,kind:'unresolved_write',unresolved:[reason],evidence_ids:context.evidence_ids||[]});
 }
 merge(yes,no,{conditionExpression,yesTerminated=false,noTerminated=false,context={},createPhi,declarations}={}){
  if(yesTerminated&&noTerminated)return;
  if(yesTerminated||noTerminated){this.bindings=new Map((yesTerminated?no:yes).bindings);return;}
  const keys=new Set([...this.bindings.keys(),...yes.bindings.keys(),...no.bindings.keys()]);
  for(const key of keys){
   const a=yes.get(key),b=no.get(key);
   if(a?.source_scalar_version_id===b?.source_scalar_version_id){if(a)this.bindings.set(key,a.source_scalar_version_id);else this.bindings.delete(key);continue;}
   const declaration=declarations(key);if(!declaration)throw Error('SOURCE_SCALAR_JOIN_DECLARATION_UNAVAILABLE');
   const unknown=unique([...(a?.unresolved||['SOURCE_SCALAR_TRUE_VALUE_UNAVAILABLE']),...(b?.unresolved||['SOURCE_SCALAR_FALSE_VALUE_UNAVAILABLE']),
    ...(!conditionExpression?['SOURCE_SCALAR_JOIN_CONDITION_UNAVAILABLE']:[])]);
   const operands=[conditionExpression,a?.value_expression_id||null,b?.value_expression_id||null],expression=operands.every(Boolean)?createPhi(declaration,operands,context):null;
   const parents=[a?.source_scalar_version_id,b?.source_scalar_version_id].filter(Boolean),phi=id(this.method.method_id,this.frameId,key,context.path,context.ast_id,parents,'source-scalar-phi');
   const value=this.capture(declaration,expression,{context,kind:'conditional_join',parents,unresolved:unknown,
    evidence_ids:unique([...(a?.evidence_ids||[]),...(b?.evidence_ids||[])]),extra:{source_scalar_phi_id:phi}});
   if(value===id(this.method.method_id,'source-scalar-version-budget'))continue;
   this.result.sections.source_scalar_phis.push({source_scalar_phi_id:phi,source_scalar_version_id:value,method_id:this.method.method_id,
    frame_id:this.frameId,source_frame_path:this.framePath,frame_basis:'source_invocation_scope_not_observed_EVM_frame',declaration_id:key,type:declaration.type,condition_expression_id:conditionExpression||null,
    cases:[{expected:true,source_scalar_version_id:a?.source_scalar_version_id||null},{expected:false,source_scalar_version_id:b?.source_scalar_version_id||null}],
    value_expression_id:expression||null,path:context.path||'',status:expression&&!unknown.length?'conditional_source_value':'partial',unresolved:unknown,
    evidence_ids:unique([...(a?.evidence_ids||[]),...(b?.evidence_ids||[])])});
  }
 }
}
