import test from'node:test';import assert from'node:assert/strict';
import{emptyResult,makeMethod,id}from'../model.mjs';import{normalizeGigahorseDataflow}from'../ir/gigahorse-dataflow.mjs';

function fixture(instructions,blocks,entry='entry'){
 const result=emptyResult({code_hash:'0x'+'1'.repeat(64)}),method=makeMethod(result.snapshot,'tac-test');method.gigahorse_function_entries=[entry];result.sections.methods.push(method);
 result.sections.ir=instructions.map(r=>({...r,ir_id:id(method.method_id,r.statement),method_id:method.method_id,engine:'gigahorse',evidence_ids:[]}));
 result.sections.control_flow=blocks.map(b=>({...b,engine:'gigahorse',method_id:method.method_id}));return result;
}
const instruction=(statement,op,reads=[],definitions=[],next=[],block='entry')=>({statement,op,reads,definitions,statement_successors:next,block});
const literal=(statement,variable,value,next=[],block='entry')=>({...instruction(statement,'CONST',[],[variable],next,block),constant_definitions:[{variable,values:['0x'+BigInt(value).toString(16)]}]});

test('a CALL binds its exact 68-byte memory construction, source argument words and target',()=>{
 const instructions=[
  literal('offsetAsset','offsetAsset',4,['asset']),instruction('asset','CALLDATALOAD',['offsetAsset'],['asset'],['offsetRecipient']),
  literal('offsetRecipient','offsetRecipient',36,['recipient']),instruction('recipient','CALLDATALOAD',['offsetRecipient'],['recipient'],['offsetAmount']),
  literal('offsetAmount','offsetAmount',68,['amount']),instruction('amount','CALLDATALOAD',['offsetAmount'],['amount'],['selector']),
  literal('selector','selector',0xa9059cbbn<<224n,['ptr']),literal('ptr','ptr',128,['writeSelector']),instruction('writeSelector','MSTORE',['ptr','selector'],[],['ptrRecipient']),
  literal('ptrRecipient','ptrRecipient',132,['writeRecipient']),instruction('writeRecipient','MSTORE',['ptrRecipient','recipient'],[],['ptrAmount']),
  literal('ptrAmount','ptrAmount',164,['writeAmount']),instruction('writeAmount','MSTORE',['ptrAmount','amount'],[],['gas']),
  instruction('gas','GAS',[],['gas'],['zero']),literal('zero','zero',0,['size']),literal('size','size',68,['call']),
  instruction('call','CALL',['gas','asset','zero','ptr','size','zero','zero'],['success'],['return']),instruction('return','STOP')];
 for(const rows of[instructions,[...instructions].reverse()]){
  const result=fixture(rows,[{block:'entry',successors:[]}]);normalizeGigahorseDataflow(result);const binding=result.sections.call_input_bindings[0];
  assert.equal(binding.status,'exact');assert.equal(binding.target_expression,'calldataload(4)');assert.equal(binding.value_expression,'0');assert.equal(binding.cases[0].bytes.length,68);
  assert.deepEqual(binding.cases[0].bytes.slice(0,4).map(b=>b.value),[169,5,156,187]);
  assert.ok(binding.cases[0].bytes.slice(4,36).every((b,i)=>b.kind==='word_byte'&&b.expression==='calldataload(36)'&&b.byte_index===i));
  assert.ok(binding.cases[0].bytes.slice(36).every((b,i)=>b.expression==='calldataload(68)'&&b.byte_index===i));
  assert.equal(result.sections.ir_effects.find(r=>r.kind==='external_call').call_input_binding_id,binding.call_input_binding_id);
  assert.equal(result.sections.memory_frontier.length,0);assert.equal(result.sections.funds.length,0,'Encoded selector alone must not assert asset settlement');
 }
});

test('MLOAD definitions bind the version at their actual program point instead of a later write',()=>{
 const rows=[literal('zero','zero',0,['one']),literal('one','one',1,['storeOne']),instruction('storeOne','MSTORE',['zero','one'],[],['loadOne']),instruction('loadOne','MLOAD',['zero'],['first'],['two']),
  literal('two','two',2,['storeTwo']),instruction('storeTwo','MSTORE',['zero','two'],[],['loadTwo']),instruction('loadTwo','MLOAD',['zero'],['second'],['sum']),instruction('sum','ADD',['first','second'],['total'],['stop']),instruction('stop','STOP')];
 const result=fixture([...rows].reverse(),[{block:'entry',successors:[]}]);normalizeGigahorseDataflow(result);
 const value=v=>result.sections.ir_dataflow.find(r=>r.variable===v);assert.equal(value('first').symbolic_expression,'1');assert.equal(value('second').symbolic_expression,'2');assert.equal(value('total').symbolic_expression,'3');
 assert.notEqual(value('first').memory_read_id,value('second').memory_read_id);
 const known=new Set(result.sections.memory_versions.map(r=>r.memory_version_id));for(const row of result.sections.memory_versions){if(row.parent_version_id)assert.ok(known.has(row.parent_version_id));for(const input of row.inputs||[])assert.ok(known.has(input.memory_version_id));}
});

test('branch merges retain correlated loaded words and expose unresolved formulas without choosing an arm',()=>{
 const rows=[literal('zero','zero',0,['one']),literal('one','one',1,['two']),literal('two','two',2,['dest']),literal('dest','dest',32,['condition']),instruction('condition','CALLER',[],['condition'],['guard']),instruction('guard','JUMPI',['dest','condition'],[],['false'],'entry'),
  instruction('true','MSTORE',['zero','one'],[],['load'],'yes'),instruction('false','MSTORE',['zero','two'],[],['load'],'no'),instruction('load','MLOAD',['zero'],['loaded'],['stop'],'join'),instruction('stop','STOP',[],[],[],'join')];
 const result=fixture(rows,[{block:'entry',successors:['yes','no']},{block:'yes',successors:['join']},{block:'no',successors:['join']},{block:'join',successors:[]}]);normalizeGigahorseDataflow(result);
 const read=result.sections.memory_reads.find(r=>r.statement==='load');assert.equal(read.status,'conditional');assert.deepEqual(new Set(read.cases.map(c=>c.value)),new Set(['1','2']));
 const derived=result.sections.ir_dataflow.find(r=>r.variable==='loaded');assert.equal(derived.status,'partial');assert.ok(derived.unresolved.includes('MEMORY_CONDITIONAL_WORD'));
});

test('cyclic and absent CFG entries cannot produce zero-initialized money inputs',()=>{
 const rows=[literal('start','zero',0,[],'entry'),instruction('loop','MSTORE',['zero','zero'],[],['branch'],'body'),instruction('branch','JUMPI',['zero','zero'],[],['call'],'body'),instruction('call','CALL',['zero','zero','zero','zero','zero','zero','zero'],['success'],[],'tail')];
 const result=fixture(rows,[{block:'entry',successors:['body']},{block:'body',successors:['body','tail']},{block:'tail',successors:[]}]);normalizeGigahorseDataflow(result);
 assert.ok(result.sections.memory_frontier.some(f=>f.reason==='TAC_MEMORY_CFG_CYCLE_FRONTIER'));assert.equal(result.sections.call_input_bindings[0].status,'partial');assert.ok(!result.sections.call_input_bindings[0].cases);
});
