import test from 'node:test';
import assert from 'node:assert/strict';
import {instructionMap,addSourcePCMap} from '../source-pc.mjs';
import {emptyResult} from '../model.mjs';

test('source maps count instructions, expand inherited fields and preserve modifier depth',()=>{
 const code='0x7f'+'11'.repeat(32)+'f16000fa00';
 const rows=instructionMap(code,'1:4:0:-:2;9:3;;20:5::i:1;');
 assert.deepEqual(rows.map(r=>r.pc),[0,33,34,36,37]);
 assert.equal(rows[2].source_start,9);assert.equal(rows[3].modifier_depth,1);assert.equal(rows[3].jump,'i');
 assert.throws(()=>instructionMap('0x60','1:2:0'),/TRUNCATED_PUSH/);
 assert.throws(()=>instructionMap('0x00','1:2:0;;'),/EXCEEDS_CODE/);
});

test('PC binding requires containment in an actual callsite and preserves method candidates',()=>{
 const result=emptyResult({code_hash:'hash',code_address:'code',storage_address:'state'});
 result.sections.evidence=[{evidence_id:'first',location:{file:'a.sol',start:12,length:10,ast_id:1}},
  {evidence_id:'second',location:{file:'a.sol',start:40,length:10,ast_id:2}}];
 result.sections.external_returns=[{dependency_id:'one',call_type:'STATICCALL',evidence_ids:['first']},{dependency_id:'two',call_type:'STATICCALL',evidence_ids:['second']}];
 const rows=addSourcePCMap(result,{bytecode:'0xfafafafa',compiler:'test',output:{sources:{'a.sol':{id:0}}},
  compiled:{evm:{deployedBytecode:{sourceMap:'12:10:0;42:4;0:100;-1:0:-1',generatedSources:[]}}}});
 assert.deepEqual(rows.map(r=>r.source_dependency_ids),[['one'],['two'],[],[]]);
 assert.deepEqual(rows.map(r=>r.status),['unique_source_callsite','unique_source_callsite','requires_unambiguous_source_range','generated_or_unmapped_code']);
 assert.equal(JSON.parse(result.sections.artifacts[0].text).call_instructions.length,4);
});
