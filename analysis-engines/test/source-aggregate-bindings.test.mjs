import test from'node:test';import assert from'node:assert/strict';import{createHash}from'node:crypto';import{analyze}from'../analyze.mjs';
function inspect(body,sourceOptions={}){const source='pragma solidity ^0.8.24; contract Aggregate {struct Pair {uint256 amount;address payable recipient;}struct Outer {Pair inner;uint256 other;}struct Triple {uint256 amount;address payable recipient;uint256 other;}'+body+'}',material={kind:'source',source:{language:'Solidity',files:{'Aggregate.sol':source},compiler_version:'0.8.30+commit.73712a01',target:'Aggregate.sol:Aggregate',settings:{evmVersion:'shanghai'}}},raw=JSON.stringify(material),r=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:createHash('sha256').update(raw).digest('hex')},engines:['whatsabi','sevm','solc-ast']},{source:sourceOptions});assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast').status,'completed');return r;}
const methods=(r,name)=>r.sections.methods.find(m=>m.name===name),funds=(r,name)=>r.sections.funds.filter(f=>f.method_id===methods(r,name)?.method_id&&f.kind==='native_payment');
function resolved(r,name){const fs=funds(r,name);assert.ok(fs.length,name+': '+JSON.stringify(methods(r,name)?.completeness));for(const f of fs)assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved',name+': '+JSON.stringify(r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id)?.unresolved));return fs;}

test('compiler field identities preserve inline, reversed named and nested constructor values',()=>{
 const r=inspect('function inlineValue(uint8 amount,address payable to) external {to.transfer(Pair(amount,to).amount+1);}function namedValue(uint256 amount,address payable to) external {Pair memory p=Pair({recipient:to,amount:amount});p.recipient.transfer(p.amount);}function nestedValue(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);Outer memory q=Outer(p,0);q.inner.recipient.transfer(q.inner.amount);}');
 assert.match(resolved(r,'inlineValue')[0].expanded_amount_expression,/uint256\(uint8\(amount\)\)/);
 for(const name of['namedValue','nestedValue']){const f=resolved(r,name)[0];assert.match(f.expanded_amount_expression,/amount/);assert.match(f.expanded_to_expression,/to/);assert.ok(!f.expanded_amount_expression.includes('.amount'));}
 const reads=r.sections.expressions.filter(e=>e.source_aggregate_field_declaration_id);assert.ok(reads.length>=5);assert.ok(reads.every(e=>e.source_aggregate_version_ids.length));
});

test('rebinding a local reference preserves its old aliases and internal parameters share the original heap',()=>{
 const r=inspect('function aliasValue(uint256 amount,address payable to,address payable other) external {Pair memory p=Pair(amount,to);Pair memory q=p;p=Pair(7,other);q.recipient.transfer(q.amount);}function _pay(Pair memory p) internal {p.recipient.transfer(p.amount);}function parameterValue(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);_pay(p);}');
 for(const name of['aliasValue','parameterValue']){const f=resolved(r,name)[0];assert.match(f.expanded_amount_expression,/amount/);assert.match(f.expanded_to_expression,/to/);assert.ok(!f.expanded_amount_expression.includes('7'));assert.ok(!f.expanded_to_expression.includes('other'));}
});

test('captured scalar reads preserve the old value while later aliases observe a scalar field assignment',()=>{
 const r=inspect('function overwritten(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);Pair memory q=p;uint256 beforeValue=q.amount;p.amount=amount+1;to.transfer(beforeValue);to.transfer(q.amount);}'),fs=funds(r,'overwritten');assert.equal(fs.length,2);for(const f of fs)assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');assert.match(fs[0].expanded_amount_expression,/amount/);assert.ok(!fs[0].expanded_amount_expression.includes('+'));assert.match(fs[1].expanded_amount_expression,/amount/);assert.match(fs[1].expanded_amount_expression,/1/);const first=r.sections.expressions.find(e=>e.expression_id===fs[0].amount_expression_id),last=r.sections.expressions.find(e=>e.expression_id===fs[1].amount_expression_id);assert.notDeepEqual(first.aggregate_version_expression_ids,last.aggregate_version_expression_ids);
});

test('branch-selected references retain distinct field values and conditions',()=>{
 const r=inspect('function selected(bool flag,uint256 left,uint256 right,address payable to) external {Pair memory p;if(flag)p=Pair(left,to);else p=Pair(right,to);to.transfer(p.amount);}'),f=resolved(r,'selected')[0];assert.match(f.expanded_amount_expression,/flag/);assert.match(f.expanded_amount_expression,/left/);assert.match(f.expanded_amount_expression,/right/);
});

test('effectful constructors and fallible sibling expressions cannot publish a resolved amount',()=>{
 const r=inspect('function change(uint256 amount) internal pure returns(uint256){return amount+1;}function effectful(uint256 amount,address payable to) external {Pair memory p=Pair(change(amount),to);to.transfer(p.amount);}function fallible(uint256 amount,uint256 divisor,address payable to) external {Triple memory p=Triple(amount,to,amount/divisor);to.transfer(p.amount);}');
 for(const[name,reason]of[['effectful','SOURCE_AGGREGATE_CONSTRUCTOR_ARGUMENT_EFFECT_UNMODELED'],['fallible','SOURCE_AGGREGATE_SIBLING_EVALUATION_REQUIRES_FAILURE_MODEL']]){const f=funds(r,name)[0];assert.ok(f);assert.equal(f.amount_derivation_status,'partial');assert.ok(r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id).unresolved.includes(reason),name);}
});

test('default local structs use typed zero while Yul writes and loop effects retain their gaps',()=>{
 const r=inspect('function empty(address payable to) external {Pair memory p;to.transfer(p.amount);}function assemblyValue(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);assembly{mstore(p,1)}to.transfer(p.amount);}function loopValue(uint256 amount,uint256 n,address payable to) external {Pair memory p=Pair(amount,to);for(uint256 i;i<n;i++){p.amount=i;}to.transfer(p.amount);}');assert.match(resolved(r,'empty')[0].expanded_amount_expression,/0/);
 for(const[name,reason]of[['assemblyValue','SOURCE_AGGREGATE_ASSEMBLY_MEMORY_UNMODELED'],['loopValue','SOURCE_AGGREGATE_LOOP_REQUIRES_INVARIANT']]){const f=funds(r,name)[0];assert.ok(f);assert.equal(f.amount_derivation_status,'partial');assert.ok(r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id).unresolved.includes(reason),name);}
});

test('readonly explicit, named, nested and mixed tuple returns preserve object values across call frames',()=>{
 const r=inspect('function _make(uint256 amount,address payable to) internal pure returns(Pair memory){return Pair(amount,to);}function _named(uint256 amount,address payable to) internal pure returns(Pair memory p){p=Pair(amount,to);}function _identity(Pair memory p) internal pure returns(Pair memory){return p;}function _outer(uint256 amount,address payable to) internal pure returns(Pair memory){Pair memory p=_make(amount,to);return _identity(p);}function _mixed(uint256 amount,address payable to) internal pure returns(Pair memory,uint256){return(Pair(amount,to),3);}function explicitValue(uint256 amount,address payable to) external {Pair memory p=_make(amount,to);p.recipient.transfer(p.amount);}function namedReturn(uint256 amount,address payable to) external {Pair memory p=_named(amount,to);p.recipient.transfer(p.amount);}function nestedReturn(uint256 amount,address payable to) external {Pair memory p=_outer(amount,to);p.recipient.transfer(p.amount);}function mixedReturn(uint256 amount,address payable to) external {(Pair memory p,uint256 fee)=_mixed(amount,to);p.recipient.transfer(p.amount+fee);}');
 for(const name of['explicitValue','namedReturn','nestedReturn','mixedReturn']){const f=resolved(r,name)[0];assert.match(f.expanded_amount_expression,/amount/);assert.match(f.expanded_to_expression,/to/);assert.ok(!f.expanded_amount_expression.includes('.amount'));}assert.match(funds(r,'mixedReturn')[0].expanded_amount_expression,/3/);
});

test('readonly scalar projections and branch-selected returned objects keep their caller values',()=>{
 const r=inspect('function _quantity(Pair memory p) internal pure returns(uint256){return p.amount;}function _selected(bool flag,uint256 left,uint256 right,address payable to) internal pure returns(Pair memory){if(flag)return Pair(left,to);return Pair(right,to);}function projected(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);to.transfer(_quantity(p));}function selectedReturn(bool flag,uint256 left,uint256 right,address payable to) external {Pair memory p=_selected(flag,left,right,to);to.transfer(p.amount);}');
 assert.match(resolved(r,'projected')[0].expanded_amount_expression,/amount/);const f=resolved(r,'selectedReturn')[0];for(const text of['flag','left','right'])assert.ok(f.expanded_amount_expression.includes(text));
});

test('a pure helper that writes a shared memory field cannot acquire a readonly return summary',()=>{
 const r=inspect('function _mutate(Pair memory p) internal pure returns(Pair memory){p.amount=7;return p;}function mutatedReturn(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);Pair memory q=_mutate(p);to.transfer(q.amount);}'),f=funds(r,'mutatedReturn')[0];assert.ok(f);assert.equal(f.amount_derivation_status,'partial');const call=r.sections.expressions.find(e=>e.method_id===methods(r,'mutatedReturn').method_id&&e.op==='FunctionCall'&&e.text.startsWith('_mutate('));assert.equal(call.unresolved_return_value,'INTERNAL_RETURN_AGGREGATE_HEAP_EFFECT_UNSUPPORTED');
});

test('memory writes inside modifiers also prevent a readonly aggregate summary',()=>{
 const r=inspect('modifier clobber(Pair memory p){assembly{mstore(p,7)}_;}function _modified(Pair memory p) internal pure clobber(p) returns(Pair memory){return p;}function modifiedReturn(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);Pair memory q=_modified(p);to.transfer(q.amount);}'),f=funds(r,'modifiedReturn')[0];assert.ok(f);assert.equal(f.amount_derivation_status,'partial');assert.ok(r.sections.expressions.some(e=>e.method_id===methods(r,'modifiedReturn').method_id&&e.unresolved_return_value==='INTERNAL_RETURN_AGGREGATE_HEAP_EFFECT_UNSUPPORTED'));
});

test('tuple assignments capture every reference and scalar source before rebinding destinations',()=>{
 const r=inspect('function _mixed(uint256 amount,address payable to) internal pure returns(Pair memory,uint256){return(Pair(amount,to),3);}function tupleAssignment(uint256 amount,address payable to,address payable other) external {Pair memory p=Pair(7,other);uint256 fee;(p,fee)=_mixed(amount,to);p.recipient.transfer(p.amount+fee);}function swapReferences(uint256 left,uint256 right,address payable to) external {Pair memory p=Pair(left,to);Pair memory q=Pair(right,to);(p,q)=(q,p);to.transfer(p.amount);to.transfer(q.amount);}function capturedSibling(uint256 amount,address payable to) external {Pair memory p=Pair(7,to);uint256 beforeValue;(p,beforeValue)=(Pair(amount,to),p.amount);to.transfer(beforeValue);}');
 const f=resolved(r,'tupleAssignment')[0];assert.match(f.expanded_amount_expression,/amount/);assert.match(f.expanded_to_expression,/to/);assert.ok(!f.expanded_to_expression.includes('other'));const fs=resolved(r,'swapReferences');assert.equal(fs.length,2);assert.match(fs[0].expanded_amount_expression,/right/);assert.match(fs[1].expanded_amount_expression,/left/);assert.match(resolved(r,'capturedSibling')[0].expanded_amount_expression,/7/);
});

test('conditional nested references preserve both inner field alternatives',()=>{
 const r=inspect('function _outer(bool flag,uint256 left,uint256 right,address payable to) internal pure returns(Outer memory){if(flag)return Outer(Pair(left,to),0);return Outer(Pair(right,to),0);}function nestedSelected(bool flag,uint256 left,uint256 right,address payable to) external {Outer memory p=_outer(flag,left,right,to);p.inner.recipient.transfer(p.inner.amount);}'),f=resolved(r,'nestedSelected')[0];for(const text of['flag','left','right'])assert.ok(f.expanded_amount_expression.includes(text),text);
});

test('ordinary tuple siblings cannot choose one pointer-write order or preserve its final guessed target',()=>{
 const r=inspect('function siblingScalar(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);(uint256 beforeValue,)=(p.amount,p=Pair(7,to));to.transfer(beforeValue);to.transfer(p.amount);}function siblingReference(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);(Pair memory saved,)=(p,p=Pair(7,to));to.transfer(saved.amount);}');
 for(const name of['siblingScalar','siblingReference'])for(const f of funds(r,name)){assert.equal(f.amount_derivation_status,'partial',name+': '+f.expanded_amount_expression);assert.ok(r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id).unresolved.includes('SOURCE_AGGREGATE_EVALUATION_ORDER_UNSPECIFIED'));}
});

test('unknown allocator writes also prevent fresh constructors from inventing independent objects',()=>{
 const r=inspect('function afterClobber(uint256 amount,address payable to) external {assembly{mstore(0x40,0)}Pair memory p=Pair(amount,to);to.transfer(p.amount);}'),f=funds(r,'afterClobber')[0];assert.ok(f);assert.equal(f.amount_derivation_status,'partial');assert.ok(r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id).unresolved.includes('SOURCE_AGGREGATE_ALLOCATOR_UNVERIFIED'));
});

test('global memory effects through direct and dynamic internal callees conflict with sibling reads',()=>{
 const r=inspect('function _read(Pair memory p) internal pure returns(uint256){return p.amount;}function _clobber() internal returns(uint256){assembly{mstore(0x80,7)}return 0;}function _through(function() internal returns(uint256) f) internal returns(uint256){return f();}function directClobber(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);(uint256 captured,)=(_read(p),_clobber());to.transfer(captured);}function dynamicClobber(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);(uint256 captured,)=(_read(p),_through(_clobber));to.transfer(captured);}');
 for(const name of['directClobber','dynamicClobber']){const f=funds(r,name)[0];assert.ok(f);assert.equal(f.amount_derivation_status,'partial');assert.ok(r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id).unresolved.includes('SOURCE_AGGREGATE_EVALUATION_ORDER_UNSPECIFIED'),name);}
});

test('a complete later overwrite restores a pointer while an independent old alias retains its old object',()=>{
 const r=inspect('function restored(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);Pair memory q=p;(uint256 ignored,)=(p.amount,p=Pair(7,to));p=Pair(amount,to);to.transfer(p.amount);to.transfer(q.amount);}');for(const f of resolved(r,'restored'))assert.match(f.expanded_amount_expression,/amount/);
});

test('enum members use their compiler declaration order even when distinct enums reuse a name',()=>{
 const r=inspect('enum Left{OFF,OK}enum Right{OK,OFF}function enumAmount(address payable to) external {to.transfer(uint256(Left.OK)*10+uint256(Right.OK));}'),f=resolved(r,'enumAmount')[0];assert.match(f.expanded_amount_expression,/uint256\(1\)/);assert.match(f.expanded_amount_expression,/uint256\(0\)/);const values=r.sections.expressions.filter(e=>e.enum_value_basis==='compiler_enum_definition_member_order');assert.ok(values.some(e=>e.enum_member_index===1));assert.ok(values.some(e=>e.enum_member_index===0));assert.equal(new Set(values.map(e=>e.enum_type_declaration_id)).size,2);
});

test('readonly error-enum and struct returns retain the enum result and payment fields',()=>{
 const r=inspect('enum Error{OK,INVALID}function _quote(uint256 amount,address payable to,bool invalid) internal pure returns(Error,Pair memory){if(invalid)return(Error.INVALID,Pair(0,to));return(Error.OK,Pair(amount,to));}function enumReturn(uint256 amount,address payable to,bool invalid) external {(Error error,Pair memory p)=_quote(amount,to,invalid);require(error==Error.OK);p.recipient.transfer(p.amount);}'),f=resolved(r,'enumReturn')[0];assert.match(f.expanded_amount_expression,/amount/);assert.match(f.expanded_amount_expression,/invalid/);const guard=r.sections.permissions.find(g=>g.method_id===methods(r,'enumReturn').method_id&&g.condition==='error==Error.OK');assert.ok(guard);assert.equal(guard.condition_derivation_status,'represented_source_dependencies_resolved');assert.ok(!guard.expanded_condition.includes('Error.OK'));
});

test('enum struct fields retain declaration identities and their stored constructor member value',()=>{
 const r=inspect('enum Phase{OFF,ON}struct EnumPair{Phase phase;uint256 amount;}function enumField(uint256 amount,address payable to) external {EnumPair memory p=EnumPair(Phase.ON,amount);require(p.phase==Phase.ON);to.transfer(p.amount);}');resolved(r,'enumField');const guard=r.sections.permissions.find(g=>g.method_id===methods(r,'enumField').method_id&&g.condition==='p.phase==Phase.ON');assert.equal(guard.condition_derivation_status,'represented_source_dependencies_resolved');assert.ok(!guard.expanded_condition.includes('Phase.ON'));assert.ok(r.sections.expressions.some(e=>e.source_aggregate_field_declaration_id&&e.type.startsWith('enum ')));
});

test('aggregate method references use the existing expression and dataflow queries with exact counts',()=>{
 const r=inspect('function references(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);to.transfer(p.amount);}'),m=methods(r,'references'),detail=r.sections.method_details.find(d=>d.method_id===m.method_id);assert.ok(m.source_aggregate_version_expression_ids.length);assert.ok(m.source_aggregate_field_expression_ids.length);assert.ok(m.source_aggregate_reference_binding_ids.length);
 for(const section of['expressions','dataflow']){const ref=detail.aggregate_model_references[section];assert.deepEqual(ref.query,{section,method_id:m.method_id});assert.equal(ref.record_count,r.sections[section].filter(row=>row.method_id===m.method_id).length);assert.ok(ref.aggregate_records>0);}
});

test('a scalar field assignment links the new value, old version and source write evidence',()=>{
 const r=inspect('function written(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);p.amount=7;to.transfer(p.amount);}'),f=resolved(r,'written')[0],field=r.sections.expressions.find(e=>e.expression_id===f.amount_expression_id);assert.ok(field.binding_expression_id);const version=r.sections.expressions.find(e=>field.aggregate_version_expression_ids.includes(e.expression_id)&&e.source_aggregate_mutation?.kind==='compiler_scalar_field_assignment');assert.ok(version);assert.equal(version.parent_aggregate_version_ids.length,1);const old=r.sections.expressions.find(e=>e.expression_id===version.parent_aggregate_version_ids[0]);assert.ok(old.fields.some(v=>v.value_expression_id!==field.binding_expression_id));const dependency=f.analysis_dependencies;for(const row of[version,old]){assert.ok(dependency.expression_ids.includes(row.expression_id));assert.ok(row.evidence_ids.every(id=>dependency.evidence_ids.includes(id)));}assert.match(f.expanded_amount_expression,/7/);
});

test('exhausted enum summaries preserve later source guards, writes and ordinary scalar payments',()=>{
 const r=inspect('enum Tag{OK,BAD}address admin;uint256 stored;function _tag(uint256 amount) internal pure returns(Tag,uint256){return(Tag.OK,amount+1);}function _plain(uint256 amount) internal pure returns(uint256){return amount+1;}function bounded(uint256 amount,address payable to) external {(Tag tag,uint256 payment)=_tag(amount);require(tag==Tag.OK);to.transfer(payment);require(msg.sender==admin);stored=amount;}function plainAfter(uint256 amount,address payable to) external {to.transfer(_plain(amount));}',{enhancedReturnExpressions:0}),f=funds(r,'bounded')[0],flow=r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id),method=methods(r,'bounded');
 assert.equal(method.source_checkpoint,'method_body_saved');assert.equal(f.amount_derivation_status,'partial');assert.ok(flow.unresolved.includes('INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET'));assert.ok(f.analysis_dependencies.gap_ids.length);
 assert.ok(r.sections.permissions.some(g=>g.method_id===method.method_id&&g.condition==='msg.sender==admin'));assert.ok(r.sections.storage.some(s=>s.method_id===method.method_id&&s.access==='write'&&s.value_expression==='amount'));
 assert.ok(r.sections.frontier.some(f=>f.method_id===method.method_id&&f.code==='INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET'&&f.budget===0&&f.source_callsite.file==='Aggregate.sol'));
 assert.match(resolved(r,'plainAfter')[0].expanded_amount_expression,/amount.*\+.*1/);const budget=r.sections.engines.find(e=>e.engine==='solc-ast').budget;assert.equal(budget.additional_return_expressions,0);assert.equal(budget.additional_return_expression_limit,0);assert.ok(r.sections.methods.every(m=>m.source_checkpoint!=='compiled_entry_only'));
});

test('a stopped struct return does not charge or taint subsequent direct constructor fields',()=>{
 const r=inspect('function _make(uint256 amount,address payable to) internal pure returns(Pair memory){return Pair(amount,to);}function boundedStruct(uint256 amount,address payable to) external {Pair memory p=_make(amount,to);to.transfer(p.amount);}function directAfter(uint256 amount,address payable to) external {Pair memory p=Pair(amount,to);p.recipient.transfer(p.amount);}',{enhancedReturnExpressions:0}),f=funds(r,'boundedStruct')[0];
 assert.equal(f.amount_derivation_status,'partial');assert.ok(r.sections.expressions.some(e=>e.method_id===methods(r,'boundedStruct').method_id&&e.unresolved_return_value==='INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET'));assert.equal(methods(r,'boundedStruct').source_checkpoint,'method_body_saved');assert.match(resolved(r,'directAfter')[0].expanded_amount_expression,/amount/);assert.equal(r.sections.engines.find(e=>e.engine==='solc-ast').budget.additional_return_expressions,0);
});

test('partial enhanced attempts consume their allowance and remain unresolved at every later call',()=>{
 const r=inspect('enum Tag{OK,BAD}function _tag(uint256 amount) internal pure returns(Tag,uint256){if(amount==0)return(Tag.BAD,0);return(Tag.OK,amount+1);}function one(uint256 amount,address payable to) external {(Tag tag,uint256 payment)=_tag(amount);require(tag==Tag.OK);to.transfer(payment);}function two(uint256 amount,address payable to) external {(Tag tag,uint256 payment)=_tag(amount);require(tag==Tag.OK);to.transfer(payment);}',{enhancedReturnExpressions:5}),budget=r.sections.engines.find(e=>e.engine==='solc-ast').budget;
 assert.equal(budget.additional_return_expressions,5);assert.equal(budget.additional_return_expression_limit,5);assert.ok(budget.consumed_expressions<=budget.total_expressions);
 for(const name of['one','two']){assert.equal(methods(r,name).source_checkpoint,'method_body_saved');const f=funds(r,name)[0];assert.equal(f.amount_derivation_status,'partial');assert.ok(r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id).unresolved.includes('INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET'));assert.ok(r.sections.frontier.some(f=>f.method_id===methods(r,name).method_id&&f.code==='INTERNAL_RETURN_ADDITIONAL_EXPRESSION_BUDGET'&&f.consumed===5));}
});
