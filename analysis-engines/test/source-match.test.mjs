import test from 'node:test';
import assert from 'node:assert/strict';
import { verifySolidityRuntime } from '../source-match.mjs';
import { analyze } from '../analyze.mjs';
import { compileFixture } from './fixtures.mjs';
const doc=(built,actual)=>({match:'exact_match',runtimeMatch:'exact_match',runtimeBytecode:{recompiledBytecode:'0x'+built,onchainBytecode:'0x'+actual}});

test('Solidity 0.4 metadata after STOP accepts only a valid metadata-only match',()=>{
 const metadata=byte=>'a165627a7a72305820'+byte.repeat(32)+'0029';
 const built='600000'+metadata('11'),actual='600000'+metadata('22'),source={...doc(built,actual),match:'match',runtimeMatch:'match'};
 assert.equal(verifySolidityRuntime({object:built},source,'0x'+actual,'0.4.18').metadata_exact,false);
 assert.throws(()=>verifySolidityRuntime({object:built},source,'0x'+actual,'0.8.30'),/SNAPSHOT_MISMATCH/);
 const bad='600100'+metadata('22');assert.throws(()=>verifySolidityRuntime({object:built},{...source,runtimeBytecode:{recompiledBytecode:'0x'+built,onchainBytecode:'0x'+bad}},'0x'+bad,'0.4.18'),/SNAPSHOT_MISMATCH/);
});
test('a provider match label cannot validate unrelated recompiled runtime bytes',()=>{
 const input=compileFixture('NativeLedger'),other=compileFixture('Misleading');
 input.source=other.source;input.source.runtimeBytecode.onchainBytecode=input.bytecode;
 const result=analyze(input);
 assert.ok(result.sections.methods.every(m=>!m.verified_signature));
 assert.ok(result.sections.coverage.some(c=>c.reason==='RECOMPILED_RUNTIME_SNAPSHOT_MISMATCH'));
});
test('compiler immutable references are bound consistently and differences outside them are rejected',()=>{
 const built='7f'+'0'.repeat(64)+'50'+'7f'+'0'.repeat(64)+'00',word='0'.repeat(62)+'2a';
 const actual='7f'+word+'50'+'7f'+word+'00',compiled={object:built,immutableReferences:{7:[{start:1,length:32},{start:35,length:32}]}};
 const result=verifySolidityRuntime(compiled,doc(built,actual),'0x'+actual,'0.8.30');
 assert.equal(result.bindings[7].word_hex,'0x'+word);assert.equal(result.metadata_exact,true);
 const conflict=actual.slice(0,70)+'0'.repeat(62)+'2b'+actual.slice(134);
 assert.throws(()=>verifySolidityRuntime(compiled,doc(built,conflict),'0x'+conflict,'0.8.30'),/REFERENCES_CONFLICT/);
 const bad='60'+actual.slice(2);
 assert.throws(()=>verifySolidityRuntime(compiled,doc(built,bad),'0x'+bad,'0.8.30'),/SNAPSHOT_MISMATCH/);
});
test('library references may normalize linker placeholders without validating other mutations',()=>{
 const placeholder='__$'+'a'.repeat(34)+'$__',built='73'+placeholder+'00',provided='73'+'0'.repeat(40)+'00',address='11'.repeat(20),actual='73'+address+'00';
 const compiled={object:built,linkReferences:{'Lib.sol':{Lib:[{start:1,length:20}]}}};
 const result=verifySolidityRuntime(compiled,doc(provided,actual),'0x'+actual,'0.8.30');
 assert.equal(result.libraries['Lib.sol:Lib'],'0x'+address);
 assert.throws(()=>verifySolidityRuntime(compiled,doc(provided,'60'+actual.slice(2)),'0x60'+actual.slice(2),'0.8.30'),/SNAPSHOT_MISMATCH/);
});
