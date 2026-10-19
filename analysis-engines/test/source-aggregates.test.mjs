import test from'node:test';import assert from'node:assert/strict';import{SourceAggregates}from'../ir/source-aggregates.mjs';
const type={kind:'struct',declaration_id:20,type:'struct Quote memory'},field={declaration_id:21,type:'uint256'};
const make=(budget={})=>new SourceAggregates({sections:{}},{method_id:'1'.repeat(40)},{budget});
const allocate=(model,value='amount')=>model.allocate(type,[{...field,value_expression_id:value}]);

test('a scalar captured before an alias write keeps its value while both later alias reads become unknown',()=>{
 const m=make(),a=allocate(m,'before');m.bind({id:1},a);m.bind({id:2},m.get(1));const captured=m.project(m.get(1),field);m.invalidate(m.get(2),'WRITE_NOT_MODELED');
 assert.equal(captured.value_expression_id,'before');assert.equal(m.project(m.get(1),field).kind,'unresolved_aggregate_value');assert.deepEqual(m.project(m.get(2),field).unresolved,['WRITE_NOT_MODELED']);assert.equal(m.shared.versions.get(captured.aggregate_version_ids[0]).fields[0].value_expression_id,'before');
});
test('rebinding one reference to a fresh object leaves a previous alias on its original object',()=>{
 const m=make(),a=allocate(m,'first');m.bind({id:1},a);m.bind({id:2},m.get(1));m.bind({id:1},allocate(m,'second'));assert.equal(m.project(m.get(1),field).value_expression_id,'second');assert.equal(m.project(m.get(2),field).value_expression_id,'first');m.invalidate(m.get(1),'SECOND_OBJECT_WRITE');assert.equal(m.project(m.get(2),field).value_expression_id,'first');
});
test('internal frames share the referenced heap and keep their local reference declarations separate',()=>{
 const m=make();m.bind({id:1},allocate(m,'caller'));const child=m.fork({newFunction:true,frameId:'entry/call:7'});assert.equal(child.get(1),null);child.bind({id:1},m.get(1));assert.notEqual(child.frameId,m.frameId);child.invalidate(child.get(1),'CALLEE_MAY_WRITE');assert.deepEqual(m.project(m.get(1),field).unresolved,['CALLEE_MAY_WRITE']);child.bind({id:1},allocate(child,'callee-local'));assert.equal(child.project(child.get(1),field).value_expression_id,'callee-local');assert.equal(m.project(m.get(1),field).kind,'unresolved_aggregate_value');
});
test('branch-selected aliases retain their actual targets and a later write invalidates every possible target',()=>{
 const m=make(),yes=m.fork(),no=m.fork();yes.bind({id:1},allocate(yes,'yes'));no.bind({id:1},allocate(no,'no'));m.merge(yes,no,{conditionExpression:'choose'});const captured=m.project(m.get(1),field);assert.equal(captured.kind,'conditional_aggregate_value');assert.equal(captured.condition_expression_id,'choose');assert.equal(captured.yes.value_expression_id,'yes');assert.equal(captured.no.value_expression_id,'no');m.invalidate(m.get(1),'SELECTED_ALIAS_WRITE');const current=m.project(m.get(1),field);assert.deepEqual(current.yes.unresolved,['SELECTED_ALIAS_WRITE']);assert.deepEqual(current.no.unresolved,['SELECTED_ALIAS_WRITE']);assert.equal(captured.yes.value_expression_id,'yes');
});
test('a write on a terminated branch cannot invalidate the normal successor heap',()=>{
 const m=make();m.bind({id:1},allocate(m,'normal'));const yes=m.fork(),no=m.fork();yes.invalidate(yes.get(1),'TERMINATED_BRANCH_WRITE');m.merge(yes,no,{conditionExpression:'stops',yesTerminated:true});assert.equal(m.project(m.get(1),field).value_expression_id,'normal');
});
test('nested references retain child alias identity and recursive invalidation reaches independent child aliases',()=>{
 const m=make(),child=allocate(m,'nested'),parent=m.allocate({kind:'struct',declaration_id:30,type:'struct Envelope memory'},[{declaration_id:31,type:'struct Quote memory',reference:child}]);m.bind({id:1},child);const nested=m.project(parent,{declaration_id:31,type:'struct Quote memory'});assert.equal(nested.kind,'nested_aggregate_reference');assert.equal(nested.reference.object_id,child.object_id);const captured=m.project(nested.reference,field);assert.equal(captured.aggregate_version_ids.length,2);assert.ok(captured.aggregate_version_ids.includes(m.version(parent.object_id).expression_id));m.invalidate(parent,'UNKNOWN_PARENT_MUTATION');assert.deepEqual(m.project(m.get(1),field).unresolved,['UNKNOWN_PARENT_MUTATION']);assert.equal(captured.value_expression_id,'nested');
});
test('resource exhaustion does not keep old values or allocate unbounded objects and an unknown write invalidates aliases',()=>{
 const m=make({maxVersions:1,maxPerMethod:1});m.bind({id:1},allocate(m,'kept'));const captured=m.project(m.get(1),field);for(let i=0;i<100;i++)assert.equal(allocate(m,'overflow').kind,'unresolved_aggregate_reference');assert.equal(m.heap.current.size,1);assert.equal(m.shared.versions.size,1);m.invalidate(m.get(1),'WRITE_AFTER_BUDGET');assert.equal(m.project(m.get(1),field).kind,'unresolved_aggregate_value');assert.equal(captured.value_expression_id,'kept');const other=make();other.bind({id:1},allocate(other,'alias'));other.invalidate(other.unknown(type,'LOST_REFERENCE'),'UNKNOWN_TARGET_WRITE');assert.deepEqual(other.project(other.get(1),field).unresolved,['UNKNOWN_TARGET_WRITE']);
});
test('field declaration identity and struct descriptors cannot be replaced by a same-named array or another field',()=>{
 const m=make(),value=allocate(m,'actual');assert.deepEqual(m.project(value,{declaration_id:99,type:'uint256'}).unresolved,['SOURCE_AGGREGATE_FIELD_UNAVAILABLE']);assert.equal(m.allocate({kind:'array',declaration_id:20,type:'struct Quote[] memory'},[]).kind,'unresolved_aggregate_reference');assert.equal(m.allocate(type,[{...field,value_expression_id:'a'},{...field,value_expression_id:'b'}]).kind,'unresolved_aggregate_reference');assert.equal(m.allocate(type,[],{depth:5}).kind,'unresolved_aggregate_reference');
});
test('nested object depth is checked from the referenced heap rather than trusting the allocation caller',()=>{
 const m=make();let reference=allocate(m,'base');for(let i=0;i<3;i++)reference=m.allocate({kind:'struct',declaration_id:40+i,type:'struct Level'+i+' memory'},[{declaration_id:50+i,type:reference.type.type,reference}]);assert.equal(reference.kind,'aggregate_reference');assert.equal(m.shared.versions.size,4);const overflow=m.allocate({kind:'struct',declaration_id:60,type:'struct TooDeep memory'},[{declaration_id:61,type:reference.type.type,reference}]);assert.equal(overflow.kind,'unresolved_aggregate_reference');assert.deepEqual(overflow.unresolved,['SOURCE_AGGREGATE_NESTING_BUDGET']);assert.equal(m.shared.versions.size,4);
});
test('losing a conditional alias to the reference budget cannot leave any possible write target falsely unchanged',()=>{
 const m=make(),a=allocate(m,'a'),b=allocate(m,'b'),captured=m.project(a,field);m.bind({id:1},a);for(let i=0;i<40;i++){const yes=m.fork(),no=m.fork();no.bind({id:1},b);m.merge(yes,no,{conditionExpression:'choose-'+i});}assert.ok(m.result.sections.frontier.some(f=>f.reason==='SOURCE_AGGREGATE_REFERENCE_BUDGET'));m.invalidate(m.get(1),'UNKNOWN_JOINED_WRITE');assert.deepEqual(m.project(a,field).unresolved,['UNKNOWN_JOINED_WRITE']);assert.deepEqual(m.project(b,field).unresolved,['UNKNOWN_JOINED_WRITE']);assert.equal(captured.value_expression_id,'a');
});
