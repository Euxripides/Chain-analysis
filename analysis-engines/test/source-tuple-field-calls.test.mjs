import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {createHash} from 'node:crypto';
import {analyze} from '../analyze.mjs';
import {evaluateReturn} from '../../scripts/contract-analysis/expression_model_eval.mjs';

const source=fs.readFileSync(new URL('./fixtures/tuple-field-calls.sol',import.meta.url),'utf8'),cache=new Map();
function inspect(optimized){
 if(cache.has(optimized))return cache.get(optimized);
 const material={kind:'source',source:{language:'Solidity',files:{'TupleFields.sol':source},compiler_version:'0.8.30',target:'TupleFields.sol:TupleFields',settings:{optimizer:{enabled:optimized,runs:200},evmVersion:'shanghai'}}},raw=JSON.stringify(material);
 const result=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:createHash('sha256').update(raw).digest('hex')},engines:['solc-ast']});
 assert.equal(result.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed');cache.set(optimized,result);return result;
}
function records(result,name){
 const method=result.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');assert.ok(method,name);
 const fund=result.sections.funds.find(f=>f.method_id===method.method_id&&f.kind==='native_payment');assert.ok(fund,name);
 const derivation=result.sections.dataflow.find(d=>d.dataflow_id===fund.amount_derivation_id);assert.ok(derivation,name);
 return{method,fund,derivation};
}
function value(result,method,key,x){const previous=method.entry_return_expression_id;try{method.entry_return_expression_id=key;return evaluateReturn(result,method.name,{x,to:1n})[0];}finally{method.entry_return_expression_id=previous;}}
for(const optimized of[false,true]){
 test(`internal scalar tuple results retain amounts across aliases, frames and mixed targets optimizer=${optimized}`,()=>{
  const r=inspect(optimized);
  for(const name of['internalTuple','aliasedTuple','forwardedTuple','namedTuple','reversedTuple','mixedTuple','separateTuple','capturedTuple','argumentTuple','divisionTuple']){
   const{method,fund,derivation}=records(r,name);assert.equal(fund.amount_derivation_status,'represented_source_dependencies_resolved',name);assert.deepEqual(derivation.unresolved,[],name);
   for(const x of[1n,23n])assert.equal(value(r,method,fund.amount_expression_id||derivation.expression_id,x),name==='capturedTuple'?x+22n:name==='argumentTuple'?x:name==='divisionTuple'?99n/x:x+3n,name);
  }
 });
 test(`saved tuple value graphs preserve checked arithmetic and complete division evaluation optimizer=${optimized}`,()=>{
  const r=inspect(optimized),max=(1n<<256n)-1n;
  for(const name of['internalTuple','aliasedTuple','forwardedTuple','namedTuple','reversedTuple','mixedTuple','separateTuple','capturedTuple']){
   const{method,fund,derivation}=records(r,name);assert.throws(()=>value(r,method,fund.amount_expression_id||derivation.expression_id,max),e=>e.kind==='panic_0x11',name);
  }
  const{method,fund,derivation}=records(r,'divisionTuple');assert.throws(()=>value(r,method,fund.amount_expression_id||derivation.expression_id,0n),e=>e.kind==='panic_0x12');
 });
 test(`discarded and overwritten tuple components keep prior failure dependencies optimizer=${optimized}`,()=>{
  const r=inspect(optimized);
  for(const name of['discardedFailure','overwrittenFailure']){
   const{fund,derivation}=records(r,name);assert.equal(fund.amount_derivation_status,'partial',name);assert.ok(derivation.unresolved.some(x=>/^SOURCE_AGGREGATE_(PRIOR|SIBLING)_EVALUATION_REQUIRES_FAILURE_MODEL$/.test(x)),name);
  }
  for(const name of['externalTuple','effectfulTuple','overlapTuple','dirtyTuple','recursiveTuple']){
   const{fund,derivation}=records(r,name);assert.equal(fund.amount_derivation_status,'partial',name);assert.ok(derivation.unresolved.length,name);
  }
 });
 test(`joint tuple dependencies reference the complete RHS and stay within their method optimizer=${optimized}`,()=>{
  const r=inspect(optimized),expressions=new Map(r.sections.expressions.map(e=>[e.expression_id,e]));let groups=0;
  for(const version of r.sections.expressions.filter(e=>e.symbolic_format==='source_aggregate_version'))for(const field of version.fields||[]){
   if(!field.evaluation_group_id)continue;groups++;const whole=expressions.get(field.evaluation_group_id);assert.ok(whole);assert.equal(whole.method_id,version.method_id);assert.equal(whole.op,'FunctionCall');assert.ok(whole.internal_return_model);
   const history=version.evaluation_history_expression_ids||[];assert.ok(history.includes(field.evaluation_group_id)||version.parent_aggregate_expression_ids.length>0);
  }
  assert.ok(groups>20);
 });
}
