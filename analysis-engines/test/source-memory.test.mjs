import test from'node:test';import assert from'node:assert/strict';import{emptyResult,makeMethod}from'../model.mjs';import{SourceByteMemory}from'../ir/source-memory.mjs';
function fresh(options={}){const result=emptyResult({code_hash:'0x'+'1'.repeat(64)}),method=makeMethod(result.snapshot,'source-memory');return{result,memory:new SourceByteMemory(result,method,{allocatorKnown:true,...options})};}
function encoded(memory,name,amount='quantity'){return memory.capture({originExpressionId:name,encoding:'encodeWithSelector',selector:'0xa9059cbb',types:['address','uint256'],
 values:[{expression:'recipient',expression_id:'recipient-at-'+name},{expression:amount,expression_id:'amount-at-'+name}],argumentExpressionIds:['recipient-at-'+name,'amount-at-'+name],argumentTexts:['recipient',amount],context:{path:'body/'+name,order:7},evidence_ids:['construction-'+name]});}

test('source bytes capture the original typed arguments and byte construction without inventing a heap address',()=>{
 const{memory}=fresh(),object=encoded(memory,'first');memory.bind(1,object);const entry=memory.single(memory.get(1));assert.ok(entry);
 assert.equal(entry.record.byte_length,'68');assert.equal(entry.record.address_space,'compiler_object_relative');assert.deepEqual(entry.record.argument_expression_ids,['recipient-at-first','amount-at-first']);
 assert.equal(entry.memory.loadWord(0).cases[0].value,'68');const read=memory.read(object);assert.equal(read.status,'exact');assert.deepEqual(read.cases[0].bytes.slice(0,4).map(b=>b.value),[169,5,156,187]);
 const next=encoded(memory,'second','laterQuantity');memory.bind(1,next);assert.equal(memory.single(object).record.argument_texts[1],'quantity');assert.equal(memory.single(memory.get(1)).record.argument_texts[1],'laterQuantity');
});

test('alias mutation changes all current aliases and leaves previous versions plus unrelated objects intact',()=>{
 const{memory}=fresh(),old=encoded(memory,'old'),other=encoded(memory,'other');memory.bind(1,old);memory.bind(2,old);memory.bind(3,other);
 const before=memory.read(old),saved=memory.snapshot(old);memory.invalidate(memory.get(2),'SOURCE_BYTES_INDEX_WRITE',{index:0n,byteValue:0x12n,context:{ast_id:99,path:'body/write'}});
 assert.equal(memory.single(memory.get(1)),null);assert.deepEqual(memory.get(1),memory.get(2));assert.ok(memory.single(memory.get(3)));
 assert.equal(memory.read(memory.get(1)).cases[0].bytes[0].value,0x12);assert.equal(before.cases[0].bytes[0].value,0xa9);assert.equal(memory.read(old).cases[0].bytes[0].value,0x12);assert.equal(memory.read(saved).cases[0].bytes[0].value,0xa9);
 memory.bind(2,other);memory.invalidate(memory.get(1),'DYNAMIC_WRITE');assert.ok(memory.single(memory.get(2)));assert.ok(memory.single(memory.get(3)));
});

test('branch merges keep different objects and a missing branch rather than selecting a convenient constructor',()=>{
 const{memory}=fresh(),initial=encoded(memory,'initial');memory.bind(1,initial);const yes=memory.fork(),no=memory.fork();yes.bind(1,encoded(yes,'yes'));no.bind(1,encoded(no,'no'));
 memory.merge(yes,no,{yesConditions:['flag'],noConditions:['!(flag)'],context:{path:'join'}});assert.equal(memory.single(memory.get(1)),null);assert.equal(memory.read(memory.get(1)).status,'conditional');assert.equal(memory.get(1).cases.length,2);
 const left=memory.fork(),right=memory.fork();left.bind(2,initial);memory.merge(left,right,{yesConditions:['next'],noConditions:['!(next)']});assert.equal(memory.single(memory.get(2)),null);assert.equal(memory.read(memory.get(2)).status,'partial');
 const same=fresh().memory,obj=encoded(same,'same');same.bind(1,obj);same.merge(same.fork(),same.fork());assert.ok(same.single(same.get(1)));
});

test('callee parameter rebinding stays local while object writes propagate to copied caller aliases',()=>{
 const{memory}=fresh(),old=encoded(memory,'caller');memory.bind(1,old);const call=memory.fork({newFunction:true});call.bind(101,old);call.bind(101,encoded(call,'replacement'));memory.propagateCall(call);assert.deepEqual(memory.get(1),old);
 const changed=memory.fork({newFunction:true});changed.bind(101,memory.get(1));changed.invalidate(changed.get(101),'CALLEE_BYTE_WRITE',{index:1n,byteValue:0x77n});memory.propagateCall(changed);
 assert.equal(memory.read(memory.get(1)).cases[0].bytes[1].value,0x77);assert.equal(memory.bindings.has(101),false);
});

test('unmodeled assembly invalidates aliases and allocator assumptions for subsequent constructors',()=>{
 const{memory}=fresh(),old=encoded(memory,'before'),saved=memory.snapshot(old);memory.bind(1,old);memory.invalidateAll('SOURCE_ASSEMBLY_MEMORY_UNRESOLVED',{path:'assembly'});
 assert.equal(memory.single(memory.get(1)),null);assert.equal(memory.read(memory.get(1)).status,'partial');assert.equal(memory.allocatorKnown,false);
 const after=encoded(memory,'after');assert.equal(memory.single(after),null);assert.ok(memory.read(after).unresolved.includes('SOURCE_BYTES_ALLOCATOR_UNVERIFIED'));assert.equal(memory.single(old),null,'Allocation reference follows the current invalidated object');assert.ok(memory.single(saved),'Earlier explicitly saved version stays independently available');
});

test('returns require every returning path and source object budgets leave existing data readable',()=>{
 const{memory,result}=fresh({budget:{objects:0,cells:0,maxObjects:1,maxCells:128}}),first=encoded(memory,'first');memory.addReturn(first,['allowed']);
 assert.equal(memory.single(memory.returned()),null);assert.ok(memory.single(memory.returned({allReturningPathsCovered:true})));
 const failed=encoded(memory,'exhausted');assert.equal(memory.single(failed),null);assert.ok(memory.read(failed).unresolved.includes('SOURCE_MEMORY_OBJECT_OR_CELL_BUDGET'));assert.ok(memory.single(first));
 const ids=new Set(result.sections.source_memory_versions.map(r=>r.source_memory_version_id));for(const row of result.sections.source_memory_versions)if(row.parent_source_memory_version_id)assert.ok(ids.has(row.parent_source_memory_version_id));
});

test('unchanged conditional bindings do not multiply paths and version exhaustion is bounded',()=>{
 const{memory,result}=fresh(),a=encoded(memory,'a'),b=encoded(memory,'b'),yes=memory.fork(),no=memory.fork();yes.bind(1,a);no.bind(1,b);memory.merge(yes,no);
 for(let i=0;i<20;i++)memory.merge(memory.fork(),memory.fork(),{yesConditions:['flag'+i],noConditions:['!flag'+i]});assert.equal(memory.get(1).cases.length,2);
 const small=fresh({budget:{objects:0,cells:0,maxObjects:10,maxCells:4096,maxVersions:3}}).memory,object=encoded(small,'object');small.bind(1,object);
 for(let i=0;i<10;i++)small.invalidate(small.get(1),'write',{index:0n,byteValue:BigInt(i)});
 assert.equal(small.read(small.get(1)).status,'partial');assert.ok(small.read(small.get(1)).unresolved.includes('SOURCE_MEMORY_VERSION_BUDGET'));assert.ok(small.shared.objects.size<=4);
});

test('equal display names from different captured conditions do not erase a feasible object write',()=>{
 const{memory}=fresh(),object=encoded(memory,'scoped'),branch=memory.fork();
 branch.assume({op:'atom',expression_id:'caller-flag-before-call',expected:true});
 branch.assume({op:'atom',expression_id:'callee-flag-other-declaration',expected:false});
 branch.invalidate(object,'BRANCH_WRITE',{index:0n,byteValue:7n});branch.recordExit(['flag','!(flag)']);
 const read=branch.read(object);assert.equal(read.status,'exact');assert.equal(read.cases[0].bytes[0].value,7);assert.equal(branch.exits.length,1);
 assert.equal(memory.read(object).cases[0].bytes[0].value,0xa9);
 const impossible=branch.fork();impossible.assume({op:'atom',expression_id:'callee-flag-other-declaration',expected:true});
 impossible.invalidate(object,'IMPOSSIBLE_WRITE',{index:0n,byteValue:9n});impossible.recordExit();assert.equal(impossible.exits.length,1);assert.equal(impossible.read(object).cases.length,0);
 assert.equal(branch.read(object).cases[0].bytes[0].value,7);
});

test('nested identity predicates select weak alias writes independently of ambiguous display text',()=>{
 const{memory}=fresh(),a=encoded(memory,'left'),b=encoded(memory,'right'),guard={op:'and',terms:[{op:'atom',expression_id:'outer',expected:true},{op:'atom',expression_id:'inner',expected:false}]};
 const alias={cases:[{...a.cases[0],conditions:['flag','!(flag)'],predicate:guard},{...b.cases[0],conditions:['!(flag && !flag)'],predicate:{op:'not',value:guard}}],unresolved:[]};
 memory.invalidate(alias,'CONDITIONAL_WRITE',{index:0n,byteValue:7n});
 const accepts=(p,values)=>!p?true:p.op==='constant'?p.value:p.op==='atom'?values[p.expression_id]===p.expected:p.op==='not'?!accepts(p.value,values):p.op==='and'?p.terms.every(t=>accepts(t,values)):p.terms.some(t=>accepts(t,values));
 for(const outer of[false,true])for(const inner of[false,true])for(const[object,selected]of[[a,outer&&!inner],[b,!(outer&&!inner)]]){
  const read=memory.read(object);assert.notEqual(read.status,'partial');const cases=read.cases.filter(c=>accepts(c.predicate,{outer,inner}));assert.equal(cases.length,1);assert.equal(cases[0].bytes[0].value,selected?7:0xa9);
 }
});

test('an unidentified conditional write and an exhausted predicate budget never expose stale exact bytes',()=>{
 const{memory}=fresh(),a=encoded(memory,'unknown-left'),b=encoded(memory,'unknown-right');
 memory.invalidate({cases:[{...a.cases[0],conditions:['flag']},{...b.cases[0],conditions:['!(flag)']}],unresolved:[]},'UNKNOWN_CONDITION_WRITE',{index:0n,byteValue:7n});
 for(const object of[a,b]){const read=memory.read(object);assert.equal(read.status,'partial');assert.ok(read.unresolved.includes('SOURCE_BYTES_CONDITION_IDENTITY_UNAVAILABLE'));}
 const limited=fresh().memory,object=encoded(limited,'predicate-budget');for(let i=0;i<257;i++)limited.assume({op:'atom',expression_id:'separate-condition-'+i,expected:true});
 const read=limited.read(object);assert.equal(read.status,'partial');assert.ok(read.unresolved.includes('SOURCE_BYTES_PREDICATE_BUDGET'));assert.equal(read.cases.length,0);
});
