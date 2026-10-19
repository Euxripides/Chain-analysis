import test from'node:test';import assert from'node:assert/strict';
import{emptyResult,finalize}from'../model.mjs';import{analyzeSource}from'../source.mjs';import{applyDataflow}from'../ir/dataflow.mjs';import{applyFundsRules}from'../rules/funds.mjs';
import{sourceBytesFixture}from'./fixtures/source-bytes.mjs';
const fixture=sourceBytesFixture(),r=emptyResult(fixture.snapshot);analyzeSource(r,fixture.source,fixture.bytecode);finalize(r);applyDataflow(r);applyFundsRules(r);
const method=name=>r.sections.methods.find(m=>m.name===name),funds=name=>r.sections.funds.filter(f=>f.method_id===method(name).method_id),
 bindings=name=>r.sections.call_input_bindings.filter(b=>b.method_id===method(name).method_id);

test('source method checkpoints all survive the bytes integration',()=>{
 const bodies=r.sections.methods.filter(m=>m.dispatch_role!=='implicit_source_definition');assert.ok(bodies.length>20);assert.ok(bodies.every(m=>m.source_checkpoint==='method_body_saved'),JSON.stringify(bodies.filter(m=>m.source_checkpoint!=='method_body_saved')));
 const evidence=new Set(r.sections.evidence.map(e=>e.evidence_id)),versions=new Set(r.sections.source_memory_versions.map(v=>v.source_memory_version_id));
 for(const b of r.sections.call_input_bindings){assert.ok(b.evidence_ids.every(e=>evidence.has(e)));assert.ok(b.source_memory_version_ids.every(v=>versions.has(v)));}
});
test('scalar reassignments cannot change the already constructed call arguments',()=>{
 for(const [name,expected]of[['direct','amount'],['helper','amount/3']]){const f=funds(name)[0];assert.ok(f,name);assert.equal(f.expanded_amount_expression.replaceAll(/[()\s]/g,''),expected);assert.equal(f.amount_derivation_status,'represented_source_dependencies_resolved');assert.ok(!f.expanded_amount_expression.includes('999')&&!f.expanded_amount_expression.includes('777'));
  const v=r.sections.source_memory_versions.find(v=>v.source_memory_version_id===f.source_memory_version_id);assert.equal(f.amount_expression_id,v.argument_expression_ids.at(-1));assert.equal(bindings(name)[0].status,'exact');}
});
test('internal explicit, named and tuple returns bind their original bytes constructors',()=>{
 for(const name of['returned','named']){assert.equal(funds(name).length,1,name);assert.equal(funds(name)[0].expanded_amount_expression.replaceAll(/[()\s]/g,''),'amount');assert.equal(bindings(name)[0].status,'exact');}
 for(const name of['tupled','tupleAssigned']){assert.equal(funds(name).length,2,name);assert.deepEqual(funds(name).map(f=>f.expanded_amount_expression.replaceAll(/[()\s]/g,'')),['amount','amount+1']);assert.ok(bindings(name).every(b=>b.status==='exact'));}
});
test('callee parameter rebinding, compiler declaration shadowing and unrelated object writes preserve the caller object',()=>{
 for(const name of['rebound','unrelated','shadow']){assert.equal(funds(name).length,1,name);assert.equal(funds(name)[0].expanded_amount_expression.replaceAll(/[()\s]/g,''),'amount');assert.equal(bindings(name)[0].status,'exact');}
 assert.equal(funds('mutated').length,0);assert.equal(bindings('mutated')[0].cases[0].bytes[0].value,0);assert.equal(bindings('mutated')[0].cases[0].encoding_provenance_status,'modified_or_unverified');
});
test('branch alternatives remain correlated and missing branches do not acquire a constructor',()=>{
 for(const name of['forked','ternary']){assert.equal(funds(name).length,0,name);const b=bindings(name)[0];assert.equal(b.status,'conditional');assert.equal(b.cases.length,2);assert.ok(b.cases.some(c=>c.conditions.includes('flag')));assert.ok(b.cases.some(c=>c.conditions.includes('!(flag)')));}
 assert.equal(funds('missing').length,0);assert.equal(bindings('missing')[0].status,'partial');assert.equal(funds('surviving').length,1);assert.ok(funds('surviving')[0].conditions.includes('!(flag)'));
});
test('loops, recursive calls, heap clobbers and source pointer writes cannot publish stale encoded funds',()=>{
 for(const name of['repeated','recursive','heapClobbered','assemblyPointer']){assert.equal(funds(name).length,0,name);assert.equal(bindings(name)[0].status,'partial',name);}
 assert.ok(bindings('repeated')[0].unresolved.includes('SOURCE_BYTES_LOOP_REQUIRES_INVARIANT'));
 assert.ok(bindings('heapClobbered')[0].unresolved.includes('SOURCE_BYTES_ALLOCATOR_UNVERIFIED'));
});
test('an aliased object feeds the first assembly CALL while proven scratch-only writes preserve later bytes',()=>{
 assert.equal(funds('raw').length,1);assert.equal(funds('raw')[0].expanded_amount_expression.replaceAll(/[()\s]/g,''),'amount');assert.equal(bindings('raw')[0].status,'exact');assert.equal(bindings('raw')[0].byte_length,'68');
 assert.equal(funds('scratch').length,1);assert.equal(bindings('scratch')[0].status,'exact');
});
test('unsupported struct values keep the canonical tuple type and a visible value gap',()=>{
 const v=r.sections.source_memory_versions.find(v=>v.method_id===method('nested').method_id&&v.encoding==='encode');assert.deepEqual(v.argument_types,['(uint256,bytes)']);assert.equal(v.valid_encoding,false);assert.equal(bindings('nested')[0].status,'partial');
});
test('decoding an unmodified same-type encoding reuses values captured before subsequent scalar assignment',()=>{
 const f=funds('decoded')[0];assert.ok(f);assert.equal(f.expanded_amount_expression.replaceAll(/[()\s]/g,''),'amount/7');assert.equal(f.expanded_to_expression.replaceAll(/[()\s]/g,''),'to');
 const decoded=r.sections.abi_decodings.find(d=>d.method_id===method('decoded').method_id);assert.ok(decoded.source_argument_equivalence);assert.deepEqual(decoded.types,['address','uint256']);
 const unknown=r.sections.abi_decodings.find(d=>d.method_id===method('decodedUnknown').method_id);assert.equal(unknown.status,'partial');assert.equal(unknown.source_argument_equivalence,null);assert.equal(funds('decodedUnknown')[0].amount_derivation_status,'partial');
});
test('short-circuit mutations preserve the skipped path and dynamic function pointers plus compound writes stay unresolved',()=>{
 for(const name of['shortAnd','shortOr']){assert.equal(funds(name).length,0,name);const b=bindings(name)[0];assert.equal(b.status,'conditional');assert.equal(b.cases.length,2);assert.deepEqual(b.cases.map(c=>c.bytes[0].value).sort((a,b)=>a-b),[0,0xa9]);}
 for(const name of['dynamic','compoundMutated','afterModifier']){assert.equal(funds(name).length,0,name);assert.equal(bindings(name)[0].status,'partial',name);}
 assert.ok(bindings('dynamic')[0].unresolved.includes('SOURCE_BYTES_DYNAMIC_INTERNAL_CALL'));
});
test('an arbitrary loop iteration never substitutes its initializer for the current index',()=>{
 const f=funds('loopIndex')[0];assert.ok(f);assert.equal(f.expanded_amount_expression,'i');assert.equal(f.amount_derivation_status,'partial');
 const v=r.sections.source_memory_versions.find(v=>v.source_memory_version_id===f.source_memory_version_id),d=r.sections.dataflow.find(d=>d.dataflow_id===v.argument_derivation_ids[1]);assert.equal(d.symbolic_expression,'i');assert.ok(d.unresolved.includes('SOURCE_SCALAR_LOOP_REQUIRES_INVARIANT'));
});
test('a CALL value never suppresses the separate typed token argument model',()=>{
 for(const name of['zeroValue','valueAndTokens']){const fs=funds(name);assert.equal(fs.length,2,name);const native=fs.find(f=>f.kind==='native_payment'),token=fs.find(f=>f.kind==='token_transfer_intent');
  assert.ok(native&&token);assert.notEqual(native.fund_id,token.fund_id);assert.equal(native.call_id,token.call_id);assert.equal(token.expanded_amount_expression.replaceAll(/[()\s]/g,''),'amount');assert.equal(token.expanded_to_expression.replaceAll(/[()\s]/g,''),'to');}
 assert.equal(funds('zeroValue').find(f=>f.kind==='native_payment').economic_value_status,'zero_call_value');assert.equal(funds('valueAndTokens').find(f=>f.kind==='native_payment').expanded_amount_expression,'msg.value');
 assert.ok(!method('zeroCallOnly').behaviors.includes('原生币支付'));assert.ok(method('zeroCallOnly').behaviors.includes('外部合约交互'));assert.equal(funds('zeroCallOnly')[0].economic_value_status,'zero_call_value');
});
test('a declared contract method named call retains its outer ABI and revert behavior',()=>{
 assert.equal(funds('wrappedCall').length,0);const m=method('wrappedCall'),c=r.sections.calls.find(c=>c.method_id===m.method_id);assert.equal(c.signature,'call(bytes)');assert.notEqual(c.selector,'0xa9059cbb');assert.equal(c.failure_propagation,'reverts_on_failure');assert.equal(bindings('wrappedCall')[0].byte_length,'164');
});

import {createRequire as consumerRequire}from'node:module';
import {evaluateReturn as consumerEvaluate}from'../../scripts/contract-analysis/expression_model_eval.mjs';
const consumerSources={"legacy": "pragma solidity ^0.4.26;contract TupleNegative{function _select(bytes memory a,bytes memory b,bool flag)internal pure returns(bytes memory){if(flag)return a;return b;}function invertedArgument(address to,bool flag)external{bytes memory a=hex\"01\";bytes memory b=hex\"02\";bytes memory c;if(flag){c=_select(a,b,!flag);}else{c=_select(a,b,!flag);}c[0]=0x07;bool x=to.call(a);require(x);bool y=to.call(b);require(y);}function constantArgument(address to,bool flag)external{bytes memory a=hex\"01\";bytes memory b=hex\"02\";bytes memory c;if(flag){c=_select(a,b,false);}else{c=_select(a,b,true);}c[0]=0x07;bool x=to.call(a);require(x);bool y=to.call(b);require(y);}function twoArguments(address to,bool flag)external{bytes memory a=hex\"01\";bytes memory b=hex\"02\";bytes memory c=_select(a,b,flag);c[0]=0x07;bool x=to.call(a,b);require(x);}}", "effects": "pragma solidity ^0.8.30;contract TupleNegative{function _exists(address target)internal view returns(bool){uint256 size;assembly{size:=extcodesize(target)}return size>0;}function _smash(bytes memory a)internal pure returns(bool){assembly{mstore(add(a,32),7)}return true;}function pureHostCheck(address payable to)external{bytes memory a=hex\"0102\";require(_exists(address(this)),\"code required\");(bool ok,)=to.call(a);require(ok);}function opaqueHostWrite(address payable to)external{bytes memory a=hex\"0102\";require(_smash(a),\"write done\");(bool ok,)=to.call(a);require(ok);}function _allowed()internal pure virtual returns(bool){return true;}function virtualPureCheck(address payable to)external{bytes memory a=hex\"0102\";require(_allowed(),\"allowed\");(bool ok,)=to.call(a);require(ok);}}", "abi": "pragma solidity ^0.8.30;\ncontract ConditionalABI {\n function decodeBranch(bool flag,address target) external pure returns(uint256,bool) {\n  bytes memory data;\n  if(flag)data=abi.encode(uint256(7),true);else data=abi.encode(uint256(9),false);\n  return abi.decode(data,(uint256,bool));\n }\n function decodeTernary(bool flag,address target) external pure returns(uint256,bool) {\n  bytes memory data=flag?abi.encode(uint256(17),false):abi.encode(uint256(19),true);\n  return abi.decode(data,(uint256,bool));\n }\n function callBranch(bool flag,address target) external {\n  bytes memory data;\n  if(flag)data=abi.encodeWithSelector(bytes4(0x12345678),uint256(27),true);\n  else data=abi.encodeWithSelector(bytes4(0x12345678),uint256(29),false);\n  (bool ok,)=target.call(data);require(ok);\n }\n function callTernary(bool flag,address target) external {\n  bytes memory data=flag?abi.encodeWithSelector(bytes4(0x12345678),uint256(37),false):abi.encodeWithSelector(bytes4(0x12345678),uint256(39),true);\n  (bool ok,)=target.call(data);require(ok);\n }\n}"};

function consumerModel(kind,version,target){
 const require=consumerRequire(import.meta.url),solc=require('solc-'+version),{keccak_256}=require('@noble/hashes/sha3.js');
 const standard={language:'Solidity',sources:{'Consumers.sol':{content:consumerSources[kind]}},settings:{...(version==='0.8.30'?{evmVersion:'shanghai'}:{}),outputSelection:{'*':{'*':['abi','evm.deployedBytecode','storageLayout'],'':['ast']}}}};
 const output=JSON.parse((solc.compileStandardWrapper||solc.compile).call(solc,JSON.stringify(standard)));
 assert.deepEqual((output.errors||[]).filter(e=>e.severity==='error'),[]);
 const compiled=output.contracts['Consumers.sol'][target],bytecode='0x'+compiled.evm.deployedBytecode.object;
 const model=emptyResult({identity_kind:'code_only',code_hash:'0x'+Buffer.from(keccak_256(Buffer.from(bytecode.slice(2),'hex'))).toString('hex'),state_granularity:'symbolic_state',evm_rules:'shanghai'});
 analyzeSource(model,{stdJsonInput:standard,compilation:{compilerVersion:solc.version(),fullyQualifiedName:'Consumers.sol:'+target},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi,storageLayout:compiled.storageLayout},bytecode);
 finalize(model);applyDataflow(model);applyFundsRules(model);return model;
}
const consumerLegacy=consumerModel('legacy','0.4.26','TupleNegative'),consumerEffects=consumerModel('effects','0.8.30','TupleNegative'),consumerABI=consumerModel('abi','0.8.30','ConditionalABI');
const consumerMethod=(model,name)=>model.sections.methods.find(m=>m.name===name&&m.dispatch_role==='external_dispatch');
function savedPredicate(model,method,predicate){
 assert.ok(predicate);if(predicate.op==='constant')return;
 if(predicate.op==='not')return savedPredicate(model,method,predicate.value);
 if(['and','or'].includes(predicate.op)){assert.ok(predicate.terms.length);for(const p of predicate.terms)savedPredicate(model,method,p);return;}
 assert.equal(predicate.op,'atom');assert.equal(typeof predicate.expected,'boolean');
 assert.ok(model.sections.expressions.some(e=>e.method_id===method.method_id&&e.expression_id===predicate.expression_id));
}
function consumerAccepts(model,method,predicate,flag){
 savedPredicate(model,method,predicate);
 if(predicate.op==='constant')return predicate.value;
 if(predicate.op==='not')return !consumerAccepts(model,method,predicate.value,flag);
 if(predicate.op==='and')return predicate.terms.every(p=>consumerAccepts(model,method,p,flag));
 if(predicate.op==='or')return predicate.terms.some(p=>consumerAccepts(model,method,p,flag));
 const old=method.entry_return_expression_id;let value;
 try{method.entry_return_expression_id=predicate.expression_id;value=consumerEvaluate(model,method.name,{flag,to:2n})[0];}finally{method.entry_return_expression_id=old;}
 return(typeof value==='boolean'?value:value!==0n)===predicate.expected;
}
test('byte-only effects preserve stack-only EXTCODESIZE and ordinary pure virtual calls while MSTORE stays opaque',()=>{
 for(const name of['pureHostCheck','virtualPureCheck']){
  const method=consumerMethod(consumerEffects,name),bindings=consumerEffects.sections.call_input_bindings.filter(b=>b.method_id===method.method_id);
  assert.equal(bindings.length,1);assert.equal(bindings[0].status,'exact');assert.deepEqual(bindings[0].cases[0].bytes.map(b=>b.value),[1,2]);
 }
 const opaque=consumerMethod(consumerEffects,'opaqueHostWrite'),binding=consumerEffects.sections.call_input_bindings.find(b=>b.method_id===opaque.method_id);
 assert.equal(binding.status,'partial');assert.ok(binding.unresolved.length);
});
test('Solidity 0.4.26 repacked byte arguments retain compatible captured predicates across all arguments',()=>{
 for(const name of['invertedArgument','constantArgument','twoArguments']){
  const method=consumerMethod(consumerLegacy,name),bindings=consumerLegacy.sections.call_input_bindings.filter(b=>b.method_id===method.method_id);
  assert.equal(bindings.length,name==='twoArguments'?1:2);
  for(const binding of bindings){assert.equal(binding.status,'conditional');assert.deepEqual(binding.unresolved,[]);for(const c of binding.cases)savedPredicate(consumerLegacy,method,c.predicate);}
  for(const flag of[false,true])for(const [index,binding]of bindings.entries()){
   const selected=binding.cases.filter(c=>consumerAccepts(consumerLegacy,method,c.predicate,flag));assert.equal(selected.length,1);
   const expected=Buffer.alloc(name==='twoArguments'?64:32);
   if(name==='twoArguments'){expected[0]=flag?7:1;expected[32]=flag?2:7;}else expected[0]=index===0?(flag?1:7):(flag?7:2);
   assert.ok(selected[0].bytes.every(b=>b.kind==='constant'));assert.deepEqual(Buffer.from(selected[0].bytes.map(b=>b.value)),expected);
  }
 }
});
test('conditional ABI decode alternatives and their typed values share saved expression identity',()=>{
 for(const [name,expected]of[['decodeBranch',[[7,true],[9,false]]],['decodeTernary',[[17,false],[19,true]]]]){
  const method=consumerMethod(consumerABI,name),decoded=consumerABI.sections.abi_decodings.find(d=>d.method_id===method.method_id);
  assert.equal(decoded.status,'conditional');assert.deepEqual(decoded.unresolved,[]);assert.equal(decoded.cases.length,2);
  const actual=decoded.cases.map(c=>{savedPredicate(consumerABI,method,c.predicate);return c.value_ids.map(id=>{const value=consumerABI.sections.abi_values.find(v=>v.abi_value_id===id);assert.ok(value);assert.deepEqual(value.predicate,c.predicate);assert.equal(value.source_memory_version_id,c.source_memory_version_id);return value.type==='bool'?value.value:Number(value.value);});});
  assert.deepEqual(actual.sort((a,b)=>a[0]-b[0]),expected);
 }
});
test('conditional typed CALL values preserve each consumed byte case predicate',()=>{
 for(const name of['callBranch','callTernary']){
  const method=consumerMethod(consumerABI,name),binding=consumerABI.sections.call_input_bindings.find(b=>b.method_id===method.method_id);
  assert.equal(binding.status,'conditional');assert.equal(binding.cases.length,2);
  for(const c of binding.cases){savedPredicate(consumerABI,method,c.predicate);const values=consumerABI.sections.abi_values.filter(v=>v.call_input_binding_id===binding.call_input_binding_id&&v.source_memory_version_id===c.source_memory_version_id);assert.equal(values.length,2);for(const value of values){assert.equal(value.status,'decoded');assert.deepEqual(value.predicate,c.predicate);}}
 }
});
