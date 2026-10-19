import test from'node:test';import assert from'node:assert/strict';import{emptyResult,makeMethod}from'../model.mjs';import{analyzeCallAbi}from'../ir/call-abi.mjs';
function run(bytes,call_type='CALL'){const r=emptyResult({code_hash:'0x'+'1'.repeat(64)}),m=makeMethod(r.snapshot,'entry');r.sections.methods.push(m);
 r.sections.call_input_bindings=[{call_input_binding_id:'input',method_id:m.method_id,ir_id:'ir',memory_version_id:'version',call_type,target_expression:'calldataload(4)',cases:[{conditions:[{expression:'flag',expected:true}],bytes:Array.from(Buffer.from(bytes,'hex'),value=>({kind:'constant',value}))}],unresolved:[],evidence_ids:[]}];return analyzeCallAbi(r);}
const word=n=>BigInt(n).toString(16).padStart(64,'0');
test('CALL byte arguments expose exact full-width money roles without promoting selector matches to settlement',()=>{
 const amount=(1n<<255n)+9007199254740993n,r=run('23b872dd'+word(17)+word(29)+word(amount)),m=r.sections.fund_call_models[0];
 assert.equal(m.amount.value,String(amount));assert.equal(m.sender.value,'0x'+'11'.padStart(40,'0'));assert.equal(m.recipient.value,'0x'+'1d'.padStart(40,'0'));
 assert.equal(m.asset_expression,'calldataload(4)');assert.equal(m.status,'pending');assert.equal(m.target_implementation_verified,false);assert.equal(r.sections.funds.length,0);assert.ok(m.unresolved.includes('TARGET_INTERFACE_NOT_VERIFIED'));
 assert.deepEqual(m.conditions,[{expression:'flag',expected:true}]);assert.equal(r.sections.methods[0].fund_call_model_ids[0],m.fund_call_model_id);
});
test('truncated or incompatible candidate words preserve invalid byte/type facts and never fabricate complete amounts',()=>{
 const short=run('a9059cbb'+word(7)),bad=run('a9059cbb'+word(1n<<200n)+word(8));assert.equal(short.sections.call_argument_models[0].status,'invalid');assert.equal(short.sections.fund_call_models[0].amount,null);
 assert.equal(bad.sections.call_argument_models[0].status,'invalid');assert.ok(bad.sections.fund_call_models[0].unresolved.includes('ABI_UNSIGNED_HIGH_BITS_NONZERO'));
});
test('delegate and static calls do not acquire ordinary external asset transfer roles and unknown schemas stay untyped',()=>{
 for(const op of['STATICCALL','DELEGATECALL','CALLCODE'])assert.equal(run('a9059cbb'+word(1)+word(2),op).sections.fund_call_models.length,0);
 const unknown=run('12345678'+word(1));assert.equal(unknown.sections.call_argument_models[0].status,'untyped_bytes');assert.equal(unknown.sections.abi_values.length,0);assert.equal(unknown.sections.fund_call_models.length,0);
});
