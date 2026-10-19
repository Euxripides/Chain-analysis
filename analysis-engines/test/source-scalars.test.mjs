import test from'node:test';import assert from'node:assert/strict';import{SourceScalars}from'../ir/source-scalars.mjs';
import{analyze}from'../analyze.mjs';import{createHash}from'node:crypto';
const decl={id:17,type:'uint256'};
function fixture(options={}){const result={sections:{}},tracker=new SourceScalars(result,{method_id:'method'},options);return{result,tracker};}
test('source frame identities satisfy the query identifier contract and survive ordinary forks',()=>{
 const{tracker}=fixture();assert.match(tracker.frameId,/^[a-f0-9]{40}$/);assert.equal(tracker.fork().frameId,tracker.frameId);
 assert.notEqual(tracker.fork({newFunction:true,frameId:'call:2'}).frameId,tracker.frameId);
});
test('captured values stay bound after later assignment and across a child frame',()=>{
 const{tracker}=fixture();const first=tracker.capture(decl,'entry-value'),read=tracker.get(decl);
 tracker.capture(decl,'later-value');assert.equal(read.value_expression_id,'entry-value');assert.equal(tracker.version(first),read);
 const child=tracker.fork({newFunction:true,frameId:'call:1'});assert.equal(child.get(decl),null);child.capture(decl,'child-parameter');assert.equal(tracker.get(decl).value_expression_id,'later-value');
});
test('normal branch joins retain both source values and the evaluated condition',()=>{
 const{tracker,result}=fixture();tracker.capture(decl,'initial');const yes=tracker.fork(),no=tracker.fork();yes.capture(decl,'requested');
 let captured;tracker.merge(yes,no,{conditionExpression:'request-is-smaller',declarations:()=>decl,createPhi:(d,operands)=>{captured=operands;return'phi-expression';}});
 assert.deepEqual(captured,['request-is-smaller','requested','initial']);assert.equal(tracker.get(decl).value_expression_id,'phi-expression');
 assert.deepEqual(result.sections.source_scalar_phis[0].cases.map(c=>tracker.version(c.source_scalar_version_id).value_expression_id),['requested','initial']);
});
test('a terminated branch never supplies the normal successor value',()=>{
 const{tracker}=fixture();tracker.capture(decl,'before');const yes=tracker.fork(),no=tracker.fork();yes.capture(decl,'reverted-only');no.capture(decl,'surviving');
 tracker.merge(yes,no,{yesTerminated:true,conditionExpression:'guard',declarations:()=>decl,createPhi:()=>{throw Error('unreachable join');}});
 assert.equal(tracker.get(decl).value_expression_id,'surviving');
});
test('unknown writes and unavailable branch values cannot turn into resolved values',()=>{
 const{tracker,result}=fixture();tracker.capture(decl,'before');const yes=tracker.fork(),no=tracker.fork();yes.invalidate([decl],'UNKNOWN_YUL_WRITE');
 tracker.merge(yes,no,{conditionExpression:'guard',declarations:()=>decl,createPhi:()=>{throw Error('must not fabricate an operand');}});
 assert.ok(tracker.get(decl).unresolved.includes('UNKNOWN_YUL_WRITE'));assert.equal(tracker.get(decl).value_expression_id,null);assert.equal(result.sections.source_scalar_phis[0].status,'partial');
});
test('version budget exhaustion replaces the current binding with an explicit gap',()=>{
 const{tracker,result}=fixture({budget:{versions:0,maxVersions:1,maxPerMethod:1}});const first=tracker.capture(decl,'before');tracker.capture(decl,'after');tracker.capture({...decl,id:18},'other');
 assert.ok(tracker.get(decl).unresolved.includes('SOURCE_SCALAR_VERSION_BUDGET'));assert.equal(tracker.version(first).value_expression_id,'before');assert.equal(result.sections.source_scalar_versions.length,2);
});
function analyzeScalarSource(body){
 const material={kind:'source',source:{language:'Solidity',files:{'Scalar.sol':'pragma solidity ^0.8.24; contract Scalar {'+body+'}'},compiler_version:'0.8.30+commit.73712a01',target:'Scalar.sol:Scalar',settings:{evmVersion:'shanghai'}}};
 const raw=JSON.stringify(material);return analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:createHash('sha256').update(raw).digest('hex'),state_granularity:'symbolic_state',evm_rules:'shanghai'},engines:['whatsabi','sevm','solc-ast']});
}

test('unknown tuple returns retain distinct typed projections and their external dependency',()=>{
 const r=analyzeScalarSource('function values() external view returns(uint,uint){return(block.number,block.timestamp);} function pay(address payable to) external {(uint first,uint second)=this.values();require(first!=second);to.transfer(first+second);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),guard=r.sections.permissions.find(g=>g.method_id===m.method_id),flow=r.sections.dataflow.find(d=>d.dataflow_id===guard.condition_derivation_id);
 assert.equal(guard.condition_derivation_status,'partial');
 assert.match(guard.expanded_condition,/tuple_component\([\s\S]*, 0\)/);
 assert.match(guard.expanded_condition,/tuple_component\([\s\S]*, 1\)/);
 assert.ok(flow.unresolved.includes('TUPLE_COMPONENT_UNAVAILABLE'));
 assert.ok(flow.unresolved.includes('EXTERNAL_FUNCTION_RETURN_NOT_BOUND_TO_EXECUTION'));
 const call=r.sections.expressions.find(e=>e.method_id===m.method_id&&e.text==='this.values()'&&e.external_return_dependency_id);
 assert.deepEqual(call.return_component_types,['uint256','uint256']);
 const projections=r.sections.expressions.filter(e=>e.method_id===m.method_id&&e.symbolic_format==='tuple_component');
 assert.ok(projections.some(e=>e.component_index===0&&e.type==='uint256'));
 assert.ok(projections.some(e=>e.component_index===1&&e.type==='uint256'));
});

test('a mixed scalar and reference return cannot masquerade as a scalar value',()=>{
 const r=analyzeScalarSource('function values(uint value) internal pure returns(uint,string memory){return(value,"label");} function pay(uint value,address payable to) external {(uint quantity,)=values(value);to.transfer(quantity);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),fund=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment'),flow=r.sections.dataflow.find(d=>d.dataflow_id===fund.amount_derivation_id);
 assert.equal(fund.amount_derivation_status,'partial');assert.match(fund.expanded_amount_expression,/tuple_component\([\s\S]*, 0\)/);
 assert.ok(flow.unresolved.includes('INTERNAL_RETURN_NONSCALAR_OR_TUPLE'));
 assert.ok(flow.unresolved.includes('TUPLE_COMPONENT_UNAVAILABLE'));
});

test('unnamed return declarations consume no local scalar versions while named returns remain captured',()=>{
 const r=analyzeScalarSource('function step(uint input) internal pure returns(uint){return input+1;} function pay(uint amount,address payable to) external {uint quantity=step(amount);quantity=step(quantity);to.transfer(quantity);} function named(uint input) external pure returns(uint output){output=input;return output+1;}');
 const pay=r.sections.methods.find(m=>m.name==='pay'),named=r.sections.methods.find(m=>m.name==='named');
 const versions=r.sections.source_scalar_versions.filter(v=>v.method_id===pay.method_id);
 assert.ok(!versions.some(v=>['named_return_default','return_statement_assignment'].includes(v.kind)));
 const namedVersions=r.sections.source_scalar_versions.filter(v=>v.method_id===named.method_id);
 assert.ok(namedVersions.some(v=>v.kind==='named_return_default'));assert.ok(namedVersions.some(v=>v.kind==='return_statement_assignment'));
 const fund=r.sections.funds.find(f=>f.method_id===pay.method_id&&f.kind==='native_payment');assert.equal(fund.amount_derivation_status,'represented_source_dependencies_resolved');
 const evaluate=new Function('amount','return '+fund.expanded_amount_expression.replace(/\b\d+\b/g,'$&n'));
 for(const amount of[0n,7n,9007199254740993n])assert.equal(evaluate(amount),amount+2n);
});

test('readonly string message parameters preserve require argument evaluation and guarded arithmetic',()=>{
 const r=analyzeScalarSource('function divide(uint amount,uint parts,string memory reason) internal pure returns(uint){require(parts>0,reason);return amount/parts;} function pay(uint amount,uint parts,address payable to) external {to.transfer(divide(amount,parts,"zero divisor"));}');
 const m=r.sections.methods.find(m=>m.name==='pay'),fund=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment'),flow=r.sections.dataflow.find(d=>d.dataflow_id===fund.amount_derivation_id);
 assert.equal(fund.amount_derivation_status,'represented_source_dependencies_resolved');
 const summary=r.sections.dataflow.find(d=>d.method_id===m.method_id&&d.kind==='internal_return_derivation'&&d.original_expression.startsWith('divide('));
 assert.ok(summary.successful_return_paths.some(p=>p.conditions.some(c=>c.includes('parts>0'))));
 assert.ok(summary.successful_return_paths.some(p=>p.execution_steps.some(s=>s.kind==='evaluate'&&s.expression.includes('zero divisor'))));
 const evaluate=new Function('amount','parts','return '+fund.expanded_amount_expression);
 for(const amount of[0n,19n,9007199254740993n])for(const parts of[1n,3n,17n])assert.equal(evaluate(amount,parts),amount/parts);
 const unsafe=analyzeScalarSource('function divide(uint amount,uint parts,string memory reason,string memory other) internal pure returns(uint){require(parts>0,(reason=other));return amount/parts;} function pay(uint amount,uint parts,address payable to) external {to.transfer(divide(amount,parts,"first","second"));}');
 const um=unsafe.sections.methods.find(m=>m.name==='pay'),uf=unsafe.sections.funds.find(f=>f.method_id===um.method_id&&f.kind==='native_payment'),ud=unsafe.sections.dataflow.find(d=>d.dataflow_id===uf.amount_derivation_id);
 assert.equal(uf.amount_derivation_status,'partial');assert.ok(ud.unresolved.includes('INTERNAL_RETURN_REQUIRE_MESSAGE_EFFECT_UNSUPPORTED'));
});
test('compiled branch assignment yields an independently evaluable capped payment',()=>{
 const r=analyzeScalarSource('function pay(uint due,uint requested,address payable to) external {uint quantity=due;if(requested>0&&requested<quantity)quantity=requested;to.transfer(quantity);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),fund=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment');
 assert.equal(fund.amount_derivation_status,'represented_source_dependencies_resolved');
 const evaluate=new Function('due','requested','return '+fund.expanded_amount_expression.replace(/\b\d+\b/g,'$&n'));
 const max=(1n<<256n)-1n;
 for(const due of[0n,1n,9007199254740993n,max])for(const requested of[0n,1n,17n,max])assert.equal(evaluate(due,requested),requested>0n&&requested<due?requested:due);
 const phi=r.sections.source_scalar_phis.find(p=>p.method_id===m.method_id);assert.ok(phi);assert.equal(phi.cases.length,2);
});
test('unsupported local increments invalidate entry-parameter fallbacks',()=>{
 const r=analyzeScalarSource('function pay(uint quantity,address payable to) external {quantity++;to.transfer(quantity);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),f=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment'),d=r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id);
 assert.equal(f.amount_derivation_status,'partial');assert.ok(d.unresolved.includes('SOURCE_SCALAR_INCREMENT_REQUIRES_ORDERED_VALUE_MODEL'));
});
test('repeated modifier definitions keep their own captured parameter values',()=>{
 const r=analyzeScalarSource('modifier sendAfter(uint quantity,address payable receiver){_;receiver.transfer(quantity);} function pay(uint first,uint second,address payable receiver) external sendAfter(first,receiver) sendAfter(second,receiver) {}');
 const m=r.sections.methods.find(m=>m.name==='pay'),funds=r.sections.funds.filter(f=>f.method_id===m.method_id&&f.kind==='native_payment');
 assert.equal(funds.length,2);for(const f of funds)assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');
 const evaluate=f=>new Function('first','second','return '+f.expanded_amount_expression);
 assert.equal(evaluate(funds[0])(3n,7n),7n);assert.equal(evaluate(funds[1])(3n,7n),3n);
});
test('tuple writes use all right-hand values before rebinding any destination',()=>{
 const r=analyzeScalarSource('function pay(uint left,uint right,address payable to) external {(left,right)=(right,left);to.transfer(left*100+right);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),f=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment');
 assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');const evaluate=new Function('left','right','return '+f.expanded_amount_expression.replace(/\b\d+\b/g,'$&n'));
 for(const left of[0n,3n,9007199254740993n])for(const right of[1n,7n])assert.equal(evaluate(left,right),right*100n+left);
});
test('short-circuit side effects change only the evaluated source binding',()=>{
 for(const op of['&&','||']){
  const r=analyzeScalarSource('function pay(bool pick,uint quantity,address payable to) external {bool ignored=pick '+op+' ((quantity=3)>0);to.transfer(quantity);}');
  const m=r.sections.methods.find(m=>m.name==='pay'),f=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment');
  assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');const evaluate=new Function('pick','quantity','uint256','return '+f.expanded_amount_expression.replace(/\b\d+\b/g,'$&n'));
  for(const pick of[false,true])assert.equal(evaluate(pick,9n,v=>BigInt.asUintN(256,v)),(op==='&&'?pick:!pick)?3n:9n);
  const partial=r.sections.source_scalar_versions.filter(v=>v.method_id===m.method_id&&v.value_derivation_status==='partial');assert.ok(partial.length);
  assert.ok(partial.every(v=>v.analysis_dependencies.support==='pending'));
 }
});
test('a value captured before an unbounded loop survives while the modified local remains unknown',()=>{
 const r=analyzeScalarSource('function pay(uint quantity,uint count,address payable to) external {uint saved=quantity;for(uint i=0;i<count;i++){quantity=7;}to.transfer(saved);to.transfer(quantity);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),funds=r.sections.funds.filter(f=>f.method_id===m.method_id&&f.kind==='native_payment');
 assert.equal(funds[0].amount_derivation_status,'represented_source_dependencies_resolved');assert.equal(funds[1].amount_derivation_status,'partial');
 const evaluate=new Function('quantity','return '+funds[0].expanded_amount_expression);assert.equal(evaluate(9007199254740993n),9007199254740993n);
});
test('assembly writes invalidate local values while read-only assembly preserves them',()=>{
 for(const write of[false,true]){
  const r=analyzeScalarSource('function pay(uint quantity,address payable to) external {assembly {'+(write?'quantity := 7':'pop(quantity)')+'}to.transfer(quantity);}');
  const m=r.sections.methods.find(m=>m.name==='pay'),f=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment');
  assert.equal(f.amount_derivation_status,write?'partial':'represented_source_dependencies_resolved');
 }
});
test('an opaque function pointer return is not reported as a recovered payment amount',()=>{
 const r=analyzeScalarSource('function twice(uint n) internal pure returns(uint){return n*2;} function pay(uint quantity,address payable to) external {function(uint) internal pure returns(uint) fn=twice;uint amount=fn(quantity);to.transfer(amount);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),f=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment'),d=r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id);
 assert.equal(f.amount_derivation_status,'partial');assert.ok(d.unresolved.includes('SOURCE_DYNAMIC_FUNCTION_RETURN_UNBOUND'));
});
test('a complete scalar overwrite retains history without inheriting an obsolete value gap',()=>{
 const r=analyzeScalarSource('function pay(uint quantity,address payable to) external {quantity++;quantity=7;to.transfer(quantity);}');
 const m=r.sections.methods.find(m=>m.name==='pay'),f=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment');
 assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');assert.match(f.expanded_amount_expression,/^\(*uint256\(7\)\)*$/);assert.equal(new Function('uint256','return '+f.expanded_amount_expression.replace(/\b\d+\b/g,'$&n'))(v=>BigInt.asUintN(256,v)),7n);
 assert.equal(f.analysis_dependencies.support,'static_derivation');
 const replacement=r.sections.source_scalar_versions.find(v=>v.method_id===m.method_id&&v.kind==='assignment');assert.ok(replacement.previous_source_scalar_version_id);assert.deepEqual(replacement.parent_source_scalar_version_ids,[]);
});
test('contract and enum arrays remain aggregate references and cannot acquire scalar input versions',()=>{
 const r=analyzeScalarSource('enum Tag{A,B} function pay(Scalar[] memory refs,Tag[] memory tags,address payable to) external {uint amount=refs.length+tags.length;to.transfer(amount);}');
 assert.equal(r.sections.source_scalar_versions.filter(v=>(v.type||'').includes('[')).length,0);
 const m=r.sections.methods.find(m=>m.name==='pay'),f=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment'),d=r.sections.dataflow.find(d=>d.dataflow_id===f.amount_derivation_id);
 assert.equal(f.amount_derivation_status,'partial');assert.ok(d.unresolved.includes('SOURCE_AGGREGATE_REFERENCE_VALUE_REQUIRES_MEMORY_MODEL'));
});

test('unresolved assignment and increment expressions retain their compiler write targets',()=>{
 for(const body of[
  'function check(uint x,uint y)external pure returns(uint z){require((z = x + y) >= x);}',
  'function check(uint x)external pure{uint z=7;require((z += x) > 0);}',
  'function check(uint x)external pure{uint z=7;require(z++ < x);}'
 ]){
  const r=analyzeScalarSource(body),m=r.sections.methods.find(m=>m.name==='check'),guard=r.sections.permissions.find(p=>p.method_id===m.method_id),
   derivation=r.sections.dataflow.find(d=>d.dataflow_id===guard.condition_derivation_id);
  assert.ok(guard.expanded_condition.includes('z'));assert.doesNotMatch(guard.expanded_condition,/\b(?:0|7)\s*(?:\+=|=(?!=)|\+\+)/);
  assert.equal(guard.condition_derivation_status,'partial');assert.ok(derivation.unresolved.includes('LOCAL_EXPRESSION_EFFECT_REQUIRES_ORDERED_RETURN_MODEL'));
 }
});

test('preserving an assignment target retains the right-hand caller values',()=>{
 const r=analyzeScalarSource('function add(uint x,uint y)internal pure returns(uint z){require((z = x + y) >= x);} function check(uint left,uint right)external pure{add(left,right);}'),
  m=r.sections.methods.find(m=>m.name==='check'),guard=r.sections.permissions.find(p=>p.method_id===m.method_id),expressions=new Map(r.sections.expressions.map(e=>[e.expression_id,e]));
 assert.equal(guard.condition_derivation_status,'partial');assert.match(guard.expanded_condition,/\bz\s*=/);assert.doesNotMatch(guard.expanded_condition,/\b[xy]\b|\b0\s*=(?!=)/);
 assert.match(guard.expanded_condition,/\bleft\b/);assert.match(guard.expanded_condition,/\bright\b/);
 const assignment=r.sections.expressions.find(e=>e.method_id===m.method_id&&e.op==='='&&e.write_target_expression_id);assert.ok(assignment);
 assert.equal(expressions.get(assignment.write_target_expression_id).text,'z');assert.equal(assignment.write_target_role,'write_only');assert.ok(expressions.has(assignment.write_value_expression_id));
 const evaluate=new Function('left','right','let z=0n;return '+guard.expanded_condition.replace(/\b\d+\b/g,'$&n'));
 for(const left of[0n,1n,9007199254740993n])for(const right of[0n,7n,1n<<128n])assert.equal(evaluate(left,right),true);
});
