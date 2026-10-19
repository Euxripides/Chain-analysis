import test from'node:test';import assert from'node:assert/strict';import fs from'node:fs';import{createHash}from'node:crypto';
import{analyze}from'../analyze.mjs';import{evaluateReturn}from'../../scripts/contract-analysis/expression_model_eval.mjs';
const source=fs.readFileSync(new URL('./fixtures/storage-memory-copy.sol',import.meta.url),'utf8'),cache=new Map();
function inspect(optimized){
 if(cache.has(optimized))return cache.get(optimized);
 const material={kind:'source',source:{language:'Solidity',files:{'StorageCopyCases.sol':source},compiler_version:'0.8.30',target:'StorageCopyCases.sol:StorageCopyCases',settings:{optimizer:{enabled:optimized,runs:200},evmVersion:'shanghai'}}},raw=JSON.stringify(material),result=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:createHash('sha256').update(raw).digest('hex')},engines:['solc-ast']});
 assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');cache.set(optimized,result);return result;
}
function records(result,name){const method=result.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(method);const fund=result.sections.funds.find(f=>f.method_id===method.method_id&&f.kind==='native_payment');assert.ok(fund);const flow=result.sections.dataflow.find(d=>d.dataflow_id===fund.amount_derivation_id);assert.ok(flow);return{method,fund,flow};}
const snapshots=(result,method)=>result.sections.expressions.filter(e=>e.method_id===method.method_id&&e.symbolic_format==='source_storage_snapshot');
function valueReads(result,key){const expressions=new Map(result.sections.expressions.map(e=>[e.expression_id,e])),seen=new Set(),reads=[];const walk=id=>{if(!id||seen.has(id))return;seen.add(id);const e=expressions.get(id);assert.ok(e);if(e.symbolic_format==='source_storage_snapshot'){reads.push(e);return;}if(e.binding_expression_id)walk(e.binding_expression_id);else for(const operand of e.operands||[])walk(operand);};walk(key);return reads;}
function keyValue(result,method,key){return evaluateReturn({sections:{...result.sections,methods:[{...method,entry_return_expression_id:key}]}},method.name,{x:23n,to:1n})[0];}
for(const optimized of[false,true]){
 test(`storage copies retain separate old values across writes rebinding and memory aliases optimizer=${optimized}`,()=>{
  const r=inspect(optimized);
  for(const name of['plainThenStore','memoryAlias','rebindMemory','storageAliasKey','branchCopy','nestedCopy','twoCopies','callMemory','implicitMemoryArgument','copyThenRevert','assignmentCopy','recipientSwap','typedFields','nestedIndependentCopies','twoCopyKeyValues','byteKey'])assert.equal(records(r,name).fund.amount_derivation_status,'represented_source_dependencies_resolved',name);
  for(const name of['memoryAlias','callMemory','implicitMemoryArgument']){
   const{fund}=records(r,name),reads=valueReads(r,fund.amount_expression_id);assert.equal(reads.length,1,name);assert.equal(reads[0].storage_projection.path.at(-1).name,'bonus',name);
  }
  const{method,fund}=records(r,'twoCopies'),reads=valueReads(r,fund.amount_expression_id),bound=r.sections.expressions.filter(e=>e.method_id===method.method_id&&e.storage_value_bound===true);
  assert.equal(reads.length,1);assert.equal(bound.length,1);assert.equal(bound[0].storage_value_basis,'dominating_scalar_storage_write_before_copy');assert.ok(bound[0].source_storage_write_order<bound[0].source_order);assert.notEqual(reads[0].source_storage_copy_point_id,bound[0].source_storage_copy_point_id);assert.ok(snapshots(r,method).every(e=>e.storage_value_bound===false));
 });
 test(`dominating writes fold into the copy while compounds stay at the copy point optimizer=${optimized}`,()=>{
  const r=inspect(optimized),plain=records(r,'storeThenCopy');
  assert.equal(plain.fund.amount_derivation_status,'represented_source_dependencies_resolved');assert.match(plain.fund.expanded_amount_expression,/(^|[^A-Za-z_])x([^A-Za-z_]|$)/);
  const plainBound=r.sections.expressions.filter(e=>e.method_id===plain.method.method_id&&e.storage_value_bound===true);assert.equal(plainBound.length,1);assert.equal(plainBound[0].type,'uint128');
  const compound=records(r,'compoundThenCopy'),compoundBound=r.sections.expressions.filter(e=>e.method_id===compound.method.method_id&&e.storage_value_bound===true);
  assert.equal(compound.fund.amount_derivation_status,'represented_source_dependencies_resolved');assert.equal(compoundBound.length,0);assert.ok(snapshots(r,compound.method).every(e=>e.storage_value_bound===false));
  const enumCase=records(r,'enumCopy');assert.equal(enumCase.fund.amount_derivation_status,'represented_source_dependencies_resolved');
 });
 test(`mapping keys are frozen by their scalar values before later reassignment optimizer=${optimized}`,()=>{
  const r=inspect(optimized),{method}=records(r,'twoCopyKeyValues'),reads=snapshots(r,method),values=new Map();
  for(const e of reads){const part=e.storage_projection.path.find(p=>p.kind==='mapping');assert.ok(part);const key=keyValue(r,method,part.key_expression_id);values.set(e.source_storage_copy_point_id,key);}
  assert.deepEqual([...values.values()].sort((a,b)=>a<b?-1:a>b?1:0),[1n,2n]);
 });
 test(`fixed bytes keys keep their canonical word encoding in readable slot formulas optimizer=${optimized}`,()=>{
  const r=inspect(optimized),{method,fund}=records(r,'byteKey'),reads=snapshots(r,method);assert.equal(reads.length,2);assert.ok(fund.expanded_amount_expression.includes('keccak_words(storage_key<bytes4>(287454020), '));assert.ok(!fund.expanded_amount_expression.includes('keccak_words(287454020, '));
  for(const e of reads){assert.equal(e.storage_projection.slot_expression.args[0].encoding_type,'bytes4');assert.equal(e.source_storage_field_declaration_id,e.storage_projection.path.at(-1).field_ast_id);}
 });
 test(`unmodeled copies keep gaps and typed source reads cannot promote state proofs optimizer=${optimized}`,()=>{
  const r=inspect(optimized);
  for(const name of['keyDivisionDiscarded','keyEffects','dirtyAllocator','arrayElementCopy','conditionalReference']){const{fund,flow}=records(r,name);assert.equal(fund.amount_derivation_status,'partial',name);assert.ok(flow.unresolved.length,name);}
  const{method}=records(r,'twoCopies'),state=r.sections.state_transitions.find(t=>t.method_id===method.method_id);assert.ok(state);assert.equal(state.complete,true);assert.deepEqual(state.unresolved,[]);
  const dynamic=records(r,'dynamicFieldCopy'),dynamicState=r.sections.state_transitions.find(t=>t.method_id===dynamic.method.method_id);
  assert.equal(dynamic.fund.amount_derivation_status,'partial');assert.equal(dynamicState.complete,false);assert.ok(dynamicState.unresolved.some(s=>s.startsWith('STATE_MEMORY_STRUCT_ARRAY_FIELD_UNSUPPORTED')));
  for(const name of['implicitStorageArgument','structReturn','wholeStructWrite','storageStructCopy']){
   const{fund,flow}=records(r,name);assert.equal(fund.amount_derivation_status,'represented_source_dependencies_resolved',name);
   const state=r.sections.state_transitions.find(t=>t.method_id===records(r,name).method.method_id);assert.ok(state,name);assert.equal(state.complete,true,name+':'+JSON.stringify(state.unresolved));
  }
  const whole=records(r,'wholeStructWrite').method,wholeState=r.sections.state_transitions.find(t=>t.method_id===whole.method_id);
  assert.equal(wholeState.paths[0].writes.length,2);
  const copied=records(r,'storageStructCopy').method,copiedState=r.sections.state_transitions.find(t=>t.method_id===copied.method_id);
  assert.equal(copiedState.paths[0].writes.length,2);
  const bag=records(r,'bagCopyOnly');assert.equal(bag.fund.amount_derivation_status,'represented_source_dependencies_resolved');
  const bagState=r.sections.state_transitions.find(t=>t.method_id===bag.method.method_id);assert.ok(bagState);assert.equal(bagState.complete,true);
  const copyVersion=r.sections.expressions.find(e=>e.method_id===bag.method.method_id&&e.source_storage_copy);assert.ok(copyVersion);assert.ok(copyVersion.source_storage_copy.field_gaps.some(g=>g.reason.startsWith('SOURCE_AGGREGATE_STORAGE_COPY_FIELD_TYPE_UNSUPPORTED:values')));
  for(const e of snapshots(r,method)){assert.equal(e.storage_value_bound,false);assert.equal(e.all_execution_paths_verified,false);}
 });
}
