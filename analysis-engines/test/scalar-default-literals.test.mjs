import test from'node:test';import assert from'node:assert/strict';import{createHash}from'node:crypto';import{analyze}from'../analyze.mjs';
function source(body){const material={kind:'source',source:{language:'Solidity',files:{'Default.sol':'pragma solidity ^0.8.24; contract Other {} contract DefaultValues {'+body+'}'},compiler_version:'0.8.30+commit.73712a01',target:'Default.sol:DefaultValues',settings:{evmVersion:'shanghai'}}},raw=JSON.stringify(material);return analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:createHash('sha256').update(raw).digest('hex')},engines:['whatsabi','sevm','solc-ast']});}
test('an uninitialized native recipient preserves the complete typed zero address',()=>{
 const r=source('function pay(uint amount) external {address payable receiver;receiver.transfer(amount);}'),m=r.sections.methods.find(m=>m.name==='pay'),fund=r.sections.funds.find(f=>f.method_id===m.method_id&&f.kind==='native_payment');
 assert.equal(fund.expanded_to_expression,'0x'+'0'.repeat(40));assert.equal(fund.expanded_amount_expression,'amount');
 const version=r.sections.source_scalar_versions.find(v=>v.method_id===m.method_id&&v.kind==='uninitialized_declaration'&&v.type==='address payable'),expression=r.sections.expressions.find(e=>e.expression_id===version.value_expression_id);
 assert.equal(expression.type,'address payable');assert.equal(expression.value,'0x'+'0'.repeat(40));assert.equal(version.status,'source_value_captured');
});
test('source-local and named-return zeros retain their address and fixed-byte widths',()=>{
 const r=source('function empty() public pure returns(address recipient,bytes2 shortValue,bytes32 longValue) {} function locals() public pure returns(bool){Other target;bytes2 shortValue;bytes32 longValue;return address(target)==address(0)&&shortValue==bytes2(0)&&longValue==bytes32(0);}');
 const expressions=new Map(r.sections.expressions.map(e=>[e.expression_id,e]));
 for(const[type,value,kind]of[['address','0x'+'0'.repeat(40),'named_return_default'],['contract Other','0x'+'0'.repeat(40),'uninitialized_declaration'],['bytes2','0x0000','named_return_default'],['bytes32','0x'+'00'.repeat(32),'uninitialized_declaration']]){
  const version=r.sections.source_scalar_versions.find(v=>v.type===type&&v.kind===kind);assert.ok(version,type+':'+kind);assert.equal(expressions.get(version.value_expression_id).value,value);
 }
 const empty=r.sections.methods.find(m=>m.name==='empty');assert.equal(empty.entry_return_model_status,'represented_source_dependencies_resolved');
});
