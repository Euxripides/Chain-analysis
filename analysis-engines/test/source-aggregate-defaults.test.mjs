import test from'node:test';import assert from'node:assert/strict';import fs from'node:fs';import{createHash}from'node:crypto';import{analyze}from'../analyze.mjs';
const sha=b=>createHash('sha256').update(b).digest('hex'),rows=[];
const source=fs.readFileSync(new URL('./fixtures/aggregate-defaults.sol',import.meta.url),'utf8');
const checks={plain:'resolved',nested:'resolved',distinct:'resolved',aliasCaptured:'resolved',afterScratch:'partial',afterHeap:'partial',afterAllocator:'partial',liveBranch:'partial',deadBranch:'resolved',returningBranch:'resolved',branchDefaults:'resolved',bound:'resolved',priorCall:'partial',parameterBound:'resolved',unsupported:'partial',namedUnbound:'partial'};
function inspect(version,optimized){
 const material={kind:'source',source:{language:'Solidity',files:{'TypedDefaults.sol':version==='0.4.26'?source.replaceAll('address payable','address'):source},compiler_version:version,target:'TypedDefaults.sol:TypedDefaults',settings:{optimizer:{enabled:optimized,runs:200},...(['0.4.26','0.5.8'].includes(version)?{}:{evmVersion:version==='0.8.30'?'shanghai':'istanbul'})}}},raw=JSON.stringify(material);
 const input={submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:sha(raw)},engines:['solc-ast']},r=analyze(input);
 assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast')?.status,'completed',JSON.stringify(r.sections.engines));
 const sourceEvidence=new Set(r.sections.evidence.filter(e=>e.kind==='source').map(e=>e.evidence_id));
 for(const[name,status]of Object.entries(checks)){
  const method=r.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch'),funds=r.sections.funds.filter(f=>f.method_id===method?.method_id&&f.kind==='native_payment'&&f.evidence_ids.some(id=>sourceEvidence.has(id)));assert.ok(funds.length,name);
  for(const f of funds){const flow=r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id);assert.equal(f.amount_derivation_status,status==='resolved'?'represented_source_dependencies_resolved':'partial',name+': '+JSON.stringify({fund:f,flow}));rows.push({version,optimized,name,status,expression:f.expanded_amount_expression,unresolved:flow?.unresolved||[]});}
  if(name==='plain')assert.match(funds[0].expanded_amount_expression,/\b0\b/);
  if(['distinct','aliasCaptured'].includes(name)){assert.match(funds[0].expanded_amount_expression,/\b0\b/);assert.match(funds[1].expanded_amount_expression,/\b7\b/);}
  if(name==='branchDefaults')for(const text of['flag','7','0'])assert.ok(funds[0].expanded_amount_expression.includes(text),name+': '+funds[0].expanded_amount_expression);
 }
 const unbound=r.sections.methods.filter(m=>m.name==='_local'&&m.dispatch_role!=='external_dispatch');assert.ok(unbound.length);for(const method of unbound){const funds=r.sections.funds.filter(f=>f.method_id===method.method_id&&f.evidence_ids.some(id=>sourceEvidence.has(id)));assert.ok(funds.length);assert.ok(funds.every(f=>f.amount_derivation_status==='partial'));}
 const versions=r.sections.expressions.filter(e=>e.implicit_initialization);assert.ok(versions.length);assert.ok(versions.every(e=>e.implicit_initialization.basis==='compiler_default_local_memory_struct'));
}
for(const version of['0.4.26','0.5.8','0.6.12','0.7.6','0.8.30'])for(const optimized of[false,true])test('default memory struct semantics '+version+' optimizer='+optimized,()=>inspect(version,optimized));
