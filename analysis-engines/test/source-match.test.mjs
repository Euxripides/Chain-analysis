import test from 'node:test';
import assert from 'node:assert/strict';
import { verifySolidityRuntime } from '../source-match.mjs';
import { analyze } from '../analyze.mjs';
import { compileFixture } from './fixtures.mjs';
import {createRequire} from 'node:module';
const doc=(built,actual)=>({match:'exact_match',runtimeMatch:'exact_match',runtimeBytecode:{recompiledBytecode:'0x'+built,onchainBytecode:'0x'+actual}});

test('Solidity 0.5 data-before-CBOR requires recompiled auxdata and unchanged code',()=>{
 const solc=createRequire(import.meta.url)('solc-0.5.16');
 const input={language:'Solidity',sources:{'Tail.sol':{content:'pragma solidity 0.5.16; contract Tail {function check(uint256 n) external pure {require(n>1,"'+ 'data-before-metadata-'.repeat(8)+'");}}'}},settings:{optimizer:{enabled:true,runs:999999},evmVersion:'istanbul',outputSelection:{'*':{'*':['evm.deployedBytecode']}}}};
 const output=JSON.parse(solc.compile(JSON.stringify(input)));assert.ok(!(output.errors||[]).some(e=>e.severity==='error'));
 const compiled=output.contracts['Tail.sol'].Tail.evm.deployedBytecode,built=Buffer.from(compiled.object,'hex'),start=built.length-built.readUInt16BE(built.length-2)-2;
 assert.ok(built[start-1]!==0xfe&&built[start-1]!==0x00,'fixture must contain compiler data before CBOR');
 const actual=Buffer.from(built);actual[start+12]^=1;
 const source={...doc(built.toString('hex'),actual.toString('hex')),runtimeMatch:'match',match:'match'};
 source.runtimeBytecode.cborAuxdata={1:{offset:start,value:'0x'+built.subarray(start).toString('hex')}};
 const result=verifySolidityRuntime(compiled,source,'0x'+actual.toString('hex'),'0.5.16');
 assert.equal(result.metadata_exact,false);assert.equal(result.metadata_ranges[0].start,start);
 const badCode=Buffer.from(actual);badCode[0]^=1;
 assert.throws(()=>verifySolidityRuntime(compiled,{...source,runtimeBytecode:{...source.runtimeBytecode,onchainBytecode:'0x'+badCode.toString('hex')}},'0x'+badCode.toString('hex'),'0.5.16'),/SNAPSHOT_MISMATCH/);
 for(const auxdata of [{},{1:{offset:start+1,value:source.runtimeBytecode.cborAuxdata[1].value}},{1:{offset:start,value:'0x'+actual.subarray(start).toString('hex')}}]) {
  const changed={...source,runtimeBytecode:{...source.runtimeBytecode,cborAuxdata:auxdata}};
  assert.throws(()=>verifySolidityRuntime(compiled,changed,'0x'+actual.toString('hex'),'0.5.16'),/SNAPSHOT_MISMATCH|AUXDATA_RANGE_MISMATCH/);
 }
 assert.throws(()=>verifySolidityRuntime(compiled,{...source,runtimeMatch:'exact_match'},'0x'+actual.toString('hex'),'0.5.16'),/SNAPSHOT_MISMATCH/);
});

test('Solidity 0.4 metadata after STOP accepts only a valid metadata-only match',()=>{
 const metadata=byte=>'a165627a7a72305820'+byte.repeat(32)+'0029';
 const built='600000'+metadata('11'),actual='600000'+metadata('22'),source={...doc(built,actual),match:'match',runtimeMatch:'match'};
 assert.equal(verifySolidityRuntime({object:built},source,'0x'+actual,'0.4.18').metadata_exact,false);
 const ranged=structuredClone(source);ranged.runtimeBytecode.cborAuxdata={1:{offset:3,value:'0x'+metadata('11')}};
 assert.equal(verifySolidityRuntime({object:built},ranged,'0x'+actual,'0.4.18').metadata_exact,false);
 assert.throws(()=>verifySolidityRuntime({object:built},source,'0x'+actual,'0.8.30'),/SNAPSHOT_MISMATCH/);
 const bad='600100'+metadata('22');assert.throws(()=>verifySolidityRuntime({object:built},{...source,runtimeBytecode:{recompiledBytecode:'0x'+built,onchainBytecode:'0x'+bad}},'0x'+bad,'0.4.18'),/SNAPSHOT_MISMATCH/);
});

test('disabled compiler metadata cannot hide a change to literal data',()=>{
 const metadata=byte=>'a265627a7a72315820'+byte.repeat(32)+'64736f6c634300081e0032';
 const built='6000fe'+metadata('11'),actual='6000fe'+metadata('22'),source={...doc(built,actual),match:'match',runtimeMatch:'match',stdJsonInput:{settings:{metadata:{appendCBOR:false}}}};
 assert.throws(()=>verifySolidityRuntime({object:built},source,'0x'+actual,'0.8.30'),/SNAPSHOT_MISMATCH/);
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
