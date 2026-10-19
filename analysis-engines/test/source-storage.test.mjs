import test from'node:test';import assert from'node:assert/strict';
import{analyze}from'../analyze.mjs';import{storageFlowFixture,storageFlowSource}from'./fixtures/storage-flow.mjs';
import{evaluateStorageSlot}from'../ir/storage-reference.mjs';
const byVersion=new Map();function run(version='0.8.30',source=storageFlowSource){if(source!==storageFlowSource)return analyze(storageFlowFixture(version,source));if(!byVersion.has(version))byVersion.set(version,analyze(storageFlowFixture(version)));return byVersion.get(version);}
const method=(r,name)=>r.sections.methods.find(m=>m.name===name),writes=(r,name)=>r.sections.storage.filter(s=>s.method_id===method(r,name).method_id&&s.access==='write'&&s.storage_projection),transition=(r,name)=>r.sections.state_transitions.find(t=>t.method_id===method(r,name).method_id);

test('ordinary aliases bind both UserInfo writes to the declaration and original key expression',()=>{
 for(const version of['0.4.26','0.6.12','0.7.6','0.8.30']){
  const r=run(version),m=method(r,'captured'),w=writes(r,'captured');assert.equal(m.source_checkpoint,'method_body_saved');assert.equal(w.length,2);
  assert.deepEqual(w.map(s=>s.storage_projection.path.at(-1).name),['amount','rewardDebt']);
  assert.ok(w.every(s=>s.variable==='users'&&s.storage_reference_ids.length===1));
  const references=r.sections.storage_references.filter(s=>m.storage_reference_ids.includes(s.storage_reference_id));assert.equal(references.length,1);
  const key=references[0].projection.path[0];assert.equal(key.key_text,'current');
  const expression=r.sections.expressions.find(e=>e.expression_id===key.key_expression_id);
  const mutation=r.sections.definitions.find(d=>d.method_id===m.method_id&&d.declaration_id===key.key_referenced_declaration&&d.order>expression.step_order);
  assert.ok(mutation,'key captured before subsequent assignment');assert.equal(transition(r,'captured').complete,true,JSON.stringify(transition(r,'captured')));
 }
});

test('internal storage parameters share the physical object but local rebinding cannot change the caller pointer',()=>{
 const r=run();for(const name of['parameter','isolated'])assert.equal(transition(r,name).complete,true,JSON.stringify(transition(r,name)));
 const w=writes(r,'isolated');assert.equal(w.length,2);assert.deepEqual(w.map(s=>s.storage_projection.path[0].key_text),['k+1','k']);
 assert.ok(writes(r,'parameter').every(s=>s.variable==='users'));
 const root=w[0].state_ast_id;assert.equal(w[1].state_ast_id,root);
});

test('explicit and named returned references reach field writes with no guessed standalone storage parameter',()=>{
 const r=run();for(const name of['returned','named']){
  const w=writes(r,name);assert.equal(w.length,1,name);assert.equal(w[0].variable,'users');assert.equal(w[0].storage_projection.path.at(-1).name,'amount');
  assert.equal(transition(r,name).complete,true,JSON.stringify(transition(r,name)));
 }
 assert.equal(writes(r,'nested').length,0,'an unbound standalone parameter has no assumed root');
 assert.ok(writes(r,'localRebind').every(w=>w.variable==='users'&&w.storage_projection.path[0].key_text==='k+1'));
});

test('branch joins preserve each candidate and its predicate instead of selecting a root',()=>{
 const r=run(),w=writes(r,'branch');assert.equal(w.length,2);assert.deepEqual(w.map(s=>s.conditions),[['choose'],['!(choose)']]);
 assert.deepEqual(w.map(s=>s.storage_projection.path[0].key_text),['k+1','k']);
 assert.equal(transition(r,'branch').complete,true);assert.equal(transition(r,'branch').paths.length,2);
});

test('arrays expose compiler strides and real bounds while symbolic packed offsets preserve neighboring bits',()=>{
 const r=run();for(const name of['arrayItem','arrayPacked','arrayMapped'])assert.equal(transition(r,name).complete,true,JSON.stringify(transition(r,name)));
 const item=writes(r,'arrayItem')[0];assert.equal(item.storage_projection.path[0].stride_slots,'2');assert.equal(item.storage_projection.bounds[0].kind,'dynamic_array');
 const packed=writes(r,'arrayPacked')[0];assert.deepEqual(packed.storage_projection.bounds.map(b=>b.length),['2','12']);
 const projected=packed.storage_projection,values=Object.fromEntries(projected.path.filter(p=>p.key_expression_id).map((p,i)=>[p.key_expression_id,i===0?1:11]));
 assert.equal(evaluateStorageSlot(projected.offset_expression,values),3n);
 const model=transition(r,'arrayPacked');assert.ok(model.paths[0].writes.some(w=>typeof w.offset==='string'));
 assert.ok(model.reverted_paths.length>=2);assert.ok(model.paths[0].writes[0].physical_storage_after);
});

test('a storage write followed by failure remains in attempted effects and the reverted state returns to the entry array',()=>{
 const t=transition(run(),'rollback');assert.equal(t.complete,true);assert.ok(t.reverted_paths.some(p=>p.attempted_writes.length===1));
 assert.ok(t.reverted_paths.every(p=>p.storage_after===t.paths[0].storage_before));
});
test('an inferred legacy storage pointer keeps the compiler reference identity',()=>{
 const r=run('0.4.26',storageFlowSource.replace('Item storage user=users[current][msg.sender]','var user=users[current][msg.sender]')),
  rows=writes(r,'captured');assert.equal(rows.length,2);assert.ok(rows.every(w=>w.storage_reference_ids.length===1));assert.equal(transition(r,'captured').complete,true);
});
