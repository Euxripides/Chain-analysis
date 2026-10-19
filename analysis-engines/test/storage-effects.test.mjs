import test from'node:test';import assert from'node:assert/strict';import{analyze}from'../analyze.mjs';
import{storageEffectsFixture,storageEffectsSource}from'./fixtures/storage-effects.mjs';
const result=analyze(storageEffectsFixture()),method=name=>result.sections.methods.find(m=>m.name===name),
 writes=name=>result.sections.storage.filter(s=>s.method_id===method(name).method_id&&s.access==='write'&&s.storage_projection),
 transition=name=>result.sections.state_transitions.find(t=>t.method_id===method(name).method_id);
test('a returned lvalue is bound after its internal CALL and library receivers keep their storage identity',()=>{
 for(const name of['directReturned','libraryReceiver']){assert.equal(writes(name).length,1,name);assert.equal(writes(name)[0].variable,'cells');assert.equal(transition(name).complete,true,JSON.stringify(transition(name)));}
});
test('returned and ternary reference alternatives retain both paths and exact locations',()=>{
 for(const name of['joined','ternary']){const rows=writes(name);assert.equal(rows.length,2,name);assert.ok(rows.every(w=>w.storage_reference_ids.length));
  assert.equal(transition(name).complete,true,JSON.stringify(transition(name)));assert.equal(transition(name).paths.length,2);}
});
test('stateful value calls preserve the counter write before the returned reference is used',()=>{
 const t=transition('stateful');assert.equal(t.complete,true,JSON.stringify(t));assert.equal(t.paths.length,1);assert.equal(t.paths[0].writes.length,2);
 assert.deepEqual(t.paths[0].writes.map(w=>w.variable),['count','cells']);
});
test('tuple declarations, assignment and named storage returns keep component order',()=>{
 for(const name of['tupleReferences','tupleNamed','tupleAssigned']){const rows=writes(name);assert.equal(rows.length,2,name);assert.deepEqual(rows.map(w=>w.storage_projection.path[0].key_text),['k','k+1']);
  const t=transition(name);assert.equal(t.complete,true,JSON.stringify(t));assert.equal(t.paths[0].writes.length,2);}
});
test('conditional and short-circuit state effects export only the writes executed on each leaf path',()=>{
 for(const[name,counts]of[['conditionalEffect',[2]],['shortEffect',[1,2]]]){const t=transition(name);assert.equal(t.complete,true,JSON.stringify(t.unresolved));
  // Repeated correlated predicates may retain infeasible guarded combinations.
  // Concrete execution separately proves which path and writes are possible.
  assert.deepEqual([...new Set(t.paths.map(p=>p.writes.length))].sort(),counts);assert.ok(t.paths.every(p=>p.condition));}
});
test('noncommuting sibling calls remain unresolved rather than selecting a source evaluation order',()=>{
 const t=transition('noncommuting');assert.equal(t.complete,false);assert.ok(t.unresolved.includes('STATE_UNSPECIFIED_SIBLING_EVALUATION_ORDER'),JSON.stringify(t));
});
test('modifier branch reference changes are conditional and late failure restores the whole entry storage',()=>{
 const rows=writes('conditionalModifier').filter(w=>w.storage_projection.path.at(-1).name==='fee');assert.equal(rows.length,2);
 assert.deepEqual(rows.map(w=>w.conditions),[['choose'],['!(choose)']]);
 const t=transition('callThenRevert');assert.equal(t.complete,true);assert.ok(t.reverted_paths.some(p=>p.attempted_writes.length===1));
 assert.ok(t.reverted_paths.every(p=>p.storage_after===t.paths[0].storage_before));
});
test('compiler array mutation builtins remain explicit storage gaps and do not become external calls',()=>{
 const source=storageEffectsSource.replace('uint public count;','uint public count;uint[] values;').replace(/\}\s*$/,'function arrays()external{values.push(1);values.pop();}}'),r=analyze(storageEffectsFixture('0.8.30',source)),m=r.sections.methods.find(m=>m.name==='arrays');
 assert.equal(r.sections.calls.filter(c=>c.method_id===m.method_id).length,0);
 assert.ok(m.completeness.unresolved.some(g=>g.code==='SOURCE_ARRAY_MUTATION_REQUIRES_LENGTH_AND_ELEMENT_VERSION'));
 assert.ok(r.sections.state_transitions.find(t=>t.method_id===m.method_id).unresolved.includes('STATE_ARRAY_MUTATION_REQUIRES_LENGTH_AND_ELEMENT_VERSION'));
});
